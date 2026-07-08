package profile

import "github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/permissions"

type AgentKind string

const (
	AgentClaude AgentKind = "claude"
	AgentCodex  AgentKind = "codex"
)

type Access struct {
	AllowedUsers          []string `json:"allowedUsers"`
	AllowedChats          []string `json:"allowedChats"`
	Admins                []string `json:"admins"`
	RequireMentionInGroup bool     `json:"requireMentionInGroup"`
}

type CodexConfig struct {
	BinaryPath       string `json:"binaryPath"`
	Realpath         string `json:"realpath,omitempty"`
	Version          string `json:"version,omitempty"`
	SHA256           string `json:"sha256,omitempty"`
	Owner            *int   `json:"owner,omitempty"`
	Mode             *int   `json:"mode,omitempty"`
	CodexHome        string `json:"codexHome,omitempty"`
	InheritCodexHome bool   `json:"inheritCodexHome,omitempty"`
	IgnoreUserConfig bool   `json:"ignoreUserConfig,omitempty"`
	IgnoreRules      bool   `json:"ignoreRules,omitempty"`
}

type AttachmentConfig struct {
	MaxCount      int   `json:"maxCount"`
	MaxBytes      int64 `json:"maxBytes"`
	MaxFileBytes  int64 `json:"maxFileBytes"`
	ImageMaxBytes int64 `json:"imageMaxBytes"`
	CacheTTLMS    int64 `json:"cacheTtlMs"`
	CacheMaxBytes int64 `json:"cacheMaxBytes"`
}

type Workspaces struct {
	Default string `json:"default,omitempty"`
}

type LarkCliIdentityPreset string

const (
	LarkCliBotOnly     LarkCliIdentityPreset = "bot-only"
	LarkCliUserDefault LarkCliIdentityPreset = "user-default"
)

type LarkCliConfig struct {
	IdentityPreset LarkCliIdentityPreset `json:"identityPreset"`
}

type Config struct {
	SchemaVersion    int                          `json:"schemaVersion"`
	AgentKind        AgentKind                    `json:"agentKind"`
	Access           Access                       `json:"access"`
	Workspaces       Workspaces                   `json:"workspaces"`
	Permissions      permissions.PermissionConfig `json:"permissions"`
	PermissionSource permissions.PermissionSource `json:"permissionSource,omitempty"`
	Codex            *CodexConfig                 `json:"codex,omitempty"`
	Attachments      AttachmentConfig             `json:"attachments"`
	LarkCli          LarkCliConfig                `json:"larkCli"`
}

func DefaultAttachmentConfig() AttachmentConfig {
	return AttachmentConfig{
		MaxCount:      10,
		MaxBytes:      100 * 1024 * 1024,
		MaxFileBytes:  25 * 1024 * 1024,
		ImageMaxBytes: 25 * 1024 * 1024,
		CacheTTLMS:    24 * 60 * 60 * 1000,
		CacheMaxBytes: 512 * 1024 * 1024,
	}
}

func DefaultConfig(agentKind AgentKind) Config {
	normalized, _ := permissions.NormalizePermissions(permissions.NormalizeInput{})
	return Config{
		SchemaVersion:    2,
		AgentKind:        agentKind,
		Access:           Access{RequireMentionInGroup: true},
		Permissions:      normalized.Permissions,
		PermissionSource: normalized.Source,
		Attachments:      DefaultAttachmentConfig(),
		LarkCli: LarkCliConfig{
			IdentityPreset: LarkCliBotOnly,
		},
	}
}
