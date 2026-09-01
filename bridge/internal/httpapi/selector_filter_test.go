package httpapi

import (
	"testing"

	"specwire/bridge/internal/domain"
	"specwire/bridge/internal/provider"
)

func TestSelectorBool(t *testing.T) {
	for _, test := range []struct {
		name string
		raw  string
		want bool
	}{
		{name: "empty", raw: "", want: false},
		{name: "true", raw: "true", want: true},
		{name: "one", raw: "1", want: true},
		{name: "false", raw: "false", want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := selectorBool(test.raw, "exclude_bound")
			if err != nil {
				t.Fatalf("selectorBool(%q): %v", test.raw, err)
			}
			if got != test.want {
				t.Fatalf("selectorBool(%q) = %v, want %v", test.raw, got, test.want)
			}
		})
	}
	if _, err := selectorBool("not-a-bool", "exclude_bound"); err == nil {
		t.Fatal("selectorBool accepted an invalid value")
	}
}

func TestSelectorProjectFiltersExcludeOnlyActiveWorkspaceBindings(t *testing.T) {
	connections := []domain.Connection{
		{
			Status:               domain.ConnectionReady,
			SourceGitLabProject:  domain.ProviderProjectRef{InstanceID: "gitlab-1", ExternalID: "source-bound"},
			TargetMulticaProject: domain.ProviderProjectRef{InstanceID: "multica-1", ExternalID: "target-bound"},
		},
		{
			Status:               domain.ConnectionDisabled,
			SourceGitLabProject:  domain.ProviderProjectRef{InstanceID: "gitlab-1", ExternalID: "source-released"},
			TargetMulticaProject: domain.ProviderProjectRef{InstanceID: "multica-1", ExternalID: "target-released"},
		},
		{
			Status:               domain.ConnectionReady,
			SourceGitLabProject:  domain.ProviderProjectRef{InstanceID: "gitlab-2", ExternalID: "source-other-instance"},
			TargetMulticaProject: domain.ProviderProjectRef{InstanceID: "multica-2", ExternalID: "target-other-instance"},
		},
	}

	gitlab := filterBoundGitLabProjects([]provider.GitLabProject{
		{InstanceID: "gitlab-1", ExternalID: "source-bound", FullPath: "platform/bound"},
		{InstanceID: "gitlab-1", ExternalID: "source-released", FullPath: "platform/released"},
		{InstanceID: "gitlab-1", ExternalID: "source-new", FullPath: "platform/new"},
	}, connections, "gitlab-1")
	if len(gitlab) != 2 || gitlab[0].ExternalID != "source-released" || gitlab[1].ExternalID != "source-new" {
		t.Fatalf("filtered GitLab projects = %+v", gitlab)
	}

	multica := filterBoundMulticaProjects([]domain.MulticaProjectRef{
		{InstanceID: "multica-1", ExternalID: "target-bound", Title: "Bound"},
		{InstanceID: "multica-1", ExternalID: "target-released", Title: "Released"},
		{InstanceID: "multica-1", ExternalID: "target-new", Title: "New"},
	}, connections, "multica-1")
	if len(multica) != 2 || multica[0].ExternalID != "target-released" || multica[1].ExternalID != "target-new" {
		t.Fatalf("filtered Multica projects = %+v", multica)
	}
}
