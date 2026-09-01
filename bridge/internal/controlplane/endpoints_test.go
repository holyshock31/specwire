package controlplane

import (
	"context"
	"testing"

	"specwire/bridge/internal/domain"
	"specwire/bridge/internal/store"
)

type fakeEndpointProbe struct{}

func (fakeEndpointProbe) ProbeGitLab(context.Context, domain.GitLabInstance) ([]domain.CapabilityResult, error) {
	return []domain.CapabilityResult{{Capability: "gitlab.read", Available: true}}, nil
}
func (fakeEndpointProbe) ProbeMultica(context.Context, domain.MulticaInstance) ([]domain.CapabilityResult, error) {
	return []domain.CapabilityResult{{Capability: "multica.read", Available: true}}, nil
}

func TestEndpointServiceSupportsMultipleProfilesAndOptionalMulticaCredential(t *testing.T) {
	s, err := store.Open(t.TempDir() + "/endpoints.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.CreateWorkspace(ctx, domain.Workspace{ID: "workspace-endpoints", Slug: "endpoints", Name: "Endpoints", Status: domain.WorkspaceActive}); err != nil {
		t.Fatal(err)
	}
	service, err := NewEndpointService(s, fakeEndpointProbe{})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []domain.GitLabInstance{
		{ID: "gitlab-one", WorkspaceID: "workspace-endpoints", Name: "Primary", BaseURL: "https://gitlab.example.test", ExternalID: "physical-gitlab"},
		{ID: "gitlab-two", WorkspaceID: "workspace-endpoints", Name: "Secondary", BaseURL: "https://gitlab.example.test", ExternalID: "physical-gitlab-secondary"},
	} {
		if err := service.AddGitLab(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	if err := service.AddMultica(ctx, domain.MulticaInstance{ID: "multica-one", WorkspaceID: "workspace-endpoints", Name: "Multica", BaseURL: "https://multica.example.test"}); err != nil {
		t.Fatal(err)
	}
	gitlab, err := s.ListGitLabInstances(ctx, "workspace-endpoints")
	if err != nil || len(gitlab) != 2 {
		t.Fatalf("GitLab profiles = %d, %v", len(gitlab), err)
	}
	multica, err := s.ListMulticaInstances(ctx, "workspace-endpoints")
	if err != nil || len(multica) != 1 {
		t.Fatalf("Multica profiles = %d, %v", len(multica), err)
	}
	if multica[0].ManagementCredentialRef != nil {
		t.Fatal("optional Multica credential was implicitly requested")
	}
	capabilities, err := service.TestGitLab(ctx, gitlab[0])
	if err != nil || !capabilities[0].Available {
		t.Fatalf("GitLab probe = %+v, %v", capabilities, err)
	}
	if err := service.DisableGitLab(ctx, "workspace-endpoints", "gitlab-two"); err != nil {
		t.Fatal(err)
	}
	remaining, err := s.ListGitLabInstances(ctx, "workspace-endpoints")
	if err != nil || remaining[1].Status != domain.EndpointDisabled {
		t.Fatalf("disabled endpoint list = %+v, %v", remaining, err)
	}
}
