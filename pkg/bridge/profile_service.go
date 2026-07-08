package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/configstore"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/larkcli"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/larkclipreflight"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/secretstore"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/compat/apppaths"
)

type ProfileServiceOptions struct {
	Home    string
	Profile string
	Config  string
	Agent   string
	AppID   string

	Executable string
	EnvPath    string
	Adapter    ServiceAdapter

	SkipCheckLarkCLI bool
	LarkCLICommand   string
	LarkCLIBaseEnv   map[string]string
	LarkCLIRunner    LarkCLIPreflightRunner

	WaitTimeout      time.Duration
	RequireConnected bool
	ProcessLister    ServiceProcessLister
	LockHandler      ServiceRuntimeLockHandler
	Preflight        func(context.Context) error
}

type ProfileServiceInfo struct {
	RootDir    string
	Profile    string
	ConfigPath string
	AppID      string
	AgentKind  RuntimeAgentKind
}

func NewProfileServiceController(ctx context.Context, options ProfileServiceOptions) (ServiceController, ProfileServiceInfo, error) {
	info, paths, err := resolveProfileServiceInfo(options)
	if err != nil {
		return ServiceController{}, ProfileServiceInfo{}, err
	}
	executable := options.Executable
	if executable == "" {
		executable = currentProcessExecutable()
	}
	adapter := options.Adapter
	if adapter == nil {
		adapter, err = NewPlatformServiceAdapter(ServiceAdapterOptions{
			Profile:    info.Profile,
			RootDir:    info.RootDir,
			Executable: executable,
			EnvPath:    options.EnvPath,
		})
		if err != nil {
			return ServiceController{}, ProfileServiceInfo{}, err
		}
	}
	controller := NewServiceController(ServiceControllerOptions{
		Adapter:          adapter,
		RootDir:          info.RootDir,
		Profile:          info.Profile,
		AppID:            info.AppID,
		AgentKind:        info.AgentKind,
		Preflight:        profileServicePreflight(options, paths, info.ConfigPath, executable),
		WaitTimeout:      options.WaitTimeout,
		RequireConnected: options.RequireConnected,
		ProcessLister:    options.ProcessLister,
		LockHandler:      options.LockHandler,
	})
	_ = ctx
	return controller, info, nil
}

func StartProfileService(ctx context.Context, options ProfileServiceOptions) (ServiceStartResult, error) {
	controller, _, err := NewProfileServiceController(ctx, options)
	if err != nil {
		return ServiceStartResult{}, err
	}
	return controller.Start(ctx)
}

func profileServicePreflight(options ProfileServiceOptions, paths apppaths.Paths, configPath string, secretsGetterCommand string) func(context.Context) error {
	return func(ctx context.Context) error {
		if err := materializeProfileServiceEnvSecret(ctx, configPath, paths.Profile); err != nil {
			return err
		}
		if options.Preflight != nil {
			if err := options.Preflight(ctx); err != nil {
				return err
			}
		}
		if options.SkipCheckLarkCLI {
			return nil
		}
		snapshot, err := configstore.Load(configPath, configstore.LoadOptions{Profile: paths.Profile})
		if err != nil {
			return err
		}
		appConfig := larkcli.AppConfig{
			Accounts: snapshot.Runtime.Accounts,
			Secrets:  snapshot.Runtime.Secrets,
		}
		_, err = larkclipreflight.Run(ctx, larkclipreflight.Options{
			Config: appConfig,
			ProjectionPaths: larkcli.ProjectionPaths{
				RootDir:                 paths.RootDir,
				Profile:                 paths.Profile,
				LarkCliSourceDir:        paths.LarkCliSourceDir,
				LarkCliSourceConfigFile: paths.LarkCliSourceConfigFile,
				SecretsGetterScript:     paths.SecretsGetterScript,
				SecretsGetterCommand:    secretsGetterCommand,
			},
			Env: larkcli.EnvContext{
				Profile:                 paths.Profile,
				RootDir:                 paths.RootDir,
				ConfigPath:              configPath,
				LarkCliConfigDir:        paths.LarkCliConfigDir,
				LarkCliSourceConfigFile: paths.LarkCliSourceConfigFile,
			},
			IdentityPreset: larkcli.IdentityPreset(snapshot.Runtime.LarkCli.IdentityPreset),
			ProfileConfig:  &snapshot.Runtime.ProfileConfig,
			Command:        options.LarkCLICommand,
			BaseEnv:        options.LarkCLIBaseEnv,
			Runner:         wrapLarkCLIPreflightRunner(options.LarkCLIRunner),
		})
		return err
	}
}

func resolveProfileServiceInfo(options ProfileServiceOptions) (ProfileServiceInfo, apppaths.Paths, error) {
	home := options.Home
	if home == "" && options.Config != "" {
		home = filepath.Dir(options.Config)
	}
	profile := options.Profile
	if profile == "" && options.Agent != "" {
		profile = options.Agent
	}
	paths, err := apppaths.Resolve(apppaths.Options{RootDir: home, Profile: profile})
	if err != nil {
		return ProfileServiceInfo{}, apppaths.Paths{}, err
	}
	configPath := options.Config
	if configPath == "" {
		configPath = paths.ConfigFile
	}
	loadOptions := configstore.LoadOptions{Profile: profile}
	if options.Agent != "" {
		loadOptions.AgentKind = configstore.AgentKind(options.Agent)
	}
	snapshot, err := configstore.Load(configPath, loadOptions)
	if err != nil {
		return ProfileServiceInfo{}, apppaths.Paths{}, err
	}
	if options.AppID != "" && options.AppID != snapshot.Runtime.Accounts.App.ID {
		return ProfileServiceInfo{}, apppaths.Paths{}, fmt.Errorf("profile already exists: %s; it uses app %s. omit AppID or use another profile", snapshot.ProfileName, snapshot.Runtime.Accounts.App.ID)
	}
	resolvedPaths, err := apppaths.Resolve(apppaths.Options{RootDir: paths.RootDir, Profile: snapshot.ProfileName})
	if err != nil {
		return ProfileServiceInfo{}, apppaths.Paths{}, err
	}
	return ProfileServiceInfo{
		RootDir:    resolvedPaths.RootDir,
		Profile:    snapshot.ProfileName,
		ConfigPath: configPath,
		AppID:      snapshot.Runtime.Accounts.App.ID,
		AgentKind:  RuntimeAgentKind(snapshot.Runtime.AgentKind),
	}, resolvedPaths, nil
}

var profileServiceEnvSecretTemplateRE = regexp.MustCompile(`^\$\{[A-Z][A-Z0-9_]{0,127}\}$`)

func materializeProfileServiceEnvSecret(ctx context.Context, configPath string, profile string) error {
	if configPath == "" {
		return fmt.Errorf("config path is required")
	}
	return configstore.WithConfigFileLock(configPath, func() error {
		snapshot, err := configstore.Load(configPath, configstore.LoadOptions{Profile: profile})
		if err != nil {
			return err
		}
		root := snapshot.Root
		profileConfig, ok := root.Profiles[snapshot.ProfileName]
		if !ok {
			return fmt.Errorf("profile not found: %s", snapshot.ProfileName)
		}
		cfg := larkcli.AppConfig{
			Accounts: snapshot.Runtime.Accounts,
			Secrets:  snapshot.Runtime.Secrets,
		}
		if !isProfileServiceEnvBackedSecret(cfg.Accounts.App.Secret) {
			return nil
		}
		paths, err := apppaths.Resolve(apppaths.Options{RootDir: filepath.Dir(configPath), Profile: snapshot.ProfileName})
		if err != nil {
			return err
		}
		plaintext, err := resolveProfileServiceAppSecret(ctx, cfg, paths)
		if err != nil {
			return err
		}
		secretID := secretstore.SecretKeyForApp(cfg.Accounts.App.ID)
		if err := storeProfileServiceAppSecret(paths, secretID, plaintext); err != nil {
			return err
		}
		profileConfig.Accounts.App.Secret = larkcli.SecretRef{
			Source:   secretstore.SourceExec,
			Provider: "bridge",
			ID:       secretID,
		}
		if profileConfig.Secrets != nil {
			profileConfig.Secrets = ensureProfileServiceBridgeSecrets(profileConfig.Secrets, paths)
		} else {
			root.Secrets = ensureProfileServiceBridgeSecrets(root.Secrets, paths)
		}
		root.Profiles[snapshot.ProfileName] = profileConfig
		return configstore.SaveRoot(configPath, root)
	})
}

func isProfileServiceEnvBackedSecret(secret any) bool {
	switch value := secret.(type) {
	case string:
		return profileServiceEnvSecretTemplateRE.MatchString(value)
	case larkcli.SecretRef:
		return value.Source == secretstore.SourceEnv
	case *larkcli.SecretRef:
		return value != nil && value.Source == secretstore.SourceEnv
	case map[string]any:
		source, _ := value["source"].(string)
		return source == secretstore.SourceEnv
	case map[string]string:
		return value["source"] == secretstore.SourceEnv
	default:
		return false
	}
}

func resolveProfileServiceAppSecret(ctx context.Context, cfg larkcli.AppConfig, paths apppaths.Paths) (string, error) {
	secretConfig, err := toProfileServiceSecretStoreAppConfig(cfg)
	if err != nil {
		return "", err
	}
	return secretstore.ResolveAppSecret(ctx, secretConfig, secretstore.ResolverOptions{
		Paths: secretstore.KeystorePaths{
			SecretsFile:         paths.SecretsFile,
			KeystoreSaltFile:    paths.KeystoreSaltFile,
			SecretsGetterScript: paths.SecretsGetterScript,
		},
	})
}

func storeProfileServiceAppSecret(paths apppaths.Paths, id string, value string) error {
	store, err := secretstore.NewKeystore(secretstore.KeystoreOptions{
		Paths: secretstore.KeystorePaths{
			SecretsFile:      paths.SecretsFile,
			KeystoreSaltFile: paths.KeystoreSaltFile,
		},
	})
	if err != nil {
		return err
	}
	return store.SetSecret(id, value)
}

func toProfileServiceSecretStoreAppConfig(cfg larkcli.AppConfig) (secretstore.AppConfig, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return secretstore.AppConfig{}, err
	}
	var out secretstore.AppConfig
	if err := json.Unmarshal(data, &out); err != nil {
		return secretstore.AppConfig{}, err
	}
	return out, nil
}

func ensureProfileServiceBridgeSecrets(secrets *larkcli.SecretsConfig, paths apppaths.Paths) *larkcli.SecretsConfig {
	if secrets == nil {
		secrets = &larkcli.SecretsConfig{}
	}
	if secrets.Providers == nil {
		secrets.Providers = map[string]larkcli.ProviderConfig{}
	}
	secrets.Providers["bridge"] = larkcli.ProviderConfig{
		"source":  secretstore.SourceExec,
		"command": larkcli.SecretsGetterWrapperPath(paths.SecretsGetterScript),
		"args":    []string{},
		"env": map[string]string{
			"LARK_CHANNEL_HOME": paths.RootDir,
		},
	}
	if secrets.Defaults == nil {
		secrets.Defaults = map[string]string{}
	}
	secrets.Defaults[secretstore.SourceExec] = "bridge"
	return secrets
}

func currentProcessExecutable() string {
	exe, err := os.Executable()
	if err == nil && exe != "" {
		if abs, absErr := filepath.Abs(exe); absErr == nil {
			return abs
		}
		return exe
	}
	return "lark-channel-bridge"
}
