package agent

type EventType string

const (
	EventSystem     EventType = "system"
	EventText       EventType = "text"
	EventThinking   EventType = "thinking"
	EventToolUse    EventType = "tool_use"
	EventToolResult EventType = "tool_result"
	EventUsage      EventType = "usage"
	EventDone       EventType = "done"
	EventError      EventType = "error"
)

type TerminationReason string

const (
	TerminationNormal      TerminationReason = "normal"
	TerminationInterrupted TerminationReason = "interrupted"
	TerminationTimeout     TerminationReason = "timeout"
	TerminationFailed      TerminationReason = "failed"
)

type AgentEvent struct {
	Type EventType `json:"type"`

	SessionID *string `json:"sessionId,omitempty"`
	ThreadID  *string `json:"threadId,omitempty"`
	CWD       *string `json:"cwd,omitempty"`
	Model     *string `json:"model,omitempty"`

	Delta *string `json:"delta,omitempty"`

	ID      *string `json:"id,omitempty"`
	Name    *string `json:"name,omitempty"`
	Input   any     `json:"input,omitempty"`
	Output  *string `json:"output,omitempty"`
	IsError *bool   `json:"isError,omitempty"`

	InputTokens           *int     `json:"inputTokens,omitempty"`
	OutputTokens          *int     `json:"outputTokens,omitempty"`
	CachedInputTokens     *int     `json:"cachedInputTokens,omitempty"`
	ReasoningOutputTokens *int     `json:"reasoningOutputTokens,omitempty"`
	CostUSD               *float64 `json:"costUsd,omitempty"`

	Message           *string           `json:"message,omitempty"`
	TerminationReason TerminationReason `json:"terminationReason,omitempty"`
}
