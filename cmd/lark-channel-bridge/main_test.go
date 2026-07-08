package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/configstore"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/larkcli"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/runtimecoord"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/secretstore"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/compat/apppaths"
	"github.com/zarazhangrui/lark-coding-agent-bridge/pkg/bridge"
)

func TestRunSecretsGetExecProviderProtocol(t *testing.T) {
	root := t.TempDir()
	paths, err := apppaths.Resolve(apppaths.Options{RootDir: root, Profile: "codex"})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	store, err := secretstore.NewKeystore(secretstore.KeystoreOptions{
		Paths: secretstore.KeystorePaths{
			SecretsFile:      paths.SecretsFile,
			KeystoreSaltFile: paths.KeystoreSaltFile,
		},
	})
	if err != nil {
		t.Fatalf("NewKeystore returned error: %v", err)
	}
	if err := store.SetSecret("app-cli_test", "secret-value"); err != nil {
		t.Fatalf("SetSecret returned error: %v", err)
	}

	stdin := bytes.NewBufferString(`{"protocolVersion":1,"provider":"bridge","ids":["app-cli_test","missing"]}`)
	var stdout, stderr bytes.Buffer
	if err := runSecretsGet([]string{"--home", root, "--profile", "codex"}, stdin, &stdout, &stderr); err != nil {
		t.Fatalf("runSecretsGet returned error: %v stderr=%s", err, stderr.String())
	}

	var resp execSecretResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("stdout is not exec response JSON: %v\n%s", err, stdout.String())
	}
	if resp.ProtocolVersion != 1 || resp.Values["app-cli_test"] != "secret-value" {
		t.Fatalf("response values = %#v", resp)
	}
	if resp.Errors["missing"].Message != "not found" {
		t.Fatalf("missing error = %#v", resp.Errors)
	}
}

func TestStartObservabilityWritesProfileLogFile(t *testing.T) {
	t.Setenv("LARK_CHANNEL_TELEMETRY_MODULE", "")
	root := t.TempDir()
	paths, err := apppaths.Resolve(apppaths.Options{RootDir: root, Profile: "codex"})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	logger, telemetry := startObservability(context.Background(), paths, larkcli.AppConfig{
		Accounts: larkcli.AccountsConfig{App: larkcli.AppCredentials{
			ID:     "cli_test",
			Tenant: larkcli.TenantFeishu,
		}},
	})
	if telemetry != nil {
		t.Fatalf("telemetry = %#v, want nil without env", telemetry)
	}
	logger.Info("bridge.started", map[string]any{"mode": "lark"})
	data, err := os.ReadFile(filepath.Join(paths.LogsDir, "bridge-"+time.Now().Format("20060102")+".jsonl"))
	if err != nil {
		t.Fatalf("read profile log: %v", err)
	}
	if !strings.Contains(string(data), `"phase":"bridge"`) || !strings.Contains(string(data), `"event":"started"`) {
		t.Fatalf("unexpected log data: %s", data)
	}
}

func TestRunSecretsGetScansProfilesWithoutExplicitProfile(t *testing.T) {
	t.Setenv("LARK_CHANNEL_PROFILE", "")
	root := t.TempDir()
	paths, err := apppaths.Resolve(apppaths.Options{RootDir: root, Profile: "codex"})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	store, err := secretstore.NewKeystore(secretstore.KeystoreOptions{
		Paths: secretstore.KeystorePaths{
			SecretsFile:      paths.SecretsFile,
			KeystoreSaltFile: paths.KeystoreSaltFile,
		},
	})
	if err != nil {
		t.Fatalf("NewKeystore returned error: %v", err)
	}
	if err := store.SetSecret("app-cli_cross", "cross-profile-secret"); err != nil {
		t.Fatalf("SetSecret returned error: %v", err)
	}

	stdin := bytes.NewBufferString(`{"protocolVersion":1,"ids":["app-cli_cross"]}`)
	var stdout, stderr bytes.Buffer
	if err := runSecretsGet([]string{"--home", root}, stdin, &stdout, &stderr); err != nil {
		t.Fatalf("runSecretsGet returned error: %v stderr=%s", err, stderr.String())
	}
	var resp execSecretResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("stdout is not exec response JSON: %v\n%s", err, stdout.String())
	}
	if resp.Values["app-cli_cross"] != "cross-profile-secret" {
		t.Fatalf("response = %#v", resp)
	}
}

func TestRunSecretsGetUsesActiveProfileFirstAndWarnsOnDuplicates(t *testing.T) {
	t.Setenv("LARK_CHANNEL_PROFILE", "")
	root := t.TempDir()
	writeCLIProfileRoot(t, root)
	if err := os.WriteFile(filepath.Join(root, "active-profile"), []byte("codex\n"), 0o600); err != nil {
		t.Fatalf("write active profile: %v", err)
	}
	for profile, value := range map[string]string{
		"claude": "claude-secret",
		"codex":  "codex-secret",
	} {
		paths, err := apppaths.Resolve(apppaths.Options{RootDir: root, Profile: profile})
		if err != nil {
			t.Fatalf("Resolve %s returned error: %v", profile, err)
		}
		store, err := secretstore.NewKeystore(secretstore.KeystoreOptions{
			Paths: secretstore.KeystorePaths{
				SecretsFile:      paths.SecretsFile,
				KeystoreSaltFile: paths.KeystoreSaltFile,
			},
		})
		if err != nil {
			t.Fatalf("NewKeystore %s returned error: %v", profile, err)
		}
		if err := store.SetSecret("app-cli_shared", value); err != nil {
			t.Fatalf("SetSecret %s returned error: %v", profile, err)
		}
	}

	var stdout, stderr bytes.Buffer
	if err := runSecretsGet([]string{"--home", root}, bytes.NewBufferString(`{"ids":["app-cli_shared"]}`), &stdout, &stderr); err != nil {
		t.Fatalf("runSecretsGet returned error: %v stderr=%s", err, stderr.String())
	}
	var resp execSecretResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("stdout is not exec response JSON: %v\n%s", err, stdout.String())
	}
	if resp.Values["app-cli_shared"] != "codex-secret" {
		t.Fatalf("response = %#v", resp)
	}
	if !strings.Contains(stderr.String(), "exists in multiple profiles; using codex") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunSecretsSetListGetAndRemove(t *testing.T) {
	root := t.TempDir()
	writeCLIProfileRoot(t, root)

	var stdout, stderr bytes.Buffer
	if code := runSecrets([]string{"set", "--home", root, "--profile", "codex", "--app-id", "cli_secret", "--value", "secret-value"}, bytes.NewReader(nil), &stdout, &stderr); code != 0 {
		t.Fatalf("secrets set code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "app-cli_secret") {
		t.Fatalf("set stdout = %q", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := runSecrets([]string{"set", "--home", root, "--profile", "codex", "--app-id", "cli_stdin"}, bytes.NewBufferString("stdin-secret\n"), &stdout, &stderr); code != 0 {
		t.Fatalf("secrets set stdin code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "app-cli_stdin") {
		t.Fatalf("set stdin stdout = %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := runSecrets([]string{"list", "--home", root, "--profile", "codex"}, bytes.NewReader(nil), &stdout, &stderr); code != 0 {
		t.Fatalf("secrets list code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "app-cli_secret") {
		t.Fatalf("list stdout = %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := runSecrets([]string{"get", "--home", root, "--profile", "codex"}, bytes.NewBufferString(`{"ids":["app-cli_secret"]}`), &stdout, &stderr); code != 0 {
		t.Fatalf("secrets get code=%d stderr=%s", code, stderr.String())
	}
	var resp execSecretResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("decode get response: %v\n%s", err, stdout.String())
	}
	if resp.Values["app-cli_secret"] != "secret-value" {
		t.Fatalf("get response = %#v", resp)
	}

	stdout.Reset()
	stderr.Reset()
	if code := runSecrets([]string{"remove", "--home", root, "--profile", "codex", "--app-id", "cli_secret"}, bytes.NewReader(nil), &stdout, &stderr); code != 0 {
		t.Fatalf("secrets remove code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "app-cli_secret") {
		t.Fatalf("remove stdout = %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := runSecrets([]string{"get", "--home", root, "--profile", "codex"}, bytes.NewBufferString(`{"ids":["app-cli_secret"]}`), &stdout, &stderr); code != 0 {
		t.Fatalf("secrets get missing code=%d stderr=%s", code, stderr.String())
	}
	resp = execSecretResponse{}
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("decode missing get response: %v\n%s", err, stdout.String())
	}
	if resp.Errors["app-cli_secret"].Message != "not found" {
		t.Fatalf("missing response = %#v", resp)
	}
}

func TestParseStartOptionsAcceptsSkipLarkCliPreflight(t *testing.T) {
	opts, err := parseStartOptions([]string{"--profile", "codex", "--skip-check-lark-cli"}, io.Discard)
	if err != nil {
		t.Fatalf("parseStartOptions returned error: %v", err)
	}
	if opts.Profile != "codex" || !opts.SkipCheckLarkCli {
		t.Fatalf("opts = %#v, want codex with skip lark-cli", opts)
	}
}

func TestResolveServiceProfileInfoAllowsExplicitRemovedProfileCleanup(t *testing.T) {
	root := t.TempDir()
	writeCLIProfileRoot(t, root)

	info, err := resolveServiceProfileInfo(serviceProfileOptions{Home: root, Profile: "removed-codex"})
	if err != nil {
		t.Fatalf("resolveServiceProfileInfo returned error: %v", err)
	}
	if info.Profile != "removed-codex" || info.RootDir != root || info.ConfigPath != filepath.Join(root, "config.json") || info.AppID != "" {
		t.Fatalf("info = %#v, want explicit removed profile cleanup target", info)
	}
}

func TestMaterializeEnvSecretForServiceStoresExecSecret(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BRIDGE_TEST_APP_SECRET", "service-mode-secret")
	body := `{
  "schemaVersion": 2,
  "activeProfile": "codex",
  "preferences": {},
  "profiles": {
    "codex": {
      "schemaVersion": 2,
      "agentKind": "codex",
      "accounts": {
        "app": {
          "id": "cli_codex",
          "secret": {"source": "env", "id": "BRIDGE_TEST_APP_SECRET"},
          "tenant": "feishu"
        }
      },
      "codex": {"binaryPath": "/bin/codex", "inheritCodexHome": true}
    }
  }
}`
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("mkdir root: %v", err)
	}
	configPath := filepath.Join(root, "config.json")
	if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if err := materializeEnvSecretForService(context.Background(), configPath, "codex"); err != nil {
		t.Fatalf("materializeEnvSecretForService returned error: %v", err)
	}

	snapshot, err := configstore.Load(configPath, configstore.LoadOptions{Profile: "codex"})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	secretRef, ok := snapshot.Profile.Accounts.App.Secret.(map[string]any)
	if !ok || secretRef["source"] != secretstore.SourceExec || secretRef["provider"] != "bridge" || secretRef["id"] != secretstore.SecretKeyForApp("cli_codex") {
		t.Fatalf("secret ref = %#v, want bridge exec ref", snapshot.Profile.Accounts.App.Secret)
	}
	if snapshot.Root.Secrets == nil || snapshot.Root.Secrets.Providers["bridge"]["source"] != secretstore.SourceExec || snapshot.Root.Secrets.Defaults[secretstore.SourceExec] != "bridge" {
		t.Fatalf("root secrets = %#v, want bridge exec provider", snapshot.Root.Secrets)
	}
	paths, err := apppaths.Resolve(apppaths.Options{RootDir: root, Profile: "codex"})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	store, err := secretstore.NewKeystore(secretstore.KeystoreOptions{
		Paths: secretstore.KeystorePaths{
			SecretsFile:      paths.SecretsFile,
			KeystoreSaltFile: paths.KeystoreSaltFile,
		},
	})
	if err != nil {
		t.Fatalf("NewKeystore returned error: %v", err)
	}
	secret, ok, err := store.GetSecret(secretstore.SecretKeyForApp("cli_codex"))
	if err != nil {
		t.Fatalf("GetSecret returned error: %v", err)
	}
	if !ok || secret != "service-mode-secret" {
		t.Fatalf("secret = %q ok=%v, want materialized secret", secret, ok)
	}
	resolved, err := resolveStartAppSecret(context.Background(), larkcli.AppConfig{Accounts: snapshot.Runtime.Accounts, Secrets: snapshot.Runtime.Secrets}, paths)
	if err != nil {
		t.Fatalf("resolveStartAppSecret returned error: %v", err)
	}
	if resolved != "service-mode-secret" {
		t.Fatalf("resolved secret = %q, want service-mode-secret", resolved)
	}
}

func TestClearDeadRuntimeLockRemovesMatchingLockFiles(t *testing.T) {
	root := t.TempDir()
	paths, err := apppaths.Resolve(apppaths.Options{RootDir: root, Profile: "codex"})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	pid := 99999999
	for processAlive(pid) {
		pid++
	}
	meta := runtimecoord.RuntimeLockMeta{
		Kind:      runtimecoord.LockProfile,
		Target:    paths.ProfileLockFile,
		Profile:   "codex",
		AgentKind: runtimecoord.AgentCodex,
		PID:       pid,
		StartedAt: "2026-07-06T00:00:00Z",
	}
	if err := os.MkdirAll(filepath.Dir(meta.Target), 0o700); err != nil {
		t.Fatalf("mkdir lock parent: %v", err)
	}
	if err := os.WriteFile(meta.Target, []byte{}, 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.MkdirAll(meta.Target+".lock", 0o700); err != nil {
		t.Fatalf("mkdir lock dir: %v", err)
	}
	body, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	if err := os.WriteFile(runtimecoord.RuntimeLockMetaFile(meta.Target), body, 0o600); err != nil {
		t.Fatalf("write meta: %v", err)
	}

	if err := clearDeadRuntimeLock(meta); err != nil {
		t.Fatalf("clearDeadRuntimeLock returned error: %v", err)
	}
	if _, err := os.Stat(meta.Target + ".lock"); !os.IsNotExist(err) {
		t.Fatalf("lock dir still exists or stat error: %v", err)
	}
	if _, err := os.Stat(runtimecoord.RuntimeLockMetaFile(meta.Target)); !os.IsNotExist(err) {
		t.Fatalf("meta file still exists or stat error: %v", err)
	}
	if _, err := os.Stat(meta.Target); err != nil {
		t.Fatalf("target file should remain: %v", err)
	}
}

func TestRunProfileListUseAndExportRedactsSecrets(t *testing.T) {
	root := t.TempDir()
	writeCLIProfileRoot(t, root)

	var stdout, stderr bytes.Buffer
	if code := runProfile([]string{"list", "--home", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("profile list code=%d stderr=%s", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "ACTIVE") || !strings.Contains(out, "claude") || !strings.Contains(out, "codex") || !strings.Contains(out, "*") {
		t.Fatalf("profile list stdout = %q", out)
	}

	stdout.Reset()
	stderr.Reset()
	if code := runProfile([]string{"use", "--home", root, "codex"}, &stdout, &stderr); code != 0 {
		t.Fatalf("profile use code=%d stderr=%s", code, stderr.String())
	}
	active, err := os.ReadFile(filepath.Join(root, "active-profile"))
	if err != nil {
		t.Fatalf("read active profile: %v", err)
	}
	if strings.TrimSpace(string(active)) != "codex" {
		t.Fatalf("active profile = %q", active)
	}
	snapshot, err := configstore.Load(filepath.Join(root, "config.json"), configstore.LoadOptions{Profile: "codex"})
	if err != nil {
		t.Fatalf("load config after use: %v", err)
	}
	if snapshot.Root.ActiveProfile != "codex" {
		t.Fatalf("root active profile = %q", snapshot.Root.ActiveProfile)
	}
	rawConfig, err := os.ReadFile(filepath.Join(root, "config.json"))
	if err != nil {
		t.Fatalf("read config after use: %v", err)
	}
	if strings.Contains(string(rawConfig), `"sandbox"`) || strings.Contains(string(rawConfig), `"permissionSource"`) {
		t.Fatalf("profile use wrote runtime-only fields:\n%s", rawConfig)
	}

	stdout.Reset()
	stderr.Reset()
	if code := runProfile([]string{"export", "--home", root, "codex"}, &stdout, &stderr); code != 0 {
		t.Fatalf("profile export code=%d stderr=%s", code, stderr.String())
	}
	exported := stdout.String()
	if !strings.Contains(exported, `"activeProfile": "codex"`) || !strings.Contains(exported, `"[REDACTED]"`) {
		t.Fatalf("export stdout = %s", exported)
	}
	if strings.Contains(exported, "secret-codex") {
		t.Fatalf("export leaked secret: %s", exported)
	}
	if strings.Contains(exported, `"sandbox"`) || strings.Contains(exported, `"permissionSource"`) {
		t.Fatalf("export wrote runtime-only fields:\n%s", exported)
	}
}

func TestRunProfileCreateAddsEncryptedProfileWithoutChangingActive(t *testing.T) {
	stubBootstrapAppCredentialValidator(t)
	root := t.TempDir()
	writeCLIProfileRoot(t, root)
	workspace := filepath.Join(root, "new-workspace")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	workspaceRealpath, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatalf("EvalSymlinks workspace: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if code := runProfile([]string{
		"create",
		"--home", root,
		"--agent", "claude",
		"--workspace", workspace,
		"--app-id", "cli_new",
		"--app-secret", "new-secret",
		"newbot",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("profile create code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "newbot") {
		t.Fatalf("profile create stdout = %q", stdout.String())
	}
	snapshot, err := configstore.Load(filepath.Join(root, "config.json"), configstore.LoadOptions{Profile: "newbot"})
	if err != nil {
		t.Fatalf("load new profile: %v", err)
	}
	if snapshot.Root.ActiveProfile != "claude" {
		t.Fatalf("active profile = %q, want claude", snapshot.Root.ActiveProfile)
	}
	if snapshot.Profile.AgentKind != configstore.AgentClaude || snapshot.Profile.Accounts.App.ID != "cli_new" {
		t.Fatalf("new profile = %#v", snapshot.Profile)
	}
	if snapshot.Profile.Workspaces.Default != workspaceRealpath {
		t.Fatalf("workspace = %q, want %q", snapshot.Profile.Workspaces.Default, workspaceRealpath)
	}
	if snapshot.Root.Secrets == nil || snapshot.Root.Secrets.Providers["bridge"]["source"] != "exec" {
		t.Fatalf("root secrets = %#v", snapshot.Root.Secrets)
	}
	secretPaths, err := apppaths.Resolve(apppaths.Options{RootDir: root, Profile: "newbot"})
	if err != nil {
		t.Fatalf("Resolve newbot: %v", err)
	}
	store, err := secretstore.NewKeystore(secretstore.KeystoreOptions{
		Paths: secretstore.KeystorePaths{
			SecretsFile:      secretPaths.SecretsFile,
			KeystoreSaltFile: secretPaths.KeystoreSaltFile,
		},
	})
	if err != nil {
		t.Fatalf("NewKeystore: %v", err)
	}
	secret, ok, err := store.GetSecret("app-cli_new")
	if err != nil {
		t.Fatalf("GetSecret: %v", err)
	}
	if !ok || secret != "new-secret" {
		t.Fatalf("secret = %q ok=%v", secret, ok)
	}
	rawConfig, err := os.ReadFile(filepath.Join(root, "config.json"))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if strings.Contains(string(rawConfig), "new-secret") || strings.Contains(string(rawConfig), `"sandbox"`) || strings.Contains(string(rawConfig), `"permissionSource"`) {
		t.Fatalf("config leaked secret or runtime fields:\n%s", rawConfig)
	}

	stdout.Reset()
	stderr.Reset()
	if code := runProfile([]string{"create", "--home", root, "--agent", "codex", "--app-id", "cli_other", "--app-secret", "secret", "newbot"}, &stdout, &stderr); code == 0 || !strings.Contains(stderr.String(), "profile create requested --agent codex") {
		t.Fatalf("profile create mismatch code=%d stderr=%s", code, stderr.String())
	}
}

func TestRunProfileRemoveArchivesProfileAndSelectsNextActive(t *testing.T) {
	root := t.TempDir()
	writeCLIProfileRoot(t, root)
	if err := os.WriteFile(filepath.Join(root, "active-profile"), []byte("codex\n"), 0o600); err != nil {
		t.Fatalf("write active profile: %v", err)
	}
	for _, profile := range []string{"claude", "codex"} {
		paths, err := apppaths.Resolve(apppaths.Options{RootDir: root, Profile: profile})
		if err != nil {
			t.Fatalf("Resolve %s: %v", profile, err)
		}
		if err := os.MkdirAll(paths.ProfileDir, 0o700); err != nil {
			t.Fatalf("mkdir profile dir %s: %v", profile, err)
		}
		if err := os.WriteFile(filepath.Join(paths.ProfileDir, "marker.txt"), []byte(profile), 0o600); err != nil {
			t.Fatalf("write marker %s: %v", profile, err)
		}
	}

	var stdout, stderr bytes.Buffer
	if code := runProfile([]string{"remove", "--home", root, "codex"}, &stdout, &stderr); code != 0 {
		t.Fatalf("profile remove code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "已归档 profile: codex") {
		t.Fatalf("profile remove stdout = %q", stdout.String())
	}
	snapshot, err := configstore.Load(filepath.Join(root, "config.json"), configstore.LoadOptions{Profile: "claude"})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if snapshot.Root.ActiveProfile != "claude" {
		t.Fatalf("active profile = %q, want claude", snapshot.Root.ActiveProfile)
	}
	if _, exists := snapshot.Root.Profiles["codex"]; exists {
		t.Fatalf("codex still exists in config")
	}
	active, err := os.ReadFile(filepath.Join(root, "active-profile"))
	if err != nil {
		t.Fatalf("read active profile: %v", err)
	}
	if strings.TrimSpace(string(active)) != "claude" {
		t.Fatalf("active sidecar = %q", active)
	}
	entries, err := os.ReadDir(filepath.Join(root, ".trash"))
	if err != nil {
		t.Fatalf("read trash: %v", err)
	}
	if len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), "codex-") {
		t.Fatalf("trash entries = %#v", entries)
	}
	if _, err := os.Stat(filepath.Join(root, "profiles", "codex")); !os.IsNotExist(err) {
		t.Fatalf("codex profile dir still exists or stat error: %v", err)
	}
}

func TestRunProfileRemovePurgeRequiresYesAndDeletesLastProfileConfig(t *testing.T) {
	root := t.TempDir()
	body := `{
  "schemaVersion": 2,
  "activeProfile": "solo",
  "preferences": {},
  "profiles": {
    "solo": {
      "schemaVersion": 2,
      "agentKind": "claude",
      "accounts": {"app": {"id": "cli_solo", "secret": "secret-solo", "tenant": "feishu"}}
    }
  }
}`
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("mkdir root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "config.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "active-profile"), []byte("solo\n"), 0o600); err != nil {
		t.Fatalf("write active: %v", err)
	}
	paths, err := apppaths.Resolve(apppaths.Options{RootDir: root, Profile: "solo"})
	if err != nil {
		t.Fatalf("Resolve solo: %v", err)
	}
	if err := os.MkdirAll(paths.ProfileDir, 0o700); err != nil {
		t.Fatalf("mkdir profile dir: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if code := runProfile([]string{"remove", "--home", root, "--purge", "solo"}, &stdout, &stderr); code == 0 || !strings.Contains(stderr.String(), "--purge requires --yes") {
		t.Fatalf("profile remove purge without yes code=%d stderr=%s", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := runProfile([]string{"remove", "--home", root, "--purge", "--yes", "solo"}, &stdout, &stderr); code != 0 {
		t.Fatalf("profile remove purge code=%d stderr=%s", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(root, "config.json")); !os.IsNotExist(err) {
		t.Fatalf("config still exists or stat error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "active-profile")); !os.IsNotExist(err) {
		t.Fatalf("active-profile still exists or stat error: %v", err)
	}
	if entries, err := os.ReadDir(filepath.Join(root, ".trash")); err == nil && len(entries) > 0 {
		t.Fatalf("trash not cleaned: %#v", entries)
	}
}

func TestRunMigrateConvertsLegacyConfigAndMovesState(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	workspaceRealpath, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatalf("EvalSymlinks workspace: %v", err)
	}
	legacyConfig := `{
  "accounts": {"app": {"id": "cli_legacy", "secret": "plain-secret", "tenant": "feishu"}},
  "preferences": {
    "access": {"allowedUsers": ["ou_1"], "admins": ["ou_admin"]},
    "requireMentionInGroup": false,
    "messageReply": "text"
  },
  "secrets": {
    "providers": {"bridge": {"source": "exec", "command": "/bridge/secrets-getter", "args": []}},
    "defaults": {"exec": "bridge"}
  }
}`
	if err := os.WriteFile(filepath.Join(root, "config.json"), []byte(legacyConfig), 0o600); err != nil {
		t.Fatalf("write legacy config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "sessions.json"), []byte(`{"items":[]}`), 0o600); err != nil {
		t.Fatalf("write sessions: %v", err)
	}
	workspacesJSON := fmt.Sprintf(`{"named":{"main":%q}}`, workspace)
	if err := os.WriteFile(filepath.Join(root, "workspaces.json"), []byte(workspacesJSON), 0o600); err != nil {
		t.Fatalf("write workspaces: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "media"), 0o700); err != nil {
		t.Fatalf("mkdir media: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "media", "a.txt"), []byte("media"), 0o600); err != nil {
		t.Fatalf("write media: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"migrate", "--home", root, "--profile", "migrated"}, &stdout, &stderr); code != 0 {
		t.Fatalf("migrate code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "已升级 profile 目录结构：migrated") {
		t.Fatalf("migrate stdout = %q", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(root, "config.json.bak")); err != nil {
		t.Fatalf("missing config backup: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "config.json.lock")); err != nil {
		t.Fatalf("missing config lock file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "sessions.json")); !os.IsNotExist(err) {
		t.Fatalf("root sessions still exists or stat error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "profiles", "migrated", "sessions.json")); err != nil {
		t.Fatalf("missing migrated sessions: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "profiles", "migrated", "media", "a.txt")); err != nil {
		t.Fatalf("missing migrated media: %v", err)
	}
	active, err := os.ReadFile(filepath.Join(root, "active-profile"))
	if err != nil {
		t.Fatalf("read active profile: %v", err)
	}
	if strings.TrimSpace(string(active)) != "migrated" {
		t.Fatalf("active profile = %q", active)
	}
	snapshot, err := configstore.Load(filepath.Join(root, "config.json"), configstore.LoadOptions{Profile: "migrated"})
	if err != nil {
		t.Fatalf("load migrated config: %v", err)
	}
	if snapshot.Profile.Accounts.App.ID != "cli_legacy" || snapshot.Profile.Workspaces.Default != workspaceRealpath {
		t.Fatalf("migrated profile = %#v", snapshot.Profile)
	}
	if !containsString(snapshot.Profile.Access.AllowedUsers, "ou_1") || !containsString(snapshot.Profile.Access.Admins, "ou_admin") || snapshot.Profile.Access.RequireMentionInGroup {
		t.Fatalf("migrated access = %#v", snapshot.Profile.Access)
	}
	rawConfig, err := os.ReadFile(filepath.Join(root, "config.json"))
	if err != nil {
		t.Fatalf("read migrated config: %v", err)
	}
	if strings.Contains(string(rawConfig), `"sandbox"`) || strings.Contains(string(rawConfig), `"permissionSource"`) {
		t.Fatalf("migrated config wrote runtime-only fields:\n%s", rawConfig)
	}
}

func TestResolveServiceRuntimeInfoMigratesLegacyConfig(t *testing.T) {
	root := t.TempDir()
	legacyConfig := `{
  "accounts": {"app": {"id": "cli_legacy", "secret": "plain-secret", "tenant": "feishu"}},
  "preferences": {"messageReply": "markdown"}
}`
	if err := os.WriteFile(filepath.Join(root, "config.json"), []byte(legacyConfig), 0o600); err != nil {
		t.Fatalf("write legacy config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "sessions.json"), []byte(`{"items":[]}`), 0o600); err != nil {
		t.Fatalf("write sessions: %v", err)
	}

	info, err := resolveServiceRuntimeInfo(startOptions{Home: root})
	if err != nil {
		t.Fatalf("resolveServiceRuntimeInfo returned error: %v", err)
	}
	if info.Profile != "claude" || info.AppID != "cli_legacy" {
		t.Fatalf("info = %#v, want migrated claude profile", info)
	}
	if _, err := os.Stat(filepath.Join(root, "sessions.json")); !os.IsNotExist(err) {
		t.Fatalf("root sessions still exists or stat error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "profiles", "claude", "sessions.json")); err != nil {
		t.Fatalf("missing migrated sessions: %v", err)
	}
	snapshot, err := configstore.Load(filepath.Join(root, "config.json"), configstore.LoadOptions{Profile: "claude"})
	if err != nil {
		t.Fatalf("load migrated config: %v", err)
	}
	if snapshot.Root.SchemaVersion != 2 || snapshot.Profile.Accounts.App.ID != "cli_legacy" {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestRunMigrateBlocksActiveLegacyProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sleep process test uses POSIX sleep")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.json"), []byte(`{"accounts":{"app":{"id":"cli_legacy","secret":"plain","tenant":"feishu"}}}`), 0o600); err != nil {
		t.Fatalf("write legacy config: %v", err)
	}
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()
	registry := fmt.Sprintf(`{"entries":[{"id":"legacy","pid":%d,"appId":"cli_legacy","profileName":"claude"}]}`, cmd.Process.Pid)
	if err := os.WriteFile(filepath.Join(root, "processes.json"), []byte(registry), 0o600); err != nil {
		t.Fatalf("write processes: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"migrate", "--home", root}, &stdout, &stderr); code == 0 || !strings.Contains(stderr.String(), "active bridge process blocks v2 migration") {
		t.Fatalf("migrate active process code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(root, "active-profile")); !os.IsNotExist(err) {
		t.Fatalf("migration should not create active-profile, stat err=%v", err)
	}
}

func TestRunProfileExportIncludeSecretsRequiresYesAndMaterializesKeystore(t *testing.T) {
	root := t.TempDir()
	body := `{
  "schemaVersion": 2,
  "activeProfile": "codex",
  "preferences": {},
  "secrets": {
    "providers": {
      "bridge": {
        "source": "exec",
        "command": "lark-channel-bridge",
        "args": ["secrets", "get"]
      }
    },
    "defaults": {"exec": "bridge"}
  },
  "profiles": {
    "codex": {
      "schemaVersion": 2,
      "agentKind": "codex",
      "accounts": {
        "app": {
          "id": "cli_codex",
          "secret": {"source": "exec", "provider": "bridge", "id": "app-cli_codex"},
          "tenant": "feishu"
        }
      },
      "codex": {"binaryPath": "/bin/codex", "inheritCodexHome": true}
    }
  }
}`
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("mkdir root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "config.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "active-profile"), []byte("codex\n"), 0o600); err != nil {
		t.Fatalf("write active profile: %v", err)
	}
	paths, err := apppaths.Resolve(apppaths.Options{RootDir: root, Profile: "codex"})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	store, err := secretstore.NewKeystore(secretstore.KeystoreOptions{
		Paths: secretstore.KeystorePaths{
			SecretsFile:      paths.SecretsFile,
			KeystoreSaltFile: paths.KeystoreSaltFile,
		},
	})
	if err != nil {
		t.Fatalf("NewKeystore returned error: %v", err)
	}
	if err := store.SetSecret("app-cli_codex", "materialized-secret"); err != nil {
		t.Fatalf("SetSecret returned error: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if code := runProfile([]string{"export", "--home", root, "--include-secrets", "codex"}, &stdout, &stderr); code == 0 || !strings.Contains(stderr.String(), "--include-secrets requires --yes") {
		t.Fatalf("profile export without --yes code=%d stderr=%s", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := runProfile([]string{"export", "--home", root, "--include-secrets", "--yes", "codex"}, &stdout, &stderr); code != 0 {
		t.Fatalf("profile export include secrets code=%d stderr=%s", code, stderr.String())
	}
	exported := stdout.String()
	if !strings.Contains(exported, `"secret": "materialized-secret"`) || strings.Contains(exported, "[REDACTED]") {
		t.Fatalf("export stdout = %s", exported)
	}
	if !strings.Contains(exported, `"secrets"`) {
		t.Fatalf("export omitted root secrets: %s", exported)
	}
}

func TestRunPSListsRuntimeRegistryProcesses(t *testing.T) {
	root := t.TempDir()
	coord, err := runtimecoord.New(runtimecoord.Options{
		RootDir:   root,
		Profile:   "codex",
		AgentKind: runtimecoord.AgentCodex,
		Adapter:   fakeRuntimeAdapter{},
	})
	if err != nil {
		t.Fatalf("runtimecoord.New returned error: %v", err)
	}
	if err := coord.Start(context.Background(), runtimecoord.StartOptions{
		AppID:      "cli_test",
		Tenant:     runtimecoord.TenantFeishu,
		AgentKind:  runtimecoord.AgentCodex,
		ConfigPath: filepath.Join(root, "config.json"),
		Version:    "test",
	}); err != nil {
		t.Fatalf("Coordinator.Start returned error: %v", err)
	}
	defer coord.Shutdown(context.Background())

	var stdout, stderr bytes.Buffer
	if err := runPS([]string{"--home", root}, &stdout, &stderr); err != nil {
		t.Fatalf("runPS returned error: %v stderr=%s", err, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "cli_test") || !strings.Contains(out, "codex") || !strings.Contains(out, "Bridge Bot") {
		t.Fatalf("ps output = %q", out)
	}
}

func TestFindRuntimeProcessMatchesIDOrPID(t *testing.T) {
	entries := []runtimecoord.ProcessEntry{
		{ID: "abc123", PID: 4242},
		{ID: "def456", PID: 5252},
	}
	if got, ok := findRuntimeProcess(entries, "abc123"); !ok || got.PID != 4242 {
		t.Fatalf("find by id = %#v %v", got, ok)
	}
	if got, ok := findRuntimeProcess(entries, "4242"); !ok || got.ID != "abc123" {
		t.Fatalf("find by pid = %#v %v", got, ok)
	}
	if got, ok := findRuntimeProcess(entries, "2"); !ok || got.ID != "def456" {
		t.Fatalf("find by list index = %#v %v", got, ok)
	}
	if _, ok := findRuntimeProcess(entries, "missing"); ok {
		t.Fatalf("find missing ok = true")
	}
}

func TestStopProcessEntryTerminatesBeforeForceKill(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell signal test uses POSIX sh")
	}
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep process: %v", err)
	}
	waitDone := make(chan error, 1)
	go func() {
		waitDone <- cmd.Wait()
	}()
	result, stillAlive, err := stopProcessEntry(context.Background(), cmd.Process.Pid, time.Second)
	if err != nil {
		t.Fatalf("stopProcessEntry returned error: %v", err)
	}
	if stillAlive || result != stopProcessTerminated {
		t.Fatalf("stopProcessEntry result = %q stillAlive=%v, want terminated false", result, stillAlive)
	}
	select {
	case <-waitDone:
	case <-time.After(time.Second):
		t.Fatalf("process was not reaped")
	}
}

func TestPreflightKeepsBotOnlyAfterBindFallback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake lark-cli script uses POSIX sh")
	}
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatalf("MkdirAll bin: %v", err)
	}
	logPath := filepath.Join(root, "lark-cli.log")
	fake := filepath.Join(binDir, "lark-cli")
	if err := os.WriteFile(fake, []byte(`#!/bin/sh
printf '%s\n' "$*" >> "$LARKCLI_LOG"
if [ "$1 $2" = "config show" ]; then exit 1; fi
exit 0
`), 0o700); err != nil {
		t.Fatalf("write fake lark-cli: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("LARKCLI_LOG", logPath)

	paths, err := apppaths.Resolve(apppaths.Options{RootDir: root, Profile: "codex"})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	_, err = preflightStartLarkCLI(
		context.Background(),
		bridge.LarkCLIAppConfig{Accounts: bridge.LarkCLIAccountsConfig{App: bridge.LarkCLIAppCredentials{ID: "cli_test", Secret: "plain", Tenant: bridge.LarkCLITenantFeishu}}},
		bridge.LarkCliProjectionPaths{
			RootDir:                 paths.RootDir,
			Profile:                 paths.Profile,
			LarkCliSourceDir:        paths.LarkCliSourceDir,
			LarkCliSourceConfigFile: paths.LarkCliSourceConfigFile,
			SecretsGetterScript:     paths.SecretsGetterScript,
			SecretsGetterCommand:    "/bridge/bin",
		},
		bridge.LarkCliEnvContext{
			Profile:                 paths.Profile,
			RootDir:                 paths.RootDir,
			LarkCliConfigDir:        paths.LarkCliConfigDir,
			LarkCliSourceConfigFile: paths.LarkCliSourceConfigFile,
		},
		bridge.LarkCliIdentityUserDefault,
		nil,
	)
	if err != nil {
		t.Fatalf("preflightStartLarkCLI returned error: %v", err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read lark-cli log: %v", err)
	}
	got := strings.Split(strings.TrimSpace(string(data)), "\n")
	want := []string{
		"config show",
		"config bind --source lark-channel --identity bot-only",
		"config strict-mode bot",
		"config default-as bot",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("lark-cli calls = %#v, want %#v", got, want)
	}
}

func TestPreflightUsesLegacyOverlayWhenBindSourceFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake lark-cli script uses POSIX sh")
	}
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatalf("MkdirAll bin: %v", err)
	}
	logPath := filepath.Join(root, "lark-cli.log")
	countPath := filepath.Join(root, "bind-count")
	fake := filepath.Join(binDir, "lark-cli")
	if err := os.WriteFile(fake, []byte(`#!/bin/sh
printf '%s\n' "$*" >> "$LARKCLI_LOG"
if [ "$1 $2" = "config show" ]; then exit 1; fi
if [ "$1 $2" = "config bind" ]; then
  count=0
  if [ -f "$BIND_COUNT" ]; then count=$(cat "$BIND_COUNT"); fi
  count=$((count + 1))
  printf '%s\n' "$count" > "$BIND_COUNT"
  if [ "$count" = "1" ]; then echo "accounts.app.id missing in $ROOT_CONFIG" >&2; exit 2; fi
  cmp -s "$ROOT_CONFIG" "$SOURCE_CONFIG"
  exit $?
fi
exit 0
`), 0o700); err != nil {
		t.Fatalf("write fake lark-cli: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("LARKCLI_LOG", logPath)
	t.Setenv("BIND_COUNT", countPath)

	paths, err := apppaths.Resolve(apppaths.Options{RootDir: root, Profile: "codex"})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if err := os.MkdirAll(paths.RootDir, 0o700); err != nil {
		t.Fatalf("MkdirAll root: %v", err)
	}
	originalConfig := []byte(`{"original":true}` + "\n")
	if err := os.WriteFile(paths.ConfigFile, originalConfig, 0o600); err != nil {
		t.Fatalf("write root config: %v", err)
	}
	t.Setenv("ROOT_CONFIG", paths.ConfigFile)
	t.Setenv("SOURCE_CONFIG", paths.LarkCliSourceConfigFile)

	_, err = preflightStartLarkCLI(
		context.Background(),
		bridge.LarkCLIAppConfig{Accounts: bridge.LarkCLIAccountsConfig{App: bridge.LarkCLIAppCredentials{ID: "cli_test", Secret: "plain", Tenant: bridge.LarkCLITenantFeishu}}},
		bridge.LarkCliProjectionPaths{
			RootDir:                 paths.RootDir,
			Profile:                 paths.Profile,
			LarkCliSourceDir:        paths.LarkCliSourceDir,
			LarkCliSourceConfigFile: paths.LarkCliSourceConfigFile,
			SecretsGetterScript:     paths.SecretsGetterScript,
			SecretsGetterCommand:    "/bridge/bin",
		},
		bridge.LarkCliEnvContext{
			Profile:                 paths.Profile,
			RootDir:                 paths.RootDir,
			ConfigPath:              paths.ConfigFile,
			LarkCliConfigDir:        paths.LarkCliConfigDir,
			LarkCliSourceConfigFile: paths.LarkCliSourceConfigFile,
		},
		bridge.LarkCliIdentityUserDefault,
		nil,
	)
	if err != nil {
		t.Fatalf("preflightStartLarkCLI returned error: %v", err)
	}
	count, err := os.ReadFile(countPath)
	if err != nil {
		t.Fatalf("read bind count: %v", err)
	}
	if strings.TrimSpace(string(count)) != "2" {
		t.Fatalf("bind count = %q, want 2", count)
	}
	restored, err := os.ReadFile(paths.ConfigFile)
	if err != nil {
		t.Fatalf("read restored config: %v", err)
	}
	if string(restored) != string(originalConfig) {
		t.Fatalf("root config was not restored:\n%s", restored)
	}
}

func TestLarkCLIConfigurationWarningClassifiesTooOldBindSource(t *testing.T) {
	got := larkCLIConfigurationWarning("codex", strings.Join([]string{
		"Usage:",
		"  lark-cli config [command]",
		"",
		"Error: unknown flag: --source",
	}, "\n"))
	for _, needle := range []string{
		"does not support the lark-channel source",
		"Install a lark-cli build that supports the lark-channel source",
		"lark-cli does not support `config bind --source lark-channel`",
		"lark-channel-bridge run --profile codex",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("warning missing %q:\n%s", needle, got)
		}
	}
	if strings.Contains(got, "valid App Secret") {
		t.Fatalf("too-old warning included generic app-secret recovery:\n%s", got)
	}
}

func TestStartCommandOptionsExposeManagedCommandState(t *testing.T) {
	root := t.TempDir()
	paths, err := apppaths.Resolve(apppaths.Options{RootDir: root, Profile: "codex"})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	configPath := filepath.Join(root, "config.json")
	opts, err := startCommandOptions(configstore.RuntimeConfig{
		ProfileConfig: configstore.ProfileConfig{
			Preferences: map[string]any{"runIdleTimeoutMinutes": float64(15)},
		},
	}, paths, configPath, bridge.LarkCliEnvContext{}, func(context.Context) error { return nil })
	if err != nil {
		t.Fatalf("startCommandOptions returned error: %v", err)
	}
	if opts.ProfileName != "codex" || opts.ConfigPath != configPath || opts.Keystore == nil {
		t.Fatalf("options = %#v", opts)
	}
	if opts.GlobalIdleTimeout != 15*time.Minute || opts.LarkCLIIdentity == nil || opts.Reconnector == nil || opts.AccountValidator == nil {
		t.Fatalf("options = %#v", opts)
	}
	if opts.Workspaces == nil {
		t.Fatalf("Workspaces = nil")
	}
	cwd := t.TempDir()
	opts.Workspaces.SetCWD("chat-1", cwd)
	opts.Workspaces.SaveNamed("main", cwd)

	data, err := os.ReadFile(paths.WorkspacesFile)
	if err != nil {
		t.Fatalf("ReadFile workspaces returned error: %v", err)
	}
	var persisted struct {
		Chats map[string]struct {
			CWD string `json:"cwd"`
		} `json:"chats"`
		Named map[string]string `json:"named"`
	}
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("Unmarshal workspaces returned error: %v", err)
	}
	if persisted.Chats["chat-1"].CWD != cwd || persisted.Named["main"] != cwd {
		t.Fatalf("persisted workspaces = %#v", persisted)
	}
	reopened, err := bridge.NewFileWorkspaceStore(paths.WorkspacesFile)
	if err != nil {
		t.Fatalf("NewFileWorkspaceStore returned error: %v", err)
	}
	if reopened.CWDFor("chat-1") != cwd || reopened.GetNamed("main") != cwd {
		t.Fatalf("reopened workspaces cwd=%q named=%q", reopened.CWDFor("chat-1"), reopened.GetNamed("main"))
	}
}

func TestBootstrapStartConfigCreatesCodexProfileWithEncryptedSecret(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake codex script uses POSIX sh")
	}
	stubBootstrapAppCredentialValidator(t)
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatalf("MkdirAll bin: %v", err)
	}
	fakeCodex := filepath.Join(binDir, "codex")
	if err := os.WriteFile(fakeCodex, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatalf("MkdirAll workspace: %v", err)
	}
	workspaceRealpath, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatalf("EvalSymlinks workspace: %v", err)
	}
	paths, err := apppaths.Resolve(apppaths.Options{RootDir: root, Profile: "codex"})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	err = bootstrapStartConfig(startOptions{
		Agent:     "codex",
		AppID:     "cli_boot",
		AppSecret: "boot-secret",
		Workspace: workspace,
	}, paths, paths.ConfigFile)
	if err != nil {
		t.Fatalf("bootstrapStartConfig returned error: %v", err)
	}

	active, err := os.ReadFile(paths.ActiveProfileFile)
	if err != nil {
		t.Fatalf("read active profile: %v", err)
	}
	if strings.TrimSpace(string(active)) != "codex" {
		t.Fatalf("active profile = %q", active)
	}
	snapshot, err := configstore.Load(paths.ConfigFile, configstore.LoadOptions{Profile: "codex"})
	if err != nil {
		t.Fatalf("Load bootstrapped config returned error: %v", err)
	}
	if snapshot.Profile.AgentKind != configstore.AgentCodex {
		t.Fatalf("agent kind = %q", snapshot.Profile.AgentKind)
	}
	if snapshot.Profile.Workspaces.Default != workspaceRealpath {
		t.Fatalf("workspace = %q, want %q", snapshot.Profile.Workspaces.Default, workspaceRealpath)
	}
	if snapshot.Profile.Codex == nil || snapshot.Profile.Codex.BinaryPath != fakeCodex || !snapshot.Profile.Codex.InheritCodexHome || !snapshot.Profile.Codex.IgnoreRules {
		t.Fatalf("codex config = %#v", snapshot.Profile.Codex)
	}
	if snapshot.Root.Secrets == nil || snapshot.Root.Secrets.Providers["bridge"]["source"] != "exec" {
		t.Fatalf("root secrets = %#v", snapshot.Root.Secrets)
	}
	appConfig := larkcli.AppConfig{Accounts: snapshot.Runtime.Accounts, Secrets: snapshot.Runtime.Secrets}
	secret, err := resolveStartAppSecret(context.Background(), appConfig, paths)
	if err != nil {
		t.Fatalf("resolveStartAppSecret returned error: %v", err)
	}
	if secret != "boot-secret" {
		t.Fatalf("resolved secret = %q", secret)
	}
}

func TestBootstrapStartConfigRequiresAppCredentials(t *testing.T) {
	root := t.TempDir()
	paths, err := apppaths.Resolve(apppaths.Options{RootDir: root, Profile: "codex"})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	err = bootstrapStartConfig(startOptions{Agent: "codex"}, paths, paths.ConfigFile)
	if err == nil || !strings.Contains(err.Error(), "--app-id") {
		t.Fatalf("bootstrapStartConfig error = %v", err)
	}
}

func TestSelectFirstRunBootstrapAgentDetectsOnlyCodex(t *testing.T) {
	root := t.TempDir()
	fakeCodex := filepath.Join(root, "codex")
	if err := os.WriteFile(fakeCodex, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	t.Setenv("LARK_CHANNEL_CLAUDE_BIN", filepath.Join(root, "missing-claude"))
	t.Setenv("LARK_CHANNEL_CODEX_BIN", fakeCodex)

	opts, paths, err := selectFirstRunBootstrapAgent(startOptions{}, root)
	if err != nil {
		t.Fatalf("selectFirstRunBootstrapAgent returned error: %v", err)
	}
	if opts.Agent != "codex" || opts.Profile != "codex" || paths.Profile != "codex" {
		t.Fatalf("opts=%#v paths.Profile=%q, want codex", opts, paths.Profile)
	}
}

func TestSelectFirstRunBootstrapAgentRejectsAmbiguousNonInteractive(t *testing.T) {
	root := t.TempDir()
	fakeClaude := filepath.Join(root, "claude")
	fakeCodex := filepath.Join(root, "codex")
	if err := os.WriteFile(fakeClaude, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
	if err := os.WriteFile(fakeCodex, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	t.Setenv("LARK_CHANNEL_CLAUDE_BIN", fakeClaude)
	t.Setenv("LARK_CHANNEL_CODEX_BIN", fakeCodex)

	_, _, err := selectFirstRunBootstrapAgent(startOptions{}, root)
	if err == nil || !strings.Contains(err.Error(), "检测到多个本地 agent") {
		t.Fatalf("selectFirstRunBootstrapAgent error = %v, want ambiguous agent error", err)
	}
}

func TestAssertStartAppMatchesExistingProfileRejectsDifferentApp(t *testing.T) {
	cfg := larkcli.AppConfig{Accounts: larkcli.AccountsConfig{App: larkcli.AppCredentials{ID: "cli_existing"}}}
	if err := assertStartAppMatchesExistingProfile(startOptions{AppID: "cli_existing"}, "codex", cfg); err != nil {
		t.Fatalf("same app returned error: %v", err)
	}
	err := assertStartAppMatchesExistingProfile(startOptions{AppID: "cli_other"}, "codex", cfg)
	if err == nil || !strings.Contains(err.Error(), "profile already exists: codex") || !strings.Contains(err.Error(), "cli_existing") {
		t.Fatalf("different app error = %v", err)
	}
}

func TestStartReplyModeAppliesLegacyTextMigration(t *testing.T) {
	tests := []struct {
		name        string
		preferences map[string]any
		want        bridge.LarkReplyMode
	}{
		{
			name:        "legacy text means markdown stream",
			preferences: map[string]any{"messageReply": "text"},
			want:        bridge.LarkReplyMarkdown,
		},
		{
			name:        "migrated text stays text",
			preferences: map[string]any{"messageReply": "text", "messageReplyMigrated": true},
			want:        bridge.LarkReplyText,
		},
		{
			name:        "card stays card",
			preferences: map[string]any{"messageReply": "card"},
			want:        bridge.LarkReplyCard,
		},
		{
			name:        "default markdown",
			preferences: map[string]any{},
			want:        bridge.LarkReplyMarkdown,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := startReplyMode(tt.preferences); got != tt.want {
				t.Fatalf("startReplyMode() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStartShowToolCallsDefaultAndOverride(t *testing.T) {
	if got := startShowToolCalls(map[string]any{}); !got {
		t.Fatalf("startShowToolCalls(default) = false, want true")
	}
	if got := startShowToolCalls(map[string]any{"showToolCalls": false}); got {
		t.Fatalf("startShowToolCalls(false) = true, want false")
	}
}

func TestStartCotMessagesNormalizesLegacyAliases(t *testing.T) {
	tests := []struct {
		name        string
		preferences map[string]any
		want        bridge.LarkCotMessagesMode
	}{
		{name: "default off", preferences: map[string]any{}, want: bridge.LarkCotMessagesOff},
		{name: "brief", preferences: map[string]any{"cotMessages": "brief"}, want: bridge.LarkCotMessagesBrief},
		{name: "simple alias", preferences: map[string]any{"cotMessages": "simple"}, want: bridge.LarkCotMessagesBrief},
		{name: "detailed", preferences: map[string]any{"cotMessages": "detailed"}, want: bridge.LarkCotMessagesDetailed},
		{name: "on alias", preferences: map[string]any{"cotMessages": "on"}, want: bridge.LarkCotMessagesDetailed},
		{name: "full is not a supported alias", preferences: map[string]any{"cotMessages": "full"}, want: bridge.LarkCotMessagesOff},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := startCotMessages(tt.preferences); got != tt.want {
				t.Fatalf("startCotMessages() = %q, want %q", got, tt.want)
			}
		})
	}
}

func writeCLIProfileRoot(t *testing.T, root string) {
	t.Helper()
	body := `{
  "schemaVersion": 2,
  "activeProfile": "claude",
  "preferences": {},
  "profiles": {
    "claude": {
      "schemaVersion": 2,
      "agentKind": "claude",
      "accounts": {"app": {"id": "cli_claude", "secret": "secret-claude", "tenant": "feishu"}}
    },
    "codex": {
      "schemaVersion": 2,
      "agentKind": "codex",
      "accounts": {"app": {"id": "cli_codex", "secret": "secret-codex", "tenant": "feishu"}},
      "codex": {"binaryPath": "/bin/codex", "inheritCodexHome": true}
    }
  }
}`
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("mkdir root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "config.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "active-profile"), []byte("claude\n"), 0o600); err != nil {
		t.Fatalf("write active profile: %v", err)
	}
}

func stubBootstrapAppCredentialValidator(t *testing.T) {
	t.Helper()
	previous := bootstrapAppCredentialValidator
	bootstrapAppCredentialValidator = func(context.Context, string, string, string) (bridge.CommandAccountValidationResult, error) {
		return bridge.CommandAccountValidationResult{OK: true, BotName: "Bridge Bot"}, nil
	}
	t.Cleanup(func() {
		bootstrapAppCredentialValidator = previous
	})
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

type fakeRuntimeAdapter struct{}

func (fakeRuntimeAdapter) Start(context.Context, runtimecoord.StartRequest) (runtimecoord.RuntimeHandle, error) {
	return fakeRuntimeHandle{}, nil
}

type fakeRuntimeHandle struct{}

func (fakeRuntimeHandle) Shutdown(context.Context) error {
	return nil
}

func (fakeRuntimeHandle) Status(context.Context) (runtimecoord.AdapterStatus, error) {
	return runtimecoord.AdapterStatus{Connected: true, BotName: "Bridge Bot"}, nil
}
