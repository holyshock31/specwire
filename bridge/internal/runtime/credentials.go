package runtime

import (
	"context"
	"errors"

	"specwire/bridge/internal/domain"
	"specwire/bridge/internal/provider"
)

type gitLabCredentialStore interface {
	GetGitLabGroupBindingByGroup(context.Context, domain.ID, domain.ID, string) (domain.GitLabGroupBinding, error)
	GetGitLabInstance(context.Context, domain.ID, domain.ID) (domain.GitLabInstance, error)
}

// StoredGitLabCredentialResolver resolves the Group-first credential policy
// used by runtime GitLab side effects.  The returned material is short-lived
// and the cleanup callback makes the ownership boundary explicit.
type StoredGitLabCredentialResolver struct {
	store gitLabCredentialStore
	vault SecretResolver
}

func NewStoredGitLabCredentialResolver(store gitLabCredentialStore, vault SecretResolver) (*StoredGitLabCredentialResolver, error) {
	if store == nil || vault == nil {
		return nil, invalid("GitLab credential resolver dependencies are required")
	}
	return &StoredGitLabCredentialResolver{store: store, vault: vault}, nil
}

func (r *StoredGitLabCredentialResolver) ResolveForConnection(ctx context.Context, connection domain.Connection) (*provider.Credential, func(), error) {
	var ref *domain.SecretRef
	if connection.SourceGitLabProject.GroupID != "" {
		binding, err := r.store.GetGitLabGroupBindingByGroup(ctx, connection.WorkspaceID, connection.SourceGitLabProject.InstanceID, connection.SourceGitLabProject.GroupID)
		if err == nil {
			ref = binding.CredentialRef
		} else if !isNotFound(err) {
			return nil, func() {}, err
		}
	}
	if ref == nil {
		instance, err := r.store.GetGitLabInstance(ctx, connection.WorkspaceID, connection.SourceGitLabProject.InstanceID)
		if err != nil {
			return nil, func() {}, err
		}
		ref = instance.CredentialRef
	}
	if ref == nil {
		return nil, func() {}, nil
	}
	material, err := r.vault.Resolve(ctx, *ref)
	if err != nil {
		return nil, func() {}, err
	}
	return &provider.Credential{Ref: *ref, Material: material}, func() { clearBytes(material) }, nil
}

func isNotFound(err error) bool { return err != nil && errors.Is(err, domain.ErrNotFound) }
