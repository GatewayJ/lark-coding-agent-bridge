package bridge

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestBootstrapProfileConfigWritesLoadableProfile(t *testing.T) {
	root := t.TempDir()
	requireMention := false

	snapshot, err := BootstrapProfileConfig(BootstrapProfileOptions{
		RootDir:          root,
		Profile:          "codex",
		AgentKind:        ConfigAgentCodex,
		AppID:            "cli_test",
		AppSecret:        SecretReference(SecretRef{Source: SecretSourceEnv, ID: "LARK_APP_SECRET"}),
		Tenant:           LarkCLITenantFeishu,
		DefaultWorkspace: filepath.Join(root, "workspaces", "codex"),
		Permissions: ConfigPermissionConfig{
			DefaultAccess: AccessWorkspace,
			MaxAccess:     AccessFull,
		},
		RequireMention: &requireMention,
	})
	if err != nil {
		t.Fatalf("BootstrapProfileConfig returned error: %v", err)
	}
	if snapshot.ProfileName != "codex" {
		t.Fatalf("ProfileName = %q", snapshot.ProfileName)
	}
	if snapshot.Profile.Accounts.App.ID != "cli_test" {
		t.Fatalf("App ID = %q", snapshot.Profile.Accounts.App.ID)
	}
	if snapshot.Profile.Codex == nil || snapshot.Profile.Codex.BinaryPath != "codex" {
		t.Fatalf("Codex config = %#v", snapshot.Profile.Codex)
	}
	if snapshot.Profile.Access.RequireMentionInGroup {
		t.Fatalf("RequireMentionInGroup = true, want false")
	}
	if snapshot.Profile.Sandbox.Default != ConfigCodexSandboxWorkspaceWrite {
		t.Fatalf("sandbox default = %q", snapshot.Profile.Sandbox.Default)
	}
	active, err := os.ReadFile(filepath.Join(root, "active-profile"))
	if err != nil {
		t.Fatalf("ReadFile active-profile returned error: %v", err)
	}
	if string(active) != "codex\n" {
		t.Fatalf("active-profile = %q", string(active))
	}
	configPath := filepath.Join(root, "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile config.json returned error: %v", err)
	}
	if !bytes.Contains(data, []byte(`"source": "env"`)) || !bytes.Contains(data, []byte(`"id": "LARK_APP_SECRET"`)) {
		t.Fatalf("config.json does not contain env secret ref: %s", string(data))
	}
	if _, err := LoadConfig(configPath, ConfigLoadOptions{Profile: "codex"}); err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}
}

func TestBootstrapProfileConfigWritesPlainSecret(t *testing.T) {
	root := t.TempDir()

	snapshot, err := BootstrapProfileConfig(BootstrapProfileOptions{
		RootDir:   root,
		Profile:   "codex",
		AppID:     "cli_test",
		AppSecret: PlainSecret("plain-secret"),
	})
	if err != nil {
		t.Fatalf("BootstrapProfileConfig returned error: %v", err)
	}
	if snapshot.Profile.Accounts.App.ID != "cli_test" {
		t.Fatalf("App ID = %q", snapshot.Profile.Accounts.App.ID)
	}
	data, err := os.ReadFile(filepath.Join(root, "config.json"))
	if err != nil {
		t.Fatalf("ReadFile config.json returned error: %v", err)
	}
	if !bytes.Contains(data, []byte(`"secret": "plain-secret"`)) {
		t.Fatalf("config.json does not contain plain secret: %s", string(data))
	}
}

func TestBootstrapProfileConfigRejectsExistingConfigByDefault(t *testing.T) {
	root := t.TempDir()
	if _, err := BootstrapProfileConfig(BootstrapProfileOptions{
		RootDir:   root,
		Profile:   "codex",
		AppID:     "cli_test",
		AppSecret: PlainSecret("secret"),
	}); err != nil {
		t.Fatalf("initial BootstrapProfileConfig returned error: %v", err)
	}
	if _, err := BootstrapProfileConfig(BootstrapProfileOptions{
		RootDir:   root,
		Profile:   "codex",
		AppID:     "cli_test",
		AppSecret: PlainSecret("secret"),
	}); err == nil {
		t.Fatalf("second BootstrapProfileConfig error = nil, want existing config error")
	}
}

func TestBootstrapProfileConfigRejectsInvalidPermissionsWithoutFiles(t *testing.T) {
	root := t.TempDir()

	if _, err := BootstrapProfileConfig(BootstrapProfileOptions{
		RootDir:   root,
		Profile:   "codex",
		AppID:     "cli_test",
		AppSecret: PlainSecret("secret"),
		Permissions: ConfigPermissionConfig{
			DefaultAccess: AccessMode("invalid"),
			MaxAccess:     AccessFull,
		},
	}); err == nil {
		t.Fatalf("BootstrapProfileConfig error = nil, want invalid permissions error")
	}
	assertPathNotExists(t, filepath.Join(root, "config.json"))
	assertPathNotExists(t, filepath.Join(root, "active-profile"))
}

func assertPathNotExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err == nil {
		t.Fatalf("%s exists, want no file", path)
	} else if !os.IsNotExist(err) {
		t.Fatalf("Stat(%s) returned error: %v", path, err)
	}
}
