package capability

import "github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/permissions"

type ID string

const (
	IDClaude ID = "claude"
	IDCodex  ID = "codex"
)

type SessionKind string

const (
	SessionKindClaude SessionKind = "claude-session"
	SessionKindCodex  SessionKind = "codex-thread"
)

type PromptInjectionMode string

const (
	PromptInjectionAppendSystemPrompt PromptInjectionMode = "append-system-prompt"
	PromptInjectionStdinPrefix        PromptInjectionMode = "stdin-prefix"
)

type Callback struct {
	Marker        string   `json:"marker"`
	LegacyMarkers []string `json:"legacyMarkers"`
}

type Permissions struct {
	MaxAccess permissions.AccessMode `json:"maxAccess"`
}

type Capability struct {
	AgentID               ID                  `json:"agentId"`
	SessionKind           SessionKind         `json:"sessionKind"`
	PromptInjection       PromptInjectionMode `json:"promptInjection"`
	SystemPrompt          string              `json:"systemPrompt"`
	SupportsNativeHistory bool                `json:"supportsNativeHistory"`
	Callback              Callback            `json:"callback"`
	Permissions           Permissions         `json:"permissions"`
}

func Claude(maxAccess permissions.AccessMode, systemPrompt string) Capability {
	if maxAccess == "" {
		maxAccess = permissions.AccessFull
	}
	return Capability{
		AgentID:               IDClaude,
		SessionKind:           SessionKindClaude,
		PromptInjection:       PromptInjectionAppendSystemPrompt,
		SystemPrompt:          systemPrompt,
		SupportsNativeHistory: true,
		Callback: Callback{
			Marker:        "__bridge_cb",
			LegacyMarkers: []string{"__claude_cb"},
		},
		Permissions: Permissions{MaxAccess: maxAccess},
	}
}

func Codex(maxAccess permissions.AccessMode, systemPrompt string) Capability {
	if maxAccess == "" {
		maxAccess = permissions.AccessFull
	}
	return Capability{
		AgentID:               IDCodex,
		SessionKind:           SessionKindCodex,
		PromptInjection:       PromptInjectionStdinPrefix,
		SystemPrompt:          systemPrompt,
		SupportsNativeHistory: false,
		Callback: Callback{
			Marker:        "__bridge_cb",
			LegacyMarkers: []string{},
		},
		Permissions: Permissions{MaxAccess: maxAccess},
	}
}
