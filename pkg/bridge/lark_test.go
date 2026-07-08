package bridge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLarkFacadeStartsFakeTransportAndProjectsLarkCLI(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	sourceDir := filepath.Join(root, "profiles", "codex", "lark-cli-source")
	sourceConfig := filepath.Join(sourceDir, "config.json")
	cliDir := filepath.Join(root, "profiles", "codex", "lark-cli")

	transport := NewFakeLarkTransport(LarkBotIdentity{OpenID: "ou_bot", Name: "Codex"})
	runner := &captureLarkCliRunner{}
	hook := NewLarkCLIProjectionHook(LarkCLIProjectionHookOptions{
		Config: LarkCLIAppConfig{
			Accounts: LarkCLIAccountsConfig{
				App: LarkCLIAppCredentials{
					ID:     "cli_app",
					Secret: "secret",
					Tenant: LarkCLITenantFeishu,
				},
			},
		},
		Paths: LarkCLIProjectionPaths{
			RootDir:                 root,
			Profile:                 "codex",
			LarkCliSourceDir:        sourceDir,
			LarkCliSourceConfigFile: sourceConfig,
		},
		Env: LarkCLIEnvContext{
			Profile:          "codex",
			RootDir:          root,
			LarkCliConfigDir: cliDir,
		},
		IdentityPreset:      LarkCLIIdentityUserDefault,
		ApplyIdentityPolicy: true,
		IdentityOptions: LarkCLIIdentityPolicyOptions{
			Runner: runner,
		},
	})
	adapter, err := NewLarkAdapter(LarkAdapterOptions{
		Transport:         transport,
		ProfileProjection: hook,
	})
	if err != nil {
		t.Fatalf("NewLarkAdapter returned error: %v", err)
	}
	if err := adapter.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	result := adapter.ProjectionResult()
	if result.BotIdentity.OpenID != "ou_bot" || result.LarkCliSourceConfigFile != sourceConfig {
		t.Fatalf("projection result = %#v", result)
	}
	if !result.IdentityPolicyApplied {
		t.Fatalf("identity policy was not applied")
	}
	if len(runner.invocations) != 2 ||
		strings.Join(runner.invocations[0].Args, " ") != "config strict-mode off" ||
		strings.Join(runner.invocations[1].Args, " ") != "config default-as auto" {
		t.Fatalf("identity invocations = %#v", runner.invocations)
	}
	if result.LarkChannelEnv["LARK_CHANNEL"] != "1" ||
		result.LarkChannelEnv["LARK_CHANNEL_PROFILE"] != "codex" ||
		result.LarkChannelEnv["LARK_CHANNEL_CONFIG"] != sourceConfig ||
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

func TestOAPILarkTransportZeroValueReturnsNilTransportErrors(t *testing.T) {
	var transport *OAPILarkTransport
	ctx := context.Background()
	if _, err := transport.SendMessage(ctx, LarkSendMessageRequest{}); !errors.Is(err, ErrNilLarkTransport) {
		t.Fatalf("SendMessage error = %v, want ErrNilLarkTransport", err)
	}
	if _, err := transport.SendCard(ctx, LarkSendCardRequest{}); !errors.Is(err, ErrNilLarkTransport) {
		t.Fatalf("SendCard error = %v, want ErrNilLarkTransport", err)
	}
	if err := transport.UpdateCard(ctx, LarkUpdateCardRequest{}); !errors.Is(err, ErrNilLarkTransport) {
		t.Fatalf("UpdateCard error = %v, want ErrNilLarkTransport", err)
	}
	if _, err := transport.CreateCard(ctx, nil); !errors.Is(err, ErrNilLarkTransport) {
		t.Fatalf("CreateCard error = %v, want ErrNilLarkTransport", err)
	}
	if _, _, err := transport.ResolveLarkQuote(ctx, LarkQuoteTarget{}); !errors.Is(err, ErrNilLarkTransport) {
		t.Fatalf("ResolveLarkQuote error = %v, want ErrNilLarkTransport", err)
	}
	if _, err := transport.DownloadResource(ctx, MediaDownloadRequest{}); !errors.Is(err, ErrNilLarkTransport) {
		t.Fatalf("DownloadResource error = %v, want ErrNilLarkTransport", err)
	}
}

type captureLarkCliRunner struct {
	invocations []LarkCLICommandInvocation
}

func (r *captureLarkCliRunner) RunLarkCliCommand(_ context.Context, invocation LarkCLICommandInvocation) error {
	r.invocations = append(r.invocations, invocation)
	return nil
}
