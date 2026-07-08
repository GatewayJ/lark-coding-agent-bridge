package cardrender

import "time"

type RunStatus string

const (
	StatusQueued    RunStatus = "queued"
	StatusRunning   RunStatus = "running"
	StatusSucceeded RunStatus = "succeeded"
	StatusFailed    RunStatus = "failed"
	StatusCancelled RunStatus = "cancelled"
	StatusTimeout   RunStatus = "timeout"
)

type FooterStatus string

const (
	FooterThinking    FooterStatus = "thinking"
	FooterToolRunning FooterStatus = "tool_running"
	FooterStreaming   FooterStatus = "streaming"
)

type ToolStatus string

const (
	ToolRunning ToolStatus = "running"
	ToolDone    ToolStatus = "done"
	ToolError   ToolStatus = "error"
)

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

type Usage struct {
	InputTokens           int      `json:"inputTokens,omitempty"`
	OutputTokens          int      `json:"outputTokens,omitempty"`
	CachedInputTokens     int      `json:"cachedInputTokens,omitempty"`
	ReasoningOutputTokens int      `json:"reasoningOutputTokens,omitempty"`
	CostUSD               *float64 `json:"costUsd,omitempty"`
}

func (u Usage) TotalTokens() int {
	return u.InputTokens + u.OutputTokens + u.CachedInputTokens + u.ReasoningOutputTokens
}

func (u Usage) Empty() bool {
	return u.InputTokens == 0 &&
		u.OutputTokens == 0 &&
		u.CachedInputTokens == 0 &&
		u.ReasoningOutputTokens == 0 &&
		u.CostUSD == nil
}

type Event struct {
	Type EventType `json:"type"`
	At   time.Time `json:"at,omitempty"`

	RunID     *string `json:"runId,omitempty"`
	Scope     *string `json:"scope,omitempty"`
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

type ToolEntry struct {
	ID     string     `json:"id"`
	Name   string     `json:"name"`
	Input  any        `json:"input,omitempty"`
	Status ToolStatus `json:"status"`
	Output string     `json:"output,omitempty"`
}

type BlockKind string

const (
	BlockText BlockKind = "text"
	BlockTool BlockKind = "tool"
)

type Block struct {
	Kind      BlockKind  `json:"kind"`
	Content   string     `json:"content,omitempty"`
	Streaming bool       `json:"streaming,omitempty"`
	Tool      *ToolEntry `json:"tool,omitempty"`
}

type Reasoning struct {
	Content string `json:"content,omitempty"`
	Active  bool   `json:"active"`
}

type RunState struct {
	RunID     string        `json:"runId,omitempty"`
	Scope     string        `json:"scope,omitempty"`
	CWD       string        `json:"cwd,omitempty"`
	SessionID string        `json:"sessionId,omitempty"`
	ThreadID  string        `json:"threadId,omitempty"`
	Model     string        `json:"model,omitempty"`
	Status    RunStatus     `json:"status"`
	Blocks    []Block       `json:"blocks,omitempty"`
	Reasoning Reasoning     `json:"reasoning"`
	Footer    FooterStatus  `json:"footer,omitempty"`
	Error     string        `json:"error,omitempty"`
	Usage     Usage         `json:"usage,omitempty"`
	LastEvent string        `json:"lastEvent,omitempty"`
	StartedAt time.Time     `json:"startedAt,omitempty"`
	UpdatedAt time.Time     `json:"updatedAt,omitempty"`
	Elapsed   time.Duration `json:"elapsed,omitempty"`

	TimeoutMinutes int `json:"timeoutMinutes,omitempty"`
}

type RunStateInput struct {
	RunID     string
	Scope     string
	CWD       string
	SessionID string
	ThreadID  string
	Model     string
	Status    RunStatus
	StartedAt time.Time
	UpdatedAt time.Time
	Elapsed   time.Duration
}

type CardView struct {
	Schema    string        `json:"schema"`
	Status    RunStatus     `json:"status"`
	Summary   string        `json:"summary"`
	Streaming bool          `json:"streaming"`
	Elements  []CardElement `json:"elements"`
	Actions   []Action      `json:"actions,omitempty"`
}

type ElementKind string

const (
	ElementMarkdown ElementKind = "markdown"
	ElementNote     ElementKind = "note"
	ElementPanel    ElementKind = "panel"
)

type CardElement struct {
	Kind     ElementKind `json:"kind"`
	Text     string      `json:"text,omitempty"`
	TextSize string      `json:"textSize,omitempty"`
	Panel    *PanelView  `json:"panel,omitempty"`
}

type PanelView struct {
	Title    string `json:"title"`
	Body     string `json:"body"`
	Expanded bool   `json:"expanded"`
	Border   string `json:"border"`
}

type TextView struct {
	Status  RunStatus `json:"status"`
	Summary string    `json:"summary"`
	Content string    `json:"content"`
}

type ActionStyle string

const (
	ActionDefault ActionStyle = "default"
	ActionPrimary ActionStyle = "primary"
	ActionDanger  ActionStyle = "danger"
)

type Action struct {
	ID    string         `json:"id,omitempty"`
	Text  string         `json:"text"`
	Style ActionStyle    `json:"style,omitempty"`
	Value map[string]any `json:"value,omitempty"`
}

type RenderOptions struct {
	SignCallback func(action string) string
	ShowMetadata bool
}
