package controlplane

import (
	"context"
	"fmt"
	"strings"

	"specwire/bridge/internal/domain"
)

type EndpointStore interface {
	CreateGitLabInstance(context.Context, domain.GitLabInstance) error
	CreateMulticaInstance(context.Context, domain.MulticaInstance) error
	ListGitLabInstances(context.Context, domain.ID) ([]domain.GitLabInstance, error)
	ListMulticaInstances(context.Context, domain.ID) ([]domain.MulticaInstance, error)
	DisableGitLabInstance(context.Context, domain.ID, domain.ID) error
	DisableMulticaInstance(context.Context, domain.ID, domain.ID) error
	UpdateGitLabCapabilities(context.Context, domain.ID, domain.ID, []string) error
	UpdateMulticaCapabilities(context.Context, domain.ID, domain.ID, []string) error
}

type EndpointProbe interface {
	ProbeGitLab(context.Context, domain.GitLabInstance) ([]domain.CapabilityResult, error)
	ProbeMultica(context.Context, domain.MulticaInstance) ([]domain.CapabilityResult, error)
}

type EndpointService struct {
	store EndpointStore
	probe EndpointProbe
}

func NewEndpointService(store EndpointStore, probe EndpointProbe) (*EndpointService, error) {
	if store == nil {
		return nil, fmt.Errorf("%w: endpoint store is required", domain.ErrInvalid)
	}
	return &EndpointService{store: store, probe: probe}, nil
}

func (s *EndpointService) AddGitLab(ctx context.Context, instance domain.GitLabInstance) error {
	return s.store.CreateGitLabInstance(ctx, instance)
}

func (s *EndpointService) AddMultica(ctx context.Context, instance domain.MulticaInstance) error {
	// A Multica management credential is optional.  The endpoint may be used
	// for selection/readiness until an operation explicitly needs that access.
	return s.store.CreateMulticaInstance(ctx, instance)
}

func (s *EndpointService) TestGitLab(ctx context.Context, instance domain.GitLabInstance) ([]domain.CapabilityResult, error) {
	if s.probe == nil {
		return nil, fmt.Errorf("%w: GitLab capability probe is not configured", domain.ErrInvalid)
	}
	results, err := s.probe.ProbeGitLab(ctx, instance)
	if err != nil {
		return nil, err
	}
	if err := s.store.UpdateGitLabCapabilities(ctx, instance.WorkspaceID, instance.ID, availableCapabilities(results)); err != nil {
		return nil, err
	}
	return results, nil
}

func (s *EndpointService) TestMultica(ctx context.Context, instance domain.MulticaInstance) ([]domain.CapabilityResult, error) {
	if s.probe == nil {
		return nil, fmt.Errorf("%w: Multica capability probe is not configured", domain.ErrInvalid)
	}
	results, err := s.probe.ProbeMultica(ctx, instance)
	if err != nil {
		return nil, err
	}
	if err := s.store.UpdateMulticaCapabilities(ctx, instance.WorkspaceID, instance.ID, availableCapabilities(results)); err != nil {
		return nil, err
	}
	return results, nil
}

func (s *EndpointService) ListGitLab(ctx context.Context, workspaceID domain.ID) ([]domain.GitLabInstance, error) {
	return s.store.ListGitLabInstances(ctx, workspaceID)
}

func (s *EndpointService) ListMultica(ctx context.Context, workspaceID domain.ID) ([]domain.MulticaInstance, error) {
	return s.store.ListMulticaInstances(ctx, workspaceID)
}

func (s *EndpointService) GetGitLab(ctx context.Context, workspaceID, instanceID domain.ID) (domain.GitLabInstance, error) {
	instances, err := s.store.ListGitLabInstances(ctx, workspaceID)
	if err != nil {
		return domain.GitLabInstance{}, err
	}
	for _, instance := range instances {
		if instance.ID == instanceID {
			return instance, nil
		}
	}
	return domain.GitLabInstance{}, fmt.Errorf("%w: GitLab instance %s", domain.ErrNotFound, instanceID)
}

func (s *EndpointService) GetMultica(ctx context.Context, workspaceID, instanceID domain.ID) (domain.MulticaInstance, error) {
	instances, err := s.store.ListMulticaInstances(ctx, workspaceID)
	if err != nil {
		return domain.MulticaInstance{}, err
	}
	for _, instance := range instances {
		if instance.ID == instanceID {
			return instance, nil
		}
	}
	return domain.MulticaInstance{}, fmt.Errorf("%w: Multica instance %s", domain.ErrNotFound, instanceID)
}

func (s *EndpointService) DisableGitLab(ctx context.Context, workspaceID, instanceID domain.ID) error {
	if workspaceID.Empty() || instanceID.Empty() {
		return fmt.Errorf("%w: endpoint scope is required", domain.ErrInvalid)
	}
	return s.store.DisableGitLabInstance(ctx, workspaceID, instanceID)
}

func (s *EndpointService) DisableMultica(ctx context.Context, workspaceID, instanceID domain.ID) error {
	if workspaceID.Empty() || instanceID.Empty() {
		return fmt.Errorf("%w: endpoint scope is required", domain.ErrInvalid)
	}
	return s.store.DisableMulticaInstance(ctx, workspaceID, instanceID)
}

func endpointName(value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: endpoint name is required", domain.ErrInvalid)
	}
	return nil
}
