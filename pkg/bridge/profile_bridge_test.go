package bridge

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/larkcli"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/compat/apppaths"
)

func TestNewProfileBridgeBuildsManagedProfileBridge(t *testing.T) {
	root := t.TempDir()
	defaultWorkspace := filepath.Join(root, "workspaces", "codex")
	writeProfileBridgeTestConfig(t, root, defaultWorkspace)

	instance, info, err := NewProfileBridge(context.Background(), ProfileBridgeOptions{
		Home:                    root,
		Profile:                 "codex",
		SkipCheckLarkCLI:        true,
		SkipAgentAvailability:   true,
		DisableDefaultLogger:    true,
		DisableDefaultTelemetry: true,
		LarkTransport:           NewFakeLarkTransport(LarkBotIdentity{OpenID: "ou_bot", Name: "Bridge Bot"}),
		SecretsGetterCommand:    "/bin/bridge",
		Version:                 "test",
	})
	if err != nil {
		t.Fatalf("NewProfileBridge returned error: %v", err)
	}
	if info.Profile != "codex" || info.AppID != "cli_profile_bridge" || info.AgentKind != RuntimeAgentCodex {
		t.Fatalf("profile info = %#v", info)
	}
	if _, err := os.Stat(defaultWorkspace); err != nil {
		t.Fatalf("default workspace was not created: %v", err)
	}

	if err := instance.Start(context.Background()); err != nil {
		t.Fatalf("Bridge.Start returned error: %v", err)
	}
	t.Cleanup(func() {
		_ = instance.Shutdown(context.Background())
	})
	status, err := instance.Status(context.Background())
	if err != nil {
		t.Fatalf("Bridge.Status returned error: %v", err)
	}
	if !status.Bridge.Started || status.Bridge.Profile != "codex" || status.Bridge.Mode != BridgeModeLark {
		t.Fatalf("bridge status = %#v", status.Bridge)
	}
	if status.Runtime == nil || status.Runtime.Entry == nil || status.Runtime.Entry.AppID != "cli_profile_bridge" {
		t.Fatalf("runtime status = %#v", status.Runtime)
	}
}

func TestNewProfileBridgeAppSecretOverrideProjectsBridgeExecSecret(t *testing.T) {
	root := t.TempDir()
	writeProfileBridgeTestConfig(t, root, filepath.Join(root, "workspaces", "codex"))

	_, _, err := NewProfileBridge(context.Background(), ProfileBridgeOptions{
		Home:                    root,
		Profile:                 "codex",
		AppSecret:               "override-secret",
		SecretsGetterCommand:    "/bin/bridge",
		SkipAgentAvailability:   true,
		DisableDefaultLogger:    true,
		DisableDefaultTelemetry: true,
		LarkTransport:           NewFakeLarkTransport(LarkBotIdentity{OpenID: "ou_bot", Name: "Bridge Bot"}),
		LarkCLIRunner: LarkCLIPreflightRunnerFunc(func(context.Context, LarkCLICommandInvocation) (LarkCLIPreflightCommandResult, error) {
			return LarkCLIPreflightCommandResult{ExitCode: 0}, nil
		}),
	})
	if err != nil {
		t.Fatalf("NewProfileBridge returned error: %v", err)
	}

	var projection struct {
		Accounts struct {
			App struct {
				Secret struct {
					Source   string `json:"source"`
					Provider string `json:"provider"`
					ID       string `json:"id"`
				} `json:"secret"`
			} `json:"app"`
		} `json:"accounts"`
	}
	data, err := os.ReadFile(filepath.Join(root, "profiles", "codex", "lark-cli-source", "config.json"))
	if err != nil {
		t.Fatalf("read lark-cli source projection: %v", err)
	}
	if err := json.Unmarshal(data, &projection); err != nil {
		t.Fatalf("unmarshal lark-cli source projection: %v", err)
	}
	secret := projection.Accounts.App.Secret
	if secret.Source != "exec" || secret.Provider != "bridge" || secret.ID != SecretKeyForApp("cli_profile_bridge") {
		t.Fatalf("projected secret = %#v", secret)
	}

	wrapper, err := os.ReadFile(larkcli.SecretsGetterWrapperPath(filepath.Join(root, "secrets-getter")))
	if err != nil {
		t.Fatalf("read secrets getter wrapper: %v", err)
	}
	if !strings.Contains(string(wrapper), "/bin/bridge") || !strings.Contains(string(wrapper), "secrets get") {
		t.Fatalf("wrapper = %s, want direct secrets getter command", wrapper)
	}
}

func TestNewProfileBridgeRequiresSecretsGetterCommandForBridgeExecSecret(t *testing.T) {
	root := t.TempDir()
	writeProfileBridgeTestConfig(t, root, filepath.Join(root, "workspaces", "codex"))

	_, _, err := NewProfileBridge(context.Background(), ProfileBridgeOptions{
		Home:                    root,
		Profile:                 "codex",
		AppSecret:               "override-secret",
		SkipCheckLarkCLI:        true,
		SkipAgentAvailability:   true,
		DisableDefaultLogger:    true,
		DisableDefaultTelemetry: true,
		LarkTransport:           NewFakeLarkTransport(LarkBotIdentity{OpenID: "ou_bot", Name: "Bridge Bot"}),
	})
	if err == nil || !strings.Contains(err.Error(), "SecretsGetterCommand") {
		t.Fatalf("NewProfileBridge error = %v, want SecretsGetterCommand validation error", err)
	}
}

func TestProfileBridgeTelemetryFromEnvIsExplicitOptIn(t *testing.T) {
	root := t.TempDir()
	paths, err := apppaths.Resolve(apppaths.Options{RootDir: root, Profile: "codex"})
	if err != nil {
		t.Fatalf("resolve paths: %v", err)
	}
	t.Setenv("LARK_CHANNEL_TELEMETRY_MODULE", filepath.Join(root, "telemetry.js"))
	appConfig := larkcli.AppConfig{Accounts: larkcli.AccountsConfig{App: larkcli.AppCredentials{
		ID:     "cli_profile_bridge",
		Secret: "profile-secret",
		Tenant: larkcli.TenantFeishu,
	}}}

	_, telemetry := profileBridgeObservability(context.Background(), ProfileBridgeOptions{
		DisableDefaultLogger: true,
	}, paths, appConfig)
	if telemetry != nil {
		t.Fatalf("profileBridgeObservability loaded telemetry from env by default")
	}
}

func writeProfileBridgeTestConfig(t *testing.T, root string, defaultWorkspace string) {
	t.Helper()
	config := map[string]any{
		"schemaVersion": 2,
		"activeProfile": "codex",
		"preferences": map[string]any{
			"messageReply":          "card",
			"showToolCalls":         true,
			"cotMessages":           "brief",
			"runIdleTimeoutMinutes": 3,
		},
		"profiles": map[string]any{
			"codex": map[string]any{
				"schemaVersion": 2,
				"agentKind":     "codex",
				"accounts": map[string]any{
					"app": map[string]any{
						"id":     "cli_profile_bridge",
						"secret": "profile-secret",
						"tenant": "feishu",
					},
				},
				"preferences": map[string]any{},
				"access": map[string]any{
					"allowedUsers":          []string{},
					"allowedChats":          []string{},
					"admins":                []string{},
					"requireMentionInGroup": true,
				},
				"workspaces": map[string]any{"default": defaultWorkspace},
				"permissions": map[string]any{
					"defaultAccess": "full",
					"maxAccess":     "full",
				},
				"codex": map[string]any{
					"binaryPath":       "codex",
					"inheritCodexHome": true,
					"ignoreRules":      true,
				},
				"attachments": map[string]any{
					"maxCount":      10,
					"maxBytes":      25000000,
					"maxFileBytes":  10000000,
					"imageMaxBytes": 5000000,
					"cacheTtlMs":    86400000,
					"cacheMaxBytes": 100000000,
				},
				"comments": map[string]any{},
				"larkCli":  map[string]any{"identityPreset": "bot-only"},
			},
		},
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		t.Fatalf("marshal test config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "config.json"), append(data, '\n'), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "active-profile"), []byte("codex\n"), 0o600); err != nil {
		t.Fatalf("write active profile: %v", err)
	}
}
