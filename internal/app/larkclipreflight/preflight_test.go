package larkclipreflight

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/configstore"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/larkcli"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/compat/apppaths"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/permissions"
)

func TestRunImportsSameAppLocalUsersIntoPrivateTarget(t *testing.T) {
	root := t.TempDir()
	paths, err := apppaths.Resolve(apppaths.Options{RootDir: root, Profile: "codex"})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	profile := testPreflightProfileConfig()
	if err := configstore.SaveRoot(paths.ConfigFile, configstore.RootConfig{
		SchemaVersion: 2,
		ActiveProfile: paths.Profile,
		Preferences:   map[string]any{},
		Profiles: map[string]configstore.ProfileConfig{
			paths.Profile: profile,
		},
	}); err != nil {
		t.Fatalf("SaveRoot returned error: %v", err)
	}

	localConfig := filepath.Join(root, "local-lark-cli-config.json")
	users := []map[string]any{
		{
			"userOpenId": "ou-user",
			"userName":   "User Name",
			"tokenRef": map[string]any{
				"source": "keychain",
				"id":     "user-token",
			},
		},
	}
	writeJSONFile(t, localConfig, map[string]any{
		"apps": []map[string]any{
			{
				"appId": "cli_codex",
				"brand": "feishu",
				"users": users,
			},
		},
	})

	runner := &preflightTestRunner{
		t:            t,
		localConfig:  localConfig,
		targetConfig: filepath.Join(paths.LarkCliConfigDir, "lark-channel", "config.json"),
	}
	result, err := Run(context.Background(), Options{
		Config: larkcli.AppConfig{
			Accounts: larkcli.AccountsConfig{
				App: larkcli.AppCredentials{
					ID:     "cli_codex",
					Secret: "plain",
					Tenant: larkcli.TenantFeishu,
				},
			},
		},
		ProjectionPaths: larkcli.ProjectionPaths{
			RootDir:                 paths.RootDir,
			Profile:                 paths.Profile,
			LarkCliSourceDir:        paths.LarkCliSourceDir,
			LarkCliSourceConfigFile: paths.LarkCliSourceConfigFile,
			SecretsGetterScript:     paths.SecretsGetterScript,
			SecretsGetterCommand:    "/bridge/bin",
		},
		Env: larkcli.EnvContext{
			Profile:                 paths.Profile,
			RootDir:                 paths.RootDir,
			ConfigPath:              paths.ConfigFile,
			LarkCliConfigDir:        paths.LarkCliConfigDir,
			LarkCliSourceConfigFile: paths.LarkCliSourceConfigFile,
		},
		IdentityPreset: larkcli.IdentityUserDefault,
		ProfileConfig:  &profile,
		Runner:         runner,
		BaseEnv:        map[string]string{},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !result.LocalUserImported || result.IdentityPreset != larkcli.IdentityUserDefault || result.LocalUserStatus != configstore.LarkCliUserImportImported {
		t.Fatalf("result = %+v, want imported user-default", result)
	}

	gotCalls := runner.callArgs()
	wantCalls := []string{
		"config show",
		"config bind --source lark-channel --identity bot-only",
		"config strict-mode off",
		"config default-as auto",
		"config show",
	}
	if !reflect.DeepEqual(gotCalls, wantCalls) {
		t.Fatalf("calls = %#v, want %#v", gotCalls, wantCalls)
	}
	if _, ok := runner.calls[0].Env["LARKSUITE_CLI_CONFIG_DIR"]; ok {
		t.Fatalf("local detection call unexpectedly used private config dir")
	}
	if got := runner.calls[1].Env["LARKSUITE_CLI_CONFIG_DIR"]; got != paths.LarkCliConfigDir {
		t.Fatalf("bind config dir = %q, want %q", got, paths.LarkCliConfigDir)
	}

	var privateTarget struct {
		Apps []map[string]any `json:"apps"`
	}
	readJSONFile(t, runner.targetConfig, &privateTarget)
	gotUsers := privateTarget.Apps[0]["users"]
	if !reflect.DeepEqual(gotUsers, jsonRoundTrip(t, users)) {
		t.Fatalf("private users = %#v, want %#v", gotUsers, users)
	}

	saved, err := configstore.Load(paths.ConfigFile, configstore.LoadOptions{Profile: paths.Profile})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	gotLarkCli := saved.Root.Profiles[paths.Profile].LarkCli
	if gotLarkCli.IdentityPreset != configstore.LarkCliIdentityUserDefault {
		t.Fatalf("persisted identity = %q, want user-default", gotLarkCli.IdentityPreset)
	}
	if gotLarkCli.LocalUserImport == nil || gotLarkCli.LocalUserImport.Status != configstore.LarkCliUserImportImported || gotLarkCli.LocalUserImport.Reason != "same-app-local-user" {
		t.Fatalf("persisted localUserImport = %+v, want imported same-app-local-user", gotLarkCli.LocalUserImport)
	}
}

func TestRunKeepsManualBotOnlyForExistingPrivateUserDefaultTarget(t *testing.T) {
	root := t.TempDir()
	paths, err := apppaths.Resolve(apppaths.Options{RootDir: root, Profile: "codex"})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	profile := testPreflightProfileConfig()
	profile.LarkCli = configstore.LarkCliConfig{
		IdentityPreset: configstore.LarkCliIdentityBotOnly,
		LocalUserImport: &configstore.LarkCliLocalUserImport{
			Status: configstore.LarkCliUserImportNotNeeded,
			Reason: "manual-bot-only",
		},
	}
	if err := configstore.SaveRoot(paths.ConfigFile, configstore.RootConfig{
		SchemaVersion: 2,
		ActiveProfile: paths.Profile,
		Preferences:   map[string]any{},
		Profiles: map[string]configstore.ProfileConfig{
			paths.Profile: profile,
		},
	}); err != nil {
		t.Fatalf("SaveRoot returned error: %v", err)
	}
	targetConfig := filepath.Join(paths.LarkCliConfigDir, "lark-channel", "config.json")
	writeJSONFile(t, targetConfig, map[string]any{
		"apps": []map[string]any{
			{
				"appId":      "cli_codex",
				"brand":      "feishu",
				"defaultAs":  "auto",
				"strictMode": "off",
				"users": []map[string]any{
					{
						"userOpenId": "ou-user",
						"tokenRef":   map[string]any{"source": "keychain", "id": "token"},
					},
				},
			},
		},
	})
	runner := &preflightTestRunner{
		t:            t,
		localConfig:  filepath.Join(root, "local-lark-cli-config.json"),
		targetConfig: targetConfig,
	}

	result, err := Run(context.Background(), Options{
		Config: larkcli.AppConfig{
			Accounts: larkcli.AccountsConfig{
				App: larkcli.AppCredentials{
					ID:     "cli_codex",
					Secret: "plain",
					Tenant: larkcli.TenantFeishu,
				},
			},
		},
		ProjectionPaths: larkcli.ProjectionPaths{
			RootDir:                 paths.RootDir,
			Profile:                 paths.Profile,
			LarkCliSourceDir:        paths.LarkCliSourceDir,
			LarkCliSourceConfigFile: paths.LarkCliSourceConfigFile,
			SecretsGetterScript:     paths.SecretsGetterScript,
			SecretsGetterCommand:    "/bridge/bin",
		},
		Env: larkcli.EnvContext{
			Profile:                 paths.Profile,
			RootDir:                 paths.RootDir,
			ConfigPath:              paths.ConfigFile,
			LarkCliConfigDir:        paths.LarkCliConfigDir,
			LarkCliSourceConfigFile: paths.LarkCliSourceConfigFile,
		},
		IdentityPreset: larkcli.IdentityBotOnly,
		ProfileConfig:  &profile,
		Runner:         runner,
		BaseEnv:        map[string]string{},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.IdentityPreset != larkcli.IdentityBotOnly || result.LocalUserReason != "manual-bot-only" {
		t.Fatalf("result = %+v, want manual bot-only", result)
	}
	wantCalls := []string{
		"config strict-mode bot",
		"config default-as bot",
	}
	if !reflect.DeepEqual(runner.callArgs(), wantCalls) {
		t.Fatalf("calls = %#v, want %#v", runner.callArgs(), wantCalls)
	}
	saved, err := configstore.Load(paths.ConfigFile, configstore.LoadOptions{Profile: paths.Profile})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	got := saved.Root.Profiles[paths.Profile].LarkCli
	if got.IdentityPreset != configstore.LarkCliIdentityBotOnly || got.LocalUserImport == nil || got.LocalUserImport.Reason != "manual-bot-only" {
		t.Fatalf("persisted larkCli = %+v, want manual bot-only", got)
	}
}

func TestRunDoesNotUseLegacyOverlayForUnsupportedSource(t *testing.T) {
	root := t.TempDir()
	paths, err := apppaths.Resolve(apppaths.Options{RootDir: root, Profile: "codex"})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	profile := testPreflightProfileConfig()
	if err := configstore.SaveRoot(paths.ConfigFile, configstore.RootConfig{
		SchemaVersion: 2,
		ActiveProfile: paths.Profile,
		Preferences:   map[string]any{},
		Profiles: map[string]configstore.ProfileConfig{
			paths.Profile: profile,
		},
	}); err != nil {
		t.Fatalf("SaveRoot returned error: %v", err)
	}
	runner := &bindFailureRunner{output: `invalid --source "lark-channel"; valid values: env, file`}

	result, err := Run(context.Background(), Options{
		Config: larkcli.AppConfig{
			Accounts: larkcli.AccountsConfig{
				App: larkcli.AppCredentials{
					ID:     "cli_codex",
					Secret: "plain",
					Tenant: larkcli.TenantFeishu,
				},
			},
		},
		ProjectionPaths: larkcli.ProjectionPaths{
			RootDir:                 paths.RootDir,
			Profile:                 paths.Profile,
			LarkCliSourceDir:        paths.LarkCliSourceDir,
			LarkCliSourceConfigFile: paths.LarkCliSourceConfigFile,
			SecretsGetterScript:     paths.SecretsGetterScript,
			SecretsGetterCommand:    "/bridge/bin",
		},
		Env: larkcli.EnvContext{
			Profile:                 paths.Profile,
			RootDir:                 paths.RootDir,
			ConfigPath:              paths.ConfigFile,
			LarkCliConfigDir:        paths.LarkCliConfigDir,
			LarkCliSourceConfigFile: paths.LarkCliSourceConfigFile,
		},
		Runner:  runner,
		BaseEnv: map[string]string{},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.IdentityPreset != larkcli.IdentityBotOnly || result.LocalUserReason != "bind-failed" || !result.BindFailed || !strings.Contains(result.BindDiagnostic, "invalid --source") {
		t.Fatalf("result = %+v, want bind failure diagnostic", result)
	}
	gotCalls := runner.callArgs()
	wantCalls := []string{
		"config show",
		"config bind --source lark-channel --identity bot-only",
	}
	if !reflect.DeepEqual(gotCalls, wantCalls) {
		t.Fatalf("calls = %#v, want %#v", gotCalls, wantCalls)
	}
}

type preflightTestRunner struct {
	t            *testing.T
	localConfig  string
	targetConfig string
	calls        []larkcli.CommandInvocation
}

func (r *preflightTestRunner) RunLarkCLICommand(ctx context.Context, invocation larkcli.CommandInvocation) (CommandResult, error) {
	r.calls = append(r.calls, copyInvocation(invocation))
	args := strings.Join(invocation.Args, " ")
	switch args {
	case "config show":
		if invocation.Env["LARKSUITE_CLI_CONFIG_DIR"] == "" {
			return CommandResult{Stdout: "Config file path: " + r.localConfig + "\n" + `{"appId":"cli_codex","brand":"feishu","users":"User Name (ou-user)"}` + "\n"}, nil
		}
		return CommandResult{Stdout: `{"appId":"cli_codex","brand":"feishu","users":"User Name (ou-user)"}` + "\n"}, nil
	case "config bind --source lark-channel --identity bot-only":
		writeJSONFile(r.t, r.targetConfig, map[string]any{
			"apps": []map[string]any{
				{
					"appId":      "cli_codex",
					"brand":      "feishu",
					"defaultAs":  "bot",
					"strictMode": "bot",
					"users":      nil,
				},
			},
		})
		return CommandResult{}, nil
	case "config strict-mode off", "config default-as auto", "config strict-mode bot", "config default-as bot":
		return CommandResult{}, nil
	default:
		r.t.Fatalf("unexpected lark-cli args: %q", args)
		return CommandResult{ExitCode: 1}, nil
	}
}

type bindFailureRunner struct {
	output string
	calls  []larkcli.CommandInvocation
}

func (r *bindFailureRunner) RunLarkCLICommand(ctx context.Context, invocation larkcli.CommandInvocation) (CommandResult, error) {
	r.calls = append(r.calls, copyInvocation(invocation))
	args := strings.Join(invocation.Args, " ")
	switch args {
	case "config show":
		return CommandResult{ExitCode: 1}, nil
	case "config bind --source lark-channel --identity bot-only":
		return CommandResult{Stderr: r.output, ExitCode: 2}, nil
	default:
		return CommandResult{}, nil
	}
}

func (r *bindFailureRunner) callArgs() []string {
	out := make([]string, 0, len(r.calls))
	for _, call := range r.calls {
		out = append(out, strings.Join(call.Args, " "))
	}
	return out
}

func (r *preflightTestRunner) callArgs() []string {
	out := make([]string, 0, len(r.calls))
	for _, call := range r.calls {
		out = append(out, strings.Join(call.Args, " "))
	}
	return out
}

func testPreflightProfileConfig() configstore.ProfileConfig {
	return configstore.ProfileConfig{
		SchemaVersion: 2,
		AgentKind:     configstore.AgentCodex,
		Accounts: larkcli.AccountsConfig{
			App: larkcli.AppCredentials{
				ID:     "cli_codex",
				Secret: "plain",
				Tenant: larkcli.TenantFeishu,
			},
		},
		Preferences: map[string]any{},
		Access:      configstore.ProfileAccess{},
		Workspaces:  configstore.Workspaces{},
		Permissions: permissions.PermissionConfig{
			DefaultAccess: permissions.AccessFull,
			MaxAccess:     permissions.AccessFull,
		},
		Codex: &configstore.CodexConfig{
			BinaryPath:       "codex",
			InheritCodexHome: true,
			IgnoreRules:      true,
		},
		Attachments: configstore.DefaultAttachmentConfig(),
		Comments:    map[string]any{},
		LarkCli: configstore.LarkCliConfig{
			IdentityPreset: configstore.LarkCliIdentityBotOnly,
		},
	}
}

func copyInvocation(invocation larkcli.CommandInvocation) larkcli.CommandInvocation {
	args := append([]string(nil), invocation.Args...)
	env := make(map[string]string, len(invocation.Env))
	for key, value := range invocation.Env {
		env[key] = value
	}
	return larkcli.CommandInvocation{
		Command: invocation.Command,
		Args:    args,
		Env:     env,
	}
}

func writeJSONFile(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent returned error: %v", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
}

func readJSONFile(t *testing.T, path string, target any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
}

func jsonRoundTrip(t *testing.T, value any) any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	return out
}
