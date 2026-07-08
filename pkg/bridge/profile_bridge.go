package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/configstore"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/larkcli"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/secretstore"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/compat/apppaths"
)

// ProfileBridgeOptions selects the profile and host integrations used by
// NewProfileBridge. Empty Home/Profile follow the same profile resolution rules
// as the CLI. AppSecret overrides the configured secret and is persisted into
// the profile keystore. SecretsGetterCommand must point to a binary that
// supports "secrets get" whenever the profile uses the bridge exec secret
// provider. Telemetry is explicit for SDK embedders: pass Telemetry directly or
// set LoadTelemetryFromEnv to opt into the JavaScript telemetry module named by
// LARK_CHANNEL_TELEMETRY_MODULE.
type ProfileBridgeOptions struct {
	Home    string
	Profile string
	Config  string
	Agent   string
	AppID   string
	Tenant  string
	Version string

	AppSecret            string
	SecretsGetterCommand string
	LarkTransport        LarkTransport

	SkipCheckLarkCLI        bool
	SkipAgentAvailability   bool
	LarkCLICommand          string
	LarkCLIBaseEnv          map[string]string
	LarkCLIRunner           LarkCLIPreflightRunner
	CommandOptions          CommandOptions
	InitialOwnerOpenID      string
	AccountValidator        CommandAccountValidator
	DisableDefaultLogger    bool
	DisableDefaultTelemetry bool
	LoadTelemetryFromEnv    bool
	Logger                  Logger
	Telemetry               TelemetryAdapter
	LogDir                  string
	LogStdout               io.Writer
	LogStderr               io.Writer
	TelemetryStderr         io.Writer
}

type ProfileBridgeInfo = ProfileServiceInfo

// NewProfileBridge builds a foreground, profile-backed bridge. It loads the
// profile config, creates required profile directories, may materialize the app
// secret into the profile keystore, optionally runs agent and lark-cli
// preflight checks, and returns a Bridge that still must be started with Start.
func NewProfileBridge(ctx context.Context, options ProfileBridgeOptions) (*Bridge, ProfileBridgeInfo, error) {
	info, paths, err := resolveProfileServiceInfo(ProfileServiceOptions{
		Home:    options.Home,
		Profile: options.Profile,
		Config:  options.Config,
		Agent:   options.Agent,
		AppID:   options.AppID,
	})
	if err != nil {
		return nil, ProfileBridgeInfo{}, err
	}
	if err := os.MkdirAll(paths.DefaultWorkspaceDir, 0o700); err != nil {
		return nil, ProfileBridgeInfo{}, err
	}
	snapshot, err := configstore.Load(info.ConfigPath, profileBridgeLoadOptions(options, info.Profile))
	if err != nil {
		return nil, ProfileBridgeInfo{}, err
	}
	runtimeConfig := snapshot.Runtime
	if options.Agent != "" {
		runtimeConfig.AgentKind = configstore.AgentKind(options.Agent)
	}
	if runtimeConfig.Workspaces.Default != "" {
		if err := os.MkdirAll(runtimeConfig.Workspaces.Default, 0o700); err != nil {
			return nil, ProfileBridgeInfo{}, err
		}
	}
	appConfig := larkcli.AppConfig{
		Accounts: runtimeConfig.Accounts,
		Secrets:  runtimeConfig.Secrets,
	}
	if options.AppID != "" {
		appConfig.Accounts.App.ID = options.AppID
	}
	if options.Tenant != "" {
		appConfig.Accounts.App.Tenant = larkcli.TenantBrand(options.Tenant)
	}
	if appConfig.Accounts.App.ID == "" {
		return nil, ProfileBridgeInfo{}, fmt.Errorf("app id is empty")
	}

	appSecret, appConfig, err := profileBridgeAppSecret(ctx, options, appConfig, paths)
	if err != nil {
		return nil, ProfileBridgeInfo{}, err
	}
	if err := validateProfileBridgeSecretsGetter(options, appConfig); err != nil {
		return nil, ProfileBridgeInfo{}, err
	}
	transport := options.LarkTransport
	if transport == nil {
		transport, err = NewOAPILarkTransport(OAPILarkTransportOptions{
			AppID:     appConfig.Accounts.App.ID,
			AppSecret: appSecret,
			Tenant:    string(appConfig.Accounts.App.Tenant),
		})
		if err != nil {
			return nil, ProfileBridgeInfo{}, err
		}
	}

	projectionEnv := LarkCliEnvContext{
		Profile:                 paths.Profile,
		RootDir:                 paths.RootDir,
		ConfigPath:              info.ConfigPath,
		LarkCliConfigDir:        paths.LarkCliConfigDir,
		LarkCliSourceConfigFile: paths.LarkCliSourceConfigFile,
	}
	larkEnv := BuildLarkChannelEnv(projectionEnv)
	client, agentKind, err := profileBridgeClient(runtimeConfig, paths, larkEnv)
	if err != nil {
		return nil, ProfileBridgeInfo{}, err
	}
	if !options.SkipAgentAvailability {
		availability, err := client.CheckAvailability(ctx)
		if err != nil {
			return nil, ProfileBridgeInfo{}, err
		}
		if !availability.OK {
			return nil, ProfileBridgeInfo{}, profileBridgeAvailabilityError(availability)
		}
	}

	projectionPaths := LarkCliProjectionPaths{
		RootDir:                 paths.RootDir,
		Profile:                 paths.Profile,
		LarkCliSourceDir:        paths.LarkCliSourceDir,
		LarkCliSourceConfigFile: paths.LarkCliSourceConfigFile,
		SecretsGetterScript:     paths.SecretsGetterScript,
		SecretsGetterCommand:    profileBridgeSecretsGetterCommand(options),
	}
	publicAppConfig, err := fromInternalLarkCLIAppConfig(appConfig)
	if err != nil {
		return nil, ProfileBridgeInfo{}, err
	}
	identityPreset := LarkCliIdentityPreset(runtimeConfig.LarkCli.IdentityPreset)
	if !options.SkipCheckLarkCLI {
		publicProfile, err := fromInternalConfigProfile(runtimeConfig.ProfileConfig)
		if err != nil {
			return nil, ProfileBridgeInfo{}, err
		}
		preflight, err := PreflightLarkCLI(ctx, LarkCLIPreflightOptions{
			Config:          publicAppConfig,
			ProjectionPaths: projectionPaths,
			Env:             projectionEnv,
			IdentityPreset:  identityPreset,
			ProfileConfig:   &publicProfile,
			Command:         options.LarkCLICommand,
			BaseEnv:         options.LarkCLIBaseEnv,
			Runner:          options.LarkCLIRunner,
		})
		if err != nil {
			return nil, ProfileBridgeInfo{}, err
		}
		if preflight.IdentityPreset != "" {
			identityPreset = preflight.IdentityPreset
		}
	}
	projection := NewLarkCLIProjectionHook(LarkCLIProjectionHookOptions{
		Config:              publicAppConfig,
		Paths:               projectionPaths,
		Env:                 projectionEnv,
		IdentityPreset:      identityPreset,
		ApplyIdentityPolicy: true,
	})
	callbackAuth, err := NewCallbackAuth(CallbackAuthOptions{
		Keys:           []CallbackKey{{Version: 1, Secret: appSecret}},
		NonceStorePath: filepath.Join(paths.ProfileDir, "callback-nonces.json"),
	})
	if err != nil {
		return nil, ProfileBridgeInfo{}, err
	}

	logger, telemetry := profileBridgeObservability(ctx, options, paths, appConfig)
	var instance *Bridge
	commandOptions, err := profileBridgeCommandOptions(runtimeConfig, paths, info.ConfigPath, projectionEnv, options, func(ctx context.Context) error {
		if instance == nil {
			return fmt.Errorf("bridge is not initialized")
		}
		refreshed, err := configstore.Load(info.ConfigPath, configstore.LoadOptions{Profile: paths.Profile})
		if err != nil {
			return err
		}
		next := refreshed.Runtime.Accounts.App
		if next.ID == "" {
			return fmt.Errorf("app id is empty")
		}
		return instance.Reconnect(ctx, RuntimeReconnectOptions{
			AppID:      next.ID,
			Tenant:     RuntimeTenant(next.Tenant),
			ConfigPath: info.ConfigPath,
		})
	})
	if err != nil {
		return nil, ProfileBridgeInfo{}, err
	}
	processHooks := profileBridgeProcessHooks{instance: func() *Bridge { return instance }}
	if commandOptions.ProcessIDFunc == nil {
		commandOptions.ProcessIDFunc = processHooks.CurrentID
	}
	if commandOptions.Processes == nil {
		commandOptions.Processes = processHooks
	}
	if commandOptions.ProcessController == nil {
		commandOptions.ProcessController = processHooks
	}

	instance, err = New(Options{
		Home:                  paths.RootDir,
		Profile:               paths.Profile,
		Logger:                logger,
		Telemetry:             telemetry,
		Client:                client,
		LarkTransport:         transport,
		LarkProfileProjection: projection,
		LarkManaged: LarkManagedOptions{
			MessageReplyMode:   startProfileBridgeReplyMode(runtimeConfig.Preferences),
			ShowToolCalls:      profileBridgeBoolPtr(startProfileBridgeShowToolCalls(runtimeConfig.Preferences)),
			CotMessages:        startProfileBridgeCotMessages(runtimeConfig.Preferences),
			CommandOptions:     commandOptions,
			InitialOwnerOpenID: options.InitialOwnerOpenID,
			CallbackAuth:       callbackAuth,
		},
		AppID:      appConfig.Accounts.App.ID,
		Tenant:     RuntimeTenant(appConfig.Accounts.App.Tenant),
		AgentKind:  agentKind,
		Version:    firstNonEmptyBridge(options.Version, "go-sdk"),
		ConfigPath: info.ConfigPath,
	})
	if err != nil {
		return nil, ProfileBridgeInfo{}, err
	}
	return instance, info, nil
}

func profileBridgeLoadOptions(options ProfileBridgeOptions, profile string) configstore.LoadOptions {
	loadOptions := configstore.LoadOptions{Profile: profile}
	if options.Agent != "" {
		loadOptions.AgentKind = configstore.AgentKind(options.Agent)
	}
	return loadOptions
}

func profileBridgeAppSecret(ctx context.Context, options ProfileBridgeOptions, cfg larkcli.AppConfig, paths apppaths.Paths) (string, larkcli.AppConfig, error) {
	if options.AppSecret == "" {
		secret, err := resolveProfileServiceAppSecret(ctx, cfg, paths)
		return secret, cfg, err
	}
	secretID := secretstore.SecretKeyForApp(cfg.Accounts.App.ID)
	if err := storeProfileServiceAppSecret(paths, secretID, options.AppSecret); err != nil {
		return "", larkcli.AppConfig{}, err
	}
	cfg.Accounts.App.Secret = larkcli.SecretRef{Source: secretstore.SourceExec, Provider: "bridge", ID: secretID}
	return options.AppSecret, cfg, nil
}

func validateProfileBridgeSecretsGetter(options ProfileBridgeOptions, cfg larkcli.AppConfig) error {
	if _, ok := larkcli.BridgeProviderName(cfg.Accounts.App.Secret); !ok {
		return nil
	}
	if options.SecretsGetterCommand != "" {
		return nil
	}
	exe := filepath.Base(currentProcessExecutable())
	if exe == "lark-channel-bridge" || exe == "lark-channel-bridge.exe" {
		return nil
	}
	return fmt.Errorf("profile bridge uses a bridge exec app secret; set ProfileBridgeOptions.SecretsGetterCommand to a binary that supports `secrets get`")
}

func profileBridgeSecretsGetterCommand(options ProfileBridgeOptions) string {
	if options.SecretsGetterCommand != "" {
		return options.SecretsGetterCommand
	}
	return currentProcessExecutable()
}

func profileBridgeClient(cfg configstore.RuntimeConfig, paths apppaths.Paths, larkEnv map[string]string) (*Client, RuntimeAgentKind, error) {
	requireMention := cfg.Access.RequireMentionInGroup
	defaultWorkingDir := cfg.Workspaces.Default
	if defaultWorkingDir == "" {
		defaultWorkingDir = paths.DefaultWorkspaceDir
	}
	defaultAccess := AccessMode(cfg.Permissions.DefaultAccess)
	maxAccess := AccessMode(cfg.Permissions.MaxAccess)
	switch cfg.AgentKind {
	case configstore.AgentCodex:
		codex := cfg.Codex
		if codex == nil {
			return nil, "", fmt.Errorf("codex profile requires codex configuration")
		}
		inheritCodexHome := codex.InheritCodexHome
		ignoreRules := codex.IgnoreRules
		client, err := NewCodexClient(CodexClientOptions{
			Binary:             codex.BinaryPath,
			ProfileStateDir:    paths.ProfileDir,
			DefaultWorkingDir:  defaultWorkingDir,
			SessionStorePath:   paths.SessionsFile,
			SessionCatalogPath: paths.SessionsFile + ".catalog.json",
			DefaultAccess:      defaultAccess,
			MaxAccess:          maxAccess,
			AllowedUsers:       cfg.Access.AllowedUsers,
			AllowedChats:       cfg.Access.AllowedChats,
			Admins:             cfg.Access.Admins,
			RequireMention:     &requireMention,
			CodexHome:          codex.CodexHome,
			InheritCodexHome:   &inheritCodexHome,
			IgnoreUserConfig:   codex.IgnoreUserConfig,
			IgnoreRules:        &ignoreRules,
			LarkChannelEnv:     larkEnv,
		})
		return client, RuntimeAgentCodex, err
	case configstore.AgentClaude:
		var permissionMode ClaudePermissionMode
		if cfg.Permissions.Claude != nil {
			permissionMode = ClaudePermissionMode(cfg.Permissions.Claude.PermissionMode)
		}
		client, err := NewClaudeClient(ClaudeClientOptions{
			DefaultWorkingDir:  defaultWorkingDir,
			SessionStorePath:   paths.SessionsFile,
			SessionCatalogPath: paths.SessionsFile + ".catalog.json",
			DefaultAccess:      defaultAccess,
			MaxAccess:          maxAccess,
			AllowedUsers:       cfg.Access.AllowedUsers,
			AllowedChats:       cfg.Access.AllowedChats,
			Admins:             cfg.Access.Admins,
			RequireMention:     &requireMention,
			PermissionMode:     permissionMode,
			LarkChannelEnv:     larkEnv,
		})
		return client, RuntimeAgentClaude, err
	default:
		return nil, "", fmt.Errorf("unsupported agent kind %q", cfg.AgentKind)
	}
}

func profileBridgeAvailabilityError(availability AgentAvailability) error {
	if availability.Diagnostic == nil {
		return fmt.Errorf("agent preflight failed: %s", availability.Error)
	}
	return fmt.Errorf("agent preflight failed (%s): %s", availability.Diagnostic.Code, availability.Error)
}

func profileBridgeObservability(ctx context.Context, options ProfileBridgeOptions, paths apppaths.Paths, appConfig larkcli.AppConfig) (Logger, TelemetryAdapter) {
	telemetry := options.Telemetry
	if telemetry == nil && options.LoadTelemetryFromEnv && !options.DisableDefaultTelemetry {
		hostname, _ := os.Hostname()
		stderr := options.TelemetryStderr
		if stderr == nil {
			stderr = options.LogStderr
		}
		loaded, err := LoadTelemetryAdapterFromEnv(ctx, AdapterMeta{
			Version:  firstNonEmptyBridge(options.Version, "go-sdk"),
			AppID:    appConfig.Accounts.App.ID,
			Tenant:   string(appConfig.Accounts.App.Tenant),
			Hostname: hostname,
		}, stderr)
		if err == nil {
			telemetry = loaded
		}
	}
	logger := options.Logger
	if logger == nil && !options.DisableDefaultLogger {
		jsonl := NewJSONLLogger(JSONLLoggerOptions{
			Dir:       firstNonEmptyBridge(options.LogDir, paths.LogsDir),
			Stdout:    options.LogStdout,
			Stderr:    options.LogStderr,
			Telemetry: telemetry,
		})
		_, _ = jsonl.GC()
		logger = jsonl
	}
	return logger, telemetry
}

func profileBridgeCommandOptions(cfg configstore.RuntimeConfig, paths apppaths.Paths, configPath string, larkEnv LarkCliEnvContext, options ProfileBridgeOptions, restart func(context.Context) error) (CommandOptions, error) {
	keystore, err := NewKeystore(KeystoreOptions{
		Paths: KeystorePaths{
			SecretsFile:      paths.SecretsFile,
			KeystoreSaltFile: paths.KeystoreSaltFile,
		},
	})
	if err != nil {
		return CommandOptions{}, err
	}
	workspaces, err := NewFileWorkspaceStore(paths.WorkspacesFile)
	if err != nil {
		return CommandOptions{}, err
	}
	base := CommandOptions{
		ProfileName:       paths.Profile,
		ConfigPath:        configPath,
		Keystore:          keystore,
		Workspaces:        workspaces,
		ProcessID:         fmt.Sprint(os.Getpid()),
		GlobalIdleTimeout: profileBridgeGlobalIdleTimeout(cfg.Preferences),
		AccountValidator:  options.AccountValidator,
		LarkCLIIdentity: CommandLarkCLIIdentityPolicyApplierFunc(func(ctx context.Context, identity string) bool {
			return ApplyLarkCLIIdentityPolicy(ctx, larkEnv, LarkCliIdentityPreset(identity), LarkCliIdentityPolicyOptions{
				Command: options.LarkCLICommand,
				BaseEnv: options.LarkCLIBaseEnv,
			})
		}),
	}
	if restart != nil {
		base.Reconnector = CommandReconnectorFunc(func(ctx context.Context, _ bool) error {
			return restart(ctx)
		})
	}
	return mergeProfileBridgeCommandOptions(base, options.CommandOptions), nil
}

func mergeProfileBridgeCommandOptions(base CommandOptions, override CommandOptions) CommandOptions {
	if override.ProfileName != "" {
		base.ProfileName = override.ProfileName
	}
	if override.RuntimeControls != (RuntimeControls{}) {
		base.RuntimeControls = override.RuntimeControls
	}
	if override.LarkCLIStatus != "" {
		base.LarkCLIStatus = override.LarkCLIStatus
	}
	if override.ProcessID != "" {
		base.ProcessID = override.ProcessID
	}
	if override.ProcessIDFunc != nil {
		base.ProcessIDFunc = override.ProcessIDFunc
	}
	if override.Processes != nil {
		base.Processes = override.Processes
	}
	if override.ProcessController != nil {
		base.ProcessController = override.ProcessController
	}
	if override.Reconnector != nil {
		base.Reconnector = override.Reconnector
	}
	if override.LarkCLIIdentity != nil {
		base.LarkCLIIdentity = override.LarkCLIIdentity
	}
	if override.ConfigPath != "" {
		base.ConfigPath = override.ConfigPath
	}
	if override.Keystore != nil {
		base.Keystore = override.Keystore
	}
	if override.AccountValidator != nil {
		base.AccountValidator = override.AccountValidator
	}
	if len(override.KnownChats) > 0 {
		base.KnownChats = override.KnownChats
	}
	if override.Workspaces != nil {
		base.Workspaces = override.Workspaces
	}
	if override.GlobalIdleTimeout != 0 {
		base.GlobalIdleTimeout = override.GlobalIdleTimeout
	}
	return base
}

type profileBridgeProcessHooks struct {
	instance func() *Bridge
}

func (h profileBridgeProcessHooks) CurrentID() string {
	status, ok := h.status()
	if !ok || status.Runtime == nil || status.Runtime.Entry == nil {
		return fmt.Sprint(os.Getpid())
	}
	return status.Runtime.Entry.ID
}

func (h profileBridgeProcessHooks) ListProcesses() []CommandProcessEntry {
	status, ok := h.status()
	if !ok || status.Runtime == nil {
		return nil
	}
	out := make([]CommandProcessEntry, 0, len(status.Runtime.Processes))
	for _, entry := range status.Runtime.Processes {
		startedAt, _ := time.Parse(time.RFC3339Nano, entry.StartedAt)
		out = append(out, CommandProcessEntry{
			ID:        entry.ID,
			PID:       entry.PID,
			AppID:     entry.AppID,
			BotName:   entry.BotName,
			StartedAt: startedAt,
		})
	}
	return out
}

func (h profileBridgeProcessHooks) ExitSelf(context.Context) error {
	process, err := os.FindProcess(os.Getpid())
	if err != nil {
		return err
	}
	return process.Signal(os.Interrupt)
}

func (h profileBridgeProcessHooks) Terminate(ctx context.Context, entry CommandProcessEntry) (bool, error) {
	_ = ctx
	if entry.PID == 0 {
		return false, nil
	}
	process, err := os.FindProcess(entry.PID)
	if err != nil {
		return false, err
	}
	if err := process.Signal(os.Interrupt); err != nil {
		return false, err
	}
	return false, nil
}

func (h profileBridgeProcessHooks) status() (Status, bool) {
	if h.instance == nil || h.instance() == nil {
		return Status{}, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	status, err := h.instance().Status(ctx)
	if err != nil {
		return Status{}, false
	}
	return status, true
}

func startProfileBridgeReplyMode(preferences map[string]any) LarkReplyMode {
	raw, _ := preferences["messageReply"].(string)
	if raw == string(LarkReplyText) && preferences["messageReplyMigrated"] != true {
		return LarkReplyMarkdown
	}
	switch raw {
	case string(LarkReplyCard):
		return LarkReplyCard
	case string(LarkReplyText):
		return LarkReplyText
	default:
		return LarkReplyMarkdown
	}
}

func startProfileBridgeShowToolCalls(preferences map[string]any) bool {
	raw, ok := preferences["showToolCalls"].(bool)
	if ok {
		return raw
	}
	return true
}

func startProfileBridgeCotMessages(preferences map[string]any) LarkCotMessagesMode {
	raw, _ := preferences["cotMessages"].(string)
	switch raw {
	case "brief", "simple":
		return LarkCotMessagesBrief
	case "detailed", "on":
		return LarkCotMessagesDetailed
	default:
		return LarkCotMessagesOff
	}
}

func profileBridgeGlobalIdleTimeout(preferences map[string]any) time.Duration {
	minutes, ok := profileBridgePreferenceInt(preferences["runIdleTimeoutMinutes"])
	if !ok || minutes <= 0 {
		return 0
	}
	if minutes > 120 {
		minutes = 120
	}
	return time.Duration(minutes) * time.Minute
}

func profileBridgePreferenceInt(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	case json.Number:
		n, err := typed.Int64()
		if err != nil {
			return 0, false
		}
		return int(n), true
	default:
		return 0, false
	}
}

func profileBridgeBoolPtr(value bool) *bool {
	return &value
}
