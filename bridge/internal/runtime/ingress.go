package runtime

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"specwire/bridge/internal/domain"
	"specwire/bridge/internal/flow"
	"specwire/bridge/internal/security"
)

func (i *Ingress) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, int64(i.maxBody)+1))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "cannot read webhook body"})
		return
	}
	if len(body) > i.maxBody {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"error": "webhook body too large"})
		return
	}
	envelope, err := parseGitLabEnvelope(r, body)
	if err != nil {
		// Missing/invalid signing headers are intentionally indistinguishable from
		// a signature mismatch to avoid turning the endpoint into an oracle.
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	result, err := i.accept(r.Context(), envelope)
	if err != nil {
		if isUnauthorized(err) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "event acceptance unavailable"})
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}

func parseGitLabEnvelope(r *http.Request, body []byte) (GitLabEnvelope, error) {
	payload, err := decodeObject(body)
	if err != nil {
		return GitLabEnvelope{}, err
	}
	delivery := strings.TrimSpace(firstHeader(r, "webhook-id", "X-Gitlab-Delivery"))
	timestamp := strings.TrimSpace(r.Header.Get("webhook-timestamp"))
	signature := strings.TrimSpace(r.Header.Get("webhook-signature"))
	if delivery == "" || timestamp == "" || signature == "" {
		return GitLabEnvelope{}, fmt.Errorf("%w: webhook signature headers are required", domain.ErrForbidden)
	}
	project := objectValue(payload["project"])
	projectID := externalID(project["id"])
	if projectID == "" {
		projectID = externalID(payload["project_id"])
	}
	if projectID == "" {
		return GitLabEnvelope{}, fmt.Errorf("%w: GitLab project external ID is required", domain.ErrInvalid)
	}
	instanceHint := strings.TrimSpace(r.URL.Query().Get("instance_id"))
	if instanceHint == "" {
		instanceHint = strings.TrimSpace(r.Header.Get("X-SpecWire-Instance-ID"))
	}
	return GitLabEnvelope{
		EventName:               strings.TrimSpace(r.Header.Get("X-Gitlab-Event")),
		DeliveryID:              delivery,
		Timestamp:               timestamp,
		Signature:               signature,
		InstanceHint:            domain.ID(instanceHint),
		SourceProjectExternalID: projectID,
		SourceProjectPath:       firstString(project["path_with_namespace"], project["web_url"]),
		Payload:                 payload,
		RawBody:                 append([]byte(nil), body...),
	}, nil
}

func (i *Ingress) accept(ctx context.Context, envelope GitLabEnvelope) (IngressResult, error) {
	routes, err := i.store.ListHookRoutesForProject(ctx, envelope.InstanceHint, envelope.SourceProjectExternalID)
	if err != nil {
		return IngressResult{}, err
	}
	if len(routes) == 0 {
		return IngressResult{Ignored: 1}, nil
	}
	verified, err := i.verifyRoutes(ctx, envelope, routes)
	if err != nil {
		return IngressResult{}, err
	}
	if len(verified) == 0 {
		return IngressResult{}, fmt.Errorf("%w: no Hook secret matched", domain.ErrForbidden)
	}

	var result IngressResult
	matched := false
	catalogs := map[domain.ID]flow.Catalog{}
	for _, route := range verified {
		catalog, ok := catalogs[route.WorkspaceID]
		if !ok {
			catalog, err = i.catalogForWorkspace(ctx, route.WorkspaceID)
			if err != nil {
				return result, err
			}
			catalogs[route.WorkspaceID] = catalog
		}
		behavior, ok := catalog.Behavior(route.BehaviorKey, route.BehaviorVersion)
		if !ok || behavior.Direction != domain.DirectionInput {
			result.Ignored++
			continue
		}
		if !matchesBehavior(envelope, route.BehaviorKey) || !matchesEventFilter(envelope.Payload, route.EventFilter) {
			result.Ignored++
			continue
		}
		flowRecord, err := i.store.GetFlow(ctx, route.WorkspaceID, route.FlowID)
		if err != nil {
			return result, err
		}
		if flowRecord.Status != domain.FlowPublished {
			result.Ignored++
			continue
		}
		version, err := i.store.GetFlowVersion(ctx, route.WorkspaceID, route.FlowID, route.FlowVersion)
		if err != nil {
			return result, err
		}
		if version.Status != domain.FlowPublished {
			result.Ignored++
			continue
		}
		connection, err := i.store.GetConnection(ctx, route.WorkspaceID, route.ConnectionID)
		if err != nil {
			return result, err
		}
		payloads := eventPayloads(envelope)
		if len(payloads) == 0 {
			result.Ignored++
			continue
		}
		matched = true
		for _, payload := range payloads {
			action := actionIdentity(envelope.EventName, envelope.DeliveryID, payload)
			receivedAt := i.now().UTC()
			retentionUntil := receivedAt.Add(i.retention)
			execution := domain.FlowExecution{
				ID:             domain.NewID(),
				WorkspaceID:    route.WorkspaceID,
				ConnectionID:   route.ConnectionID,
				FlowID:         route.FlowID,
				FlowVersionID:  version.ID,
				FlowVersion:    version.Version,
				DeliveryID:     envelope.DeliveryID,
				IdempotencyKey: idempotencyKey(route.HookRoute, connection, action),
				CorrelationID:  correlationID(route.HookRoute, action),
				Status:         domain.ExecutionQueued,
				CurrentNodeID:  inputNodeID(version.Graph, catalog),
			}
			event := domain.InboundEvent{
				ID:                      domain.NewID(),
				WorkspaceID:             route.WorkspaceID,
				ConnectionID:            route.ConnectionID,
				Provider:                domain.ProviderGitLab,
				SourceInstanceID:        route.SourceProject.InstanceID,
				SourceProjectExternalID: envelope.SourceProjectExternalID,
				BehaviorKey:             route.BehaviorKey,
				BehaviorVersion:         route.BehaviorVersion,
				DeliveryID:              actionDeliveryID(envelope, payload),
				Payload:                 security.RedactValue(payload).(map[string]any),
				PayloadHash:             rawHash(envelope.RawBody),
				ReceivedAt:              receivedAt,
				RetentionUntil:          &retentionUntil,
			}
			job := domain.Job{ID: domain.NewID(), WorkspaceID: route.WorkspaceID, Kind: JobKindFlowExecute, Payload: map[string]any{
				"execution_id":  execution.ID,
				"connection_id": route.ConnectionID,
			}}
			created, isNew, err := i.store.AcceptInboundEvent(ctx, event, execution, job)
			if err != nil {
				return result, err
			}
			if isNew {
				result.Accepted++
			} else {
				_ = created
				result.Duplicates++
			}
		}
	}
	if !matched && result.Ignored == 0 {
		result.Ignored = 1
	}
	return result, nil
}

type verifiedRoute struct {
	domain.HookRoute
	hook domain.Hook
}

func (i *Ingress) verifyRoutes(ctx context.Context, envelope GitLabEnvelope, routes []domain.HookRoute) ([]verifiedRoute, error) {
	type hookCheck struct {
		hook  domain.Hook
		valid bool
	}
	checkedHooks := map[string]hookCheck{}
	var verified []verifiedRoute
	for _, route := range routes {
		key := string(route.WorkspaceID) + ":" + string(route.HookRef)
		if check, ok := checkedHooks[key]; ok {
			if check.valid {
				verified = append(verified, verifiedRoute{HookRoute: route, hook: check.hook})
			}
			continue
		}
		hook, err := i.store.GetHook(ctx, route.WorkspaceID, route.HookRef)
		if err != nil {
			return nil, err
		}
		if hook.SigningRef == nil {
			checkedHooks[key] = hookCheck{hook: hook}
			continue
		}
		secret, err := i.secrets.Resolve(ctx, *hook.SigningRef)
		if err != nil {
			checkedHooks[key] = hookCheck{hook: hook}
			continue
		}
		valid := verifySignature(secret, envelope.DeliveryID, envelope.Timestamp, envelope.Signature, envelope.RawBody, i.now(), i.maxAge)
		clearBytes(secret)
		checkedHooks[key] = hookCheck{hook: hook, valid: valid}
		if valid {
			verified = append(verified, verifiedRoute{HookRoute: route, hook: hook})
		}
	}
	return verified, nil
}

func verifySignature(secret []byte, id, timestamp, signature string, body []byte, now time.Time, maxAge time.Duration) bool {
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || maxAge <= 0 {
		return false
	}
	age := now.Sub(time.Unix(seconds, 0))
	if age > maxAge || age < -maxAge {
		return false
	}
	key := secret
	if strings.HasPrefix(string(secret), "whsec_") {
		key, err = base64.StdEncoding.DecodeString(strings.TrimPrefix(string(secret), "whsec_"))
		if err != nil {
			return false
		}
	}
	message := []byte(id + "." + timestamp + "." + string(body))
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(message)
	want := "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
	for _, candidate := range strings.Fields(signature) {
		if subtle.ConstantTimeCompare([]byte(want), []byte(candidate)) == 1 {
			return true
		}
	}
	return false
}

func matchesBehavior(envelope GitLabEnvelope, behaviorKey string) bool {
	key := strings.ToLower(strings.TrimSpace(behaviorKey))
	switch {
	case key == "gitlab.issue-abandon-hook":
		return matchesAbandonIssue(envelope)
	case key == "gitlab.issue-hook":
		return matchesPublicationIssue(envelope)
	case key == "gitlab.push-hook":
		return matchesArchivePush(envelope)
	case strings.Contains(key, "issue"):
		return matchesPublicationIssue(envelope)
	case strings.Contains(key, "push") || strings.Contains(key, "archive"):
		return matchesArchivePush(envelope)
	default:
		return false
	}
}

func matchesPublicationIssue(envelope GitLabEnvelope) bool {
	if envelope.EventName != "Issue Hook" {
		return false
	}
	attributes := objectValue(envelope.Payload["object_attributes"])
	if stringValue(envelope.Payload["object_kind"]) != "issue" || stringValue(attributes["action"]) != "open" {
		return false
	}
	return hasIssueLabel(attributes["labels"], "change")
}

func matchesArchivePush(envelope GitLabEnvelope) bool {
	return envelope.EventName == "Push Hook" &&
		stringValue(envelope.Payload["ref"]) == "refs/heads/main" &&
		!isZeroSHA(stringValue(envelope.Payload["after"])) &&
		hasArchivedLifecycleTrailer(envelope.Payload)
}

const abandonLabel = "specwire::abandoned"

func matchesAbandonIssue(envelope GitLabEnvelope) bool {
	if envelope.EventName != "Issue Hook" || stringValue(envelope.Payload["object_kind"]) != "issue" {
		return false
	}
	attributes := objectValue(envelope.Payload["object_attributes"])
	if stringValue(attributes["action"]) != "update" || issueChangeID(envelope.Payload) == "" {
		return false
	}
	if !hasIssueLabel(attributes["labels"], abandonLabel) {
		return false
	}
	changes, hasChanges := envelope.Payload["changes"].(map[string]any)
	if !hasChanges {
		// An update without an explicit label diff may be caused by Bridge's
		// own description/note/close follow-up.  The abandon protocol is a
		// transition, not a level-triggered "label is present" condition.
		return false
	}
	labelChanges, hasLabelChanges := changes["labels"].(map[string]any)
	if !hasLabelChanges {
		return false
	}
	return hasIssueLabel(labelChanges["current"], abandonLabel) &&
		!hasIssueLabel(labelChanges["previous"], abandonLabel)
}

func hasIssueLabel(value any, wanted string) bool {
	for _, item := range arrayValues(value) {
		if stringValue(objectValue(item)["title"]) == wanted || stringValue(objectValue(item)["name"]) == wanted {
			return true
		}
	}
	return false
}

func issueChangeID(payload map[string]any) string {
	attributes := objectValue(payload["object_attributes"])
	fields := parseDescriptionFields(stringValue(attributes["description"]))
	return firstNonEmpty(fields["change_id"], stringValue(payload["change_id"]))
}

func issueAbandonReason(payload map[string]any) string {
	attributes := objectValue(payload["object_attributes"])
	fields := parseDescriptionFields(stringValue(attributes["description"]))
	reason := firstNonEmpty(
		stringValue(payload["lifecycle_reason"]),
		fields["specwire_reason"],
		fields["specwire-reason"],
		fields["abandon_reason"],
		fields["abandon-reason"],
		fields["reason"],
		"GitLab Issue 添加 specwire::abandoned 标签",
	)
	reason = strings.TrimSpace(reason)
	if strings.ContainsAny(reason, "\r\n") || utf8.RuneCountInString(reason) > maxLifecycleReasonRunes {
		return "GitLab Issue 添加 specwire::abandoned 标签"
	}
	return reason
}

func hasArchivedLifecycleTrailer(payload map[string]any) bool {
	for _, trailer := range lifecycleTrailers(payload) {
		if trailer.Event == "archived" {
			return true
		}
	}
	return false
}

func matchesEventFilter(payload map[string]any, raw map[string]any) bool {
	if len(raw) == 0 {
		return true
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return false
	}
	var filter flow.Filter
	if err := json.Unmarshal(encoded, &filter); err != nil || strings.TrimSpace(filter.Op) == "" {
		return false
	}
	matched, err := flow.EvaluateFilter(payload, filter)
	return err == nil && matched
}

func eventPayloads(envelope GitLabEnvelope) []map[string]any {
	if envelope.EventName == "Issue Hook" && matchesAbandonIssue(envelope) {
		payload := cloneMap(envelope.Payload)
		payload["change_id"] = issueChangeID(envelope.Payload)
		payload["source_project"] = envelope.SourceProjectPath
		payload["target_ref"] = "refs/heads/main"
		payload["provider_delivery_id"] = envelope.DeliveryID
		payload["lifecycle_event"] = "abandoned"
		payload["lifecycle_reason"] = issueAbandonReason(envelope.Payload)
		attributes := objectValue(envelope.Payload["object_attributes"])
		if iid := intValue(attributes["iid"]); iid != 0 {
			payload["issue_iid"] = iid
		}
		if url := firstNonEmpty(stringValue(attributes["url"]), stringValue(attributes["web_url"])); url != "" {
			payload["issue_url"] = url
		}
		return []map[string]any{payload}
	}
	if envelope.EventName != "Push Hook" {
		return []map[string]any{cloneMap(envelope.Payload)}
	}
	trailers := archivedLifecycleTrailers(envelope.Payload)
	result := make([]map[string]any, 0, len(trailers))
	for _, trailer := range trailers {
		payload := cloneMap(envelope.Payload)
		payload["change_id"] = trailer.ChangeID
		payload["provider_delivery_id"] = envelope.DeliveryID
		payload["lifecycle_event"] = trailer.Event
		if trailer.Reason != "" {
			payload["lifecycle_reason"] = trailer.Reason
		}
		result = append(result, payload)
	}
	return result
}

func archiveChangeIDs(payload map[string]any) []string {
	seen := map[string]bool{}
	var result []string
	for _, trailer := range lifecycleTrailers(payload) {
		if trailer.Event == "archived" && !seen[trailer.ChangeID] {
			seen[trailer.ChangeID] = true
			result = append(result, trailer.ChangeID)
		}
	}
	return result
}

func archiveTrailer(message string) (string, bool) {
	trailer, ok := parseLifecycleTrailer(message)
	return trailer.ChangeID, ok && trailer.Event == "archived"
}

type lifecycleTrailer struct {
	Event    string
	Status   string
	ChangeID string
	Reason   string
}

const maxLifecycleReasonRunes = 1000

func lifecycleTrailers(payload map[string]any) []lifecycleTrailer {
	messages := commitMessages(payload)
	seen := map[string]bool{}
	result := make([]lifecycleTrailer, 0, len(messages))
	for index := len(messages) - 1; index >= 0; index-- {
		trailer, ok := parseLifecycleTrailer(messages[index])
		if !ok {
			continue
		}
		key := trailer.Event + ":" + trailer.ChangeID
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, trailer)
	}
	return result
}

func acceptedLifecycleTrailers(payload map[string]any) []lifecycleTrailer {
	return archivedLifecycleTrailers(payload)
}

func archivedLifecycleTrailers(payload map[string]any) []lifecycleTrailer {
	if stringValue(payload["ref"]) != "refs/heads/main" || isZeroSHA(stringValue(payload["after"])) {
		return nil
	}
	var result []lifecycleTrailer
	for _, trailer := range lifecycleTrailers(payload) {
		if trailer.Event == "archived" {
			result = append(result, trailer)
		}
	}
	return result
}

func commitMessages(payload map[string]any) []string {
	var messages []string
	if head := objectValue(payload["head_commit"]); head != nil {
		messages = append(messages, stringValue(head["message"]))
	}
	for _, commit := range arrayValues(payload["commits"]) {
		messages = append(messages, stringValue(objectValue(commit)["message"]))
	}
	return messages
}

func parseLifecycleTrailer(message string) (lifecycleTrailer, bool) {
	var trailer lifecycleTrailer
	for _, line := range strings.Split(message, "\n") {
		line = strings.TrimSpace(line)
		if value, ok := strings.CutPrefix(line, "SpecWire-Event:"); ok {
			trailer.Event = strings.TrimSpace(value)
		}
		if value, ok := strings.CutPrefix(line, "SpecWire-Status:"); ok {
			trailer.Status = strings.TrimSpace(value)
		}
		if value, ok := strings.CutPrefix(line, "SpecWire-Change:"); ok {
			trailer.ChangeID = strings.TrimSpace(value)
		}
		if value, ok := strings.CutPrefix(line, "SpecWire-Reason:"); ok {
			trailer.Reason = strings.TrimSpace(value)
		}
	}
	if trailer.Event != "archived" && trailer.Event != "abandoned" || trailer.ChangeID == "" {
		return lifecycleTrailer{}, false
	}
	if trailer.Status != "" && trailer.Status != trailer.Event {
		return lifecycleTrailer{}, false
	}
	if trailer.Event == "abandoned" {
		if trailer.Status != "abandoned" || trailer.Reason == "" || strings.ContainsAny(trailer.Reason, "\r\n") || utf8.RuneCountInString(trailer.Reason) > maxLifecycleReasonRunes {
			return lifecycleTrailer{}, false
		}
	}
	return trailer, true
}

func abandonedRefMatches(ref, changeID string) bool {
	changeID = strings.TrimSpace(changeID)
	return ref == "refs/heads/change/feat-"+changeID || ref == "refs/heads/change/fix-"+changeID
}

func lifecycleTrailerForPayload(payload map[string]any) (lifecycleTrailer, bool) {
	trailers := archivedLifecycleTrailers(payload)
	if len(trailers) == 0 {
		return lifecycleTrailer{}, false
	}
	return trailers[0], true
}

func actionIdentity(eventName, delivery string, payload map[string]any) string {
	change := firstString(payload["change_id"], publicationField(payload, "change_id"))
	if change != "" {
		if lifecycleEvent := stringValue(payload["lifecycle_event"]); lifecycleEvent == "abandoned" {
			return "lifecycle:abandoned:" + change
		}
		if eventName == "Push Hook" {
			lifecycleEvent := stringValue(payload["lifecycle_event"])
			if lifecycleEvent == "" {
				if trailer, ok := lifecycleTrailerForPayload(payload); ok {
					lifecycleEvent = trailer.Event
				}
			}
			return "lifecycle:" + firstNonEmpty(lifecycleEvent, "archived") + ":" + change
		}
		sha := firstString(payload["branch_head_sha"], publicationField(payload, "branch_head_sha"))
		return "publication:" + change + ":" + sha
	}
	return "delivery:" + delivery
}

func publicationField(payload map[string]any, field string) string {
	return stringValue(parseDescriptionFields(stringValue(objectValue(payload["object_attributes"])["description"]))[field])
}

func parseDescriptionFields(description string) map[string]string {
	result := map[string]string{}
	for _, line := range strings.Split(description, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if ok {
			result[strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(value)
		}
	}
	return result
}

func idempotencyKey(route domain.HookRoute, connection domain.Connection, action string) string {
	return digest("idempotency", string(route.WorkspaceID), string(connection.ID), string(route.FlowID), strconv.Itoa(route.FlowVersion), string(connection.SourceGitLabProject.InstanceID), connection.SourceGitLabProject.ExternalID, route.BehaviorKey, route.BehaviorVersion, action, string(connection.TargetMulticaProject.InstanceID), connection.TargetMulticaProject.ExternalID)
}

func correlationID(route domain.HookRoute, action string) string {
	return "corr_" + digest("correlation", string(route.WorkspaceID), string(route.ConnectionID), string(route.FlowID), action)
}

func digest(values ...string) string {
	hash := sha256.New()
	for _, value := range values {
		_, _ = fmt.Fprintf(hash, "%d:", len(value))
		_, _ = hash.Write([]byte(value))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func actionDeliveryID(envelope GitLabEnvelope, payload map[string]any) string {
	if envelope.EventName == "Issue Hook" && stringValue(payload["lifecycle_event"]) == "abandoned" {
		if change := stringValue(payload["change_id"]); change != "" {
			return envelope.DeliveryID + ":abandoned:" + change
		}
	}
	if envelope.EventName == "Push Hook" {
		if change := stringValue(payload["change_id"]); change != "" {
			lifecycleEvent := stringValue(payload["lifecycle_event"])
			if lifecycleEvent == "" {
				if trailer, ok := lifecycleTrailerForPayload(payload); ok {
					lifecycleEvent = trailer.Event
				}
			}
			return envelope.DeliveryID + ":" + firstNonEmpty(lifecycleEvent, "archived") + ":" + change
		}
	}
	return envelope.DeliveryID
}

func inputNodeID(graph domain.FlowGraph, catalog flow.Catalog) domain.ID {
	for _, node := range graph.Nodes {
		if node.Kind == domain.NodeConnector && node.Connector != nil {
			if behavior, ok := catalog.Behavior(node.Connector.BehaviorKey, node.Connector.BehaviorVersion); ok && behavior.Direction == domain.DirectionInput {
				return node.ID
			}
		}
	}
	return ""
}

func decodeObject(body []byte) (map[string]any, error) {
	var payload map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return nil, err
	}
	if payload == nil {
		return nil, fmt.Errorf("%w: JSON object required", domain.ErrInvalid)
	}
	return payload, nil
}

func objectValue(value any) map[string]any {
	if object, ok := value.(map[string]any); ok {
		return object
	}
	return map[string]any{}
}

func arrayValues(value any) []any {
	if items, ok := value.([]any); ok {
		return items
	}
	return nil
}

func externalID(value any) string {
	return stringValue(value)
}

func firstString(values ...any) string {
	for _, value := range values {
		if text := stringValue(value); text != "" {
			return text
		}
	}
	return ""
}

func cloneMap(value map[string]any) map[string]any {
	encoded, _ := json.Marshal(value)
	var clone map[string]any
	_ = json.Unmarshal(encoded, &clone)
	if clone == nil {
		clone = map[string]any{}
	}
	return clone
}

func rawHash(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func isZeroSHA(value string) bool {
	value = strings.TrimSpace(value)
	return value == "" || strings.Trim(value, "0") == ""
}

func clearBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func firstHeader(r *http.Request, names ...string) string {
	for _, name := range names {
		if value := r.Header.Get(name); value != "" {
			return value
		}
	}
	return ""
}

func isUnauthorized(err error) bool { return errors.Is(err, domain.ErrForbidden) }

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
