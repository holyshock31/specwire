package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	tkSecret    = "test-secret"
	tkProjectID = "3e7d61cd-900b-41a8-85f4-c97019e2020f"
	tkDelivery  = "550e8400-e29b-41d4-a716-446655440000"
)

var (
	tkSigningSecret = "whsec_" + base64.StdEncoding.EncodeToString([]byte(tkSecret))
)

func signHeaders(req *http.Request, body string) {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	msg := tkDelivery + "." + ts + "." + body
	key, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(tkSigningSecret, "whsec_"))
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(msg))
	req.Header.Set("webhook-id", tkDelivery)
	req.Header.Set("webhook-timestamp", ts)
	req.Header.Set("webhook-signature", "v1,"+base64.StdEncoding.EncodeToString(mac.Sum(nil)))
}

func signWith(t *testing.T, secret, body string) map[string]string {
	t.Helper()
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	msg := tkDelivery + "." + ts + "." + body
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
	if err != nil {
		t.Fatalf("decode signing secret: %v", err)
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(msg))
	return map[string]string{
		"webhook-id":        tkDelivery,
		"webhook-timestamp": ts,
		"webhook-signature": "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil)),
	}
}

func fakeMultica(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	script, err := os.ReadFile("scripts/fake-multica")
	if err != nil {
		t.Fatalf("read scripts/fake-multica: %v", err)
	}
	path := filepath.Join(dir, "multica")
	if err := os.WriteFile(path, script, 0o755); err != nil {
		t.Fatalf("write fake multica: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

func newTestHandler(t *testing.T, cliTimeout time.Duration) (*webhookHandler, *Store, string) {
	t.Helper()
	dir := fakeMultica(t)
	argvFile := filepath.Join(dir, "argv")
	stdinFile := filepath.Join(dir, "stdin")
	t.Setenv("FAKE_MULTICA_ARGV_FILE", argvFile)
	t.Setenv("FAKE_MULTICA_STDIN_FILE", stdinFile)

	cfg := &Config{
		AllowedProjects:  map[string]bool{"specwire/specwire-poc": true},
		WebhookSecrets:   []string{tkSigningSecret},
		MulticaProfile:   "specwire-local",
		MulticaProjectID: tkProjectID,
		RefFilter:        "refs/heads/main",
		CLITimeout:       cliTimeout,
	}
	cfgPtr := &atomic.Pointer[Config]{}
	cfgPtr.Store(cfg)
	store := newTestStore(t)
	return &webhookHandler{cfgPtr: cfgPtr, store: store, gitlab: newGitlabClient(cfg)}, store, argvFile
}

func pushPayload(ref, after, project, headMsg string, commits []gitlabCommit) string {
	p := gitlabPushPayload{Ref: ref, After: after, Project: gitlabProject{PathWithNamespace: project}}
	if headMsg != "" {
		p.HeadCommit = &gitlabCommit{ID: after, Message: headMsg}
	}
	p.Commits = commits
	b, _ := json.Marshal(p)
	return string(b)
}

func issueHookPayload(iid int, action string, labels []string, desc string) string {
	p := gitlabIssuePayload{
		ObjectKind: "issue",
		ObjectAttributes: gitlabIssueAttributes{
			IID: iid, Action: action, Description: desc,
		},
		Project: gitlabProject{PathWithNamespace: "specwire/specwire-poc"},
	}
	for _, label := range labels {
		p.ObjectAttributes.Labels = append(p.ObjectAttributes.Labels, gitlabIssueLabel{Title: label})
	}
	b, _ := json.Marshal(p)
	return string(b)
}

const issueDesc = "change_id: issue-change\nbranch: feat/issue-change\nbranch_head_sha: sha-issue-1"

func doRequest(t *testing.T, h *webhookHandler, event, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/gitlab/specwire", strings.NewReader(body))
	req.Header.Set("X-Gitlab-Event", event)
	signHeaders(req, body)
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func doIssue(t *testing.T, h *webhookHandler, body string, headers map[string]string) *httptest.ResponseRecorder {
	return doRequest(t, h, "Issue Hook", body, headers)
}

func doPush(t *testing.T, h *webhookHandler, body string, headers map[string]string) *httptest.ResponseRecorder {
	return doRequest(t, h, "Push Hook", body, headers)
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	s := strings.TrimSuffix(string(b), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func readStdin(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(os.Getenv("FAKE_MULTICA_STDIN_FILE"))
	if err != nil {
		t.Fatalf("read fake stdin: %v", err)
	}
	return string(b)
}

func TestIssueHookCreatesProjectionWithFrozenContext(t *testing.T) {
	h, store, argvFile := newTestHandler(t, 5*time.Second)

	rec := doIssue(t, h, issueHookPayload(42, "open", []string{"change"}, issueDesc), nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "created" {
		t.Fatalf("status=%d body=%q, want 200 created", rec.Code, rec.Body.String())
	}
	argv := readLines(t, argvFile)
	if !slices.Contains(argv, "[SpecWire] issue-change") {
		t.Errorf("argv should contain issue title: %v", argv)
	}
	stdin := readStdin(t)
	for _, want := range []string{
		"repository: specwire/specwire-poc",
		"change_id: issue-change",
		"branch: feat/issue-change",
		"branch_head_sha: sha-issue-1",
		"target_branch: main",
	} {
		if !strings.Contains(stdin, want) {
			t.Errorf("description missing %q: %s", want, stdin)
		}
	}
	for _, forbidden := range []string{"approved_commit_sha", "Agent Instructions", "specwire-workflow skill"} {
		if strings.Contains(stdin, forbidden) {
			t.Errorf("description contains client-only field %q: %s", forbidden, stdin)
		}
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM issue_links WHERE gitlab_project = ? AND issue_iid = 42`, "specwire/specwire-poc").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("issue_links rows=%d, want 1", count)
	}
}

func TestIssueHookAppliesInitialStatusAndAssignee(t *testing.T) {
	h, _, argvFile := newTestHandler(t, 5*time.Second)
	desc := issueDesc + "\nSpecWire-Status: todo\nSpecWire-Assignee: alice"
	if rec := doIssue(t, h, issueHookPayload(42, "open", []string{"change"}, desc), nil); rec.Body.String() != "created" {
		t.Fatalf("body=%q, want created", rec.Body.String())
	}
	args := readLines(t, argvFile)
	for _, want := range []string{"--status", "todo", "--assignee", "alice"} {
		if !slices.Contains(args, want) {
			t.Errorf("argv=%v, want %q", args, want)
		}
	}
}

func TestIssueHookFiltersPublication(t *testing.T) {
	cases := []struct {
		name   string
		action string
		labels []string
		desc   string
	}{
		{"non-change label", "open", []string{"bug"}, issueDesc},
		{"non-open action", "update", []string{"change"}, issueDesc},
		{"missing fields", "open", []string{"change"}, "change_id: only-id"},
		{"invalid status", "open", []string{"change"}, issueDesc + "\nSpecWire-Status: nonsense"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, _, argvFile := newTestHandler(t, 5*time.Second)
			rec := doIssue(t, h, issueHookPayload(42, tc.action, tc.labels, tc.desc), nil)
			if rec.Code != http.StatusOK || rec.Body.String() != "ignored" {
				t.Fatalf("status=%d body=%q, want 200 ignored", rec.Code, rec.Body.String())
			}
			if argv, err := os.ReadFile(argvFile); err == nil && len(argv) != 0 {
				t.Errorf("Multica should not be called: %q", argv)
			}
		})
	}
}

func TestPushProposalIsIgnored(t *testing.T) {
	h, store, argvFile := newTestHandler(t, 5*time.Second)
	body := pushPayload("refs/heads/main", "sha-proposal", "specwire/specwire-poc",
		"spec(x): publish\n\nSpecWire-Event: proposal-ready\nSpecWire-Change: x", nil)
	rec := doPush(t, h, body, nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "ignored" {
		t.Fatalf("status=%d body=%q, want 200 ignored", rec.Code, rec.Body.String())
	}
	var events, links int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM issue_links`).Scan(&links); err != nil {
		t.Fatal(err)
	}
	if events != 0 || links != 0 {
		t.Errorf("proposal push caused side effects: events=%d links=%d", events, links)
	}
	if argv, err := os.ReadFile(argvFile); err == nil && len(argv) != 0 {
		t.Errorf("Multica should not be called: %q", argv)
	}
}

func TestPushArchiveCompletesProjectionAndClosesIssue(t *testing.T) {
	var closed []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		closed = append(closed, r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	h, _, argvFile := newTestHandler(t, 5*time.Second)
	h.gitlab = newGitlabClient(&Config{GitLabToken: "glpat-test", GitLabURL: srv.URL})
	if rec := doIssue(t, h, issueHookPayload(42, "open", []string{"change"}, issueDesc), nil); rec.Body.String() != "created" {
		t.Fatalf("issue publication body=%q, want created", rec.Body.String())
	}

	archive := pushPayload("refs/heads/main", "sha-archive", "specwire/specwire-poc",
		"spec(issue-change): archive\n\nSpecWire-Event: archived\nSpecWire-Change: issue-change", nil)
	rec := doPush(t, h, archive, nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "ignored" {
		t.Fatalf("archive status=%d body=%q, want 200 ignored", rec.Code, rec.Body.String())
	}
	wantArgs := []string{"--profile", "specwire-local", "issue", "status", "WW1-fake-123", "done"}
	if got := readLines(t, argvFile); !slices.Equal(got, wantArgs) {
		t.Errorf("argv=%v, want %v", got, wantArgs)
	}
	if len(closed) != 1 || !strings.Contains(closed[0], "/issues/42") {
		t.Errorf("GitLab close calls=%v, want issue 42", closed)
	}
}

func TestArchivedWithoutProjectionDoesNotCreateCard(t *testing.T) {
	h, _, argvFile := newTestHandler(t, 5*time.Second)
	body := pushPayload("refs/heads/main", "sha-archive", "specwire/specwire-poc",
		"spec(x): archive\n\nSpecWire-Event: archived\nSpecWire-Change: never-created", nil)
	rec := doPush(t, h, body, nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "ignored" {
		t.Fatalf("status=%d body=%q, want 200 ignored", rec.Code, rec.Body.String())
	}
	if argv, err := os.ReadFile(argvFile); err == nil && len(argv) != 0 {
		t.Errorf("Multica should not be called for unknown change: %q", argv)
	}
}

func TestArchiveCompletesWithoutGitLabCredentials(t *testing.T) {
	h, _, argvFile := newTestHandler(t, 5*time.Second)
	if rec := doIssue(t, h, issueHookPayload(42, "open", []string{"change"}, issueDesc), nil); rec.Body.String() != "created" {
		t.Fatalf("issue publication body=%q, want created", rec.Body.String())
	}
	body := pushPayload("refs/heads/main", "sha-archive", "specwire/specwire-poc",
		"archive\n\nSpecWire-Event: archived\nSpecWire-Change: issue-change", nil)
	rec := doPush(t, h, body, nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "ignored" {
		t.Fatalf("status=%d body=%q, want 200 ignored", rec.Code, rec.Body.String())
	}
	wantArgs := []string{"--profile", "specwire-local", "issue", "status", "WW1-fake-123", "done"}
	if got := readLines(t, argvFile); !slices.Equal(got, wantArgs) {
		t.Errorf("argv=%v, want %v", got, wantArgs)
	}
}

func TestArchivedPushCanCompleteMultipleChanges(t *testing.T) {
	h, _, _ := newTestHandler(t, 5*time.Second)
	changes := []struct {
		iid    int
		change string
		sha    string
	}{
		{7, "change-a", "sha-a"},
		{8, "change-b", "sha-b"},
	}
	for _, change := range changes {
		desc := fmt.Sprintf("change_id: %s\nbranch: feat/%s\nbranch_head_sha: %s", change.change, change.change, change.sha)
		if rec := doIssue(t, h, issueHookPayload(change.iid, "open", []string{"change"}, desc), nil); rec.Body.String() != "created" {
			t.Fatalf("issue %d body=%q, want created", change.iid, rec.Body.String())
		}
	}
	commits := []gitlabCommit{
		{ID: "sha-archive-a", Message: "archive a\n\nSpecWire-Event: archived\nSpecWire-Change: change-a"},
		{ID: "sha-archive-b", Message: "archive b\n\nSpecWire-Event: archived\nSpecWire-Change: change-b"},
	}
	rec := doPush(t, h, pushPayload("refs/heads/main", "sha-head", "specwire/specwire-poc", "", commits), nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "ignored" {
		t.Fatalf("status=%d body=%q, want 200 ignored", rec.Code, rec.Body.String())
	}
}

func TestIssueHookReplayAndCorrelationRepair(t *testing.T) {
	h, store, _ := newTestHandler(t, 5*time.Second)
	body := issueHookPayload(42, "open", []string{"change"}, issueDesc)
	if rec := doIssue(t, h, body, nil); rec.Body.String() != "created" {
		t.Fatalf("first body=%q, want created", rec.Body.String())
	}
	if _, err := store.db.Exec(`DELETE FROM issue_links`); err != nil {
		t.Fatal(err)
	}
	if rec := doIssue(t, h, body, nil); rec.Body.String() != "duplicate" {
		t.Fatalf("replay body=%q, want duplicate", rec.Body.String())
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM issue_links`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("repaired links=%d, want 1", count)
	}
}

func TestIssueHookReplayIsIdempotentConcurrently(t *testing.T) {
	h, store, _ := newTestHandler(t, 5*time.Second)
	body := issueHookPayload(42, "open", []string{"change"}, issueDesc)
	const n = 10
	results := make([]string, n)
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rec := doIssue(t, h, body, map[string]string{"X-Gitlab-Delivery": fmt.Sprintf("delivery-%d", i)})
			results[i], codes[i] = rec.Body.String(), rec.Code
		}(i)
	}
	wg.Wait()
	created := 0
	for i := 0; i < n; i++ {
		if codes[i] != http.StatusOK {
			t.Fatalf("request %d status=%d body=%q", i, codes[i], results[i])
		}
		if results[i] == "created" {
			created++
		} else if results[i] != "duplicate" {
			t.Fatalf("request %d body=%q, want created|duplicate", i, results[i])
		}
	}
	if created != 1 {
		t.Fatalf("created=%d, want exactly 1", created)
	}
	var events, links int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM events WHERE state = ?`, StateCreated).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM issue_links`).Scan(&links); err != nil {
		t.Fatal(err)
	}
	if events != 1 || links != 1 {
		t.Errorf("events=%d links=%d, want 1/1", events, links)
	}
}

func TestIssueHookFailedCreationCanRetry(t *testing.T) {
	h, store, _ := newTestHandler(t, 5*time.Second)
	body := issueHookPayload(42, "open", []string{"change"}, issueDesc)
	t.Setenv("FAKE_MULTICA_EXIT_CODE", "1")
	if rec := doIssue(t, h, body, nil); rec.Code != http.StatusBadGateway {
		t.Fatalf("failed status=%d, want 502", rec.Code)
	}
	t.Setenv("FAKE_MULTICA_EXIT_CODE", "0")
	if rec := doIssue(t, h, body, nil); rec.Body.String() != "created" {
		t.Fatalf("retry body=%q, want created", rec.Body.String())
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("events=%d, want 1", count)
	}
}

func TestDifferentFrozenRevisionDoesNotOverwriteExistingProjection(t *testing.T) {
	h, store, _ := newTestHandler(t, 5*time.Second)
	first := issueHookPayload(42, "open", []string{"change"}, issueDesc)
	if rec := doIssue(t, h, first, nil); rec.Body.String() != "created" {
		t.Fatalf("first body=%q, want created", rec.Body.String())
	}
	secondDesc := "change_id: issue-change\nbranch: feat/issue-change\nbranch_head_sha: sha-issue-2"
	if rec := doIssue(t, h, issueHookPayload(43, "open", []string{"change"}, secondDesc), nil); rec.Body.String() != "created" {
		t.Fatalf("second body=%q, want created independent projection", rec.Body.String())
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM events WHERE project_path = ? AND change_id = ?`, "specwire/specwire-poc", "issue-change").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("events=%d, want 2 independent frozen revisions", count)
	}
}

func TestProjectMappingAndAllowlist(t *testing.T) {
	t.Run("mapped project", func(t *testing.T) {
		h, _, argvFile := newTestHandler(t, 5*time.Second)
		h.cfg().AllowedProjects["personal/webdeck"] = true
		h.cfg().ProjectMap = map[string]string{"personal/webdeck": "mapped-project"}
		desc := "change_id: mapped\nbranch: feat/mapped\nbranch_head_sha: sha-mapped"
		body := issueHookPayload(42, "open", []string{"change"}, desc)
		body = strings.Replace(body, "specwire/specwire-poc", "personal/webdeck", 1)
		if rec := doIssue(t, h, body, nil); rec.Body.String() != "created" {
			t.Fatalf("body=%q, want created", rec.Body.String())
		}
		args := readLines(t, argvFile)
		if i := slices.Index(args, "--project"); i < 0 || args[i+1] != "mapped-project" {
			t.Errorf("args=%v, want mapped project", args)
		}
	})
	t.Run("missing mapping", func(t *testing.T) {
		h, _, _ := newTestHandler(t, 5*time.Second)
		h.cfg().AllowedProjects["personal/webdeck"] = true
		h.cfg().ProjectMap = map[string]string{"specwire/specwire-poc": tkProjectID}
		body := issueHookPayload(42, "open", []string{"change"}, issueDesc)
		body = strings.Replace(body, "specwire/specwire-poc", "personal/webdeck", 1)
		if rec := doIssue(t, h, body, nil); rec.Code != http.StatusBadGateway {
			t.Fatalf("status=%d, want 502", rec.Code)
		}
	})
	t.Run("not allowlisted", func(t *testing.T) {
		h, _, _ := newTestHandler(t, 5*time.Second)
		body := issueHookPayload(42, "open", []string{"change"}, issueDesc)
		body = strings.Replace(body, "specwire/specwire-poc", "other/repo", 1)
		if rec := doIssue(t, h, body, nil); rec.Body.String() != "ignored" {
			t.Fatalf("body=%q, want ignored", rec.Body.String())
		}
	})
}

func TestWebhookSignatureFilters(t *testing.T) {
	t.Run("wrong signature", func(t *testing.T) {
		h, _, _ := newTestHandler(t, 5*time.Second)
		rec := doIssue(t, h, issueHookPayload(42, "open", []string{"change"}, issueDesc), map[string]string{"webhook-signature": "v1,AAAA"})
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status=%d, want 401", rec.Code)
		}
	})
	t.Run("multiple configured tokens", func(t *testing.T) {
		h, _, _ := newTestHandler(t, 5*time.Second)
		h.cfg().WebhookSecrets = []string{tkSigningSecret, tkSigningSecretB}
		body := issueHookPayload(42, "open", []string{"change"}, issueDesc)
		if rec := doIssue(t, h, body, signWith(t, tkSigningSecretB, body)); rec.Body.String() != "created" {
			t.Fatalf("body=%q, want created", rec.Body.String())
		}
	})
	t.Run("none configured tokens match", func(t *testing.T) {
		h, store, _ := newTestHandler(t, 5*time.Second)
		h.cfg().WebhookSecrets = []string{tkSigningSecret, tkSigningSecretB}
		body := issueHookPayload(42, "open", []string{"change"}, issueDesc)
		if rec := doIssue(t, h, body, signWith(t, tkSigningSecretC, body)); rec.Code != http.StatusUnauthorized {
			t.Fatalf("status=%d, want 401", rec.Code)
		}
		var count int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Errorf("events=%d, want no side effects", count)
		}
	})
}

func TestWebhookFiltersPushAndInvalidPayloads(t *testing.T) {
	h, _, _ := newTestHandler(t, 5*time.Second)
	cases := []struct {
		name  string
		event string
		body  string
	}{
		{"non-push hook", "Merge Request Hook", "{}"},
		{"non-target branch", "Push Hook", pushPayload("refs/heads/feature/x", "sha", "specwire/specwire-poc", "docs: update", nil)},
		{"deleted branch", "Push Hook", pushPayload("refs/heads/main", strings.Repeat("0", 40), "specwire/specwire-poc", "docs: update", nil)},
		{"no archive", "Push Hook", pushPayload("refs/heads/main", "sha", "specwire/specwire-poc", "docs: update", nil)},
		{"invalid json", "Push Hook", "not-json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doRequest(t, h, tc.event, tc.body, nil)
			if rec.Code != http.StatusOK || rec.Body.String() != "ignored" {
				t.Fatalf("status=%d body=%q, want 200 ignored", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestHandlerNoShellInjectionAndTimeout(t *testing.T) {
	t.Run("literal publication fields", func(t *testing.T) {
		h, _, argvFile := newTestHandler(t, 5*time.Second)
		pwned := filepath.Join(t.TempDir(), "pwned")
		malicious := fmt.Sprintf("$(touch %s);$(id)", pwned)
		desc := "change_id: " + malicious + "\nbranch: feat/safe\nbranch_head_sha: sha-safe"
		if rec := doIssue(t, h, issueHookPayload(42, "open", []string{"change"}, desc), nil); rec.Body.String() != "created" {
			t.Fatalf("body=%q, want created", rec.Body.String())
		}
		if !slices.Contains(readLines(t, argvFile), "[SpecWire] "+malicious) {
			t.Errorf("argv did not preserve literal change_id: %v", readLines(t, argvFile))
		}
		if _, err := os.Stat(pwned); err == nil {
			t.Fatal("shell injection executed")
		}
	})
	t.Run("timeout is retryable", func(t *testing.T) {
		h, store, _ := newTestHandler(t, time.Second)
		t.Setenv("FAKE_MULTICA_DELAY", "5")
		if rec := doIssue(t, h, issueHookPayload(42, "open", []string{"change"}, issueDesc), nil); rec.Code != http.StatusBadGateway {
			t.Fatalf("status=%d, want 502", rec.Code)
		}
		var state string
		if err := store.db.QueryRow(`SELECT state FROM events`).Scan(&state); err != nil {
			t.Fatal(err)
		}
		if state != StateError {
			t.Errorf("state=%q, want error", state)
		}
	})
}

func TestHandlerInvalidJSONAndEmptyBody(t *testing.T) {
	h, _, _ := newTestHandler(t, 5*time.Second)
	for _, body := range []string{"not-json", ""} {
		rec := doPush(t, h, body, nil)
		if rec.Code != http.StatusOK || rec.Body.String() != "ignored" {
			t.Fatalf("body=%q: status=%d response=%q, want ignored", body, rec.Code, rec.Body.String())
		}
	}
}
