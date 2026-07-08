package secretstore

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveAppSecretPlainTemplateEnvFileAndInline(t *testing.T) {
	ctx := context.Background()
	resolver := newTestResolver(t, ResolverOptions{
		Env: map[string]string{
			"APP_SECRET":       "from-template",
			"ALLOWLIST_SECRET": "from-env",
		},
	})

	plain, err := resolver.ResolveSecretInput(ctx, PlainSecret("literal"), nil, "cli")
	if err != nil || plain != "literal" {
		t.Fatalf("plain secret = (%q, %v), want literal nil", plain, err)
	}
	template, err := resolver.ResolveSecretInput(ctx, PlainSecret("${APP_SECRET}"), nil, "cli")
	if err != nil || template != "from-template" {
		t.Fatalf("template secret = (%q, %v), want from-template nil", template, err)
	}

	envCfg := &SecretsConfig{
		Providers: map[string]ProviderConfig{
			"profileEnv": {Source: SourceEnv, Allowlist: []string{"ALLOWLIST_SECRET"}},
		},
		Defaults: map[string]string{SourceEnv: "profileEnv"},
	}
	envValue, err := resolver.ResolveSecretInput(ctx, SecretReference(SecretRef{Source: SourceEnv, ID: "ALLOWLIST_SECRET"}), envCfg, "cli")
	if err != nil || envValue != "from-env" {
		t.Fatalf("env secret = (%q, %v), want from-env nil", envValue, err)
	}
	if _, err := resolver.ResolveSecretInput(ctx, SecretReference(SecretRef{Source: SourceEnv, ID: "DENIED"}), envCfg, "cli"); err == nil || !strings.Contains(err.Error(), "allowlisted") {
		t.Fatalf("denied env error = %v, want allowlist error", err)
	}

	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "secret.txt"), []byte("from-file\n"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	fileValue, err := resolver.ResolveSecretInput(ctx,
		SecretReference(SecretRef{Source: SourceFile, Provider: "disk", ID: "secret.txt"}),
		&SecretsConfig{Providers: map[string]ProviderConfig{"disk": {Source: SourceFile, Path: base}}},
		"cli",
	)
	if err != nil || fileValue != "from-file" {
		t.Fatalf("file secret = (%q, %v), want from-file nil", fileValue, err)
	}

	inlineValue, err := resolver.ResolveSecretInput(ctx, SecretReference(SecretRef{Source: SourceInline, ID: "from-inline"}), nil, "cli")
	if err != nil || inlineValue != "from-inline" {
		t.Fatalf("inline secret = (%q, %v), want from-inline nil", inlineValue, err)
	}
}

func TestResolveExecProviderUsesInjectedRunner(t *testing.T) {
	ctx := context.Background()
	runner := &recordingRunner{
		result: ExecResult{Stdout: []byte(`{"values":{"secret-id":"from-exec"}}`)},
	}
	resolver := newTestResolver(t, ResolverOptions{
		Runner: runner,
		Env: map[string]string{
			"PASS_ME": "kept",
			"DROP_ME": "ignored",
		},
	})
	cfg := &SecretsConfig{Providers: map[string]ProviderConfig{
		"vault": {
			Source:            SourceExec,
			Command:           "/bin/provider",
			Args:              []string{"--json"},
			Env:               map[string]string{"STATIC": "1"},
			PassEnv:           []string{"PASS_ME", "DROP_ME"},
			NoOutputTimeoutMs: 250,
			MaxOutputBytes:    1234,
		},
	}}
	got, err := resolver.ResolveSecretInput(ctx, SecretReference(SecretRef{
		Source:   SourceExec,
		Provider: "vault",
		ID:       "secret-id",
	}), cfg, "cli")
	if err != nil || got != "from-exec" {
		t.Fatalf("exec secret = (%q, %v), want from-exec nil", got, err)
	}
	if runner.calls != 1 {
		t.Fatalf("runner calls = %d, want 1", runner.calls)
	}
	req := runner.request
	if req.Command != "/bin/provider" || len(req.Args) != 1 || req.Args[0] != "--json" {
		t.Fatalf("exec request command/args = %q %#v", req.Command, req.Args)
	}
	if req.Env["STATIC"] != "1" || req.Env["PASS_ME"] != "kept" || req.Env["DROP_ME"] != "ignored" {
		t.Fatalf("exec request env = %#v", req.Env)
	}
	if req.Timeout.Milliseconds() != 250 || req.MaxOutputBytes != 1234 {
		t.Fatalf("exec request limits = %s/%d", req.Timeout, req.MaxOutputBytes)
	}
	var payload struct {
		ProtocolVersion int      `json:"protocolVersion"`
		Provider        string   `json:"provider"`
		IDs             []string `json:"ids"`
	}
	if err := json.Unmarshal(req.Stdin, &payload); err != nil {
		t.Fatalf("request stdin invalid JSON: %v", err)
	}
	if payload.ProtocolVersion != 1 || payload.Provider != "vault" || len(payload.IDs) != 1 || payload.IDs[0] != "secret-id" {
		t.Fatalf("request stdin = %#v", payload)
	}
}

func TestResolveExecProviderShortCircuitsSelfWrapper(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	paths := testKeystorePaths(root)
	store, err := NewKeystore(KeystoreOptions{Paths: paths, Seed: "host|user"})
	if err != nil {
		t.Fatalf("NewKeystore returned error: %v", err)
	}
	if err := store.SetSecret(SecretKeyForApp("cli_self"), "from-keystore"); err != nil {
		t.Fatalf("SetSecret returned error: %v", err)
	}
	runner := &recordingRunner{err: errRunnerCalled}
	resolver := newTestResolver(t, ResolverOptions{Keystore: store, Paths: paths, Runner: runner})
	value, err := resolver.ResolveSecretInput(ctx,
		SecretReference(SecretRef{Source: SourceExec, Provider: "bridge", ID: "custom-id"}),
		&SecretsConfig{Providers: map[string]ProviderConfig{
			"bridge": {Source: SourceExec, Command: paths.SecretsGetterScript + ".cmd", Args: []string{}},
		}},
		"cli_self",
	)
	if err != nil || value != "from-keystore" {
		t.Fatalf("self wrapper secret = (%q, %v), want from-keystore nil", value, err)
	}
	if runner.calls != 0 {
		t.Fatalf("runner calls = %d, want 0", runner.calls)
	}
}

func TestResolveExecProviderShortCircuitsLegacySecretsGetArgs(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	paths := testKeystorePaths(root)
	store, err := NewKeystore(KeystoreOptions{Paths: paths, Seed: "host|user"})
	if err != nil {
		t.Fatalf("NewKeystore returned error: %v", err)
	}
	if err := store.SetSecret("legacy-id", "legacy-secret"); err != nil {
		t.Fatalf("SetSecret returned error: %v", err)
	}
	runner := &recordingRunner{err: errRunnerCalled}
	resolver := newTestResolver(t, ResolverOptions{Keystore: store, Paths: paths, Runner: runner})
	value, err := resolver.ResolveSecretInput(ctx,
		SecretReference(SecretRef{Source: SourceExec, Provider: "bridge", ID: "legacy-id"}),
		&SecretsConfig{Providers: map[string]ProviderConfig{
			"bridge": {Source: SourceExec, Command: "node", Args: []string{"/bridge/bin.mjs", "secrets", "get"}},
		}},
		"cli_legacy",
	)
	if err != nil || value != "legacy-secret" {
		t.Fatalf("legacy self wrapper secret = (%q, %v), want legacy-secret nil", value, err)
	}
	if runner.calls != 0 {
		t.Fatalf("runner calls = %d, want 0", runner.calls)
	}
}

func TestSecretKeyForAppCompatibility(t *testing.T) {
	if got := SecretKeyForApp("cli_xxx"); got != "app-cli_xxx" {
		t.Fatalf("SecretKeyForApp = %q, want app-cli_xxx", got)
	}
}

type recordingRunner struct {
	calls   int
	request ExecRequest
	result  ExecResult
	err     error
}

var errRunnerCalled = errString("runner should not be called")

func (r *recordingRunner) Run(ctx context.Context, request ExecRequest) (ExecResult, error) {
	r.calls++
	r.request = request
	return r.result, r.err
}

type errString string

func (e errString) Error() string {
	return string(e)
}

func newTestResolver(t *testing.T, options ResolverOptions) *Resolver {
	t.Helper()
	if options.Keystore == nil {
		store, err := NewKeystore(KeystoreOptions{Paths: testKeystorePaths(t.TempDir()), Seed: "host|user"})
		if err != nil {
			t.Fatalf("NewKeystore returned error: %v", err)
		}
		options.Keystore = store
	}
	resolver, err := NewResolver(options)
	if err != nil {
		t.Fatalf("NewResolver returned error: %v", err)
	}
	return resolver
}
