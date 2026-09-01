package controlplane

import (
	"context"
	"fmt"
	"strings"

	"specwire/bridge/internal/domain"
	"specwire/bridge/internal/provider"
)

type SelectorStore interface {
	UpsertMulticaWorkspace(context.Context, domain.MulticaWorkspaceRef) (domain.MulticaWorkspaceRef, error)
	GetMulticaWorkspace(context.Context, domain.ID, domain.ID, string) (domain.MulticaWorkspaceRef, error)
	UpsertMulticaProject(context.Context, domain.MulticaProjectRef) (domain.MulticaProjectRef, error)
}

// SelectionService is the server-backed cascade used by onboarding.  It
// keeps provider external IDs and SpecWire instance/workspace IDs together;
// the UI never has to infer scope from a display label.
type SelectionService struct {
	connections *ConnectionService
	store       SelectorStore
}

func NewSelectionService(connections *ConnectionService, store SelectorStore) (*SelectionService, error) {
	if connections == nil || store == nil {
		return nil, fmt.Errorf("%w: selector service dependencies are required", domain.ErrInvalid)
	}
	return &SelectionService{connections: connections, store: store}, nil
}

func (s *SelectionService) GitLabGroups(ctx context.Context, instance domain.GitLabInstance, query string, credentialRef *domain.SecretRef) ([]provider.GitLabGroup, error) {
	if err := validateInstanceScope(instance.WorkspaceID, instance.ID); err != nil {
		return nil, err
	}
	return s.connections.ListGitLabGroups(ctx, instance, strings.TrimSpace(query), credentialRef)
}

func (s *SelectionService) GitLabProjects(ctx context.Context, instance domain.GitLabInstance, group provider.GitLabGroup, query string, credentialRef *domain.SecretRef) ([]provider.GitLabProject, error) {
	if err := validateInstanceScope(instance.WorkspaceID, instance.ID); err != nil {
		return nil, err
	}
	if group.InstanceID != "" && group.InstanceID != instance.ID {
		return nil, fmt.Errorf("%w: GitLab Group belongs to another instance", domain.ErrForbidden)
	}
	return s.connections.ListGitLabProjects(ctx, instance, group, strings.TrimSpace(query), credentialRef)
}

func (s *SelectionService) MulticaWorkspaces(ctx context.Context, instance domain.MulticaInstance, query string, credentialRef *domain.SecretRef) ([]domain.MulticaWorkspaceRef, error) {
	if err := validateInstanceScope(instance.WorkspaceID, instance.ID); err != nil {
		return nil, err
	}
	items, err := s.connections.ListMulticaWorkspaces(ctx, instance, strings.TrimSpace(query), credentialRef)
	if err != nil {
		return nil, err
	}
	result := make([]domain.MulticaWorkspaceRef, 0, len(items))
	for _, item := range items {
		stored, err := s.store.UpsertMulticaWorkspace(ctx, domain.MulticaWorkspaceRef{WorkspaceID: instance.WorkspaceID, InstanceID: instance.ID, ExternalID: item.ExternalID, Name: item.Name})
		if err != nil {
			return nil, err
		}
		result = append(result, stored)
	}
	return result, nil
}

func (s *SelectionService) MulticaProjects(ctx context.Context, instance domain.MulticaInstance, workspace domain.MulticaWorkspaceRef, query string, credentialRef *domain.SecretRef) ([]domain.MulticaProjectRef, error) {
	if err := validateInstanceScope(instance.WorkspaceID, instance.ID); err != nil {
		return nil, err
	}
	if workspace.WorkspaceID != instance.WorkspaceID || workspace.InstanceID != instance.ID {
		return nil, fmt.Errorf("%w: Multica workspace belongs to another scope", domain.ErrForbidden)
	}
	items, err := s.connections.ListMulticaProjects(ctx, instance, provider.MulticaWorkspace{InstanceID: instance.ID, ExternalID: workspace.ExternalID, Name: workspace.Name}, strings.TrimSpace(query), credentialRef)
	if err != nil {
		return nil, err
	}
	result := make([]domain.MulticaProjectRef, 0, len(items))
	for _, item := range items {
		stored, err := s.store.UpsertMulticaProject(ctx, domain.MulticaProjectRef{WorkspaceID: instance.WorkspaceID, InstanceID: instance.ID, MulticaWorkspaceID: workspace.ID, ExternalID: item.ExternalID, Title: item.Title, WebURL: item.WebURL})
		if err != nil {
			return nil, err
		}
		result = append(result, stored)
	}
	return result, nil
}

func validateInstanceScope(workspaceID, instanceID domain.ID) error {
	if workspaceID.Empty() || instanceID.Empty() {
		return fmt.Errorf("%w: provider instance scope is required", domain.ErrInvalid)
	}
	return nil
}
