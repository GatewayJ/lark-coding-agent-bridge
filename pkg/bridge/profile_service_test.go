package bridge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/configstore"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/larkcli"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/secretstore"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/compat/apppaths"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/permissions"
)

func TestStartProfileServiceMaterializesEnvSecret(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	paths, err := apppaths.Resolve(apppaths.Options{RootDir: root, Profile: "codex"})
	if err != nil {
		t.Fatalf("resolve paths: %v", err)
	}
	t.Setenv("PROFILE_SERVICE_SECRET", "super-secret")
	writeProfileServiceConfig(t, paths, "${PROFILE_SERVICE_SECRET}")

	adapter := &testServiceAdapter{}
	result, err := StartProfileService(ctx, ProfileServiceOptions{
		Home:             root,
		Profile:          "codex",
		Adapter:          adapter,
		SkipCheckLarkCLI: true,
		WaitTimeout:      10 * time.Millisecond,
		ProcessLister: ServiceProcessListerFunc(func(context.Context) ([]ServiceProcessEntry, error) {
			if !adapter.started {
				return nil, nil
			}
			return []ServiceProcessEntry{{
				ID:          "proc",
				PID:         321,
				AppID:       "cli_profile_service",
				Tenant:      "feishu",
				ProfileName: "codex",
				AgentKind:   "codex",
				BotName:     "Profile Service Bot",
			}}, nil
		}),
	})
	if err != nil {
		t.Fatalf("StartProfileService returned error: %v", err)
	}
	if result.Process == nil || result.Process.BotName != "Profile Service Bot" {
		t.Fatalf("result = %#v, want connected profile service process", result)
	}

	snapshot, err := configstore.Load(paths.ConfigFile, configstore.LoadOptions{Profile: "codex"})
	if err != nil {
		t.Fatalf("load materialized config: %v", err)
	}
	cfg := larkcli.AppConfig{Accounts: snapshot.Runtime.Accounts, Secrets: snapshot.Runtime.Secrets}
	secretCfg, err := toProfileServiceSecretStoreAppConfig(cfg)
	if err != nil {
		t.Fatalf("convert secret config: %v", err)
	}
	plaintext, err := secretstore.ResolveAppSecret(ctx, secretCfg, secretstore.ResolverOptions{
		Paths: secretstore.KeystorePaths{
			SecretsFile:         paths.SecretsFile,
			KeystoreSaltFile:    paths.KeystoreSaltFile,
			SecretsGetterScript: paths.SecretsGetterScript,
		},
	})
	if err != nil {
		t.Fatalf("resolve materialized secret: %v", err)
	}
	if plaintext != "super-secret" {
		t.Fatalf("plaintext = %q, want materialized env secret", plaintext)
	}
}

func TestStartProfileServiceUsesConfiguredExecutableForSecretsGetter(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	paths, err := apppaths.Resolve(apppaths.Options{RootDir: root, Profile: "codex"})
	if err != nil {
		t.Fatalf("resolve paths: %v", err)
	}
	t.Setenv("PROFILE_SERVICE_SECRET", "super-secret")
	writeProfileServiceConfig(t, paths, "${PROFILE_SERVICE_SECRET}")

	adapter := &testServiceAdapter{}
	executable := "/opt/lark-channel-bridge/bin/lark-channel-bridge"
	_, err = StartProfileService(ctx, ProfileServiceOptions{
		Home:       root,
		Profile:    "codex",
		Executable: executable,
		Adapter:    adapter,
		LarkCLIRunner: LarkCLIPreflightRunnerFunc(func(context.Context, LarkCLICommandInvocation) (LarkCLIPreflightCommandResult, error) {
			return LarkCLIPreflightCommandResult{ExitCode: 0}, nil
		}),
		WaitTimeout: 10 * time.Millisecond,
		ProcessLister: ServiceProcessListerFunc(func(context.Context) ([]ServiceProcessEntry, error) {
			if !adapter.started {
				return nil, nil
			}
			return []ServiceProcessEntry{{ID: "proc", PID: 321, AppID: "cli_profile_service", Tenant: "feishu", ProfileName: "codex", AgentKind: "codex"}}, nil
		}),
	})
	if err != nil {
		t.Fatalf("StartProfileService returned error: %v", err)
	}
	wrapper, err := os.ReadFile(larkcli.SecretsGetterWrapperPath(paths.SecretsGetterScript))
	if err != nil {
		t.Fatalf("read secrets getter wrapper: %v", err)
	}
	if !strings.Contains(string(wrapper), executable) {
		t.Fatalf("wrapper = %s, want executable %q", wrapper, executable)
	}
}

func writeProfileServiceConfig(t *testing.T, paths apppaths.Paths, secret any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(paths.ConfigFile), 0o700); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	root := configstore.RootConfig{
		SchemaVersion: 2,
		ActiveProfile: "codex",
		Preferences:   map[string]any{},
		Profiles: map[string]configstore.ProfileConfig{
			"codex": {
				SchemaVersion: 2,
				AgentKind:     configstore.AgentCodex,
				Accounts: larkcli.AccountsConfig{App: larkcli.AppCredentials{
					ID:     "cli_profile_service",
					Secret: secret,
					Tenant: larkcli.TenantFeishu,
				}},
				Preferences: map[string]any{},
				Workspaces:  configstore.Workspaces{Default: paths.DefaultWorkspaceDir},
				Permissions: permissionsFullAccess(),
				Codex: &configstore.CodexConfig{
					BinaryPath:       "codex",
					InheritCodexHome: true,
					IgnoreRules:      true,
				},
				Attachments: configstore.DefaultAttachmentConfig(),
				Comments:    map[string]any{},
				LarkCli:     configstore.LarkCliConfig{IdentityPreset: configstore.LarkCliIdentityBotOnly},
			},
		},
	}
	if err := configstore.SaveRoot(paths.ConfigFile, root); err != nil {
		t.Fatalf("save config: %v", err)
	}
	if err := configstore.WriteActiveProfile(paths.RootDir, "codex"); err != nil {
		t.Fatalf("write active profile: %v", err)
	}
}

func permissionsFullAccess() permissions.PermissionConfig {
	return permissions.PermissionConfig{
		DefaultAccess: permissions.AccessFull,
		MaxAccess:     permissions.AccessFull,
	}
}
