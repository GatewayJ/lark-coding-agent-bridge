package bridge

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/configstore"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/compat/apppaths"
)

type ConfigAgentKind string

const (
	ConfigAgentClaude ConfigAgentKind = "claude"
	ConfigAgentCodex  ConfigAgentKind = "codex"
)

type ConfigLarkCliIdentityPreset string

const (
	ConfigLarkCliIdentityBotOnly     ConfigLarkCliIdentityPreset = "bot-only"
	ConfigLarkCliIdentityUserDefault ConfigLarkCliIdentityPreset = "user-default"
)

type ConfigLarkCliUserImportStatus string

const (
	ConfigLarkCliUserImportNotNeeded              ConfigLarkCliUserImportStatus = "not-needed"
	ConfigLarkCliUserImportImported               ConfigLarkCliUserImportStatus = "imported"
	ConfigLarkCliUserImportSkippedExistingPrivate ConfigLarkCliUserImportStatus = "skipped-existing-private-user"
	ConfigLarkCliUserImportSkippedNoLocalUser     ConfigLarkCliUserImportStatus = "skipped-no-local-user"
	ConfigLarkCliUserImportFailed                 ConfigLarkCliUserImportStatus = "failed"
)

type ConfigPermissionSource string

const (
	ConfigPermissionSourcePermissions ConfigPermissionSource = "permissions"
	ConfigPermissionSourceSandbox     ConfigPermissionSource = "sandbox"
	ConfigPermissionSourceDefault     ConfigPermissionSource = "default"
)

type ConfigCodexSandboxMode string

const (
	ConfigCodexSandboxReadOnly         ConfigCodexSandboxMode = "read-only"
	ConfigCodexSandboxWorkspaceWrite   ConfigCodexSandboxMode = "workspace-write"
	ConfigCodexSandboxDangerFullAccess ConfigCodexSandboxMode = "danger-full-access"
)

type ConfigClaudePermissionMode string

const (
	ConfigClaudePermissionDefault           ConfigClaudePermissionMode = "default"
	ConfigClaudePermissionAcceptEdits       ConfigClaudePermissionMode = "acceptEdits"
	ConfigClaudePermissionBypassPermissions ConfigClaudePermissionMode = "bypassPermissions"
	ConfigClaudePermissionPlan              ConfigClaudePermissionMode = "plan"
)

type ConfigLoadOptions struct {
	Profile       string
	ActiveProfile string
	AgentKind     ConfigAgentKind
	Codex         *ConfigCodex
}

type BootstrapProfileOptions struct {
	RootDir          string
	ConfigPath       string
	Profile          string
	AgentKind        ConfigAgentKind
	AppID            string
	AppSecret        SecretInput
	Tenant           LarkCLITenantBrand
	DefaultWorkspace string
	Preferences      map[string]any
	Access           ConfigProfileAccess
	Permissions      ConfigPermissionConfig
	RequireMention   *bool
	Codex            *ConfigCodex
	Overwrite        bool
}

type ConfigSnapshot struct {
	Root        ConfigRoot
	ProfileName string
	Profile     ConfigProfile
	Runtime     ConfigRuntime
}

type ConfigRoot struct {
	SchemaVersion int                        `json:"schemaVersion"`
	ActiveProfile string                     `json:"activeProfile"`
	Preferences   map[string]any             `json:"preferences"`
	Secrets       *LarkCLISecretsConfig      `json:"secrets,omitempty"`
	Migrations    *ConfigRootMigrations      `json:"migrations,omitempty"`
	Profiles      map[string]ConfigProfile   `json:"profiles"`
	Extra         map[string]json.RawMessage `json:"-"`
}

type ConfigRootMigrations struct {
	PermissionDefaultsV1 []string `json:"permissionDefaultsV1,omitempty"`
}

type ConfigProfile struct {
	SchemaVersion    int                        `json:"schemaVersion"`
	AgentKind        ConfigAgentKind            `json:"agentKind"`
	Accounts         LarkCLIAccountsConfig      `json:"accounts"`
	Secrets          *LarkCLISecretsConfig      `json:"secrets,omitempty"`
	Preferences      map[string]any             `json:"preferences"`
	Access           ConfigProfileAccess        `json:"access"`
	Workspaces       ConfigWorkspaces           `json:"workspaces"`
	Sandbox          ConfigLegacySandbox        `json:"sandbox"`
	Permissions      ConfigPermissionConfig     `json:"permissions"`
	PermissionSource ConfigPermissionSource     `json:"permissionSource,omitempty"`
	Codex            *ConfigCodex               `json:"codex,omitempty"`
	Attachments      ConfigAttachments          `json:"attachments"`
	Comments         map[string]any             `json:"comments"`
	LarkCli          ConfigLarkCli              `json:"larkCli"`
	Extra            map[string]json.RawMessage `json:"-"`
}

type ConfigRuntime struct {
	ConfigProfile
	Secrets *LarkCLISecretsConfig `json:"secrets,omitempty"`
}

type ConfigProfileAccess struct {
	AllowedUsers          []string `json:"allowedUsers"`
	AllowedChats          []string `json:"allowedChats"`
	Admins                []string `json:"admins"`
	RequireMentionInGroup bool     `json:"requireMentionInGroup"`
}

type ConfigWorkspaces struct {
	Default string `json:"default,omitempty"`
}

type ConfigLegacySandbox struct {
	Default     ConfigCodexSandboxMode `json:"default"`
	Max         ConfigCodexSandboxMode `json:"max"`
	DefaultMode ConfigCodexSandboxMode `json:"defaultMode"`
	MaxMode     ConfigCodexSandboxMode `json:"maxMode"`
}

type ConfigPermissionConfig struct {
	DefaultAccess AccessMode                    `json:"defaultAccess"`
	MaxAccess     AccessMode                    `json:"maxAccess"`
	Claude        *ConfigClaudePermissionConfig `json:"claude,omitempty"`
}

type ConfigClaudePermissionConfig struct {
	PermissionMode ConfigClaudePermissionMode `json:"permissionMode"`
}

type ConfigCodex struct {
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

type ConfigAttachments struct {
	MaxCount      int   `json:"maxCount"`
	MaxBytes      int64 `json:"maxBytes"`
	MaxFileBytes  int64 `json:"maxFileBytes"`
	ImageMaxBytes int64 `json:"imageMaxBytes"`
	CacheTTLMS    int64 `json:"cacheTtlMs"`
	CacheMaxBytes int64 `json:"cacheMaxBytes"`
}

type ConfigLarkCli struct {
	IdentityPreset  ConfigLarkCliIdentityPreset `json:"identityPreset"`
	LocalUserImport *ConfigLarkCliUserImport    `json:"localUserImport,omitempty"`
}

type ConfigLarkCliUserImport struct {
	Status      ConfigLarkCliUserImportStatus `json:"status"`
	AttemptedAt string                        `json:"attemptedAt,omitempty"`
	ImportedAt  string                        `json:"importedAt,omitempty"`
	Reason      string                        `json:"reason,omitempty"`
}

func LoadConfig(path string, options ConfigLoadOptions) (*ConfigSnapshot, error) {
	internalOptions, err := toInternalConfigLoadOptions(options)
	if err != nil {
		return nil, err
	}
	snapshot, err := configstore.Load(path, internalOptions)
	if err != nil {
		return nil, err
	}
	out, err := fromInternalConfigSnapshot(*snapshot)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func NormalizeConfig(data []byte, options ConfigLoadOptions) (*ConfigSnapshot, error) {
	internalOptions, err := toInternalConfigLoadOptions(options)
	if err != nil {
		return nil, err
	}
	snapshot, err := configstore.Normalize(data, internalOptions)
	if err != nil {
		return nil, err
	}
	out, err := fromInternalConfigSnapshot(*snapshot)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func NormalizeRootConfig(data []byte) (ConfigRoot, error) {
	root, err := configstore.NormalizeRoot(data)
	if err != nil {
		return ConfigRoot{}, err
	}
	return fromInternalConfigRoot(root)
}

func NormalizeProfileConfig(data []byte) (ConfigProfile, error) {
	profile, err := configstore.NormalizeProfile(data)
	if err != nil {
		return ConfigProfile{}, err
	}
	return fromInternalConfigProfile(profile)
}

func SaveConfig(path string, root ConfigRoot) error {
	internalRoot, err := toInternalConfigRoot(root)
	if err != nil {
		return err
	}
	return configstore.SaveRoot(path, internalRoot)
}

func WriteActiveProfile(rootDir string, profile string) error {
	return configstore.WriteActiveProfile(rootDir, profile)
}

func BootstrapProfileConfig(options BootstrapProfileOptions) (*ConfigSnapshot, error) {
	agentKind := options.AgentKind
	if agentKind == "" {
		agentKind = ConfigAgentCodex
	}
	profileName := options.Profile
	if profileName == "" {
		profileName = string(agentKind)
	}
	rootDir := options.RootDir
	if rootDir == "" && options.ConfigPath != "" {
		rootDir = filepath.Dir(options.ConfigPath)
	}
	paths, err := apppaths.Resolve(apppaths.Options{RootDir: rootDir, Profile: profileName})
	if err != nil {
		return nil, err
	}
	configPath := options.ConfigPath
	if configPath == "" {
		configPath = paths.ConfigFile
	}
	if !options.Overwrite {
		if _, err := os.Stat(configPath); err == nil {
			return nil, errors.New("config already exists; set Overwrite to replace it")
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	if options.AppID == "" {
		return nil, errors.New("app id is required")
	}
	if err := validateConfigSecret(options.AppSecret); err != nil {
		return nil, err
	}
	tenant := options.Tenant
	if tenant == "" {
		tenant = LarkCLITenantFeishu
	}
	defaultWorkspace := options.DefaultWorkspace
	if defaultWorkspace == "" {
		defaultWorkspace = paths.DefaultWorkspaceDir
	}
	preferences := cloneAnyMap(options.Preferences)
	permissions := options.Permissions
	if permissions.DefaultAccess == "" {
		permissions.DefaultAccess = AccessFull
	}
	if permissions.MaxAccess == "" {
		permissions.MaxAccess = AccessFull
	}
	access := options.Access
	if access.AllowedUsers == nil {
		access.AllowedUsers = []string{}
	}
	if access.AllowedChats == nil {
		access.AllowedChats = []string{}
	}
	if access.Admins == nil {
		access.Admins = []string{}
	}
	if options.RequireMention != nil {
		access.RequireMentionInGroup = *options.RequireMention
	} else {
		access.RequireMentionInGroup = true
	}
	attachments, err := convertBridgeJSON[ConfigAttachments](configstore.DefaultAttachmentConfig())
	if err != nil {
		return nil, err
	}
	defaultSandbox := configSandboxForAccess(permissions.DefaultAccess)
	maxSandbox := configSandboxForAccess(permissions.MaxAccess)
	profile := ConfigProfile{
		SchemaVersion: 2,
		AgentKind:     agentKind,
		Accounts: LarkCLIAccountsConfig{App: LarkCLIAppCredentials{
			ID:     options.AppID,
			Secret: options.AppSecret,
			Tenant: tenant,
		}},
		Preferences: preferences,
		Access:      access,
		Workspaces:  ConfigWorkspaces{Default: defaultWorkspace},
		Sandbox: ConfigLegacySandbox{
			Default:     defaultSandbox,
			Max:         maxSandbox,
			DefaultMode: defaultSandbox,
			MaxMode:     maxSandbox,
		},
		Permissions:      permissions,
		PermissionSource: ConfigPermissionSourceDefault,
		Attachments:      attachments,
		Comments:         map[string]any{},
		LarkCli:          ConfigLarkCli{IdentityPreset: ConfigLarkCliIdentityBotOnly},
	}
	if agentKind == ConfigAgentCodex {
		if options.Codex != nil {
			profile.Codex = options.Codex
		} else {
			profile.Codex = &ConfigCodex{
				BinaryPath:       "codex",
				InheritCodexHome: true,
				IgnoreRules:      true,
			}
		}
	}
	root := ConfigRoot{
		SchemaVersion: 2,
		ActiveProfile: profileName,
		Preferences:   map[string]any{},
		Profiles:      map[string]ConfigProfile{profileName: profile},
	}
	data, err := encodeValidatedBootstrapRoot(root, profileName)
	if err != nil {
		return nil, err
	}
	if err := configstore.WriteFileAtomic(configPath, data, 0o600); err != nil {
		return nil, err
	}
	if err := WriteActiveProfile(paths.RootDir, profileName); err != nil {
		return nil, err
	}
	return LoadConfig(configPath, ConfigLoadOptions{Profile: profileName})
}

func toInternalConfigLoadOptions(options ConfigLoadOptions) (configstore.LoadOptions, error) {
	var codex *configstore.CodexConfig
	if options.Codex != nil {
		converted, err := convertBridgeJSON[configstore.CodexConfig](*options.Codex)
		if err != nil {
			return configstore.LoadOptions{}, err
		}
		codex = &converted
	}
	return configstore.LoadOptions{
		Profile:       options.Profile,
		ActiveProfile: options.ActiveProfile,
		AgentKind:     configstore.AgentKind(options.AgentKind),
		Codex:         codex,
	}, nil
}

func fromInternalConfigSnapshot(snapshot configstore.Snapshot) (ConfigSnapshot, error) {
	root, err := fromInternalConfigRoot(snapshot.Root)
	if err != nil {
		return ConfigSnapshot{}, err
	}
	profile, err := fromInternalConfigProfile(snapshot.Profile)
	if err != nil {
		return ConfigSnapshot{}, err
	}
	runtime, err := fromInternalConfigRuntime(snapshot.Runtime)
	if err != nil {
		return ConfigSnapshot{}, err
	}
	return ConfigSnapshot{
		Root:        root,
		ProfileName: snapshot.ProfileName,
		Profile:     profile,
		Runtime:     runtime,
	}, nil
}

func fromInternalConfigRoot(root configstore.RootConfig) (ConfigRoot, error) {
	out, err := convertBridgeJSON[ConfigRoot](root)
	if err != nil {
		return ConfigRoot{}, err
	}
	out.Extra = root.Extra
	return out, nil
}

func toInternalConfigRoot(root ConfigRoot) (configstore.RootConfig, error) {
	out, err := convertBridgeJSON[configstore.RootConfig](root)
	if err != nil {
		return configstore.RootConfig{}, err
	}
	out.Extra = root.Extra
	return out, nil
}

func fromInternalConfigProfile(profile configstore.ProfileConfig) (ConfigProfile, error) {
	out, err := convertBridgeJSON[ConfigProfile](profile)
	if err != nil {
		return ConfigProfile{}, err
	}
	out.Extra = profile.Extra
	return out, nil
}

func fromInternalConfigRuntime(runtime configstore.RuntimeConfig) (ConfigRuntime, error) {
	return convertBridgeJSON[ConfigRuntime](runtime)
}

func cloneAnyMap(input map[string]any) map[string]any {
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func configSandboxForAccess(access AccessMode) ConfigCodexSandboxMode {
	switch access {
	case AccessReadOnly:
		return ConfigCodexSandboxReadOnly
	case AccessWorkspace:
		return ConfigCodexSandboxWorkspaceWrite
	default:
		return ConfigCodexSandboxDangerFullAccess
	}
}

func encodeValidatedBootstrapRoot(root ConfigRoot, profileName string) ([]byte, error) {
	internalRoot, err := toInternalConfigRoot(root)
	if err != nil {
		return nil, err
	}
	data, err := configstore.FormatRootConfig(internalRoot)
	if err != nil {
		return nil, err
	}
	loadOptions, err := toInternalConfigLoadOptions(ConfigLoadOptions{Profile: profileName})
	if err != nil {
		return nil, err
	}
	if _, err := configstore.Normalize(data, loadOptions); err != nil {
		return nil, err
	}
	return data, nil
}

func validateConfigSecret(secret SecretInput) error {
	if secret.Plain == nil && secret.Ref == nil {
		return errors.New("app secret is required")
	}
	if secret.Plain != nil && secret.Ref != nil {
		return errors.New("app secret must be plain or reference, not both")
	}
	if secret.Plain != nil {
		if strings.TrimSpace(*secret.Plain) == "" {
			return errors.New("app secret is required")
		}
		return nil
	}
	ref := secret.Ref
	if strings.TrimSpace(ref.ID) == "" {
		return errors.New("app secret ref id is required")
	}
	switch ref.Source {
	case SecretSourceEnv, SecretSourceFile, SecretSourceInline, SecretSourceExec:
		return nil
	default:
		return errors.New("app secret ref source is invalid")
	}
}

func convertBridgeJSON[T any](input any) (T, error) {
	var out T
	data, err := json.Marshal(input)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return out, err
	}
	return out, nil
}
