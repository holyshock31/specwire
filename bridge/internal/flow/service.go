package flow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"specwire/bridge/internal/domain"
)

type Store interface {
	CreateFlow(context.Context, domain.Flow) error
	GetFlow(context.Context, domain.ID, domain.ID) (domain.Flow, error)
	SaveFlowDraft(context.Context, domain.ID, domain.ID, domain.FlowGraph) error
	GetFlowDraft(context.Context, domain.ID, domain.ID) (domain.FlowGraph, error)
	SaveFlowVersion(context.Context, domain.FlowVersion) error
	GetFlowVersion(context.Context, domain.ID, domain.ID, int) (domain.FlowVersion, error)
	NextFlowVersion(context.Context, domain.ID, domain.ID) (int, error)
	UpdateFlowStatus(context.Context, domain.ID, domain.ID, domain.FlowStatus) error
	CreateFlowTemplate(context.Context, domain.FlowTemplate) error
	GetFlowTemplate(context.Context, domain.ID, string, string) (domain.FlowTemplate, error)
	ListFlowTemplates(context.Context, domain.ID) ([]domain.FlowTemplate, error)
}

type RouteActivator interface {
	ActivateInputFlow(context.Context, domain.FlowVersion) error
	PauseInputFlow(context.Context, domain.FlowVersion) error
}

type Service struct {
	store           Store
	catalog         Catalog
	catalogResolver CatalogResolver
	routes          RouteActivator
	now             func() time.Time
}

func NewService(store Store, catalog Catalog) (*Service, error) {
	if store == nil {
		return nil, fmt.Errorf("%w: flow store is required", domain.ErrInvalid)
	}
	return &Service{store: store, catalog: catalog, now: time.Now}, nil
}

func (s *Service) SetRouteActivator(routes RouteActivator) { s.routes = routes }

func (s *Service) SetCatalogResolver(resolver CatalogResolver) { s.catalogResolver = resolver }

func (s *Service) catalogForWorkspace(ctx context.Context, workspaceID domain.ID) (Catalog, error) {
	if s.catalogResolver != nil {
		return s.catalogResolver.CatalogForWorkspace(ctx, workspaceID)
	}
	return s.catalog, nil
}

func (s *Service) SeedBuiltins(ctx context.Context, workspaceID domain.ID) error {
	for _, template := range BuiltinTemplates() {
		template.WorkspaceID = workspaceID
		if err := s.store.CreateFlowTemplate(ctx, template); err != nil && !errors.Is(err, domain.ErrConflict) {
			return err
		}
	}
	return nil
}

func (s *Service) CreateBlank(ctx context.Context, workspaceID, connectionID, actorID domain.ID, name string) (domain.Flow, error) {
	flow := domain.Flow{ID: domain.NewID(), WorkspaceID: workspaceID, ConnectionID: connectionID, Name: strings.TrimSpace(name), Status: domain.FlowDraft, CreatedBy: actorID, UpdatedAt: s.now().UTC()}
	if err := s.store.CreateFlow(ctx, flow); err != nil {
		return domain.Flow{}, err
	}
	if err := s.store.SaveFlowDraft(ctx, workspaceID, flow.ID, domain.FlowGraph{}); err != nil {
		return domain.Flow{}, err
	}
	return flow, nil
}

func (s *Service) CreateFromTemplate(ctx context.Context, workspaceID, connectionID, actorID domain.ID, templateKey, templateVersion, name string) (domain.Flow, error) {
	template, err := s.store.GetFlowTemplate(ctx, workspaceID, templateKey, templateVersion)
	if err != nil {
		return domain.Flow{}, err
	}
	flow := domain.Flow{ID: domain.NewID(), WorkspaceID: workspaceID, ConnectionID: connectionID, Name: firstNonEmptyFlowName(name, template.Name), Status: domain.FlowDraft, CreatedBy: actorID, UpdatedAt: s.now().UTC()}
	if err := s.store.CreateFlow(ctx, flow); err != nil {
		return domain.Flow{}, err
	}
	if err := s.store.SaveFlowDraft(ctx, workspaceID, flow.ID, cloneGraph(template.Graph)); err != nil {
		return domain.Flow{}, err
	}
	return flow, nil
}

func (s *Service) SaveDraft(ctx context.Context, workspaceID, flowID domain.ID, graph domain.FlowGraph) (ValidationResult, error) {
	if err := s.store.SaveFlowDraft(ctx, workspaceID, flowID, graph); err != nil {
		return ValidationResult{}, err
	}
	catalog, err := s.catalogForWorkspace(ctx, workspaceID)
	if err != nil {
		return ValidationResult{}, err
	}
	return catalog.Validate(graph, false), nil
}

func (s *Service) ValidateDraft(ctx context.Context, workspaceID, flowID domain.ID) (ValidationResult, error) {
	graph, err := s.store.GetFlowDraft(ctx, workspaceID, flowID)
	if err != nil {
		return ValidationResult{}, err
	}
	catalog, err := s.catalogForWorkspace(ctx, workspaceID)
	if err != nil {
		return ValidationResult{}, err
	}
	return catalog.Validate(graph, true), nil
}

// Simulate evaluates the current draft without creating a FlowVersion or
// invoking any provider adapter.  The caller supplies the read-only runtime
// context so mapping references resolve exactly as they would for a real
// execution.
func (s *Service) Simulate(ctx context.Context, workspaceID, flowID domain.ID, event map[string]any, runtimeContext RuntimeContext) (SimulationResult, error) {
	graph, err := s.store.GetFlowDraft(ctx, workspaceID, flowID)
	if err != nil {
		return SimulationResult{}, err
	}
	catalog, err := s.catalogForWorkspace(ctx, workspaceID)
	if err != nil {
		return SimulationResult{}, err
	}
	return Simulate(graph, catalog, event, runtimeContext), nil
}

func (s *Service) Publish(ctx context.Context, workspaceID, flowID, actorID domain.ID) (domain.FlowVersion, ValidationResult, error) {
	flowRecord, err := s.store.GetFlow(ctx, workspaceID, flowID)
	if err != nil {
		return domain.FlowVersion{}, ValidationResult{}, err
	}
	if flowRecord.Status == domain.FlowArchived {
		return domain.FlowVersion{}, ValidationResult{}, fmt.Errorf("%w: archived Flow cannot be published", domain.ErrConflict)
	}
	graph, err := s.store.GetFlowDraft(ctx, workspaceID, flowID)
	if err != nil {
		return domain.FlowVersion{}, ValidationResult{}, err
	}
	catalog, err := s.catalogForWorkspace(ctx, workspaceID)
	if err != nil {
		return domain.FlowVersion{}, ValidationResult{}, err
	}
	validation := catalog.Validate(graph, true)
	if !validation.Valid {
		return domain.FlowVersion{}, validation, fmt.Errorf("%w: Flow cannot be published", domain.ErrInvalid)
	}
	plan, err := Compile(graph, catalog)
	if err != nil {
		return domain.FlowVersion{}, validation, err
	}
	versionNumber, err := s.store.NextFlowVersion(ctx, workspaceID, flowID)
	if err != nil {
		return domain.FlowVersion{}, validation, err
	}
	now := s.now().UTC()
	version := domain.FlowVersion{ID: domain.NewID(), WorkspaceID: workspaceID, FlowID: flowID, Version: versionNumber, Status: domain.FlowPublished, Graph: cloneGraph(graph), CompiledPlan: plan, BehaviorRefs: behaviorRefs(graph), ModelRefs: modelRefs(graph), PublishedAt: &now, PublishedBy: actorID}
	if err := s.store.SaveFlowVersion(ctx, version); err != nil {
		return domain.FlowVersion{}, validation, err
	}
	if s.routes != nil {
		if err := s.routes.ActivateInputFlow(ctx, version); err != nil {
			// The immutable version remains available for reconciliation, but the
			// Flow itself is paused so no ingress can be considered active until
			// Hook/route activation succeeds on a later retry.
			_ = s.store.UpdateFlowStatus(ctx, workspaceID, flowID, domain.FlowPaused)
			return domain.FlowVersion{}, validation, err
		}
	}
	return version, validation, nil
}

func (s *Service) Pause(ctx context.Context, workspaceID, flowID domain.ID, activeVersion int) error {
	flowRecord, err := s.store.GetFlow(ctx, workspaceID, flowID)
	if err != nil {
		return err
	}
	if flowRecord.Status != domain.FlowPublished {
		return fmt.Errorf("%w: only a published Flow can be paused", domain.ErrConflict)
	}
	if activeVersion <= 0 {
		activeVersion = flowRecord.ActiveVersion
	}
	if activeVersion <= 0 {
		return fmt.Errorf("%w: published Flow has no active version", domain.ErrConflict)
	}
	if s.routes != nil {
		version, err := s.store.GetFlowVersion(ctx, workspaceID, flowID, activeVersion)
		if err != nil {
			return err
		}
		if err := s.routes.PauseInputFlow(ctx, version); err != nil {
			return err
		}
	}
	if err := s.store.UpdateFlowStatus(ctx, workspaceID, flowID, domain.FlowPaused); err != nil {
		return err
	}
	return nil
}

func (s *Service) Archive(ctx context.Context, workspaceID, flowID domain.ID) error {
	flowRecord, err := s.store.GetFlow(ctx, workspaceID, flowID)
	if err != nil {
		return err
	}
	if flowRecord.Status != domain.FlowPublished && flowRecord.Status != domain.FlowPaused {
		return fmt.Errorf("%w: only a published or paused Flow can be archived", domain.ErrConflict)
	}
	if s.routes != nil && flowRecord.ActiveVersion > 0 {
		version, err := s.store.GetFlowVersion(ctx, workspaceID, flowID, flowRecord.ActiveVersion)
		if err != nil {
			return err
		}
		if err := s.routes.PauseInputFlow(ctx, version); err != nil && !errors.Is(err, domain.ErrNotFound) {
			return err
		}
	}
	return s.store.UpdateFlowStatus(ctx, workspaceID, flowID, domain.FlowArchived)
}

func cloneGraph(graph domain.FlowGraph) domain.FlowGraph {
	encoded, _ := json.Marshal(graph)
	var copy domain.FlowGraph
	_ = json.Unmarshal(encoded, &copy)
	return copy
}
func firstNonEmptyFlowName(name, fallback string) string {
	if strings.TrimSpace(name) != "" {
		return strings.TrimSpace(name)
	}
	return fallback
}
func behaviorRefs(graph domain.FlowGraph) []string {
	out := []string{}
	for _, node := range graph.Nodes {
		if node.Connector != nil {
			out = append(out, node.Connector.BehaviorKey+"@"+node.Connector.BehaviorVersion)
		}
	}
	return uniqueStrings(out)
}
func modelRefs(graph domain.FlowGraph) []string {
	seen := map[string]struct{}{}
	out := []string{}
	for _, node := range graph.Nodes {
		for _, port := range append(append([]domain.Port(nil), node.Inputs...), node.Outputs...) {
			if port.ModelRef != "" && !isProviderModel(port.ModelRef) {
				if _, ok := seen[port.ModelRef]; !ok {
					seen[port.ModelRef] = struct{}{}
					out = append(out, port.ModelRef)
				}
			}
		}
	}
	return out
}
