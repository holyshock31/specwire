package gitlab

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"specwire/bridge/internal/domain"
	"specwire/bridge/internal/provider"
)

func TestClientListsProjectsAndUsesProviderIDs(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("X-Request-ID", "request-1")
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/groups"):
			_, _ = w.Write([]byte(`[{"id":17,"full_path":"platform","name":"Platform"}]`))
		case strings.HasSuffix(r.URL.Path, "/projects"):
			_, _ = w.Write([]byte(`[{"id":42,"path_with_namespace":"platform/webdeck","name":"WebDeck","web_url":"https://gitlab.example/platform/webdeck","ssh_url_to_repo":"git@gitlab.example:platform/webdeck.git","http_url_to_repo":"https://gitlab.example/platform/webdeck.git","namespace":{"id":17}}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := NewClient(server.Client(), "glpat-test")
	instance := domain.GitLabInstance{ID: "gitlab-1", BaseURL: server.URL}
	groups, err := client.ListGroups(context.Background(), instance, "plat", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || groups[0].ExternalID != "17" || groups[0].FullPath != "platform" {
		t.Fatalf("groups = %+v", groups)
	}
	projects, err := client.ListProjects(context.Background(), instance, groups[0], "web", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].ExternalID != "42" || projects[0].FullPath != "platform/webdeck" || projects[0].SSHURL == "" {
		t.Fatalf("projects = %+v", projects)
	}
	if len(paths) != 2 || paths[0] != "/api/v4/groups" || paths[1] != "/api/v4/groups/17/projects" {
		t.Fatalf("request paths = %v", paths)
	}
}

func TestClientEnsuresLabelAndSharedHook(t *testing.T) {
	var labelCreates, hookCreates, hookUpdates int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-ID", "request-2")
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/labels"):
			if labelCreates == 0 {
				_, _ = w.Write([]byte(`[]`))
			} else {
				_, _ = w.Write([]byte(`[{"id":9,"name":"change"}]`))
			}
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/labels"):
			labelCreates++
			_, _ = w.Write([]byte(`{"id":9,"name":"change"}`))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/hooks"):
			_, _ = w.Write([]byte(`[{"id":11,"url":"https://specwire.example/hook","push_events":true,"issues_events":true}]`))
		case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/hooks/11"):
			hookUpdates++
			_, _ = w.Write([]byte(`{"id":11}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/hooks"):
			hookCreates++
			_, _ = w.Write([]byte(`{"id":12}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := NewClient(server.Client(), "glpat-test")
	instance := domain.GitLabInstance{ID: "gitlab-1", BaseURL: server.URL}
	project := provider.GitLabProject{InstanceID: instance.ID, ExternalID: "42", FullPath: "platform/webdeck"}
	label, err := client.EnsureLabel(context.Background(), instance, project, "change", nil)
	if err != nil || !label.Created || label.ExternalID != "9" {
		t.Fatalf("created label = %+v, %v", label, err)
	}
	label, err = client.EnsureLabel(context.Background(), instance, project, "change", nil)
	if err != nil || !label.Adopted || label.Created {
		t.Fatalf("adopted label = %+v, %v", label, err)
	}
	hook, err := client.EnsureHook(context.Background(), instance, project, provider.HookSpec{URL: "https://specwire.example/hook", Events: []string{"Issue Hook", "Push Hook"}, SigningToken: []byte("whsec-test")}, nil)
	if err != nil || !hook.Adopted || hook.ExternalID != "11" || hookCreates != 0 || hookUpdates != 1 {
		t.Fatalf("updated hook = %+v, creates=%d updates=%d err=%v", hook, hookCreates, hookUpdates, err)
	}
	if _, err := json.Marshal(hook); err != nil {
		t.Fatal(err)
	}
}

func TestClientMapsGitLabErrorsAndRequiresCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"forbidden"}`))
	}))
	defer server.Close()
	client := NewClient(server.Client(), "glpat-test")
	instance := domain.GitLabInstance{ID: "gitlab-1", BaseURL: server.URL}
	_, err := client.GetProject(context.Background(), instance, "group/project", nil)
	if err == nil || !provider.IsRetryable(err) && !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("forbidden error = %v", err)
	}
	var providerErr *provider.ProviderError
	if !errors.As(err, &providerErr) || providerErr.Category != provider.ErrorForbidden {
		t.Fatalf("provider error = %#v", providerErr)
	}
	noCredential := NewClient(server.Client(), "")
	if _, err := noCredential.GetProject(context.Background(), instance, "group/project", nil); err == nil {
		t.Fatal("missing GitLab credential was accepted")
	}
}
