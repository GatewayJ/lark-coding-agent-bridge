package configstore

import (
	"encoding/json"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/larkcli"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/permissions"
)

var rootKnownFields = map[string]struct{}{
	"schemaVersion": {},
	"activeProfile": {},
	"preferences":   {},
	"secrets":       {},
	"migrations":    {},
	"profiles":      {},
}

var profileKnownFields = map[string]struct{}{
	"schemaVersion":    {},
	"agentKind":        {},
	"accounts":         {},
	"secrets":          {},
	"preferences":      {},
	"access":           {},
	"workspaces":       {},
	"sandbox":          {},
	"permissions":      {},
	"permissionSource": {},
	"codex":            {},
	"attachments":      {},
	"comments":         {},
	"larkCli":          {},
}

func (r *RootConfig) UnmarshalJSON(data []byte) error {
	type rootAlias RootConfig
	var alias rootAlias
	if err := json.Unmarshal(data, &alias); err != nil {
		return err
	}
	*r = RootConfig(alias)
	r.Extra = extraFields(data, rootKnownFields)
	return nil
}

func (r RootConfig) MarshalJSON() ([]byte, error) {
	fields := make(map[string]json.RawMessage, len(r.Extra)+6)
	for key, value := range r.Extra {
		fields[key] = value
	}
	putJSON(fields, "schemaVersion", r.SchemaVersion)
	putJSON(fields, "activeProfile", r.ActiveProfile)
	if r.Preferences == nil {
		putJSON(fields, "preferences", map[string]any{})
	} else {
		putJSON(fields, "preferences", r.Preferences)
	}
	if r.Secrets != nil {
		putJSON(fields, "secrets", r.Secrets)
	}
	if r.Migrations != nil && len(r.Migrations.PermissionDefaultsV1) > 0 {
		putJSON(fields, "migrations", r.Migrations)
	}
	putJSON(fields, "profiles", r.Profiles)
	return json.Marshal(fields)
}

func (p *ProfileConfig) UnmarshalJSON(data []byte) error {
	type profileAlias ProfileConfig
	var alias profileAlias
	if err := json.Unmarshal(data, &alias); err != nil {
		return err
	}
	*p = ProfileConfig(alias)
	p.Extra = extraFields(data, profileKnownFields)
	return nil
}

func (p ProfileConfig) MarshalJSON() ([]byte, error) {
	fields := make(map[string]json.RawMessage, len(p.Extra)+14)
	for key, value := range p.Extra {
		fields[key] = value
	}
	putJSON(fields, "schemaVersion", p.SchemaVersion)
	putJSON(fields, "agentKind", p.AgentKind)
	putJSON(fields, "accounts", p.Accounts)
	if p.Secrets != nil {
		putJSON(fields, "secrets", p.Secrets)
	}
	if p.Preferences == nil {
		putJSON(fields, "preferences", map[string]any{})
	} else {
		putJSON(fields, "preferences", p.Preferences)
	}
	putJSON(fields, "access", p.Access)
	putJSON(fields, "workspaces", p.Workspaces)
	putJSON(fields, "sandbox", p.Sandbox)
	putJSON(fields, "permissions", p.Permissions)
	if p.PermissionSource != "" {
		putJSON(fields, "permissionSource", p.PermissionSource)
	}
	if p.Codex != nil {
		putJSON(fields, "codex", p.Codex)
	}
	putJSON(fields, "attachments", p.Attachments)
	if p.Comments == nil {
		putJSON(fields, "comments", map[string]any{})
	} else {
		putJSON(fields, "comments", p.Comments)
	}
	putJSON(fields, "larkCli", p.LarkCli)
	return json.Marshal(fields)
}

type storedRootConfig struct {
	SchemaVersion int                            `json:"schemaVersion"`
	ActiveProfile string                         `json:"activeProfile"`
	Preferences   map[string]any                 `json:"preferences"`
	Secrets       *larkcli.SecretsConfig         `json:"secrets,omitempty"`
	Migrations    *RootMigrations                `json:"migrations,omitempty"`
	Profiles      map[string]storedProfileConfig `json:"profiles"`
}

type storedProfileConfig struct {
	SchemaVersion int                          `json:"schemaVersion"`
	AgentKind     AgentKind                    `json:"agentKind"`
	Accounts      larkcli.AccountsConfig       `json:"accounts"`
	Secrets       *larkcli.SecretsConfig       `json:"secrets,omitempty"`
	Preferences   map[string]any               `json:"preferences"`
	Access        ProfileAccess                `json:"access"`
	Workspaces    Workspaces                   `json:"workspaces"`
	Permissions   permissions.PermissionConfig `json:"permissions"`
	Codex         *CodexConfig                 `json:"codex,omitempty"`
	Attachments   AttachmentConfig             `json:"attachments"`
	Comments      map[string]any               `json:"comments"`
	LarkCli       LarkCliConfig                `json:"larkCli"`
}

func serializeRootConfig(root RootConfig) storedRootConfig {
	profiles := make(map[string]storedProfileConfig, len(root.Profiles))
	for name, profile := range root.Profiles {
		profiles[name] = serializeProfileConfig(profile)
	}
	return storedRootConfig{
		SchemaVersion: 2,
		ActiveProfile: root.ActiveProfile,
		Preferences:   map[string]any{},
		Secrets:       root.Secrets,
		Migrations:    normalizeRootMigrations(root.Migrations),
		Profiles:      profiles,
	}
}

func serializeProfileConfig(profile ProfileConfig) storedProfileConfig {
	preferences := profile.Preferences
	if preferences == nil {
		preferences = map[string]any{}
	}
	comments := profile.Comments
	if comments == nil {
		comments = map[string]any{}
	}
	return storedProfileConfig{
		SchemaVersion: 2,
		AgentKind:     profile.AgentKind,
		Accounts:      profile.Accounts,
		Secrets:       profile.Secrets,
		Preferences:   preferences,
		Access:        profile.Access,
		Workspaces:    profile.Workspaces,
		Permissions:   profile.Permissions,
		Codex:         profile.Codex,
		Attachments:   profile.Attachments,
		Comments:      comments,
		LarkCli:       profile.LarkCli,
	}
}

func extraFields(data []byte, known map[string]struct{}) map[string]json.RawMessage {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	extra := make(map[string]json.RawMessage)
	for key, value := range raw {
		if _, ok := known[key]; !ok {
			extra[key] = value
		}
	}
	if len(extra) == 0 {
		return nil
	}
	return extra
}

func putJSON(fields map[string]json.RawMessage, key string, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		return
	}
	fields[key] = data
}
