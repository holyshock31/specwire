package runtime

import (
	"context"
	"fmt"
	"time"

	"specwire/bridge/internal/domain"
	"specwire/bridge/internal/flow"
	"specwire/bridge/internal/provider"
)

const (
	JobKindFlowExecute = "flow.execute"
	JobKindFlowRetry   = "flow.retry"
	maxWebhookBody     = 1 << 20
	defaultWebhookAge  = 5 * time.Minute
	defaultRetention   = 30 * 24 * time.Hour
)

// Store is the runtime-facing storage seam.  It intentionally contains
// use-case operations rather than exposing the SQL connection to ingress or
// executor code.
type Store interface {
	ListHookRoutesForProject(context.Context, domain.ID, string) ([]domain.HookRoute, error)
	GetHook(context.Context, domain.ID, domain.ID) (domain.Hook, error)
	GetFlow(context.Context, domain.ID, domain.ID) (domain.Flow, error)
	GetFlowVersion(context.Context, domain.ID, domain.ID, int) (domain.FlowVersion, error)
	GetConnection(context.Context, domain.ID, domain.ID) (domain.Connection, error)
	GetGitLabInstance(context.Context, domain.ID, domain.ID) (domain.GitLabInstance, error)
	GetMulticaInstance(context.Context, domain.ID, domain.ID) (domain.MulticaInstance, error)
	AcceptInboundEvent(context.Context, domain.InboundEvent, domain.FlowExecution, domain.Job) (domain.FlowExecution, bool, error)
	GetInboundEvent(context.Context, domain.ID, domain.ID) (domain.InboundEvent, error)
	GetFlowExecution(context.Context, domain.ID, domain.ID) (domain.FlowExecution, error)
	UpdateFlowExecution(context.Context, domain.FlowExecution) error
	CreateNodeExecution(context.Context, domain.NodeExecution) error
	UpdateNodeExecution(context.Context, domain.NodeExecution) error
	ListNodeExecutions(context.Context, domain.ID, domain.ID) ([]domain.NodeExecution, error)
	EnqueueJob(context.Context, domain.Job) error
	ClaimNextJob(context.Context, string, time.Duration) (domain.Job, error)
	CompleteJob(context.Context, domain.ID, domain.ID, string) error
	FailJob(context.Context, domain.ID, domain.ID, string, *time.Time, string) error
	GetCorrelation(context.Context, domain.ID, domain.ID, domain.ID, string, string) (domain.Correlation, error)
	ListCorrelations(context.Context, domain.ID, domain.ID, string, string) ([]domain.Correlation, error)
	UpsertCorrelation(context.Context, domain.Correlation) (domain.Correlation, error)
	MarkCorrelationLifecycle(context.Context, domain.ID, domain.ID, domain.ProjectionLifecycleStatus) error
}

// RetentionStore is optional for runtime implementations. The SQLite store
// implements it; keeping the seam optional lets focused runtime fakes remain
// small while the production worker performs periodic payload scrubbing.
type RetentionStore interface {
	PurgeExpiredRuntimePayloads(context.Context, time.Time) (int, error)
}

type SecretResolver interface {
	Resolve(context.Context, domain.SecretRef) ([]byte, error)
}

type AuditRecorder interface {
	CreateAuditEvent(context.Context, domain.AuditEvent) error
}

type GitLabCredentialResolver interface {
	ResolveForConnection(context.Context, domain.Connection) (*provider.Credential, func(), error)
}

type GitLabEnvelope struct {
	EventName               string
	DeliveryID              string
	Timestamp               string
	Signature               string
	InstanceHint            domain.ID
	SourceProjectExternalID string
	SourceProjectPath       string
	Payload                 map[string]any
	RawBody                 []byte
}

type IngressResult struct {
	Accepted   int `json:"accepted"`
	Duplicates int `json:"duplicates"`
	Ignored    int `json:"ignored"`
}

type Ingress struct {
	store       Store
	secrets     SecretResolver
	maxBody     int
	maxAge      time.Duration
	retention   time.Duration
	now         func() time.Time
	behaviors   flow.Catalog
	catalog     flow.CatalogResolver
	instanceURL bool
}

type IngressOption func(*Ingress)

func WithWebhookLimits(maxBody int, maxAge time.Duration) IngressOption {
	return func(i *Ingress) {
		if maxBody > 0 {
			i.maxBody = maxBody
		}
		if maxAge > 0 {
			i.maxAge = maxAge
		}
	}
}

func WithRuntimeRetention(value time.Duration) IngressOption {
	return func(i *Ingress) {
		if value > 0 {
			i.retention = value
		}
	}
}

func WithCatalogResolver(resolver flow.CatalogResolver) IngressOption {
	return func(i *Ingress) { i.catalog = resolver }
}

func NewIngress(store Store, secrets SecretResolver, catalog flow.Catalog, options ...IngressOption) (*Ingress, error) {
	if store == nil || secrets == nil {
		return nil, invalid("runtime ingress dependencies are required")
	}
	ingress := &Ingress{store: store, secrets: secrets, maxBody: maxWebhookBody, maxAge: defaultWebhookAge, retention: defaultRetention, now: time.Now, behaviors: catalog}
	for _, option := range options {
		option(ingress)
	}
	return ingress, nil
}

func (i *Ingress) catalogForWorkspace(ctx context.Context, workspaceID domain.ID) (flow.Catalog, error) {
	if i.catalog != nil {
		return i.catalog.CatalogForWorkspace(ctx, workspaceID)
	}
	return i.behaviors, nil
}

type Executor struct {
	store       Store
	gitlab      GitLabAdapter
	multica     MulticaAdapter
	secrets     SecretResolver
	credentials GitLabCredentialResolver
	catalog     flow.Catalog
	resolver    flow.CatalogResolver
	now         func() time.Time
	retention   time.Duration
}

type ExecutorOption func(*Executor)

func WithGitLabCredentialResolver(resolver GitLabCredentialResolver) ExecutorOption {
	return func(e *Executor) { e.credentials = resolver }
}

func WithExecutorRetention(value time.Duration) ExecutorOption {
	return func(e *Executor) {
		if value > 0 {
			e.retention = value
		}
	}
}

func WithExecutorCatalogResolver(resolver flow.CatalogResolver) ExecutorOption {
	return func(e *Executor) { e.resolver = resolver }
}

func NewExecutor(store Store, gitlab GitLabAdapter, multica MulticaAdapter, secrets SecretResolver, catalog flow.Catalog, options ...ExecutorOption) (*Executor, error) {
	if store == nil || gitlab == nil || multica == nil {
		return nil, invalid("runtime executor dependencies are required")
	}
	e := &Executor{store: store, gitlab: gitlab, multica: multica, secrets: secrets, catalog: catalog, retention: defaultRetention, now: time.Now}
	for _, option := range options {
		option(e)
	}
	return e, nil
}

func (e *Executor) catalogForWorkspace(ctx context.Context, workspaceID domain.ID) (flow.Catalog, error) {
	if e.resolver != nil {
		return e.resolver.CatalogForWorkspace(ctx, workspaceID)
	}
	return e.catalog, nil
}

type GitLabAdapter interface {
	NoteIssue(context.Context, domain.GitLabInstance, provider.GitLabProject, int, string, *provider.Credential) error
	CloseIssue(context.Context, domain.GitLabInstance, provider.GitLabProject, int, *provider.Credential) error
}

type MulticaAdapter interface {
	CreateIssue(context.Context, domain.MulticaInstance, provider.IssueInput, *provider.Credential) (provider.IssueResult, error)
	SetIssueStatus(context.Context, domain.MulticaInstance, string, string, *provider.Credential) (provider.IssueStatusResult, error)
}

func invalid(message string) error { return fmt.Errorf("%w: %s", domain.ErrInvalid, message) }
