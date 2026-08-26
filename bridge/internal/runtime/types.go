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
	GetCorrelation(context.Context, domain.ID, domain.ID, string, string) (domain.Correlation, error)
	UpsertCorrelation(context.Context, domain.Correlation) (domain.Correlation, error)
}

type SecretResolver interface {
	Resolve(context.Context, domain.SecretRef) ([]byte, error)
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
	now         func() time.Time
	behaviors   flow.Catalog
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

func NewIngress(store Store, secrets SecretResolver, catalog flow.Catalog, options ...IngressOption) (*Ingress, error) {
	if store == nil || secrets == nil {
		return nil, invalid("runtime ingress dependencies are required")
	}
	ingress := &Ingress{store: store, secrets: secrets, maxBody: maxWebhookBody, maxAge: defaultWebhookAge, now: time.Now, behaviors: catalog}
	for _, option := range options {
		option(ingress)
	}
	return ingress, nil
}

type Executor struct {
	store       Store
	gitlab      GitLabAdapter
	multica     MulticaAdapter
	secrets     SecretResolver
	credentials GitLabCredentialResolver
	catalog     flow.Catalog
	now         func() time.Time
}

type ExecutorOption func(*Executor)

func WithGitLabCredentialResolver(resolver GitLabCredentialResolver) ExecutorOption {
	return func(e *Executor) { e.credentials = resolver }
}

func NewExecutor(store Store, gitlab GitLabAdapter, multica MulticaAdapter, secrets SecretResolver, catalog flow.Catalog, options ...ExecutorOption) (*Executor, error) {
	if store == nil || gitlab == nil || multica == nil {
		return nil, invalid("runtime executor dependencies are required")
	}
	e := &Executor{store: store, gitlab: gitlab, multica: multica, secrets: secrets, catalog: catalog, now: time.Now}
	for _, option := range options {
		option(e)
	}
	return e, nil
}

type GitLabAdapter interface {
	CloseIssue(context.Context, domain.GitLabInstance, provider.GitLabProject, int, *provider.Credential) error
}

type MulticaAdapter interface {
	CreateIssue(context.Context, domain.MulticaInstance, provider.IssueInput, *provider.Credential) (provider.IssueResult, error)
	SetIssueStatus(context.Context, domain.MulticaInstance, string, string, *provider.Credential) (provider.IssueStatusResult, error)
}

func invalid(message string) error { return fmt.Errorf("%w: %s", domain.ErrInvalid, message) }
