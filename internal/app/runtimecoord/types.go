package runtimecoord

import (
	"context"
	"errors"
	"time"
)

type LockKind string

const (
	LockProfile LockKind = "profile"
	LockApp     LockKind = "app"
)

type AgentKind string

const (
	AgentClaude AgentKind = "claude"
	AgentCodex  AgentKind = "codex"
)

type TenantBrand string

const (
	TenantFeishu TenantBrand = "feishu"
	TenantLark   TenantBrand = "lark"
)

var (
	ErrAlreadyStarted    = errors.New("runtime coordinator already started")
	ErrNotStarted        = errors.New("runtime coordinator is not started")
	ErrShuttingDown      = errors.New("runtime coordinator shutdown already in progress")
	ErrAgentKindRequired = errors.New("runtime coordinator agentKind is required")
)

type RuntimeLockMeta struct {
	Kind      LockKind  `json:"kind"`
	Target    string    `json:"target"`
	Profile   string    `json:"profile"`
	AgentKind AgentKind `json:"agentKind"`
	AppID     string    `json:"appId,omitempty"`
	PID       int       `json:"pid"`
	StartedAt string    `json:"startedAt"`
}

type ProcessEntry struct {
	ID          string      `json:"id"`
	PID         int         `json:"pid"`
	AppID       string      `json:"appId"`
	Tenant      TenantBrand `json:"tenant"`
	ProfileName string      `json:"profileName"`
	AgentKind   AgentKind   `json:"agentKind"`
	ConfigPath  string      `json:"configPath"`
	StartedAt   string      `json:"startedAt"`
	Version     string      `json:"version"`
	BotName     string      `json:"botName,omitempty"`
}

type RuntimeAdapter interface {
	Start(context.Context, StartRequest) (RuntimeHandle, error)
}

type RuntimeHandle interface {
	Shutdown(context.Context) error
	Status(context.Context) (AdapterStatus, error)
}

type Reconnecter interface {
	Reconnect(context.Context, ReconnectRequest) error
}

type StartRequest struct {
	AppID      string
	Tenant     TenantBrand
	Profile    string
	AgentKind  AgentKind
	ConfigPath string
}

type ReconnectRequest struct {
	PreviousAppID string
	AppID         string
	Tenant        TenantBrand
	ConfigPath    string
}

type AdapterStatus struct {
	Connected bool
	BotName   string
	Details   map[string]string
}

type StartOptions struct {
	AppID      string
	Tenant     TenantBrand
	AgentKind  AgentKind
	ConfigPath string
	Version    string
}

type ReconnectOptions struct {
	AppID      string
	Tenant     TenantBrand
	ConfigPath string
}

type RuntimeStatus struct {
	Started   bool
	Entry     *ProcessEntry
	Processes []ProcessEntry
	Adapter   AdapterStatus
	Profile   string
	AppID     string
	AgentKind AgentKind
	StartedAt time.Time
}
