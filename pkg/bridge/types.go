package bridge

import "time"

type Source string

const (
	SourceIM      Source = "im"
	SourceCard    Source = "card"
	SourceComment Source = "comment"
)

type AgentKind string

const (
	AgentCodex AgentKind = "codex"
)

type AccessMode string

const (
	AccessReadOnly  AccessMode = "read-only"
	AccessWorkspace AccessMode = "workspace"
	AccessFull      AccessMode = "full"
)

type AccessReason string

const (
	AccessOwner          AccessReason = "owner"
	AccessAllowedUser    AccessReason = "allowed-user"
	AccessAllowedAdmin   AccessReason = "allowed-admin"
	AccessAllowedChat    AccessReason = "allowed-chat"
	AccessCommentMention AccessReason = "comment-mention"
	AccessDeniedUser     AccessReason = "denied-user"
	AccessDeniedChat     AccessReason = "denied-chat"
	AccessDeniedAdmin    AccessReason = "denied-admin"
)

type AccessDecision struct {
	OK     bool         `json:"ok"`
	Reason AccessReason `json:"reason"`

	trusted bool
}

type AccessInput struct {
	Scope           Scope
	ChatMode        LarkChatMode
	RuntimeControls RuntimeControls
	AdminCommand    bool
}

type Scope struct {
	Source         Source `json:"source"`
	ChatID         string `json:"chatId,omitempty"`
	ThreadID       string `json:"threadId,omitempty"`
	ActorID        string `json:"actorId"`
	CommentScopeID string `json:"commentScopeId,omitempty"`
}

type AttachmentRequiredness string

const (
	AttachmentRequired AttachmentRequiredness = "required"
	AttachmentOptional AttachmentRequiredness = "optional"
)

type AttachmentDecision string

const (
	AttachmentAccepted AttachmentDecision = "accepted"
	AttachmentRejected AttachmentDecision = "rejected"
	AttachmentSkipped  AttachmentDecision = "skipped"
)

type Attachment struct {
	Kind            string                 `json:"kind"`
	Requiredness    AttachmentRequiredness `json:"requiredness"`
	Decision        AttachmentDecision     `json:"decision"`
	RejectionReason string                 `json:"rejectionReason,omitempty"`
	OriginalName    string                 `json:"originalName,omitempty"`
	Size            int64                  `json:"size,omitempty"`
	Hash            string                 `json:"hash,omitempty"`
	Path            string                 `json:"path,omitempty"`
}

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

type Event struct {
	Type EventType `json:"type"`
	At   time.Time `json:"at,omitempty"`

	RunID   *string `json:"runId,omitempty"`
	ScopeID *string `json:"scopeId,omitempty"`

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

type RunMetadata struct {
	RunID             string    `json:"runId"`
	ScopeID           string    `json:"scopeId"`
	CWDRealpath       string    `json:"cwdRealpath"`
	ResumeFrom        string    `json:"resumeFrom,omitempty"`
	PolicyFingerprint string    `json:"policyFingerprint"`
	ExpiresAt         time.Time `json:"expiresAt"`
}
