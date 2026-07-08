package secretstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	defaultExecTimeout   = 5 * time.Second
	defaultExecMaxOutput = 64 * 1024
)

var envTemplateRE = regexp.MustCompile(`^\$\{([A-Z][A-Z0-9_]{0,127})\}$`)

type ResolverOptions struct {
	Keystore *Keystore
	Paths    KeystorePaths
	Seed     string
	Runner   ExecRunner
	Env      map[string]string
}

type Resolver struct {
	keystore *Keystore
	paths    KeystorePaths
	runner   ExecRunner
	env      map[string]string
}

type ExecRequest struct {
	Command        string
	Args           []string
	Env            map[string]string
	Stdin          []byte
	Timeout        time.Duration
	MaxOutputBytes int
}

type ExecResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

type ExecRunner interface {
	Run(ctx context.Context, request ExecRequest) (ExecResult, error)
}

func NewResolver(options ResolverOptions) (*Resolver, error) {
	keystore := options.Keystore
	var err error
	if keystore == nil {
		keystore, err = NewKeystore(KeystoreOptions{Paths: options.Paths, Seed: options.Seed})
		if err != nil {
			return nil, err
		}
	}
	paths := options.Paths
	if paths.SecretsFile == "" || paths.KeystoreSaltFile == "" || paths.SecretsGetterScript == "" {
		kp := keystore.Paths()
		if paths.SecretsFile == "" {
			paths.SecretsFile = kp.SecretsFile
		}
		if paths.KeystoreSaltFile == "" {
			paths.KeystoreSaltFile = kp.KeystoreSaltFile
		}
		if paths.SecretsGetterScript == "" {
			paths.SecretsGetterScript = kp.SecretsGetterScript
		}
	}
	runner := options.Runner
	if runner == nil {
		runner = OSExecRunner{}
	}
	env := options.Env
	if env == nil {
		env = getenvMap()
	}
	return &Resolver{keystore: keystore, paths: paths, runner: runner, env: env}, nil
}

func ResolveAppSecret(ctx context.Context, cfg AppConfig, options ResolverOptions) (string, error) {
	resolver, err := NewResolver(options)
	if err != nil {
		return "", err
	}
	return resolver.ResolveAppSecret(ctx, cfg)
}

func ResolveSecretInput(ctx context.Context, input SecretInput, secrets *SecretsConfig, appID string, options ResolverOptions) (string, error) {
	resolver, err := NewResolver(options)
	if err != nil {
		return "", err
	}
	return resolver.ResolveSecretInput(ctx, input, secrets, appID)
}

func (r *Resolver) ResolveAppSecret(ctx context.Context, cfg AppConfig) (string, error) {
	return r.ResolveSecretInput(ctx, cfg.Accounts.App.Secret, cfg.Secrets, cfg.Accounts.App.ID)
}

func (r *Resolver) ResolveSecretInput(ctx context.Context, input SecretInput, secrets *SecretsConfig, appID string) (string, error) {
	if input.IsZero() {
		return "", errors.New("app secret is missing")
	}
	if input.Plain != nil {
		return r.resolvePlainOrTemplate(*input.Plain)
	}
	if input.Ref == nil {
		return "", errors.New("app secret is missing")
	}
	ref := *input.Ref
	switch ref.Source {
	case SourceEnv:
		return r.resolveEnvRef(ref, lookupProvider(secrets, ref))
	case SourceFile:
		return resolveFileRef(ref, lookupProvider(secrets, ref))
	case SourceInline:
		return resolveInlineRef(ref, lookupProvider(secrets, ref))
	case SourceExec:
		return r.resolveExecRef(ctx, ref, lookupProvider(secrets, ref), appID)
	default:
		return "", fmt.Errorf("unknown secret source: %s", ref.Source)
	}
}

func (r *Resolver) resolvePlainOrTemplate(value string) (string, error) {
	if value == "" {
		return "", errors.New("app secret is empty")
	}
	m := envTemplateRE.FindStringSubmatch(value)
	if len(m) == 2 {
		name := m[1]
		v := r.env[name]
		if v == "" {
			return "", fmt.Errorf("env var %s referenced by secret is not set", name)
		}
		return v, nil
	}
	return value, nil
}

func lookupProvider(secrets *SecretsConfig, ref SecretRef) *ProviderConfig {
	if secrets == nil || len(secrets.Providers) == 0 {
		return nil
	}
	name := ref.Provider
	if name == "" && secrets.Defaults != nil {
		name = secrets.Defaults[ref.Source]
	}
	if name == "" {
		name = DefaultProvider
	}
	pc, ok := secrets.Providers[name]
	if !ok {
		return nil
	}
	return &pc
}

func (r *Resolver) resolveEnvRef(ref SecretRef, pc *ProviderConfig) (string, error) {
	if pc != nil && len(pc.Allowlist) > 0 && !contains(pc.Allowlist, ref.ID) {
		return "", fmt.Errorf("env var %s is not allowlisted in provider", ref.ID)
	}
	v := r.env[ref.ID]
	if v == "" {
		return "", fmt.Errorf("env var %s is not set", ref.ID)
	}
	return v, nil
}

func resolveFileRef(ref SecretRef, pc *ProviderConfig) (string, error) {
	path := ref.ID
	if pc != nil && pc.Path != "" {
		path = filepath.Join(pc.Path, ref.ID)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func resolveInlineRef(ref SecretRef, pc *ProviderConfig) (string, error) {
	if pc != nil && pc.Value != "" {
		return pc.Value, nil
	}
	if ref.ID == "" {
		return "", errors.New("inline secret is empty")
	}
	return ref.ID, nil
}

func (r *Resolver) resolveExecRef(ctx context.Context, ref SecretRef, pc *ProviderConfig, appID string) (string, error) {
	if pc == nil || pc.Command == "" {
		return "", errors.New("exec provider missing `command`")
	}
	if r.isSelfBridgeCommand(pc.Command, pc.Args) {
		if value, ok, err := r.keystore.GetSecret(ref.ID); err != nil {
			return "", err
		} else if ok {
			return value, nil
		}
		conventional := SecretKeyForApp(appID)
		if value, ok, err := r.keystore.GetSecret(conventional); err != nil {
			return "", err
		} else if ok {
			return value, nil
		}
		return "", fmt.Errorf("keystore has no entry for %q or %q", ref.ID, conventional)
	}
	return r.spawnExecProvider(ctx, ref, *pc)
}

func (r *Resolver) isSelfBridgeCommand(command string, args []string) bool {
	if r.paths.SecretsGetterScript != "" {
		if command == r.paths.SecretsGetterScript || command == r.paths.SecretsGetterScript+".cmd" {
			return true
		}
	}
	if len(args) >= 2 && args[len(args)-2] == "secrets" && args[len(args)-1] == "get" {
		return true
	}
	return false
}

func (r *Resolver) spawnExecProvider(ctx context.Context, ref SecretRef, pc ProviderConfig) (string, error) {
	timeout := defaultExecTimeout
	if pc.NoOutputTimeoutMs > 0 {
		timeout = time.Duration(pc.NoOutputTimeoutMs) * time.Millisecond
	}
	maxOutput := defaultExecMaxOutput
	if pc.MaxOutputBytes > 0 {
		maxOutput = pc.MaxOutputBytes
	}
	env := map[string]string{}
	for _, key := range pc.PassEnv {
		if value := r.env[key]; value != "" {
			env[key] = value
		}
	}
	for key, value := range pc.Env {
		env[key] = value
	}
	providerName := ref.Provider
	if providerName == "" {
		providerName = DefaultProvider
	}
	request, err := json.Marshal(execProviderRequest{
		ProtocolVersion: 1,
		Provider:        providerName,
		IDs:             []string{ref.ID},
	})
	if err != nil {
		return "", err
	}
	result, err := r.runner.Run(ctx, ExecRequest{
		Command:        pc.Command,
		Args:           pc.Args,
		Env:            env,
		Stdin:          request,
		Timeout:        timeout,
		MaxOutputBytes: maxOutput,
	})
	if err != nil {
		return "", err
	}
	if result.ExitCode != 0 {
		detail := strings.TrimSpace(string(result.Stderr))
		if len(detail) > 200 {
			detail = detail[:200]
		}
		if detail != "" {
			return "", fmt.Errorf("exec provider exited with code %d: %s", result.ExitCode, detail)
		}
		return "", fmt.Errorf("exec provider exited with code %d", result.ExitCode)
	}
	var parsed execProviderResponse
	if err := json.Unmarshal(result.Stdout, &parsed); err != nil {
		return "", fmt.Errorf("exec provider returned invalid JSON: %w", err)
	}
	if value, ok := parsed.Values[ref.ID]; ok {
		return value, nil
	}
	if entry, ok := parsed.Errors[ref.ID]; ok && entry.Message != "" {
		return "", fmt.Errorf("exec provider did not return secret for %s: %s", ref.ID, entry.Message)
	}
	return "", fmt.Errorf("exec provider did not return secret for %s", ref.ID)
}

type execProviderRequest struct {
	ProtocolVersion int      `json:"protocolVersion"`
	Provider        string   `json:"provider"`
	IDs             []string `json:"ids"`
}

type execProviderResponse struct {
	Values map[string]string              `json:"values"`
	Errors map[string]execProviderErrItem `json:"errors"`
}

type execProviderErrItem struct {
	Message string `json:"message"`
}

type OSExecRunner struct{}

func (OSExecRunner) Run(ctx context.Context, request ExecRequest) (ExecResult, error) {
	timeout := request.Timeout
	if timeout <= 0 {
		timeout = defaultExecTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, request.Command, request.Args...)
	cmd.Env = mergeEnv(os.Environ(), request.Env)
	cmd.Stdin = bytes.NewReader(request.Stdin)
	var stdout limitedBuffer
	stdout.limit = request.MaxOutputBytes
	if stdout.limit <= 0 {
		stdout.limit = defaultExecMaxOutput
	}
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if runCtx.Err() == context.DeadlineExceeded {
		return ExecResult{}, fmt.Errorf("exec provider timed out after %dms", timeout.Milliseconds())
	}
	if stdout.exceeded {
		return ExecResult{}, fmt.Errorf("exec provider stdout exceeded %d bytes", stdout.limit)
	}
	result := ExecResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	if err == nil {
		return result, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result, nil
	}
	return ExecResult{}, fmt.Errorf("exec provider failed to start: %w", err)
}

type limitedBuffer struct {
	bytes.Buffer
	limit    int
	exceeded bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.exceeded {
		return len(p), nil
	}
	if b.Len()+len(p) > b.limit {
		b.exceeded = true
		return len(p), io.ErrShortWrite
	}
	return b.Buffer.Write(p)
}

func mergeEnv(base []string, override map[string]string) []string {
	if len(override) == 0 {
		return base
	}
	out := make([]string, 0, len(base)+len(override))
	seen := map[string]bool{}
	for _, entry := range base {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			out = append(out, entry)
			continue
		}
		if _, replace := override[key]; replace {
			if !seen[key] {
				out = append(out, key+"="+override[key])
				seen[key] = true
			}
			continue
		}
		out = append(out, entry)
	}
	for key, value := range override {
		if !seen[key] {
			out = append(out, key+"="+value)
		}
	}
	return out
}

func getenvMap() map[string]string {
	out := map[string]string{}
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			out[key] = value
		}
	}
	return out
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
