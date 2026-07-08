package bridge

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/runtimecoord"
)

type RuntimeAgentKind string

const (
	RuntimeAgentClaude RuntimeAgentKind = "claude"
	RuntimeAgentCodex  RuntimeAgentKind = "codex"
)

type RuntimeTenant string

const (
	RuntimeTenantFeishu RuntimeTenant = "feishu"
	RuntimeTenantLark   RuntimeTenant = "lark"
)

type RuntimeAdapter interface {
	Start(context.Context, RuntimeStartRequest) (RuntimeHandle, error)
}

type RuntimeAdapterFunc func(context.Context, RuntimeStartRequest) (RuntimeHandle, error)

func (f RuntimeAdapterFunc) Start(ctx context.Context, req RuntimeStartRequest) (RuntimeHandle, error) {
	return f(ctx, req)
}

type RuntimeHandle interface {
	Shutdown(context.Context) error
	Status(context.Context) (RuntimeAdapterStatus, error)
}

type RuntimeReconnecter interface {
	Reconnect(context.Context, RuntimeReconnectRequest) error
}

type RuntimeStartRequest struct {
	AppID      string
	Tenant     RuntimeTenant
	Profile    string
	AgentKind  RuntimeAgentKind
	ConfigPath string
}

type RuntimeReconnectRequest struct {
	PreviousAppID string
	AppID         string
	Tenant        RuntimeTenant
	ConfigPath    string
}

type RuntimeAdapterStatus struct {
	Connected bool
	BotName   string
	Details   map[string]string
}

type RuntimeOptions struct {
	RootDir    string
	Profile    string
	AgentKind  RuntimeAgentKind
	Version    string
	Adapter    RuntimeAdapter
	LockStale  time.Duration
	LockUpdate time.Duration
}

type RuntimeStartOptions struct {
	AppID      string
	Tenant     RuntimeTenant
	AgentKind  RuntimeAgentKind
	ConfigPath string
	Version    string
}

type RuntimeReconnectOptions struct {
	AppID      string
	Tenant     RuntimeTenant
	ConfigPath string
}

type RuntimeProcessEntry struct {
	ID          string           `json:"id"`
	PID         int              `json:"pid"`
	AppID       string           `json:"appId"`
	Tenant      RuntimeTenant    `json:"tenant"`
	ProfileName string           `json:"profileName"`
	AgentKind   RuntimeAgentKind `json:"agentKind"`
	ConfigPath  string           `json:"configPath"`
	StartedAt   string           `json:"startedAt"`
	Version     string           `json:"version"`
	BotName     string           `json:"botName,omitempty"`
}

type RuntimeLockMeta struct {
	Kind      string `json:"kind"`
	Target    string `json:"target"`
	Profile   string `json:"profile,omitempty"`
	AgentKind string `json:"agentKind,omitempty"`
	AppID     string `json:"appId,omitempty"`
	PID       int    `json:"pid,omitempty"`
	StartedAt string `json:"startedAt,omitempty"`
}

// RuntimeLockConflictError reports that another bridge process already holds a
// profile or app runtime lock. Use errors.As(err, *RuntimeLockConflictError) to
// inspect the lock holder without importing internal packages.
type RuntimeLockConflictError struct {
	Kind   string
	Target string
	Meta   *RuntimeLockMeta
	Err    error
}

func (e *RuntimeLockConflictError) Error() string {
	return fmt.Sprintf("runtime %s lock is already held: %s", e.Kind, e.Target)
}

func (e *RuntimeLockConflictError) Unwrap() error {
	return e.Err
}

// RuntimeSameAppConflictError reports that another bridge process already
// serves the same Lark app ID.
type RuntimeSameAppConflictError struct {
	AppID   string
	Others  []RuntimeProcessEntry
	Message string
}

func (e *RuntimeSameAppConflictError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("runtime app already has active bridge processes: %s", e.AppID)
}

type RuntimeStatus struct {
	Started   bool                  `json:"started"`
	Entry     *RuntimeProcessEntry  `json:"entry,omitempty"`
	Processes []RuntimeProcessEntry `json:"processes"`
	Adapter   RuntimeAdapterStatus  `json:"adapter"`
	Profile   string                `json:"profile"`
	AppID     string                `json:"appId,omitempty"`
	AgentKind RuntimeAgentKind      `json:"agentKind"`
	StartedAt time.Time             `json:"startedAt,omitempty"`
}

type Runtime struct {
	coord     *runtimecoord.Coordinator
	agentKind RuntimeAgentKind
}

var (
	ErrNilRuntime               = errors.New("bridge runtime is nil")
	ErrNilRuntimeHandle         = errors.New("bridge runtime handle is nil")
	ErrRuntimeAgentKindRequired = errors.New("runtime agentKind is required when it cannot be inferred from a client")
	ErrRuntimeAgentKindInvalid  = errors.New("runtime agentKind must be claude or codex")
	ErrRuntimeTenantInvalid     = errors.New("runtime tenant must be feishu or lark")
	ErrRuntimeAlreadyStarted    = errors.New("bridge runtime already started")
	ErrRuntimeNotStarted        = errors.New("bridge runtime is not started")
	ErrRuntimeShuttingDown      = errors.New("bridge runtime shutdown already in progress")
)

func NewRuntime(options RuntimeOptions) (*Runtime, error) {
	var adapter runtimecoord.RuntimeAdapter
	if options.Adapter != nil {
		adapter = runtimeAdapter{inner: options.Adapter}
	}
	agentKind, err := toRuntimeCoordAgentKind(options.AgentKind)
	if err != nil {
		return nil, err
	}
	coord, err := runtimecoord.New(runtimecoord.Options{
		RootDir:    options.RootDir,
		Profile:    options.Profile,
		AgentKind:  agentKind,
		Version:    options.Version,
		Adapter:    adapter,
		LockStale:  options.LockStale,
		LockUpdate: options.LockUpdate,
	})
	if err != nil {
		return nil, err
	}
	return &Runtime{coord: coord, agentKind: options.AgentKind}, nil
}

func (r *Runtime) Start(ctx context.Context, options RuntimeStartOptions) error {
	if r == nil || r.coord == nil {
		return ErrNilRuntime
	}
	if options.AgentKind == "" {
		options.AgentKind = r.agentKind
	}
	if options.AgentKind == "" {
		return ErrRuntimeAgentKindRequired
	}
	tenant, err := toRuntimeCoordTenant(options.Tenant)
	if err != nil {
		return err
	}
	agentKind, err := toRuntimeCoordAgentKind(options.AgentKind)
	if err != nil {
		return err
	}
	return toPublicRuntimeError(r.coord.Start(ctx, runtimecoord.StartOptions{
		AppID:      options.AppID,
		Tenant:     tenant,
		AgentKind:  agentKind,
		ConfigPath: options.ConfigPath,
		Version:    options.Version,
	}))
}

func (r *Runtime) Shutdown(ctx context.Context) error {
	if r == nil || r.coord == nil {
		return ErrNilRuntime
	}
	return toPublicRuntimeError(r.coord.Shutdown(ctx))
}

func (r *Runtime) Status(ctx context.Context) (RuntimeStatus, error) {
	if r == nil || r.coord == nil {
		return RuntimeStatus{}, ErrNilRuntime
	}
	status, err := r.coord.Status(ctx)
	if err != nil {
		return RuntimeStatus{}, toPublicRuntimeError(err)
	}
	return fromRuntimeCoordStatus(status), nil
}

func (r *Runtime) Reconnect(ctx context.Context, options RuntimeReconnectOptions) error {
	if r == nil || r.coord == nil {
		return ErrNilRuntime
	}
	tenant, err := toRuntimeCoordTenant(options.Tenant)
	if err != nil {
		return err
	}
	return toPublicRuntimeError(r.coord.Reconnect(ctx, runtimecoord.ReconnectOptions{
		AppID:      options.AppID,
		Tenant:     tenant,
		ConfigPath: options.ConfigPath,
	}))
}

type runtimeAdapter struct {
	inner RuntimeAdapter
}

func (a runtimeAdapter) Start(ctx context.Context, req runtimecoord.StartRequest) (runtimecoord.RuntimeHandle, error) {
	handle, err := a.inner.Start(ctx, RuntimeStartRequest{
		AppID:      req.AppID,
		Tenant:     RuntimeTenant(req.Tenant),
		Profile:    req.Profile,
		AgentKind:  RuntimeAgentKind(req.AgentKind),
		ConfigPath: req.ConfigPath,
	})
	if err != nil {
		return nil, err
	}
	if handle == nil {
		return nil, ErrNilRuntimeHandle
	}
	if _, ok := handle.(RuntimeReconnecter); ok {
		return runtimeReconnectHandle{runtimeHandle{inner: handle}}, nil
	}
	return runtimeHandle{inner: handle}, nil
}

type runtimeHandle struct {
	inner RuntimeHandle
}

func (h runtimeHandle) Shutdown(ctx context.Context) error {
	if h.inner == nil {
		return ErrNilRuntimeHandle
	}
	return h.inner.Shutdown(ctx)
}

func (h runtimeHandle) Status(ctx context.Context) (runtimecoord.AdapterStatus, error) {
	if h.inner == nil {
		return runtimecoord.AdapterStatus{}, ErrNilRuntimeHandle
	}
	status, err := h.inner.Status(ctx)
	if err != nil {
		return runtimecoord.AdapterStatus{}, err
	}
	return runtimecoord.AdapterStatus{
		Connected: status.Connected,
		BotName:   status.BotName,
		Details:   status.Details,
	}, nil
}

type runtimeReconnectHandle struct {
	runtimeHandle
}

func (h runtimeReconnectHandle) Reconnect(ctx context.Context, req runtimecoord.ReconnectRequest) error {
	if h.inner == nil {
		return ErrNilRuntimeHandle
	}
	reconnecter := h.inner.(RuntimeReconnecter)
	return reconnecter.Reconnect(ctx, RuntimeReconnectRequest{
		PreviousAppID: req.PreviousAppID,
		AppID:         req.AppID,
		Tenant:        RuntimeTenant(req.Tenant),
		ConfigPath:    req.ConfigPath,
	})
}

func fromRuntimeCoordStatus(status runtimecoord.RuntimeStatus) RuntimeStatus {
	return RuntimeStatus{
		Started:   status.Started,
		Entry:     fromRuntimeCoordEntry(status.Entry),
		Processes: fromRuntimeCoordEntries(status.Processes),
		Adapter:   fromRuntimeCoordAdapterStatus(status.Adapter),
		Profile:   status.Profile,
		AppID:     status.AppID,
		AgentKind: RuntimeAgentKind(status.AgentKind),
		StartedAt: status.StartedAt,
	}
}

func fromRuntimeCoordEntries(entries []runtimecoord.ProcessEntry) []RuntimeProcessEntry {
	out := make([]RuntimeProcessEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, *fromRuntimeCoordEntry(&entry))
	}
	return out
}

func fromRuntimeCoordEntry(entry *runtimecoord.ProcessEntry) *RuntimeProcessEntry {
	if entry == nil {
		return nil
	}
	return &RuntimeProcessEntry{
		ID:          entry.ID,
		PID:         entry.PID,
		AppID:       entry.AppID,
		Tenant:      RuntimeTenant(entry.Tenant),
		ProfileName: entry.ProfileName,
		AgentKind:   RuntimeAgentKind(entry.AgentKind),
		ConfigPath:  entry.ConfigPath,
		StartedAt:   entry.StartedAt,
		Version:     entry.Version,
		BotName:     entry.BotName,
	}
}

func fromRuntimeCoordLockMeta(meta *runtimecoord.RuntimeLockMeta) *RuntimeLockMeta {
	if meta == nil {
		return nil
	}
	return &RuntimeLockMeta{
		Kind:      string(meta.Kind),
		Target:    meta.Target,
		Profile:   meta.Profile,
		AgentKind: string(meta.AgentKind),
		AppID:     meta.AppID,
		PID:       meta.PID,
		StartedAt: meta.StartedAt,
	}
}

func toPublicRuntimeError(err error) error {
	if err == nil {
		return nil
	}
	var conflict *runtimecoord.RuntimeLockConflictError
	if errors.As(err, &conflict) {
		return &RuntimeLockConflictError{
			Kind:   string(conflict.Kind),
			Target: conflict.Target,
			Meta:   fromRuntimeCoordLockMeta(conflict.Meta),
			Err:    conflict.Err,
		}
	}
	var sameApp *runtimecoord.SameAppConflictError
	if errors.As(err, &sameApp) {
		return &RuntimeSameAppConflictError{
			AppID:   sameApp.AppID,
			Others:  fromRuntimeCoordEntries(sameApp.Others),
			Message: sameApp.Message,
		}
	}
	switch {
	case errors.Is(err, runtimecoord.ErrAlreadyStarted):
		return ErrRuntimeAlreadyStarted
	case errors.Is(err, runtimecoord.ErrNotStarted):
		return ErrRuntimeNotStarted
	case errors.Is(err, runtimecoord.ErrShuttingDown):
		return ErrRuntimeShuttingDown
	case errors.Is(err, runtimecoord.ErrAgentKindRequired):
		return ErrRuntimeAgentKindRequired
	default:
		return err
	}
}

func fromRuntimeCoordAdapterStatus(status runtimecoord.AdapterStatus) RuntimeAdapterStatus {
	return RuntimeAdapterStatus{
		Connected: status.Connected,
		BotName:   status.BotName,
		Details:   status.Details,
	}
}

func toRuntimeCoordAgentKind(kind RuntimeAgentKind) (runtimecoord.AgentKind, error) {
	switch kind {
	case "":
		return "", nil
	case RuntimeAgentClaude:
		return runtimecoord.AgentClaude, nil
	case RuntimeAgentCodex:
		return runtimecoord.AgentCodex, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrRuntimeAgentKindInvalid, kind)
	}
}

func toRuntimeCoordTenant(tenant RuntimeTenant) (runtimecoord.TenantBrand, error) {
	switch tenant {
	case "":
		return runtimecoord.TenantFeishu, nil
	case RuntimeTenantFeishu:
		return runtimecoord.TenantFeishu, nil
	case RuntimeTenantLark:
		return runtimecoord.TenantLark, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrRuntimeTenantInvalid, tenant)
	}
}
