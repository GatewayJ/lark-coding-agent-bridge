package runpolicy

import (
	"time"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/access"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/capability"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/permissions"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/profile"
)

type Source string

const (
	SourceIM      Source = "im"
	SourceCard    Source = "card"
	SourceComment Source = "comment"
)

type ResourceBindingKind string

const (
	ResourceBindingDoc    ResourceBindingKind = "doc"
	ResourceBindingFolder ResourceBindingKind = "folder"
)

type ResourceBinding struct {
	Kind     ResourceBindingKind `json:"kind"`
	ID       string              `json:"id"`
	Verified bool                `json:"verified"`
}

type ScopeContext struct {
	Source           Source            `json:"source"`
	ChatID           string            `json:"chatId,omitempty"`
	ThreadID         string            `json:"threadId,omitempty"`
	ActorID          string            `json:"actorId"`
	CommentScopeID   string            `json:"commentScopeId,omitempty"`
	ResourceBindings []ResourceBinding `json:"resourceBindings,omitempty"`
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

type AgentAttachment struct {
	Kind            string                 `json:"kind"`
	Requiredness    AttachmentRequiredness `json:"requiredness"`
	Decision        AttachmentDecision     `json:"decision"`
	RejectionReason string                 `json:"rejectionReason,omitempty"`
	OriginalName    string                 `json:"originalName,omitempty"`
	Size            int64                  `json:"size,omitempty"`
	Hash            string                 `json:"hash,omitempty"`
	Path            string                 `json:"path,omitempty"`
}

type RejectCode string

const (
	RejectAccessDenied              RejectCode = "access-denied"
	RejectFolderAllowlistUnverified RejectCode = "folder-allowlist-unverified"
	RejectRequiredAttachment        RejectCode = "required-attachment-rejected"
)

type RejectReason struct {
	Code        RejectCode `json:"code"`
	UserVisible string     `json:"userVisible"`
}

type Input struct {
	Scope            ScopeContext
	Attachments      []AgentAttachment
	Prompt           string
	RequestedCWD     string
	CWDRealpath      string
	Access           access.Decision
	Capability       capability.Capability
	ProfileConfig    profile.Config
	Now              time.Time
	CodexHome        string
	InheritCodexHome bool
	TTL              time.Duration
}

type Allow struct {
	Prompt            string
	RequestedCWD      string
	CWDRealpath       string
	AccessMode        permissions.AccessMode
	Sandbox           permissions.CodexSandboxMode
	PermissionMode    permissions.ClaudePermissionMode
	Access            access.Decision
	Attachments       []AgentAttachment
	PolicyFingerprint string
	ExpiresAt         time.Time
}

type Result struct {
	OK           bool
	Allow        Allow
	RejectReason RejectReason
}
