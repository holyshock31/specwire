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
	case strings.Contains(key, "issue"):
		if envelope.EventName != "Issue Hook" {
			return false
		}
		attributes := objectValue(envelope.Payload["object_attributes"])
		if stringValue(envelope.Payload["object_kind"]) != "issue" || stringValue(attributes["action"]) != "open" {
			return false
		}
		for _, item := range arrayValues(attributes["labels"]) {
			if stringValue(objectValue(item)["title"]) == "change" {
				return true
			}
		}
		return false
	case strings.Contains(key, "push") || strings.Contains(key, "archive"):
		return envelope.EventName == "Push Hook" && stringValue(envelope.Payload["ref"]) == "refs/heads/main" && !isZeroSHA(stringValue(envelope.Payload["after"])) && len(archiveChangeIDs(envelope.Payload)) != 0
	default:
		return false
	}
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
	if envelope.EventName != "Push Hook" {
		return []map[string]any{cloneMap(envelope.Payload)}
	}
	ids := archiveChangeIDs(envelope.Payload)
	result := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		payload := cloneMap(envelope.Payload)
		payload["change_id"] = id
		payload["provider_delivery_id"] = envelope.DeliveryID
		result = append(result, payload)
	}
	return result
}

func archiveChangeIDs(payload map[string]any) []string {
	var messages []string
	if head := objectValue(payload["head_commit"]); head != nil {
		messages = append(messages, stringValue(head["message"]))
	}
	for _, commit := range arrayValues(payload["commits"]) {
		messages = append(messages, stringValue(objectValue(commit)["message"]))
	}
	seen := map[string]bool{}
	var result []string
	for index := len(messages) - 1; index >= 0; index-- {
		changeID, ok := archiveTrailer(messages[index])
		if ok && !seen[changeID] {
			seen[changeID] = true
			result = append(result, changeID)
		}
	}
	return result
}

func archiveTrailer(message string) (string, bool) {
	var event, change string
	for _, line := range strings.Split(message, "\n") {
		line = strings.TrimSpace(line)
		if value, ok := strings.CutPrefix(line, "SpecWire-Event:"); ok {
			event = strings.TrimSpace(value)
		}
		if value, ok := strings.CutPrefix(line, "SpecWire-Change:"); ok {
			change = strings.TrimSpace(value)
		}
	}
	return change, event == "archived" && change != ""
}

func actionIdentity(eventName, delivery string, payload map[string]any) string {
	change := firstString(payload["change_id"], publicationField(payload, "change_id"))
	if change != "" {
		if eventName == "Push Hook" {
			return "archive:" + change
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
	if envelope.EventName == "Push Hook" {
		if change := stringValue(payload["change_id"]); change != "" {
			return envelope.DeliveryID + ":" + change
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
