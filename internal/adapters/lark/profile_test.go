package lark

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/larkcli"
)

func TestLarkCLIProjectionHookWritesSourceProjection(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	sourceDir := filepath.Join(root, "profiles", "codex", "lark-cli-source")
	sourceConfig := filepath.Join(sourceDir, "config.json")
	cliDir := filepath.Join(root, "profiles", "codex", "lark-cli")

	hook := NewLarkCLIProjectionHook(LarkCLIProjectionHookOptions{
		Config: larkcli.AppConfig{
			Accounts: larkcli.AccountsConfig{
				App: larkcli.AppCredentials{
					ID:     "cli_app",
					Secret: "secret",
					Tenant: larkcli.TenantFeishu,
				},
			},
		},
		Paths: larkcli.ProjectionPaths{
			RootDir:                 root,
			Profile:                 "codex",
			LarkCliSourceDir:        sourceDir,
			LarkCliSourceConfigFile: sourceConfig,
		},
		Env: larkcli.EnvContext{
			Profile:          "codex",
			RootDir:          root,
			LarkCliConfigDir: cliDir,
		},
	})

	result, err := hook.ProjectLarkProfile(ctx, ProfileProjectionRequest{
		BotIdentity: BotIdentity{OpenID: "ou_bot"},
	})
	if err != nil {
		t.Fatalf("ProjectLarkProfile returned error: %v", err)
	}
	if result.BotIdentity.OpenID != "ou_bot" || result.LarkCliSourceConfigFile != sourceConfig {
		t.Fatalf("projection result = %#v", result)
	}
	if result.LarkChannelEnv["LARK_CHANNEL_CONFIG"] != sourceConfig ||
		result.LarkChannelEnv["LARKSUITE_CLI_CONFIG_DIR"] != cliDir {
		t.Fatalf("projection env = %#v", result.LarkChannelEnv)
	}
	data, err := os.ReadFile(sourceConfig)
	if err != nil {
		t.Fatalf("read projection: %v", err)
	}
	if !strings.Contains(string(data), `"id": "cli_app"`) || !strings.Contains(string(data), `"tenant": "feishu"`) {
		t.Fatalf("projection file = %s", data)
	}
}
