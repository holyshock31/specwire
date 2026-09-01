package controlplane

import (
	"context"
	"fmt"

	"specwire/bridge/internal/domain"
	"specwire/bridge/internal/provider"
)

// ProviderEndpointProbe adapts the provider contracts to the endpoint
// capability API.  It intentionally performs a small read-only operation;
// capability records are metadata and never contain credential material.
type ProviderEndpointProbe struct {
	gitlab  provider.GitLab
	multica provider.Multica
	vault   CredentialResolver
}

func NewProviderEndpointProbe(gitlab provider.GitLab, multica provider.Multica, vault CredentialResolver) (*ProviderEndpointProbe, error) {
	if gitlab == nil || multica == nil {
		return nil, fmt.Errorf("%w: provider endpoint probe dependencies are required", domain.ErrInvalid)
	}
	return &ProviderEndpointProbe{gitlab: gitlab, multica: multica, vault: vault}, nil
}

func (p *ProviderEndpointProbe) ProbeGitLab(ctx context.Context, instance domain.GitLabInstance) ([]domain.CapabilityResult, error) {
	credential, cleanup, err := resolveProbeCredential(ctx, p.vault, instance.CredentialRef)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	if _, err := p.gitlab.ListGroups(ctx, instance, "", credential); err != nil {
		return nil, err
	}
	return []domain.CapabilityResult{{Capability: "gitlab.groups.read", Available: true}}, nil
}

// ProbeGitLabCredential is used while configuring the instance-level
// discovery credential, before the instance has a persisted SecretRef. The
// caller owns the material and CredentialService clears it after the probe.
func (p *ProviderEndpointProbe) ProbeGitLabCredential(ctx context.Context, instance domain.GitLabInstance, material []byte) ([]domain.CapabilityResult, error) {
	if len(material) == 0 {
		return nil, fmt.Errorf("%w: GitLab credential material is required", domain.ErrInvalid)
	}
	credential := &provider.Credential{Ref: domain.SecretRef{ID: domain.ID("credential-probe"), WorkspaceID: instance.WorkspaceID, Alias: "credential-probe", Kind: domain.SecretGroupCredential}, Material: material}
	if _, err := p.gitlab.ListGroups(ctx, instance, "", credential); err != nil {
		return nil, err
	}
	return []domain.CapabilityResult{{Capability: "gitlab.groups.read", Available: true}}, nil
}

// ProbeGitLabGroup verifies the capabilities that can be observed without a
// write-side permission probe.  GitLab does not expose a portable endpoint
// for testing webhook-write permission, so that capability remains
// provider-side and is reported when the hook reconciliation actually runs.
func (p *ProviderEndpointProbe) ProbeGitLabGroup(ctx context.Context, instance domain.GitLabInstance, binding domain.GitLabGroupBinding, material []byte) ([]domain.CapabilityResult, error) {
	if len(material) == 0 {
		return nil, fmt.Errorf("%w: Group credential material is required", domain.ErrInvalid)
	}
	credential := &provider.Credential{Ref: domain.SecretRef{ID: domain.ID("credential-probe"), WorkspaceID: binding.WorkspaceID, Alias: "credential-probe", Kind: domain.SecretGroupCredential}, Material: material}
	group := provider.GitLabGroup{InstanceID: instance.ID, ExternalID: binding.ExternalGroupID, FullPath: binding.FullPath}
	if _, err := p.gitlab.ListProjects(ctx, instance, group, "", credential); err != nil {
		return nil, err
	}
	return []domain.CapabilityResult{
		{Capability: "gitlab.groups.read", Available: true},
		{Capability: "gitlab.projects.read", Available: true},
	}, nil
}

func (p *ProviderEndpointProbe) ProbeMultica(ctx context.Context, instance domain.MulticaInstance) ([]domain.CapabilityResult, error) {
	readiness, err := p.multica.ProbeReadiness(ctx, instance)
	if err != nil {
		return nil, err
	}
	return []domain.CapabilityResult{{Capability: "multica.api", Available: readiness.Ready, Reason: readiness.Reason, RequestID: readiness.RequestID}}, nil
}

func resolveProbeCredential(ctx context.Context, vault CredentialResolver, ref *domain.SecretRef) (*provider.Credential, func(), error) {
	if ref == nil {
		return nil, func() {}, nil
	}
	if vault == nil {
		return nil, func() {}, fmt.Errorf("%w: credential resolver is not configured", domain.ErrInvalid)
	}
	material, err := vault.Resolve(ctx, *ref)
	if err != nil {
		return nil, func() {}, err
	}
	return &provider.Credential{Ref: *ref, Material: material}, func() { clearBytes(material) }, nil
}
