package larkcli

import (
	"path/filepath"
	"testing"
)

func TestBuildLarkChannelEnvUsesSourceConfigBeforeRootConfig(t *testing.T) {
	root := t.TempDir()
	sourceConfig := filepath.Join(root, "profiles", "codex-dev", "lark-cli-source", "config.json")

	got := BuildLarkChannelEnv(EnvContext{
		Profile:                 " codex-dev ",
		RootDir:                 root,
		ConfigPath:              filepath.Join(root, "custom-config.json"),
		LarkCliSourceConfigFile: sourceConfig,
		LarkCliConfigDir:        "  ",
	})

	if got["LARK_CHANNEL"] != "1" {
		t.Fatalf("LARK_CHANNEL = %q, want 1", got["LARK_CHANNEL"])
	}
	if got["LARK_CHANNEL_PROFILE"] != " codex-dev " {
		t.Fatalf("LARK_CHANNEL_PROFILE = %q", got["LARK_CHANNEL_PROFILE"])
	}
	if got["LARK_CHANNEL_HOME"] != root {
		t.Fatalf("LARK_CHANNEL_HOME = %q, want %q", got["LARK_CHANNEL_HOME"], root)
	}
	if got["LARK_CHANNEL_CONFIG"] != sourceConfig {
		t.Fatalf("LARK_CHANNEL_CONFIG = %q, want %q", got["LARK_CHANNEL_CONFIG"], sourceConfig)
	}
	if _, ok := got["LARKSUITE_CLI_CONFIG_DIR"]; ok {
		t.Fatalf("LARKSUITE_CLI_CONFIG_DIR should be omitted for blank input")
	}
}

func TestBuildLarkChannelEnvFallsBackToRootConfigAndPrivateCliDir(t *testing.T) {
	root := t.TempDir()
	cliDir := filepath.Join(root, "profiles", "codex", "lark-cli")

	got := BuildLarkChannelEnv(EnvContext{
		RootDir:          root,
		LarkCliConfigDir: cliDir,
	})

	if got["LARK_CHANNEL_CONFIG"] != filepath.Join(root, "config.json") {
		t.Fatalf("LARK_CHANNEL_CONFIG = %q", got["LARK_CHANNEL_CONFIG"])
	}
	if got["LARKSUITE_CLI_CONFIG_DIR"] != cliDir {
		t.Fatalf("LARKSUITE_CLI_CONFIG_DIR = %q, want %q", got["LARKSUITE_CLI_CONFIG_DIR"], cliDir)
	}
	if _, ok := got["LARK_CHANNEL_PROFILE"]; ok {
		t.Fatalf("LARK_CHANNEL_PROFILE should be omitted without a profile")
	}
}

func TestBuildLarkChannelEnvMinimal(t *testing.T) {
	got := BuildLarkChannelEnv(EnvContext{})

	if len(got) != 1 || got["LARK_CHANNEL"] != "1" {
		t.Fatalf("minimal env = %#v, want only LARK_CHANNEL=1", got)
	}
}
