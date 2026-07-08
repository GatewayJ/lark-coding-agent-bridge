package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/secretstore"
)

type SecretRef struct {
	Source   string `json:"source"`
	Provider string `json:"provider,omitempty"`
	ID       string `json:"id"`
}

type SecretInput struct {
	Plain *string
	Ref   *SecretRef
}

func PlainSecret(value string) SecretInput {
	return SecretInput{Plain: &value}
}

func SecretReference(ref SecretRef) SecretInput {
	return SecretInput{Ref: &ref}
}

func (s SecretInput) IsZero() bool {
	return s.Plain == nil && s.Ref == nil
}

func (s SecretInput) MarshalJSON() ([]byte, error) {
	if s.Ref != nil {
		return json.Marshal(s.Ref)
	}
	if s.Plain != nil {
		return json.Marshal(*s.Plain)
	}
	return []byte("null"), nil
}

func (s *SecretInput) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*s = SecretInput{}
		return nil
	}
	var plain string
	if err := json.Unmarshal(data, &plain); err == nil {
		*s = PlainSecret(plain)
		return nil
	}
	var ref SecretRef
	if err := json.Unmarshal(data, &ref); err == nil && ref.Source != "" {
		*s = SecretReference(ref)
		return nil
	}
	return fmt.Errorf("unsupported secret input JSON: %s", string(data))
}

type SecretAppCredentials struct {
	ID     string      `json:"id"`
	Secret SecretInput `json:"secret"`
	Tenant string      `json:"tenant"`
}

type SecretAccountsConfig struct {
	App SecretAppCredentials `json:"app"`
}

type SecretProviderConfig struct {
	Source            string            `json:"source"`
	Allowlist         []string          `json:"allowlist,omitempty"`
	Path              string            `json:"path,omitempty"`
	Value             string            `json:"value,omitempty"`
	Command           string            `json:"command,omitempty"`
	Args              []string          `json:"args,omitempty"`
	Env               map[string]string `json:"env,omitempty"`
	PassEnv           []string          `json:"passEnv,omitempty"`
	NoOutputTimeoutMs int               `json:"noOutputTimeoutMs,omitempty"`
	MaxOutputBytes    int               `json:"maxOutputBytes,omitempty"`
}

type SecretSecretsConfig struct {
	Providers map[string]SecretProviderConfig `json:"providers,omitempty"`
	Defaults  map[string]string               `json:"defaults,omitempty"`
}

type SecretAppConfig struct {
	Accounts SecretAccountsConfig `json:"accounts"`
	Secrets  *SecretSecretsConfig `json:"secrets,omitempty"`
}

type KeystorePaths struct {
	SecretsFile         string
	KeystoreSaltFile    string
	SecretsGetterScript string
}

type KeystoreOptions struct {
	Paths KeystorePaths
	Seed  string
}

type Keystore struct {
	inner *secretstore.Keystore
}

type SecretResolverOptions struct {
	Keystore *Keystore
	Paths    KeystorePaths
	Seed     string
	Runner   SecretExecRunner
	Env      map[string]string
}

type SecretResolver struct {
	inner *secretstore.Resolver
}

type SecretExecRequest struct {
	Command        string
	Args           []string
	Env            map[string]string
	Stdin          []byte
	Timeout        time.Duration
	MaxOutputBytes int
}

type SecretExecResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

type SecretExecRunner interface {
	Run(ctx context.Context, request SecretExecRequest) (SecretExecResult, error)
}

const (
	SecretSourceEnv    = secretstore.SourceEnv
	SecretSourceFile   = secretstore.SourceFile
	SecretSourceInline = secretstore.SourceInline
	SecretSourceExec   = secretstore.SourceExec
)

func SecretKeyForApp(appID string) string {
	return secretstore.SecretKeyForApp(appID)
}

func DefaultKeystorePaths() (KeystorePaths, error) {
	paths, err := secretstore.DefaultKeystorePaths()
	if err != nil {
		return KeystorePaths{}, err
	}
	return fromInternalKeystorePaths(paths), nil
}

func NewKeystore(options KeystoreOptions) (*Keystore, error) {
	store, err := secretstore.NewKeystore(secretstore.KeystoreOptions{
		Paths: toInternalKeystorePaths(options.Paths),
		Seed:  options.Seed,
	})
	if err != nil {
		return nil, err
	}
	return &Keystore{inner: store}, nil
}

func (k *Keystore) Paths() KeystorePaths {
	if k == nil || k.inner == nil {
		return KeystorePaths{}
	}
	return fromInternalKeystorePaths(k.inner.Paths())
}

func (k *Keystore) GetSecret(id string) (string, bool, error) {
	if k == nil || k.inner == nil {
		return "", false, fmt.Errorf("bridge keystore is nil")
	}
	return k.inner.GetSecret(id)
}

func (k *Keystore) SetSecret(id string, plaintext string) error {
	if k == nil || k.inner == nil {
		return fmt.Errorf("bridge keystore is nil")
	}
	return k.inner.SetSecret(id, plaintext)
}

func (k *Keystore) RemoveSecret(id string) (bool, error) {
	if k == nil || k.inner == nil {
		return false, fmt.Errorf("bridge keystore is nil")
	}
	return k.inner.RemoveSecret(id)
}

func (k *Keystore) ListSecretIDs() ([]string, error) {
	if k == nil || k.inner == nil {
		return nil, fmt.Errorf("bridge keystore is nil")
	}
	return k.inner.ListSecretIDs()
}

func NewSecretResolver(options SecretResolverOptions) (*SecretResolver, error) {
	resolver, err := secretstore.NewResolver(toInternalSecretResolverOptions(options))
	if err != nil {
		return nil, err
	}
	return &SecretResolver{inner: resolver}, nil
}

func internalKeystore(store *Keystore) *secretstore.Keystore {
	if store == nil {
		return nil
	}
	return store.inner
}

func (r *SecretResolver) ResolveAppSecret(ctx context.Context, cfg SecretAppConfig) (string, error) {
	if r == nil || r.inner == nil {
		return "", fmt.Errorf("bridge secret resolver is nil")
	}
	return r.inner.ResolveAppSecret(ctx, toInternalSecretAppConfig(cfg))
}

func (r *SecretResolver) ResolveSecretInput(ctx context.Context, input SecretInput, secrets *SecretSecretsConfig, appID string) (string, error) {
	if r == nil || r.inner == nil {
		return "", fmt.Errorf("bridge secret resolver is nil")
	}
	return r.inner.ResolveSecretInput(ctx, toInternalSecretInput(input), toInternalSecretSecretsConfig(secrets), appID)
}

func ResolveAppSecret(ctx context.Context, cfg SecretAppConfig, options SecretResolverOptions) (string, error) {
	return secretstore.ResolveAppSecret(ctx, toInternalSecretAppConfig(cfg), toInternalSecretResolverOptions(options))
}

func ResolveSecretInput(ctx context.Context, input SecretInput, secrets *SecretSecretsConfig, appID string, options SecretResolverOptions) (string, error) {
	return secretstore.ResolveSecretInput(ctx, toInternalSecretInput(input), toInternalSecretSecretsConfig(secrets), appID, toInternalSecretResolverOptions(options))
}

func toInternalSecretRef(ref SecretRef) secretstore.SecretRef {
	return secretstore.SecretRef{
		Source:   ref.Source,
		Provider: ref.Provider,
		ID:       ref.ID,
	}
}

func toInternalSecretInput(input SecretInput) secretstore.SecretInput {
	if input.Ref != nil {
		return secretstore.SecretReference(toInternalSecretRef(*input.Ref))
	}
	if input.Plain != nil {
		return secretstore.PlainSecret(*input.Plain)
	}
	return secretstore.SecretInput{}
}

func toInternalSecretAppConfig(cfg SecretAppConfig) secretstore.AppConfig {
	return secretstore.AppConfig{
		Accounts: secretstore.AccountsConfig{
			App: secretstore.AppCredentials{
				ID:     cfg.Accounts.App.ID,
				Secret: toInternalSecretInput(cfg.Accounts.App.Secret),
				Tenant: cfg.Accounts.App.Tenant,
			},
		},
		Secrets: toInternalSecretSecretsConfig(cfg.Secrets),
	}
}

func toInternalSecretSecretsConfig(secrets *SecretSecretsConfig) *secretstore.SecretsConfig {
	if secrets == nil {
		return nil
	}
	out := &secretstore.SecretsConfig{}
	if len(secrets.Providers) > 0 {
		out.Providers = make(map[string]secretstore.ProviderConfig, len(secrets.Providers))
		for name, provider := range secrets.Providers {
			out.Providers[name] = secretstore.ProviderConfig{
				Source:            provider.Source,
				Allowlist:         provider.Allowlist,
				Path:              provider.Path,
				Value:             provider.Value,
				Command:           provider.Command,
				Args:              provider.Args,
				Env:               provider.Env,
				PassEnv:           provider.PassEnv,
				NoOutputTimeoutMs: provider.NoOutputTimeoutMs,
				MaxOutputBytes:    provider.MaxOutputBytes,
			}
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

func toInternalSecretResolverOptions(options SecretResolverOptions) secretstore.ResolverOptions {
	var keystore *secretstore.Keystore
	if options.Keystore != nil {
		keystore = options.Keystore.inner
	}
	return secretstore.ResolverOptions{
		Keystore: keystore,
		Paths:    toInternalKeystorePaths(options.Paths),
		Seed:     options.Seed,
		Runner:   wrapSecretExecRunner(options.Runner),
		Env:      options.Env,
	}
}

func toInternalKeystorePaths(paths KeystorePaths) secretstore.KeystorePaths {
	return secretstore.KeystorePaths{
		SecretsFile:         paths.SecretsFile,
		KeystoreSaltFile:    paths.KeystoreSaltFile,
		SecretsGetterScript: paths.SecretsGetterScript,
	}
}

func fromInternalKeystorePaths(paths secretstore.KeystorePaths) KeystorePaths {
	return KeystorePaths{
		SecretsFile:         paths.SecretsFile,
		KeystoreSaltFile:    paths.KeystoreSaltFile,
		SecretsGetterScript: paths.SecretsGetterScript,
	}
}

func wrapSecretExecRunner(runner SecretExecRunner) secretstore.ExecRunner {
	if runner == nil {
		return nil
	}
	return secretExecRunnerAdapter{runner: runner}
}

type secretExecRunnerAdapter struct {
	runner SecretExecRunner
}

func (a secretExecRunnerAdapter) Run(ctx context.Context, request secretstore.ExecRequest) (secretstore.ExecResult, error) {
	result, err := a.runner.Run(ctx, SecretExecRequest{
		Command:        request.Command,
		Args:           request.Args,
		Env:            request.Env,
		Stdin:          request.Stdin,
		Timeout:        request.Timeout,
		MaxOutputBytes: request.MaxOutputBytes,
	})
	return secretstore.ExecResult{
		Stdout:   result.Stdout,
		Stderr:   result.Stderr,
		ExitCode: result.ExitCode,
	}, err
}
