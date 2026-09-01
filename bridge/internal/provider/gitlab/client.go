package gitlab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"specwire/bridge/internal/domain"
	"specwire/bridge/internal/provider"
)

const maxResponseBytes = 4 << 20

// Client is the GitLab control-plane adapter. Every provider request must
// receive a credential resolved from the persistent control plane. The client
// deliberately has no process-level token slot: legacy configuration is
// imported by the application before it reaches this adapter and is never a
// request-time fallback.
type Client struct {
	httpClient *http.Client
}

func NewClient(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{httpClient: httpClient}
}

type apiError struct {
	Status    int
	RequestID string
	Body      string
}

func (e *apiError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("GitLab returned HTTP %d", e.Status)
	}
	return fmt.Sprintf("GitLab returned HTTP %d: %s", e.Status, truncate(e.Body, 300))
}

func (c *Client) ListGroups(ctx context.Context, instance domain.GitLabInstance, query string, credential *provider.Credential) ([]provider.GitLabGroup, error) {
	values := url.Values{}
	values.Set("per_page", "100")
	if strings.TrimSpace(query) != "" {
		values.Set("search", strings.TrimSpace(query))
	}
	body, _, err := c.do(ctx, instance, credential, http.MethodGet, "/groups", values, nil)
	if err != nil {
		return nil, err
	}
	var raw []struct {
		ID       json.RawMessage `json:"id"`
		FullPath string          `json:"full_path"`
		Name     string          `json:"name"`
		Path     string          `json:"path"`
		ParentID json.RawMessage `json:"parent_id"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, c.invalidResponse("list groups", err)
	}
	groups := make([]provider.GitLabGroup, 0, len(raw))
	for _, item := range raw {
		groups = append(groups, provider.GitLabGroup{
			InstanceID:       instance.ID,
			ExternalID:       jsonID(item.ID),
			FullPath:         item.FullPath,
			Name:             firstNonEmpty(item.Name, item.Path, item.FullPath),
			ParentExternalID: jsonID(item.ParentID),
		})
	}
	return groups, nil
}

func (c *Client) ListProjects(ctx context.Context, instance domain.GitLabInstance, group provider.GitLabGroup, query string, credential *provider.Credential) ([]provider.GitLabProject, error) {
	groupID := strings.TrimSpace(group.ExternalID)
	if groupID == "" {
		groupID = group.FullPath
	}
	values := url.Values{}
	values.Set("per_page", "100")
	if strings.TrimSpace(query) != "" {
		values.Set("search", strings.TrimSpace(query))
	}
	body, _, err := c.do(ctx, instance, credential, http.MethodGet, "/groups/"+url.PathEscape(groupID)+"/projects", values, nil)
	if err != nil {
		return nil, err
	}
	var raw []projectResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, c.invalidResponse("list projects", err)
	}
	projects := make([]provider.GitLabProject, 0, len(raw))
	for _, item := range raw {
		projects = append(projects, item.project(instance.ID, groupID))
	}
	return projects, nil
}

func (c *Client) GetProject(ctx context.Context, instance domain.GitLabInstance, externalID string, credential *provider.Credential) (provider.GitLabProject, error) {
	if strings.TrimSpace(externalID) == "" {
		return provider.GitLabProject{}, fmt.Errorf("%w: GitLab project external ID is required", domain.ErrInvalid)
	}
	body, _, err := c.do(ctx, instance, credential, http.MethodGet, "/projects/"+url.PathEscape(strings.TrimSpace(externalID)), nil, nil)
	if err != nil {
		return provider.GitLabProject{}, err
	}
	var raw projectResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return provider.GitLabProject{}, c.invalidResponse("get project", err)
	}
	return raw.project(instance.ID, raw.namespaceGroupID()), nil
}

func (c *Client) EnsureLabel(ctx context.Context, instance domain.GitLabInstance, project provider.GitLabProject, title string, credential *provider.Credential) (provider.LabelResult, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return provider.LabelResult{}, fmt.Errorf("%w: label title is required", domain.ErrInvalid)
	}
	values := url.Values{"search": []string{title}, "per_page": []string{"100"}}
	body, requestID, err := c.do(ctx, instance, credential, http.MethodGet, projectPath(project)+"/labels", values, nil)
	if err != nil {
		return provider.LabelResult{}, err
	}
	var labels []labelResponse
	if err := json.Unmarshal(body, &labels); err != nil {
		return provider.LabelResult{}, c.invalidResponse("list labels", err)
	}
	for _, label := range labels {
		if label.title() == title {
			return provider.LabelResult{ExternalID: jsonID(label.ID), Title: title, Adopted: true, RequestID: firstNonEmpty(requestID, label.RequestID)}, nil
		}
	}
	form := url.Values{"name": []string{title}, "color": []string{"#4285F4"}}
	body, requestID, err = c.do(ctx, instance, credential, http.MethodPost, projectPath(project)+"/labels", nil, form)
	if err != nil {
		// A concurrent creator is safe to adopt on retry.
		var apiErr *apiError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusConflict {
			return c.EnsureLabel(ctx, instance, project, title, credential)
		}
		return provider.LabelResult{}, err
	}
	var label labelResponse
	if err := json.Unmarshal(body, &label); err != nil {
		return provider.LabelResult{}, c.invalidResponse("create label", err)
	}
	return provider.LabelResult{ExternalID: jsonID(label.ID), Title: firstNonEmpty(label.title(), title), Created: true, RequestID: firstNonEmpty(requestID, label.RequestID)}, nil
}

func (c *Client) EnsureHook(ctx context.Context, instance domain.GitLabInstance, project provider.GitLabProject, spec provider.HookSpec, credential *provider.Credential) (provider.HookResult, error) {
	if strings.TrimSpace(spec.URL) == "" {
		return provider.HookResult{}, fmt.Errorf("%w: hook URL is required", domain.ErrInvalid)
	}
	if len(spec.SigningToken) == 0 {
		return provider.HookResult{}, fmt.Errorf("%w: hook signing token is required for reconciliation", domain.ErrInvalid)
	}
	body, requestID, err := c.do(ctx, instance, credential, http.MethodGet, projectPath(project)+"/hooks", nil, nil)
	if err != nil {
		return provider.HookResult{}, err
	}
	var hooks []hookResponse
	if err := json.Unmarshal(body, &hooks); err != nil {
		return provider.HookResult{}, c.invalidResponse("list hooks", err)
	}
	pushEvents, issueEvents := hookEvents(spec.Events)
	form := hookForm(spec.URL, string(spec.SigningToken), pushEvents, issueEvents)
	for _, hook := range hooks {
		if hook.URL != spec.URL && !sameHookEndpoint(hook.URL, spec.URL) {
			continue
		}
		path := projectPath(project) + "/hooks/" + strconv.FormatInt(hook.ID, 10)
		_, updateRequestID, err := c.do(ctx, instance, credential, http.MethodPut, path, nil, form)
		if err != nil {
			return provider.HookResult{}, err
		}
		return provider.HookResult{ExternalID: strconv.FormatInt(hook.ID, 10), Adopted: true, RequestID: firstNonEmpty(updateRequestID, requestID)}, nil
	}
	body, requestID, err = c.do(ctx, instance, credential, http.MethodPost, projectPath(project)+"/hooks", nil, form)
	if err != nil {
		var apiErr *apiError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusConflict {
			return c.EnsureHook(ctx, instance, project, spec, credential)
		}
		return provider.HookResult{}, err
	}
	var hook hookResponse
	if err := json.Unmarshal(body, &hook); err != nil {
		return provider.HookResult{}, c.invalidResponse("create hook", err)
	}
	if hook.ID == 0 {
		return provider.HookResult{}, c.invalidResponse("create hook", errors.New("response did not contain hook id"))
	}
	return provider.HookResult{ExternalID: strconv.FormatInt(hook.ID, 10), Created: true, RequestID: firstNonEmpty(requestID, hook.RequestID)}, nil
}

// sameHookEndpoint lets migration adopt the pre-instance-hint callback URL.
// Query parameters are deliberately ignored here: the project and endpoint
// identity have already been fixed by the API path, while instance_id is the
// new routing hint added by the persistent control plane.
func sameHookEndpoint(left, right string) bool {
	a, err := url.Parse(strings.TrimSpace(left))
	if err != nil {
		return false
	}
	b, err := url.Parse(strings.TrimSpace(right))
	if err != nil {
		return false
	}
	return a.Scheme == b.Scheme && a.Host == b.Host && a.Path == b.Path
}

func (c *Client) NoteIssue(ctx context.Context, instance domain.GitLabInstance, project provider.GitLabProject, iid int, body string, credential *provider.Credential) error {
	if iid <= 0 {
		return fmt.Errorf("%w: GitLab issue IID must be positive", domain.ErrInvalid)
	}
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("%w: GitLab issue note body is required", domain.ErrInvalid)
	}
	_, _, err := c.do(ctx, instance, credential, http.MethodPost, projectPath(project)+"/issues/"+strconv.Itoa(iid)+"/notes", nil, url.Values{"body": []string{body}})
	return err
}

func (c *Client) CloseIssue(ctx context.Context, instance domain.GitLabInstance, project provider.GitLabProject, iid int, credential *provider.Credential) error {
	if iid <= 0 {
		return fmt.Errorf("%w: GitLab issue IID must be positive", domain.ErrInvalid)
	}
	values := url.Values{"state_event": []string{"close"}}
	_, _, err := c.do(ctx, instance, credential, http.MethodPut, projectPath(project)+"/issues/"+strconv.Itoa(iid), values, nil)
	return err
}

type projectResponse struct {
	ID                json.RawMessage `json:"id"`
	PathWithNamespace string          `json:"path_with_namespace"`
	Name              string          `json:"name"`
	Path              string          `json:"path"`
	WebURL            string          `json:"web_url"`
	SSHURL            string          `json:"ssh_url_to_repo"`
	HTTPSURL          string          `json:"http_url_to_repo"`
	Namespace         struct {
		ID json.RawMessage `json:"id"`
	} `json:"namespace"`
}

func (p projectResponse) namespaceGroupID() string { return jsonID(p.Namespace.ID) }

func (p projectResponse) project(instanceID domain.ID, fallbackGroupID string) provider.GitLabProject {
	return provider.GitLabProject{
		InstanceID: instanceID,
		ExternalID: jsonID(p.ID),
		GroupID:    firstNonEmpty(p.namespaceGroupID(), fallbackGroupID),
		FullPath:   firstNonEmpty(p.PathWithNamespace, p.Path),
		Name:       firstNonEmpty(p.Name, p.Path, p.PathWithNamespace),
		WebURL:     p.WebURL,
		SSHURL:     p.SSHURL,
		HTTPSURL:   p.HTTPSURL,
	}
}

type labelResponse struct {
	ID        json.RawMessage `json:"id"`
	Name      string          `json:"name"`
	Title     string          `json:"title"`
	RequestID string          `json:"request_id"`
}

func (l labelResponse) title() string { return firstNonEmpty(l.Name, l.Title) }

type hookResponse struct {
	ID           int64  `json:"id"`
	URL          string `json:"url"`
	RequestID    string `json:"request_id"`
	PushEvents   bool   `json:"push_events"`
	IssuesEvents bool   `json:"issues_events"`
}

func (c *Client) do(ctx context.Context, instance domain.GitLabInstance, credential *provider.Credential, method, path string, query, form url.Values) ([]byte, string, error) {
	token, err := c.token(credential)
	if err != nil {
		return nil, "", err
	}
	base := strings.TrimRight(strings.TrimSpace(instance.BaseURL), "/")
	if base == "" {
		return nil, "", fmt.Errorf("%w: GitLab instance base URL is required", domain.ErrInvalid)
	}
	if !strings.HasSuffix(base, "/api/v4") {
		base += "/api/v4"
	}
	endpoint := base + "/" + strings.TrimLeft(path, "/")
	if len(query) != 0 {
		endpoint += "?" + query.Encode()
	}
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, "", fmt.Errorf("%w: build GitLab request: %v", domain.ErrInvalid, err)
	}
	req.Header.Set("PRIVATE-TOKEN", token)
	req.Header.Set("Accept", "application/json")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		category := provider.ErrorTransient
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			category = provider.ErrorTimeout
		}
		return nil, "", &provider.ProviderError{Provider: domain.ProviderGitLab, Operation: method + " " + path, Category: category, Err: err}
	}
	defer resp.Body.Close()
	responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if readErr != nil {
		return nil, resp.Header.Get("X-Request-ID"), &provider.ProviderError{Provider: domain.ProviderGitLab, Operation: method + " " + path, Category: provider.ErrorInvalidResponse, RequestID: resp.Header.Get("X-Request-ID"), Err: readErr}
	}
	requestID := resp.Header.Get("X-Request-ID")
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		apiErr := &apiError{Status: resp.StatusCode, RequestID: requestID, Body: string(responseBody)}
		return nil, requestID, &provider.ProviderError{Provider: domain.ProviderGitLab, Operation: method + " " + path, Category: categoryForStatus(resp.StatusCode), RequestID: requestID, Err: apiErr}
	}
	return responseBody, requestID, nil
}

func (c *Client) token(credential *provider.Credential) (string, error) {
	if credential == nil || len(credential.Material) == 0 {
		return "", &provider.ProviderError{Provider: domain.ProviderGitLab, Operation: "authenticate", Category: provider.ErrorUnauthorized, Err: provider.ErrNotConfigured}
	}
	if err := credential.Validate(); err != nil {
		return "", &provider.ProviderError{Provider: domain.ProviderGitLab, Operation: "authenticate", Category: provider.ErrorUnauthorized, Err: err}
	}
	return string(credential.Material), nil
}

func (c *Client) invalidResponse(operation string, err error) error {
	return &provider.ProviderError{Provider: domain.ProviderGitLab, Operation: operation, Category: provider.ErrorInvalidResponse, Err: err}
}

func projectPath(project provider.GitLabProject) string {
	return "/projects/" + url.PathEscape(firstNonEmpty(project.ExternalID, project.FullPath))
}

func hookEvents(events []string) (push, issues bool) {
	for _, event := range events {
		switch strings.ToLower(strings.TrimSpace(event)) {
		case "push", "push hook", "push_events":
			push = true
		case "issue", "issue hook", "issues", "issues_events":
			issues = true
		}
	}
	return push, issues
}

func hookForm(hookURL, token string, push, issues bool) url.Values {
	return url.Values{"url": []string{hookURL}, "token": []string{token}, "push_events": []string{strconv.FormatBool(push)}, "issues_events": []string{strconv.FormatBool(issues)}}
}

func categoryForStatus(status int) provider.ErrorCategory {
	switch status {
	case http.StatusUnauthorized:
		return provider.ErrorUnauthorized
	case http.StatusForbidden:
		return provider.ErrorForbidden
	case http.StatusNotFound:
		return provider.ErrorNotFound
	case http.StatusConflict:
		return provider.ErrorConflict
	case http.StatusTooManyRequests:
		return provider.ErrorRateLimited
	default:
		if status >= 500 {
			return provider.ErrorTransient
		}
		return provider.ErrorInvalidResponse
	}
}

func jsonID(value json.RawMessage) string {
	if len(value) == 0 || string(value) == "null" {
		return ""
	}
	var text string
	if json.Unmarshal(value, &text) == nil {
		return text
	}
	var number json.Number
	if json.Unmarshal(value, &number) == nil {
		return number.String()
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
