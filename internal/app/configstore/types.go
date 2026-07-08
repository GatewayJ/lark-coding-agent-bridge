package configstore

import (
	"encoding/json"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/larkcli"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/permissions"
)

type AgentKind string

const (
	AgentClaude AgentKind = "claude"
	AgentCodex  AgentKind = "codex"
)

type LoadOptions struct {
	Profile       string
	ActiveProfile string
	AgentKind     AgentKind
	Codex         *CodexConfig
}

type Snapshot struct {
	Root        RootConfig
	ProfileName string
	Profile     ProfileConfig
	Runtime     RuntimeConfig
}

type RootConfig struct {
	SchemaVersion int                        `json:"schemaVersion"`
	ActiveProfile string                     `json:"activeProfile"`
	Preferences   map[string]any             `json:"preferences"`
	Secrets       *larkcli.SecretsConfig     `json:"secrets,omitempty"`
	Migrations    *RootMigrations            `json:"migrations,omitempty"`
	Profiles      map[string]ProfileConfig   `json:"profiles"`
	Extra         map[string]json.RawMessage `json:"-"`
}

type RootMigrations struct {
	PermissionDefaultsV1 []string `json:"permissionDefaultsV1,omitempty"`
}

type ProfileConfig struct {
	SchemaVersion    int                          `json:"schemaVersion"`
	AgentKind        AgentKind                    `json:"agentKind"`
	Accounts         larkcli.AccountsConfig       `json:"accounts"`
	Secrets          *larkcli.SecretsConfig       `json:"secrets,omitempty"`
	Preferences      map[string]any               `json:"preferences"`
	Access           ProfileAccess                `json:"access"`
	Workspaces       Workspaces                   `json:"workspaces"`
	Sandbox          permissions.LegacySandbox    `json:"sandbox"`
	Permissions      permissions.PermissionConfig `json:"permissions"`
	PermissionSource permissions.PermissionSource `json:"permissionSource,omitempty"`
	Codex            *CodexConfig                 `json:"codex,omitempty"`
	Attachments      AttachmentConfig             `json:"attachments"`
	Comments         map[string]any               `json:"comments"`
	LarkCli          LarkCliConfig                `json:"larkCli"`
	Extra            map[string]json.RawMessage   `json:"-"`
}

type RuntimeConfig struct {
	ProfileConfig
	Secrets *larkcli.SecretsConfig `json:"secrets,omitempty"`
}

type ProfileAccess struct {
	AllowedUsers          []string `json:"allowedUsers"`
	AllowedChats          []string `json:"allowedChats"`
	Admins                []string `json:"admins"`
	RequireMentionInGroup bool     `json:"requireMentionInGroup"`
}

type Workspaces struct {
	Default string `json:"default,omitempty"`
}

type CodexConfig struct {
	BinaryPath       string `json:"binaryPath"`
	Realpath         string `json:"realpath,omitempty"`
	Version          string `json:"version,omitempty"`
	SHA256           string `json:"sha256,omitempty"`
	Owner            *int   `json:"owner,omitempty"`
	Mode             *int   `json:"mode,omitempty"`
	CodexHome        string `json:"codexHome,omitempty"`
	InheritCodexHome bool   `json:"inheritCodexHome"`
	IgnoreUserConfig bool   `json:"ignoreUserConfig"`
	IgnoreRules      bool   `json:"ignoreRules"`
}

type AttachmentConfig struct {
	MaxCount      int   `json:"maxCount"`
	MaxBytes      int64 `json:"maxBytes"`
	MaxFileBytes  int64 `json:"maxFileBytes"`
	ImageMaxBytes int64 `json:"imageMaxBytes"`
	CacheTTLMS    int64 `json:"cacheTtlMs"`
	CacheMaxBytes int64 `json:"cacheMaxBytes"`
}

type LarkCliIdentityPreset string

const (
	LarkCliIdentityBotOnly     LarkCliIdentityPreset = "bot-only"
	LarkCliIdentityUserDefault LarkCliIdentityPreset = "user-default"
)

type LarkCliUserImportStatus string

const (
	LarkCliUserImportNotNeeded              LarkCliUserImportStatus = "not-needed"
	LarkCliUserImportImported               LarkCliUserImportStatus = "imported"
	LarkCliUserImportSkippedExistingPrivate LarkCliUserImportStatus = "skipped-existing-private-user"
	LarkCliUserImportSkippedNoLocalUser     LarkCliUserImportStatus = "skipped-no-local-user"
	LarkCliUserImportFailed                 LarkCliUserImportStatus = "failed"
)

type LarkCliConfig struct {
	IdentityPreset  LarkCliIdentityPreset   `json:"identityPreset"`
	LocalUserImport *LarkCliLocalUserImport `json:"localUserImport,omitempty"`
}

type LarkCliLocalUserImport struct {
	Status      LarkCliUserImportStatus `json:"status"`
	AttemptedAt string                  `json:"attemptedAt,omitempty"`
	ImportedAt  string                  `json:"importedAt,omitempty"`
	Reason      string                  `json:"reason,omitempty"`
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
