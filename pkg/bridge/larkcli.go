package bridge

import (
	"context"
	"time"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/configstore"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/larkcli"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/larkclipreflight"
)

type LarkCLIEnvContext struct {
	Profile                 string
	RootDir                 string
	ConfigPath              string
	LarkCliConfigDir        string
	LarkCliSourceConfigFile string
}

type LarkCLITenantBrand string

const (
	LarkCLITenantFeishu LarkCLITenantBrand = "feishu"
	LarkCLITenantLark   LarkCLITenantBrand = "lark"
)

type LarkCLISecretRef struct {
	Source   string `json:"source"`
	Provider string `json:"provider,omitempty"`
	ID       string `json:"id"`
}

type LarkCLIAppCredentials struct {
	ID     string             `json:"id"`
	Secret any                `json:"secret"`
	Tenant LarkCLITenantBrand `json:"tenant"`
}

type LarkCLIAccountsConfig struct {
	App LarkCLIAppCredentials `json:"app"`
}

type LarkCLIProviderConfig map[string]any

type LarkCLISecretsConfig struct {
	Providers map[string]LarkCLIProviderConfig `json:"providers,omitempty"`
	Defaults  map[string]string                `json:"defaults,omitempty"`
}

type LarkCLIAppConfig struct {
	Accounts LarkCLIAccountsConfig `json:"accounts"`
	Secrets  *LarkCLISecretsConfig `json:"secrets,omitempty"`
}

type LarkCLIProjectionPaths struct {
	RootDir                  string
	Profile                  string
	LarkCliSourceDir         string
	LarkCliSourceConfigFile  string
	SecretsGetterScript      string
	SecretsGetterCommand     string
	SecretsGetterNodePath    string
	SecretsGetterBridgeEntry string
}

type LarkCLISourceProjection struct {
	Accounts LarkCLIAccountsConfig `json:"accounts"`
	Secrets  *LarkCLISecretsConfig `json:"secrets,omitempty"`
}

type LarkCLIIdentityPreset string

const (
	LarkCLIIdentityBotOnly     LarkCLIIdentityPreset = "bot-only"
	LarkCLIIdentityUserDefault LarkCLIIdentityPreset = "user-default"
)

type LarkCLICommandInvocation struct {
	Command string
	Args    []string
	Env     map[string]string
}

type LarkCLICommandRunner interface {
	RunLarkCliCommand(ctx context.Context, invocation LarkCLICommandInvocation) error
}

type LarkCLICommandRunnerFunc func(ctx context.Context, invocation LarkCLICommandInvocation) error

func (f LarkCLICommandRunnerFunc) RunLarkCliCommand(ctx context.Context, invocation LarkCLICommandInvocation) error {
	return f(ctx, invocation)
}

type LarkCLIIdentityPolicyOptions struct {
	Command string
	Timeout time.Duration
	BaseEnv map[string]string
	Runner  LarkCLICommandRunner
}

type LarkCLIPreflightOptions struct {
	Config          LarkCLIAppConfig
	ProjectionPaths LarkCLIProjectionPaths
	Env             LarkCLIEnvContext
	IdentityPreset  LarkCLIIdentityPreset
	ProfileConfig   *ConfigProfile
	Command         string
	BaseEnv         map[string]string
	Timeout         time.Duration
	Runner          LarkCLIPreflightRunner
}

type LarkCLIPreflightResult struct {
	Bound             bool
	BindFailed        bool
	BindDiagnostic    string
	IdentityPreset    LarkCLIIdentityPreset
	LocalUserImported bool
	LocalUserStatus   ConfigLarkCliUserImportStatus
	LocalUserReason   string
}

type LarkCLIPreflightCommandResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

func (r LarkCLIPreflightCommandResult) OK() bool {
	return r.ExitCode == 0
}

func (r LarkCLIPreflightCommandResult) Output() string {
	if r.Stderr == "" {
		return r.Stdout
	}
	if r.Stdout == "" {
		return r.Stderr
	}
	return r.Stdout + "\n" + r.Stderr
}

type LarkCLIPreflightRunner interface {
	RunLarkCLICommand(ctx context.Context, invocation LarkCLICommandInvocation) (LarkCLIPreflightCommandResult, error)
}

type LarkCLIPreflightRunnerFunc func(ctx context.Context, invocation LarkCLICommandInvocation) (LarkCLIPreflightCommandResult, error)

func (f LarkCLIPreflightRunnerFunc) RunLarkCLICommand(ctx context.Context, invocation LarkCLICommandInvocation) (LarkCLIPreflightCommandResult, error) {
	return f(ctx, invocation)
}

// Deprecated: use LarkCLIEnvContext.
type LarkCliEnvContext = LarkCLIEnvContext

// Deprecated: use LarkCLITenantBrand.
type LarkCliTenantBrand = LarkCLITenantBrand

// Deprecated: use LarkCLISecretRef.
type LarkCliSecretRef = LarkCLISecretRef

// Deprecated: use LarkCLIAppCredentials.
type LarkCliAppCredentials = LarkCLIAppCredentials

// Deprecated: use LarkCLIAccountsConfig.
type LarkCliAccountsConfig = LarkCLIAccountsConfig

// Deprecated: use LarkCLIProviderConfig.
type LarkCliProviderConfig = LarkCLIProviderConfig

// Deprecated: use LarkCLISecretsConfig.
type LarkCliSecretsConfig = LarkCLISecretsConfig

// Deprecated: use LarkCLIAppConfig.
type LarkCliAppConfig = LarkCLIAppConfig

// Deprecated: use LarkCLIProjectionPaths.
type LarkCliProjectionPaths = LarkCLIProjectionPaths

// Deprecated: use LarkCLISourceProjection.
type LarkCliSourceProjection = LarkCLISourceProjection

// Deprecated: use LarkCLIIdentityPreset.
type LarkCliIdentityPreset = LarkCLIIdentityPreset

// Deprecated: use LarkCLIIdentityPolicyOptions.
type LarkCliIdentityPolicyOptions = LarkCLIIdentityPolicyOptions

// Deprecated: use LarkCLICommandInvocation.
type LarkCliCommandInvocation = LarkCLICommandInvocation

// Deprecated: use LarkCLICommandRunner.
type LarkCliCommandRunner = LarkCLICommandRunner

// Deprecated: use LarkCLICommandRunnerFunc.
type LarkCliCommandRunnerFunc = LarkCLICommandRunnerFunc

// Deprecated: use LarkCLIPreflightOptions.
type LarkCliPreflightOptions = LarkCLIPreflightOptions

// Deprecated: use LarkCLIPreflightResult.
type LarkCliPreflightResult = LarkCLIPreflightResult

// Deprecated: use LarkCLIPreflightCommandResult.
type LarkCliPreflightCommandResult = LarkCLIPreflightCommandResult

// Deprecated: use LarkCLIPreflightRunner.
type LarkCliPreflightRunner = LarkCLIPreflightRunner

// Deprecated: use LarkCLIPreflightRunnerFunc.
type LarkCliPreflightRunnerFunc = LarkCLIPreflightRunnerFunc

const (
	// Deprecated: use LarkCLITenantFeishu.
	LarkCliTenantFeishu = LarkCLITenantFeishu
	// Deprecated: use LarkCLITenantLark.
	LarkCliTenantLark = LarkCLITenantLark
	// Deprecated: use LarkCLIIdentityBotOnly.
	LarkCliIdentityBotOnly = LarkCLIIdentityBotOnly
	// Deprecated: use LarkCLIIdentityUserDefault.
	LarkCliIdentityUserDefault = LarkCLIIdentityUserDefault
)

func BuildLarkChannelEnv(context LarkCLIEnvContext) map[string]string {
	return larkcli.BuildLarkChannelEnv(toInternalLarkCLIEnvContext(context))
}

func BuildLarkCLISourceProjection(cfg LarkCLIAppConfig, paths LarkCLIProjectionPaths) LarkCLISourceProjection {
	internalCfg, _ := toInternalLarkCLIAppConfig(cfg)
	internalPaths := toInternalLarkCLIProjectionPaths(paths)
	projection := larkcli.BuildLarkCliSourceProjection(internalCfg, internalPaths)
	out, _ := fromInternalLarkCLISourceProjection(projection)
	return out
}

func WriteLarkCLISourceProjection(cfg LarkCLIAppConfig, paths LarkCLIProjectionPaths) (string, error) {
	internalCfg, err := toInternalLarkCLIAppConfig(cfg)
	if err != nil {
		return "", err
	}
	return larkcli.WriteLarkCliSourceProjection(internalCfg, toInternalLarkCLIProjectionPaths(paths))
}

func ApplyLarkCLIIdentityPolicy(ctx context.Context, env LarkCLIEnvContext, preset LarkCLIIdentityPreset, options LarkCLIIdentityPolicyOptions) bool {
	return larkcli.ApplyLarkCliIdentityPolicy(ctx, toInternalLarkCLIEnvContext(env), larkcli.IdentityPreset(preset), larkcli.IdentityPolicyOptions{
		Command: options.Command,
		Timeout: options.Timeout,
		BaseEnv: options.BaseEnv,
		Runner:  wrapLarkCLICommandRunner(options.Runner),
	})
}

func PreflightLarkCLI(ctx context.Context, options LarkCLIPreflightOptions) (LarkCLIPreflightResult, error) {
	internalOptions, err := toInternalLarkCLIPreflightOptions(options)
	if err != nil {
		return LarkCLIPreflightResult{}, err
	}
	result, err := larkclipreflight.Run(ctx, internalOptions)
	if err != nil {
		return LarkCLIPreflightResult{}, err
	}
	return fromInternalLarkCLIPreflightResult(result), nil
}

// Deprecated: use BuildLarkCLISourceProjection.
func BuildLarkCliSourceProjection(cfg LarkCliAppConfig, paths LarkCliProjectionPaths) LarkCliSourceProjection {
	return BuildLarkCLISourceProjection(cfg, paths)
}

// Deprecated: use WriteLarkCLISourceProjection.
func WriteLarkCliSourceProjection(cfg LarkCliAppConfig, paths LarkCliProjectionPaths) (string, error) {
	return WriteLarkCLISourceProjection(cfg, paths)
}

// Deprecated: use ApplyLarkCLIIdentityPolicy.
func ApplyLarkCliIdentityPolicy(ctx context.Context, env LarkCliEnvContext, preset LarkCliIdentityPreset, options LarkCliIdentityPolicyOptions) bool {
	return ApplyLarkCLIIdentityPolicy(ctx, env, preset, options)
}

func toInternalLarkCLIEnvContext(context LarkCLIEnvContext) larkcli.EnvContext {
	return larkcli.EnvContext{
		Profile:                 context.Profile,
		RootDir:                 context.RootDir,
		ConfigPath:              context.ConfigPath,
		LarkCliConfigDir:        context.LarkCliConfigDir,
		LarkCliSourceConfigFile: context.LarkCliSourceConfigFile,
	}
}

func toInternalLarkCLIAppConfig(cfg LarkCLIAppConfig) (larkcli.AppConfig, error) {
	return larkcli.AppConfig{
		Accounts: larkcli.AccountsConfig{
			App: larkcli.AppCredentials{
				ID:     cfg.Accounts.App.ID,
				Secret: cfg.Accounts.App.Secret,
				Tenant: larkcli.TenantBrand(cfg.Accounts.App.Tenant),
			},
		},
		Secrets: toInternalLarkCLISecretsConfig(cfg.Secrets),
	}, nil
}

func fromInternalLarkCLIAppConfig(cfg larkcli.AppConfig) (LarkCLIAppConfig, error) {
	return LarkCLIAppConfig{
		Accounts: LarkCLIAccountsConfig{
			App: LarkCLIAppCredentials{
				ID:     cfg.Accounts.App.ID,
				Secret: cfg.Accounts.App.Secret,
				Tenant: LarkCLITenantBrand(cfg.Accounts.App.Tenant),
			},
		},
		Secrets: fromInternalLarkCLISecretsConfig(cfg.Secrets),
	}, nil
}

func toInternalLarkCLISecretsConfig(secrets *LarkCLISecretsConfig) *larkcli.SecretsConfig {
	if secrets == nil {
		return nil
	}
	out := &larkcli.SecretsConfig{}
	if len(secrets.Providers) > 0 {
		out.Providers = make(map[string]larkcli.ProviderConfig, len(secrets.Providers))
		for name, provider := range secrets.Providers {
			copied := make(larkcli.ProviderConfig, len(provider))
			for key, value := range provider {
				copied[key] = value
			}
			out.Providers[name] = copied
		}
	}
	if len(secrets.Defaults) > 0 {
		out.Defaults = make(map[string]string, len(secrets.Defaults))
		for key, value := range secrets.Defaults {
			out.Defaults[key] = value
		}
	}
	return out
}

func fromInternalLarkCLISecretsConfig(secrets *larkcli.SecretsConfig) *LarkCLISecretsConfig {
	if secrets == nil {
		return nil
	}
	out := &LarkCLISecretsConfig{}
	if len(secrets.Providers) > 0 {
		out.Providers = make(map[string]LarkCLIProviderConfig, len(secrets.Providers))
		for name, provider := range secrets.Providers {
			copied := make(LarkCLIProviderConfig, len(provider))
			for key, value := range provider {
				copied[key] = value
			}
			out.Providers[name] = copied
		}
	}
	if len(secrets.Defaults) > 0 {
		out.Defaults = make(map[string]string, len(secrets.Defaults))
		for key, value := range secrets.Defaults {
			out.Defaults[key] = value
		}
	}
	return out
}

func toInternalLarkCLIProjectionPaths(paths LarkCLIProjectionPaths) larkcli.ProjectionPaths {
	return larkcli.ProjectionPaths{
		RootDir:                  paths.RootDir,
		Profile:                  paths.Profile,
		LarkCliSourceDir:         paths.LarkCliSourceDir,
		LarkCliSourceConfigFile:  paths.LarkCliSourceConfigFile,
		SecretsGetterScript:      paths.SecretsGetterScript,
		SecretsGetterCommand:     paths.SecretsGetterCommand,
		SecretsGetterNodePath:    paths.SecretsGetterNodePath,
		SecretsGetterBridgeEntry: paths.SecretsGetterBridgeEntry,
	}
}

func fromInternalLarkCLISourceProjection(projection larkcli.SourceProjection) (LarkCLISourceProjection, error) {
	return convertBridgeJSON[LarkCLISourceProjection](projection)
}

func toInternalLarkCLICommandInvocation(invocation LarkCLICommandInvocation) larkcli.CommandInvocation {
	return larkcli.CommandInvocation{
		Command: invocation.Command,
		Args:    invocation.Args,
		Env:     invocation.Env,
	}
}

func fromInternalLarkCLICommandInvocation(invocation larkcli.CommandInvocation) LarkCLICommandInvocation {
	return LarkCLICommandInvocation{
		Command: invocation.Command,
		Args:    invocation.Args,
		Env:     invocation.Env,
	}
}

func wrapLarkCLICommandRunner(runner LarkCLICommandRunner) larkcli.CommandRunner {
	if runner == nil {
		return nil
	}
	return larkcli.CommandRunnerFunc(func(ctx context.Context, invocation larkcli.CommandInvocation) error {
		return runner.RunLarkCliCommand(ctx, fromInternalLarkCLICommandInvocation(invocation))
	})
}

func wrapLarkCLIPreflightRunner(runner LarkCLIPreflightRunner) larkclipreflight.Runner {
	if runner == nil {
		return nil
	}
	return larkclipreflight.RunnerFunc(func(ctx context.Context, invocation larkcli.CommandInvocation) (larkclipreflight.CommandResult, error) {
		result, err := runner.RunLarkCLICommand(ctx, fromInternalLarkCLICommandInvocation(invocation))
		return larkclipreflight.CommandResult{
			Stdout:   result.Stdout,
			Stderr:   result.Stderr,
			ExitCode: result.ExitCode,
		}, err
	})
}

func toInternalLarkCLIPreflightOptions(options LarkCLIPreflightOptions) (larkclipreflight.Options, error) {
	cfg, err := toInternalLarkCLIAppConfig(options.Config)
	if err != nil {
		return larkclipreflight.Options{}, err
	}
	var profileConfig *configstore.ProfileConfig
	if options.ProfileConfig != nil {
		converted, err := convertBridgeJSON[configstore.ProfileConfig](*options.ProfileConfig)
		if err != nil {
			return larkclipreflight.Options{}, err
		}
		profileConfig = &converted
	}
	return larkclipreflight.Options{
		Config:          cfg,
		ProjectionPaths: toInternalLarkCLIProjectionPaths(options.ProjectionPaths),
		Env:             toInternalLarkCLIEnvContext(options.Env),
		IdentityPreset:  larkcli.IdentityPreset(options.IdentityPreset),
		ProfileConfig:   profileConfig,
		Command:         options.Command,
		BaseEnv:         options.BaseEnv,
		Timeout:         options.Timeout,
		Runner:          wrapLarkCLIPreflightRunner(options.Runner),
	}, nil
}

func fromInternalLarkCLIPreflightResult(result larkclipreflight.Result) LarkCLIPreflightResult {
	return LarkCLIPreflightResult{
		Bound:             result.Bound,
		BindFailed:        result.BindFailed,
		BindDiagnostic:    result.BindDiagnostic,
		IdentityPreset:    LarkCLIIdentityPreset(result.IdentityPreset),
		LocalUserImported: result.LocalUserImported,
		LocalUserStatus:   ConfigLarkCliUserImportStatus(result.LocalUserStatus),
		LocalUserReason:   result.LocalUserReason,
	}
}
