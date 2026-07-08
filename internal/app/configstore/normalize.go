package configstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/larkcli"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/permissions"
)

type rootInput struct {
	SchemaVersion int                        `json:"schemaVersion"`
	ActiveProfile string                     `json:"activeProfile"`
	Preferences   map[string]any             `json:"preferences"`
	Secrets       *larkcli.SecretsConfig     `json:"secrets"`
	Migrations    *RootMigrations            `json:"migrations"`
	Profiles      map[string]json.RawMessage `json:"profiles"`
	Extra         map[string]json.RawMessage `json:"-"`
}

type profileInput struct {
	SchemaVersion int                                  `json:"schemaVersion"`
	AgentKind     AgentKind                            `json:"agentKind"`
	Accounts      *larkcli.AccountsConfig              `json:"accounts"`
	Secrets       *larkcli.SecretsConfig               `json:"secrets"`
	Preferences   map[string]any                       `json:"preferences"`
	Access        *accessInput                         `json:"access"`
	Workspaces    *workspacesInput                     `json:"workspaces"`
	Sandbox       *permissions.LegacySandboxInput      `json:"sandbox"`
	Permissions   *permissions.PartialPermissionConfig `json:"permissions"`
	Codex         *codexInput                          `json:"codex"`
	Attachments   *attachmentInput                     `json:"attachments"`
	Comments      any                                  `json:"comments"`
	LarkCli       *larkCliInput                        `json:"larkCli"`
	Extra         map[string]json.RawMessage           `json:"-"`
}

type accessInput struct {
	AllowedUsers          []any `json:"allowedUsers"`
	AllowedChats          []any `json:"allowedChats"`
	Admins                []any `json:"admins"`
	RequireMentionInGroup *bool `json:"requireMentionInGroup"`
}

type workspacesInput struct {
	Default string `json:"default"`
}

type codexInput struct {
	BinaryPath       string `json:"binaryPath"`
	Realpath         string `json:"realpath"`
	Version          string `json:"version"`
	SHA256           string `json:"sha256"`
	Owner            *int   `json:"owner"`
	Mode             *int   `json:"mode"`
	CodexHome        string `json:"codexHome"`
	InheritCodexHome *bool  `json:"inheritCodexHome"`
	IgnoreUserConfig *bool  `json:"ignoreUserConfig"`
	IgnoreRules      *bool  `json:"ignoreRules"`
}

type attachmentInput struct {
	MaxCount      any `json:"maxCount"`
	MaxBytes      any `json:"maxBytes"`
	MaxFileBytes  any `json:"maxFileBytes"`
	ImageMaxBytes any `json:"imageMaxBytes"`
	CacheTTLMS    any `json:"cacheTtlMs"`
	CacheMaxBytes any `json:"cacheMaxBytes"`
}

type larkCliInput struct {
	IdentityPreset  string                  `json:"identityPreset"`
	LocalUserImport *larkCliUserImportInput `json:"localUserImport"`
}

type larkCliUserImportInput struct {
	Status      string `json:"status"`
	AttemptedAt string `json:"attemptedAt"`
	ImportedAt  string `json:"importedAt"`
	Reason      string `json:"reason"`
}

type legacyConfigInput struct {
	SchemaVersion any                     `json:"schemaVersion"`
	Accounts      *larkcli.AccountsConfig `json:"accounts"`
	App           *larkcli.AppCredentials `json:"app"`
	Secrets       *larkcli.SecretsConfig  `json:"secrets"`
	Preferences   map[string]any          `json:"preferences"`
}

func Normalize(data []byte, options LoadOptions) (*Snapshot, error) {
	root, err := NormalizeRootOrLegacy(data, options)
	if err != nil {
		return nil, err
	}
	profileName := selectProfile(root, options)
	profile, ok := root.Profiles[profileName]
	if !ok {
		return nil, fmt.Errorf("profile not found: %s", profileName)
	}
	return &Snapshot{
		Root:        root,
		ProfileName: profileName,
		Profile:     profile,
		Runtime:     runtimeConfig(root, profileName),
	}, nil
}

func NormalizeRootOrLegacy(data []byte, options LoadOptions) (RootConfig, error) {
	var probe struct {
		SchemaVersion any                        `json:"schemaVersion"`
		Profiles      map[string]json.RawMessage `json:"profiles"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return RootConfig{}, err
	}
	if schemaVersionIsTwo(probe.SchemaVersion) && probe.Profiles != nil {
		return NormalizeRoot(data)
	}
	return normalizeLegacy(data, options)
}

func NormalizeRoot(data []byte) (RootConfig, error) {
	var input rootInput
	if err := json.Unmarshal(data, &input); err != nil {
		return RootConfig{}, err
	}
	if input.SchemaVersion != 2 {
		return RootConfig{}, errors.New("root schemaVersion must be 2")
	}
	if input.Profiles == nil {
		return RootConfig{}, errors.New("root profiles is required")
	}

	profiles := make(map[string]ProfileConfig, len(input.Profiles))
	for name, raw := range input.Profiles {
		profile, err := NormalizeProfile(raw)
		if err != nil {
			return RootConfig{}, fmt.Errorf("profile %s: %w", name, err)
		}
		profiles[name] = profile
	}
	migrations := normalizeRootMigrations(input.Migrations)
	root := RootConfig{
		SchemaVersion: 2,
		ActiveProfile: input.ActiveProfile,
		Preferences:   map[string]any{},
		Secrets:       input.Secrets,
		Migrations:    migrations,
		Profiles:      profiles,
		Extra:         extraFields(data, rootKnownFields),
	}
	return root, nil
}

func NormalizeProfile(data []byte) (ProfileConfig, error) {
	var input profileInput
	if err := json.Unmarshal(data, &input); err != nil {
		return ProfileConfig{}, err
	}
	return normalizeProfileInput(input, extraFields(data, profileKnownFields))
}

func normalizeProfileInput(input profileInput, extra map[string]json.RawMessage) (ProfileConfig, error) {
	if input.SchemaVersion != 2 {
		return ProfileConfig{}, errors.New("profile schemaVersion must be 2")
	}
	if input.AgentKind != AgentClaude && input.AgentKind != AgentCodex {
		return ProfileConfig{}, errors.New("agentKind must be claude or codex")
	}
	if input.Accounts == nil || !validAppCredentials(input.Accounts.App) {
		return ProfileConfig{}, errors.New("accounts.app is incomplete")
	}
	if input.AgentKind == AgentCodex && input.Codex == nil {
		return ProfileConfig{}, errors.New("codex profile requires codex configuration")
	}

	normalizedPermissions, err := permissions.NormalizePermissions(permissions.NormalizeInput{
		Permissions: input.Permissions,
		Sandbox:     input.Sandbox,
	})
	if err != nil {
		return ProfileConfig{}, err
	}
	sandbox, err := permissions.PermissionsToLegacySandbox(normalizedPermissions.Permissions)
	if err != nil {
		return ProfileConfig{}, err
	}

	return ProfileConfig{
		SchemaVersion:    2,
		AgentKind:        input.AgentKind,
		Accounts:         *input.Accounts,
		Secrets:          input.Secrets,
		Preferences:      normalizePreferences(input.Preferences),
		Access:           normalizeAccess(input.Access, boolFromPreferences(input.Preferences, "requireMentionInGroup")),
		Workspaces:       normalizeWorkspaces(input.Workspaces),
		Sandbox:          sandbox,
		Permissions:      normalizedPermissions.Permissions,
		PermissionSource: normalizedPermissions.Source,
		Codex:            normalizeCodex(input.Codex),
		Attachments:      normalizeAttachments(input.Attachments),
		Comments:         map[string]any{},
		LarkCli:          normalizeLarkCli(input.LarkCli),
		Extra:            extra,
	}, nil
}

func normalizeLegacy(data []byte, options LoadOptions) (RootConfig, error) {
	var legacy legacyConfigInput
	if err := json.Unmarshal(data, &legacy); err != nil {
		return RootConfig{}, err
	}
	accounts := legacy.Accounts
	if accounts == nil && legacy.App != nil {
		accounts = &larkcli.AccountsConfig{App: *legacy.App}
	}
	if accounts == nil || !validAppCredentials(accounts.App) {
		return RootConfig{}, errors.New("legacy config is missing accounts.app")
	}

	profile := options.Profile
	if profile == "" {
		profile = options.ActiveProfile
	}
	if profile == "" {
		profile = "claude"
	}
	agentKind := options.AgentKind
	if agentKind == "" {
		agentKind = AgentClaude
	}
	input := profileInput{
		SchemaVersion: 2,
		AgentKind:     agentKind,
		Accounts:      accounts,
		Preferences:   legacy.Preferences,
		Access:        legacyAccess(legacy.Preferences),
		Codex:         codexInputFromConfig(options.Codex),
	}
	profileConfig, err := normalizeProfileInput(input, nil)
	if err != nil {
		return RootConfig{}, err
	}
	profileConfig.PermissionSource = permissions.PermissionSourcePermissions
	return RootConfig{
		SchemaVersion: 2,
		ActiveProfile: profile,
		Preferences:   map[string]any{},
		Secrets:       legacy.Secrets,
		Migrations:    &RootMigrations{PermissionDefaultsV1: []string{profile}},
		Profiles: map[string]ProfileConfig{
			profile: profileConfig,
		},
	}, nil
}

func runtimeConfig(root RootConfig, profileName string) RuntimeConfig {
	profile := root.Profiles[profileName]
	secrets := profile.Secrets
	if secrets == nil {
		secrets = root.Secrets
	}
	return RuntimeConfig{
		ProfileConfig: profile,
		Secrets:       secrets,
	}
}

func selectProfile(root RootConfig, options LoadOptions) string {
	if options.Profile != "" {
		return options.Profile
	}
	if options.ActiveProfile != "" {
		return options.ActiveProfile
	}
	return root.ActiveProfile
}

func normalizeRootMigrations(input *RootMigrations) *RootMigrations {
	if input == nil {
		return nil
	}
	values := uniqueSortedStrings(input.PermissionDefaultsV1)
	if len(values) == 0 {
		return nil
	}
	return &RootMigrations{PermissionDefaultsV1: values}
}

func normalizePreferences(input map[string]any) map[string]any {
	if input == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		if key == "access" || key == "requireMentionInGroup" || key == "messageReply" {
			continue
		}
		out[key] = value
	}
	if reply, ok := input["messageReply"].(string); ok && isMessageReply(reply) {
		out["messageReply"] = reply
	}
	return out
}

func normalizeAccess(input *accessInput, legacyRequireMention *bool) ProfileAccess {
	requireMention := true
	if legacyRequireMention != nil {
		requireMention = *legacyRequireMention
	}
	if input != nil && input.RequireMentionInGroup != nil {
		requireMention = *input.RequireMentionInGroup
	}
	return ProfileAccess{
		AllowedUsers:          stringArray(inputArray(input, "allowedUsers")),
		AllowedChats:          stringArray(inputArray(input, "allowedChats")),
		Admins:                stringArray(inputArray(input, "admins")),
		RequireMentionInGroup: requireMention,
	}
}

func normalizeWorkspaces(input *workspacesInput) Workspaces {
	if input == nil {
		return Workspaces{}
	}
	defaultWorkspace := strings.TrimSpace(input.Default)
	if defaultWorkspace == "" {
		return Workspaces{}
	}
	return Workspaces{Default: defaultWorkspace}
}

func normalizeCodex(input *codexInput) *CodexConfig {
	if input == nil {
		return nil
	}
	return &CodexConfig{
		BinaryPath:       input.BinaryPath,
		Realpath:         input.Realpath,
		Version:          input.Version,
		SHA256:           input.SHA256,
		Owner:            input.Owner,
		Mode:             input.Mode,
		CodexHome:        input.CodexHome,
		InheritCodexHome: input.InheritCodexHome == nil || *input.InheritCodexHome,
		IgnoreUserConfig: input.IgnoreUserConfig != nil && *input.IgnoreUserConfig,
		IgnoreRules:      input.IgnoreRules == nil || *input.IgnoreRules,
	}
}

func normalizeAttachments(input *attachmentInput) AttachmentConfig {
	defaults := DefaultAttachmentConfig()
	if input == nil {
		return defaults
	}
	return AttachmentConfig{
		MaxCount:      int(numberOr(input.MaxCount, float64(defaults.MaxCount))),
		MaxBytes:      int64(numberOr(input.MaxBytes, float64(defaults.MaxBytes))),
		MaxFileBytes:  int64(numberOr(input.MaxFileBytes, float64(defaults.MaxFileBytes))),
		ImageMaxBytes: int64(numberOr(input.ImageMaxBytes, float64(defaults.ImageMaxBytes))),
		CacheTTLMS:    int64(numberOr(input.CacheTTLMS, float64(defaults.CacheTTLMS))),
		CacheMaxBytes: int64(numberOr(input.CacheMaxBytes, float64(defaults.CacheMaxBytes))),
	}
}

func normalizeLarkCli(input *larkCliInput) LarkCliConfig {
	if input == nil {
		return LarkCliConfig{IdentityPreset: LarkCliIdentityBotOnly}
	}
	preset := LarkCliIdentityBotOnly
	if input.IdentityPreset == string(LarkCliIdentityUserDefault) {
		preset = LarkCliIdentityUserDefault
	}
	return LarkCliConfig{
		IdentityPreset:  preset,
		LocalUserImport: normalizeLocalUserImport(input.LocalUserImport),
	}
}

func normalizeLocalUserImport(input *larkCliUserImportInput) *LarkCliLocalUserImport {
	if input == nil || !isLocalUserImportStatus(input.Status) {
		return nil
	}
	return &LarkCliLocalUserImport{
		Status:      LarkCliUserImportStatus(input.Status),
		AttemptedAt: input.AttemptedAt,
		ImportedAt:  input.ImportedAt,
		Reason:      input.Reason,
	}
}

func validAppCredentials(app larkcli.AppCredentials) bool {
	if app.ID == "" || !hasSecret(app.Secret) {
		return false
	}
	return app.Tenant == larkcli.TenantFeishu || app.Tenant == larkcli.TenantLark
}

func hasSecret(secret any) bool {
	switch value := secret.(type) {
	case nil:
		return false
	case string:
		return value != ""
	default:
		return true
	}
}

func boolFromPreferences(preferences map[string]any, key string) *bool {
	if preferences == nil {
		return nil
	}
	value, ok := preferences[key].(bool)
	if !ok {
		return nil
	}
	return &value
}

func legacyAccess(preferences map[string]any) *accessInput {
	if preferences == nil {
		return nil
	}
	accessValue, ok := preferences["access"].(map[string]any)
	if !ok {
		return &accessInput{RequireMentionInGroup: boolFromPreferences(preferences, "requireMentionInGroup")}
	}
	return &accessInput{
		AllowedUsers:          anyArray(accessValue["allowedUsers"]),
		AllowedChats:          anyArray(accessValue["allowedChats"]),
		Admins:                anyArray(accessValue["admins"]),
		RequireMentionInGroup: boolFromPreferences(preferences, "requireMentionInGroup"),
	}
}

func codexInputFromConfig(cfg *CodexConfig) *codexInput {
	if cfg == nil {
		return nil
	}
	return &codexInput{
		BinaryPath:       cfg.BinaryPath,
		Realpath:         cfg.Realpath,
		Version:          cfg.Version,
		SHA256:           cfg.SHA256,
		Owner:            cfg.Owner,
		Mode:             cfg.Mode,
		CodexHome:        cfg.CodexHome,
		InheritCodexHome: &cfg.InheritCodexHome,
		IgnoreUserConfig: &cfg.IgnoreUserConfig,
		IgnoreRules:      &cfg.IgnoreRules,
	}
}

func schemaVersionIsTwo(value any) bool {
	switch typed := value.(type) {
	case float64:
		return typed == 2
	case int:
		return typed == 2
	default:
		return false
	}
}

func isMessageReply(value string) bool {
	return value == "card" || value == "markdown" || value == "text"
}

func isLocalUserImportStatus(value string) bool {
	switch LarkCliUserImportStatus(value) {
	case LarkCliUserImportNotNeeded, LarkCliUserImportImported, LarkCliUserImportSkippedExistingPrivate, LarkCliUserImportSkippedNoLocalUser, LarkCliUserImportFailed:
		return true
	default:
		return false
	}
}

func inputArray(input *accessInput, field string) []any {
	if input == nil {
		return nil
	}
	switch field {
	case "allowedUsers":
		return input.AllowedUsers
	case "allowedChats":
		return input.AllowedChats
	case "admins":
		return input.Admins
	default:
		return nil
	}
}

func anyArray(value any) []any {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	return items
}

func stringArray(values []any) []string {
	out := []string{}
	for _, value := range values {
		if text, ok := value.(string); ok {
			out = append(out, text)
		}
	}
	return out
}

func numberOr(value any, fallback float64) float64 {
	var number float64
	switch typed := value.(type) {
	case float64:
		number = typed
	case int:
		number = float64(typed)
	case int64:
		number = float64(typed)
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return fallback
		}
		number = parsed
	default:
		return fallback
	}
	if math.IsInf(number, 0) || math.IsNaN(number) || number <= 0 {
		return fallback
	}
	return number
}

func uniqueSortedStrings(values []string) []string {
	seen := map[string]struct{}{}
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		seen[trimmed] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for value := range seen {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
