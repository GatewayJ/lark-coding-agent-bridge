package agent

import (
	"context"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/permissions"
)

type AgentRunOptions struct {
	RunID          string                           `json:"runId"`
	Prompt         string                           `json:"prompt"`
	CWD            string                           `json:"cwd,omitempty"`
	SessionID      string                           `json:"sessionId,omitempty"`
	ThreadID       string                           `json:"threadId,omitempty"`
	Model          string                           `json:"model,omitempty"`
	Images         []string                         `json:"images,omitempty"`
	Sandbox        permissions.CodexSandboxMode     `json:"sandbox,omitempty"`
	PermissionMode permissions.ClaudePermissionMode `json:"permissionMode,omitempty"`
	StopGraceMs    int                              `json:"stopGraceMs,omitempty"`
}

type AgentRun interface {
	RunID() string
	Events() <-chan AgentEvent
	Stop(ctx context.Context) error
	WaitForExit(ctx context.Context) (bool, error)
}

type AgentAdapter interface {
	ID() string
	DisplayName() string
	IsAvailable(ctx context.Context) (bool, error)
	PrepareRun(ctx context.Context, opts AgentRunOptions) error
	// Run returns an AgentRun once the request is valid enough to enter the
	// agent lifecycle. Real agent process startup/runtime failures should be
	// reported as a terminal EventError on the returned run so subscribers see
	// the same stream contract as the JavaScript bridge. Return error only for
	// pre-run failures such as canceled context, invalid options, or argv/env
	// construction failures.
	Run(ctx context.Context, opts AgentRunOptions) (AgentRun, error)
}

type AgentBotIdentity struct {
	OpenID string `json:"openId"`
	Name   string `json:"name,omitempty"`
}

type BotIdentitySetter interface {
	SetBotIdentity(identity AgentBotIdentity)
}

type EnvMerger interface {
	MergeEnv(values map[string]string)
}

type AgentAvailability struct {
	OK         bool                    `json:"ok"`
	Version    string                  `json:"version,omitempty"`
	Error      string                  `json:"error,omitempty"`
	Diagnostic *AvailabilityDiagnostic `json:"diagnostic,omitempty"`
}

type AvailabilityDiagnostic struct {
	Code          string   `json:"code,omitempty"`
	Command       string   `json:"command,omitempty"`
	BinaryPath    string   `json:"binaryPath,omitempty"`
	Args          []string `json:"args,omitempty"`
	ExitCode      *int     `json:"exitCode,omitempty"`
	TimeoutMs     int      `json:"timeoutMs,omitempty"`
	StdoutExcerpt string   `json:"stdoutExcerpt,omitempty"`
	StderrExcerpt string   `json:"stderrExcerpt,omitempty"`
	Message       string   `json:"message,omitempty"`
}

type AvailabilityChecker interface {
	CheckAvailability(ctx context.Context) (AgentAvailability, error)
}
