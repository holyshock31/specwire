package controlplane

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"specwire/bridge/internal/domain"
	"specwire/bridge/internal/security"
)

var (
	ErrCapabilityUnavailable = errors.New("provider capability unavailable")
	ErrProviderTransient     = errors.New("provider transient failure")
)

type GroupCredentialProbe interface {
	ProbeGitLabGroup(context.Context, domain.GitLabInstance, domain.GitLabGroupBinding, []byte) ([]domain.CapabilityResult, error)
}

// GitLabInstanceCredentialProbe verifies the Workspace-level credential used
// to discover Groups before a Group binding exists. A Group credential can
// later override this reference for narrower project/onboarding access.
type GitLabInstanceCredentialProbe interface {
	ProbeGitLabCredential(context.Context, domain.GitLabInstance, []byte) ([]domain.CapabilityResult, error)
}

type CredentialStore interface {
	CreateCredentialProfile(context.Context, domain.CredentialProfile) error
	GetCredentialProfile(context.Context, domain.ID, domain.ID) (domain.CredentialProfile, error)
	RotateCredentialProfile(context.Context, domain.ID, domain.ID, domain.SecretRef) error
	CreateGitLabGroupBinding(context.Context, domain.GitLabGroupBinding) error
	GetGitLabGroupBinding(context.Context, domain.ID, domain.ID) (domain.GitLabGroupBinding, error)
	UpdateGitLabGroupCredential(context.Context, domain.ID, domain.ID, domain.ID, *domain.SecretRef) error
	UpdateGitLabInstanceCredential(context.Context, domain.ID, domain.ID, *domain.SecretRef) error
	RecordCapabilityResults(context.Context, domain.ID, domain.ProviderKind, domain.ID, string, string, []domain.CapabilityResult) error
}

type CredentialService struct {
	store         CredentialStore
	vault         *security.Vault
	probe         GroupCredentialProbe
	instanceProbe GitLabInstanceCredentialProbe
}

func NewCredentialService(store CredentialStore, vault *security.Vault, probe GroupCredentialProbe) (*CredentialService, error) {
	if store == nil || vault == nil || probe == nil {
		return nil, fmt.Errorf("%w: credential service dependencies are required", domain.ErrInvalid)
	}
	service := &CredentialService{store: store, vault: vault, probe: probe}
	if instanceProbe, ok := probe.(GitLabInstanceCredentialProbe); ok {
		service.instanceProbe = instanceProbe
	}
	return service, nil
}

// SetGitLabInstanceProbe wires the probe used by the pre-Group discovery
// credential endpoint. It is separate from the Group probe so unit tests and
// alternate adapters can implement only the credential scopes they support.
func (s *CredentialService) SetGitLabInstanceProbe(probe GitLabInstanceCredentialProbe) {
	s.instanceProbe = probe
}

type CapabilityError struct {
	Missing []string
	Reason  string
}

func (e *CapabilityError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("%v: %s", ErrCapabilityUnavailable, e.Reason)
	}
	return fmt.Sprintf("%v: missing %s", ErrCapabilityUnavailable, strings.Join(e.Missing, ", "))
}

func (e *CapabilityError) Unwrap() error { return ErrCapabilityUnavailable }

type ProviderTransientError struct{ Err error }

func (e *ProviderTransientError) Error() string {
	return fmt.Sprintf("%v: %v", ErrProviderTransient, e.Err)
}
func (e *ProviderTransientError) Unwrap() error { return ErrProviderTransient }

// BindGitLabInstanceCredential stores and verifies the Workspace-level
// discovery credential, then attaches only its SecretRef to the GitLab
// instance. The material is never returned or persisted in an API/domain
// object. This is intentionally distinct from BindGroupCredential: the
// instance reference is what makes the initial Group selector usable.
func (s *CredentialService) BindGitLabInstanceCredential(ctx context.Context, instance domain.GitLabInstance, alias string, kind domain.CredentialProfileKind, ref domain.SecretRef, material []byte, requiredCapabilities []string) (domain.SecretRef, []domain.CapabilityResult, error) {
	if err := validateInstanceCredentialInput(instance, ref, alias, kind); err != nil {
		return domain.SecretRef{}, nil, err
	}
	if s.instanceProbe == nil {
		return domain.SecretRef{}, nil, fmt.Errorf("%w: GitLab instance credential probe is not configured", domain.ErrInvalid)
	}
	if len(material) == 0 {
		return domain.SecretRef{}, nil, fmt.Errorf("%w: GitLab instance credential material is required", domain.ErrInvalid)
	}
	if len(requiredCapabilities) == 0 {
		requiredCapabilities = []string{"gitlab.groups.read"}
	}
	if err := s.vault.Put(ctx, ref, material); err != nil {
		return domain.SecretRef{}, nil, err
	}
	defer clear(material)
	results, err := s.instanceProbe.ProbeGitLabCredential(ctx, instance, material)
	if err != nil {
		return domain.SecretRef{}, nil, fmt.Errorf("probe GitLab instance credential: %w", err)
	}
	if err := s.store.RecordCapabilityResults(ctx, instance.WorkspaceID, domain.ProviderGitLab, instance.ID, "gitlab_instance", string(instance.ID), results); err != nil {
		return domain.SecretRef{}, nil, err
	}
	if err := requireCapabilities(results, requiredCapabilities, "configured GitLab instance credential lacks required permissions"); err != nil {
		return domain.SecretRef{}, nil, err
	}
	if err := s.store.UpdateGitLabInstanceCredential(ctx, instance.WorkspaceID, instance.ID, &ref); err != nil {
		return domain.SecretRef{}, nil, err
	}
	return ref, results, nil
}

func validateInstanceCredentialInput(instance domain.GitLabInstance, ref domain.SecretRef, alias string, kind domain.CredentialProfileKind) error {
	if instance.WorkspaceID.Empty() || instance.ID.Empty() {
		return fmt.Errorf("%w: GitLab instance workspace and ID are required", domain.ErrInvalid)
	}
	if strings.TrimSpace(alias) == "" {
		return fmt.Errorf("%w: credential alias is required", domain.ErrInvalid)
	}
	if kind != domain.CredentialPAT && kind != domain.CredentialGroupAccessToken {
		return fmt.Errorf("%w: unsupported GitLab credential kind", domain.ErrInvalid)
	}
	if ref.Kind != domain.SecretGroupCredential {
		return fmt.Errorf("%w: GitLab credential must use group_credential secret kind", domain.ErrInvalid)
	}
	if ref.Alias != strings.TrimSpace(alias) {
		return fmt.Errorf("%w: credential alias and secret reference alias must match", domain.ErrInvalid)
	}
	if ref.WorkspaceID != instance.WorkspaceID {
		return fmt.Errorf("%w: credential belongs to another workspace", domain.ErrForbidden)
	}
	return ref.Validate()
}

func (s *CredentialService) BindGroupCredential(ctx context.Context, instance domain.GitLabInstance, binding domain.GitLabGroupBinding, profileID domain.ID, alias string, kind domain.CredentialProfileKind, ref domain.SecretRef, material []byte, requiredCapabilities []string) (domain.CredentialProfile, error) {
	if err := validateGroupCredentialInput(binding.WorkspaceID, ref, alias, kind, profileID); err != nil {
		return domain.CredentialProfile{}, err
	}
	if instance.WorkspaceID != binding.WorkspaceID {
		return domain.CredentialProfile{}, fmt.Errorf("%w: GitLab endpoint and Group are in different Workspaces", domain.ErrForbidden)
	}
	if ref.Kind != domain.SecretGroupCredential {
		return domain.CredentialProfile{}, fmt.Errorf("%w: Group credential must use group_credential secret kind", domain.ErrInvalid)
	}
	if err := s.vault.Put(ctx, ref, material); err != nil {
		return domain.CredentialProfile{}, err
	}
	defer clear(material)
	results, err := s.probeWithMaterial(ctx, instance, binding, material)
	if err != nil {
		return domain.CredentialProfile{}, err
	}
	if err := s.recordAndRequire(ctx, binding, instance, results, requiredCapabilities); err != nil {
		return domain.CredentialProfile{}, err
	}
	profile := domain.CredentialProfile{ID: profileID, WorkspaceID: binding.WorkspaceID, Provider: domain.ProviderGitLab, Kind: kind, Alias: alias, SecretRef: ref, Status: domain.CredentialActive, Capabilities: availableCapabilities(results)}
	if err := s.store.CreateCredentialProfile(ctx, profile); err != nil {
		return domain.CredentialProfile{}, err
	}
	if err := s.store.UpdateGitLabGroupCredential(ctx, binding.WorkspaceID, binding.ID, profile.ID, &ref); err != nil {
		return domain.CredentialProfile{}, err
	}
	return profile, nil
}

func (s *CredentialService) RotateGroupCredential(ctx context.Context, instance domain.GitLabInstance, binding domain.GitLabGroupBinding, newRef domain.SecretRef, material []byte, requiredCapabilities []string) (domain.CredentialProfile, error) {
	if binding.CredentialProfileID.Empty() {
		return domain.CredentialProfile{}, fmt.Errorf("%w: Group binding has no credential profile", domain.ErrNotFound)
	}
	old, err := s.store.GetCredentialProfile(ctx, binding.WorkspaceID, binding.CredentialProfileID)
	if err != nil {
		return domain.CredentialProfile{}, err
	}
	if err := validateGroupCredentialInput(binding.WorkspaceID, newRef, newRef.Alias, old.Kind, old.ID); err != nil {
		return domain.CredentialProfile{}, err
	}
	if newRef.Kind != domain.SecretGroupCredential {
		return domain.CredentialProfile{}, fmt.Errorf("%w: Group credential must use group_credential secret kind", domain.ErrInvalid)
	}
	// Secret aliases are unique within a Workspace.  If the operator keeps the
	// alias, rotate the encrypted record in place; a deliberately renamed
	// profile may receive a new reference without creating an alias collision.
	if newRef.Alias == old.SecretRef.Alias {
		newRef.ID = old.SecretRef.ID
	}
	if err := s.vault.Put(ctx, newRef, material); err != nil {
		return domain.CredentialProfile{}, err
	}
	defer clear(material)
	results, err := s.probeWithMaterial(ctx, instance, binding, material)
	if err != nil {
		return domain.CredentialProfile{}, err
	}
	if err := s.recordAndRequire(ctx, binding, instance, results, requiredCapabilities); err != nil {
		return domain.CredentialProfile{}, err
	}
	if err := s.store.RotateCredentialProfile(ctx, binding.WorkspaceID, old.ID, newRef); err != nil {
		return domain.CredentialProfile{}, err
	}
	if err := s.store.UpdateGitLabGroupCredential(ctx, binding.WorkspaceID, binding.ID, old.ID, &newRef); err != nil {
		return domain.CredentialProfile{}, err
	}
	old.SecretRef = newRef
	old.Alias = newRef.Alias
	old.Capabilities = availableCapabilities(results)
	old.Status = domain.CredentialActive
	return old, nil
}

func validateGroupCredentialInput(workspaceID domain.ID, ref domain.SecretRef, alias string, kind domain.CredentialProfileKind, profileID domain.ID) error {
	if workspaceID.Empty() || profileID.Empty() || ref.ID.Empty() {
		return fmt.Errorf("%w: credential workspace, profile and secret IDs are required", domain.ErrInvalid)
	}
	if ref.WorkspaceID != workspaceID {
		return fmt.Errorf("%w: secret belongs to another workspace", domain.ErrForbidden)
	}
	if strings.TrimSpace(alias) == "" {
		return fmt.Errorf("%w: credential alias is required", domain.ErrInvalid)
	}
	if kind != domain.CredentialPAT && kind != domain.CredentialGroupAccessToken {
		return fmt.Errorf("%w: unsupported Group credential kind", domain.ErrInvalid)
	}
	return ref.Validate()
}

func (s *CredentialService) probeWithMaterial(ctx context.Context, instance domain.GitLabInstance, binding domain.GitLabGroupBinding, material []byte) ([]domain.CapabilityResult, error) {
	results, err := s.probe.ProbeGitLabGroup(ctx, instance, binding, material)
	if err != nil {
		var transient *ProviderTransientError
		if errors.As(err, &transient) {
			return nil, err
		}
		return nil, fmt.Errorf("probe GitLab Group credential: %w", err)
	}
	return results, nil
}

func (s *CredentialService) recordAndRequire(ctx context.Context, binding domain.GitLabGroupBinding, instance domain.GitLabInstance, results []domain.CapabilityResult, required []string) error {
	if err := s.store.RecordCapabilityResults(ctx, binding.WorkspaceID, domain.ProviderGitLab, instance.ID, "gitlab_group", binding.ExternalGroupID, results); err != nil {
		return err
	}
	return requireCapabilities(results, required, "configured Group credential lacks required permissions")
}

func requireCapabilities(results []domain.CapabilityResult, required []string, reason string) error {
	available := map[string]bool{}
	for _, result := range results {
		if result.Available {
			available[result.Capability] = true
		}
	}
	var missing []string
	for _, capability := range required {
		if !available[capability] {
			missing = append(missing, capability)
		}
	}
	if len(missing) != 0 {
		return &CapabilityError{Missing: missing, Reason: reason}
	}
	return nil
}

func availableCapabilities(results []domain.CapabilityResult) []string {
	out := make([]string, 0, len(results))
	for _, result := range results {
		if result.Available {
			out = append(out, result.Capability)
		}
	}
	return out
}

func clear(value []byte) {
	for i := range value {
		value[i] = 0
	}
}
