package larkcli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWriteLarkCliSourceProjectionWritesProfileScopedSourceConfig(t *testing.T) {
	root := t.TempDir()
	paths := ProjectionPaths{
		RootDir:                  root,
		Profile:                  "codex-dev",
		LarkCliSourceDir:         filepath.Join(root, "profiles", "codex-dev", "lark-cli-source"),
		LarkCliSourceConfigFile:  filepath.Join(root, "profiles", "codex-dev", "lark-cli-source", "config.json"),
		SecretsGetterScript:      filepath.Join(root, "secrets-getter"),
		SecretsGetterNodePath:    "/opt/node/bin/node",
		SecretsGetterBridgeEntry: "/opt/bridge/bin.mjs",
	}
	cfg := AppConfig{
		Accounts: AccountsConfig{
			App: AppCredentials{
				ID:     "cli_codex",
				Tenant: TenantFeishu,
				Secret: SecretRef{
					Source:   "exec",
					Provider: "bridge",
					ID:       "app-cli_codex",
				},
			},
		},
		Secrets: &SecretsConfig{
			Providers: map[string]ProviderConfig{
				"bridge": {
					"source":  "exec",
					"command": "/stale/secrets-getter",
					"args":    []string{"old"},
					"env": map[string]any{
						"KEEP":              "1",
						"LARK_CHANNEL_HOME": "stale",
					},
				},
			},
		},
	}

	path, err := WriteLarkCliSourceProjection(cfg, paths)
	if err != nil {
		t.Fatalf("WriteLarkCliSourceProjection returned error: %v", err)
	}
	if path != paths.LarkCliSourceConfigFile {
		t.Fatalf("path = %q, want %q", path, paths.LarkCliSourceConfigFile)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read projection: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("projection is invalid JSON: %v", err)
	}

	app := got["accounts"].(map[string]any)["app"].(map[string]any)
	if app["id"] != "cli_codex" || app["tenant"] != "feishu" {
		t.Fatalf("projected app = %#v", app)
	}
	secret := app["secret"].(map[string]any)
	if secret["source"] != "exec" || secret["provider"] != "bridge" || secret["id"] != "app-cli_codex" {
		t.Fatalf("projected secret = %#v", secret)
	}

	provider := got["secrets"].(map[string]any)["providers"].(map[string]any)["bridge"].(map[string]any)
	if provider["source"] != "exec" {
		t.Fatalf("provider source = %#v", provider["source"])
	}
	if provider["command"] != SecretsGetterWrapperPath(paths.SecretsGetterScript) {
		t.Fatalf("provider command = %q", provider["command"])
	}
	if args := provider["args"].([]any); len(args) != 0 {
		t.Fatalf("provider args = %#v, want empty array", args)
	}
	env := provider["env"].(map[string]any)
	if env["KEEP"] != "1" {
		t.Fatalf("provider env lost existing key: %#v", env)
	}
	if env["LARK_CHANNEL_HOME"] != root || env["LARK_CHANNEL_PROFILE"] != "codex-dev" {
		t.Fatalf("provider env = %#v", env)
	}

	assertMode(t, path, 0o600)
	assertMode(t, SecretsGetterWrapperPath(paths.SecretsGetterScript), 0o700)
	if runtime.GOOS != "windows" {
		assertMode(t, paths.LarkCliSourceDir, 0o700)
	}
	wrapper, err := os.ReadFile(SecretsGetterWrapperPath(paths.SecretsGetterScript))
	if err != nil {
		t.Fatalf("read secrets getter wrapper: %v", err)
	}
	if !strings.Contains(string(wrapper), "/opt/node/bin/node") || !strings.Contains(string(wrapper), "/opt/bridge/bin.mjs") {
		t.Fatalf("wrapper did not use projection entrypoint:\n%s", string(wrapper))
	}
}

func TestWriteLarkCliSourceProjectionRequiresSecretsGetterEntrypointForExecSecret(t *testing.T) {
	root := t.TempDir()
	_, err := WriteLarkCliSourceProjection(AppConfig{
		Accounts: AccountsConfig{
			App: AppCredentials{
				ID:     "cli_codex",
				Tenant: TenantFeishu,
				Secret: SecretRef{
					Source:   "exec",
					Provider: "bridge",
					ID:       "app-cli_codex",
				},
			},
		},
	}, ProjectionPaths{
		RootDir:                 root,
		Profile:                 "codex-dev",
		LarkCliSourceDir:        filepath.Join(root, "profiles", "codex-dev", "lark-cli-source"),
		LarkCliSourceConfigFile: filepath.Join(root, "profiles", "codex-dev", "lark-cli-source", "config.json"),
		SecretsGetterScript:     filepath.Join(root, "secrets-getter"),
	})
	if err == nil || !strings.Contains(err.Error(), "SecretsGetterNodePath") {
		t.Fatalf("WriteLarkCliSourceProjection error = %v, want missing entrypoint error", err)
	}
}

func TestEnsureSecretsGetterWrapperPlatformOutput(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "secrets-getter")

	posixPath, err := EnsureSecretsGetterWrapper(SecretsGetterWrapperOptions{
		RootDir:             root + "'quoted",
		SecretsGetterScript: script,
		Platform:            "darwin",
		NodePath:            "/opt/node/bin/node",
		BridgeEntry:         "/opt/bridge/bin.mjs",
	})
	if err != nil {
		t.Fatalf("EnsureSecretsGetterWrapper returned error: %v", err)
	}
	if posixPath != script {
		t.Fatalf("posix wrapper path = %q, want %q", posixPath, script)
	}
	posix, err := os.ReadFile(posixPath)
	if err != nil {
		t.Fatalf("ReadFile posix wrapper returned error: %v", err)
	}
	content := string(posix)
	if !strings.Contains(content, "#!/bin/sh") ||
		!strings.Contains(content, "LARK_CHANNEL_HOME='"+root+"'\\''quoted'") ||
		!strings.Contains(content, "'/opt/node/bin/node'") {
		t.Fatalf("unexpected posix wrapper content:\n%s", content)
	}

	windowsPath, err := EnsureSecretsGetterWrapper(SecretsGetterWrapperOptions{
		RootDir:             root,
		SecretsGetterScript: script,
		Platform:            "windows",
		NodePath:            `C:\Program Files\nodejs\node.exe`,
		BridgeEntry:         `C:\bridge\bin\bridge.mjs`,
	})
	if err != nil {
		t.Fatalf("EnsureSecretsGetterWrapper windows returned error: %v", err)
	}
	if windowsPath != script+".cmd" {
		t.Fatalf("windows wrapper path = %q, want %q", windowsPath, script+".cmd")
	}
	windows, err := os.ReadFile(windowsPath)
	if err != nil {
		t.Fatalf("ReadFile windows wrapper returned error: %v", err)
	}
	windowsContent := string(windows)
	if !strings.Contains(windowsContent, "@echo off") ||
		!strings.Contains(windowsContent, `"`+`C:\Program Files\nodejs\node.exe`+`"`) ||
		!strings.Contains(windowsContent, `"`+`C:\bridge\bin\bridge.mjs`+`" secrets get %*`) {
		t.Fatalf("unexpected windows wrapper content:\n%s", windowsContent)
	}
}

func TestEnsureSecretsGetterWrapperDirectCommandOutput(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "secrets-getter")
	path, err := EnsureSecretsGetterWrapper(SecretsGetterWrapperOptions{
		RootDir:             root,
		SecretsGetterScript: script,
		Platform:            "darwin",
		DirectCommand:       "/opt/bridge/lark-channel-bridge",
	})
	if err != nil {
		t.Fatalf("EnsureSecretsGetterWrapper returned error: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read wrapper: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "'/opt/bridge/lark-channel-bridge' secrets get") ||
		strings.Contains(content, "node") {
		t.Fatalf("unexpected direct wrapper content:\n%s", content)
	}
}

func TestBuildLarkCliSourceProjectionOmitsSecretsWithoutExternalSecret(t *testing.T) {
	root := t.TempDir()
	got := BuildLarkCliSourceProjection(AppConfig{
		Accounts: AccountsConfig{
			App: AppCredentials{
				ID:     "cli_codex",
				Tenant: TenantFeishu,
				Secret: "plain-secret",
			},
		},
	}, ProjectionPaths{RootDir: root, Profile: "codex"})

	if got.Secrets != nil {
		t.Fatalf("Secrets = %#v, want nil", got.Secrets)
	}
}

func TestBridgeProviderNameDefaultsExecSecretProvider(t *testing.T) {
	got, ok := BridgeProviderName(map[string]any{
		"source": "exec",
		"id":     "app-cli_codex",
	})
	if !ok || got != "default" {
		t.Fatalf("BridgeProviderName = %q, %v; want default, true", got, ok)
	}

	if _, ok := BridgeProviderName(SecretRef{Source: "env", Provider: "bridge", ID: "APP_SECRET"}); ok {
		t.Fatalf("BridgeProviderName should reject non-exec secret refs")
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	if got := info.Mode() & 0o777; got != want {
		t.Fatalf("mode(%s) = %#o, want %#o", path, got, want)
	}
}
