package apppaths

import (
	"path/filepath"
	"testing"
)

func TestResolveUsesEnvRootAndDefaultProfile(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	t.Setenv("LARK_CHANNEL_HOME", root)

	paths, err := Resolve(Options{})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if paths.RootDir != root {
		t.Fatalf("RootDir = %q, want %q", paths.RootDir, root)
	}
	if paths.Profile != "claude" {
		t.Fatalf("Profile = %q, want claude", paths.Profile)
	}
	if got, want := paths.ProfileDir, filepath.Join(root, "profiles", "claude"); got != want {
		t.Fatalf("ProfileDir = %q, want %q", got, want)
	}
	if got, want := paths.DefaultWorkspaceDir, filepath.Join(root+"-workspaces", "claude", "default"); got != want {
		t.Fatalf("DefaultWorkspaceDir = %q, want %q", got, want)
	}
	if got, want := paths.LarkCliTargetConfigFile, filepath.Join(root, "profiles", "claude", "lark-cli", "lark-channel", "config.json"); got != want {
		t.Fatalf("LarkCliTargetConfigFile = %q, want %q", got, want)
	}
}

func TestResolveUsesExplicitRootAndProfile(t *testing.T) {
	root := filepath.Join(t.TempDir(), "custom")
	paths, err := Resolve(Options{RootDir: root, Profile: "codex-dev"})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if got, want := paths.SessionsFile, filepath.Join(root, "profiles", "codex-dev", "sessions.json"); got != want {
		t.Fatalf("SessionsFile = %q, want %q", got, want)
	}
	if got, want := paths.ProfileLockFile, filepath.Join(root, "registry", "locks", "profile", "codex-dev.lock"); got != want {
		t.Fatalf("ProfileLockFile = %q, want %q", got, want)
	}
}

func TestResolveRejectsInvalidProfiles(t *testing.T) {
	for _, profile := range []string{" ", ".", "..", "bad/profile", "bad profile"} {
		t.Run(profile, func(t *testing.T) {
			if _, err := Resolve(Options{RootDir: t.TempDir(), Profile: profile}); err == nil {
				t.Fatalf("Resolve(%q) error = nil, want error", profile)
			}
		})
	}
}

func TestAppLockFileSanitizesAppID(t *testing.T) {
	paths, err := Resolve(Options{RootDir: t.TempDir(), Profile: "codex"})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	got := paths.AppLockFile("cli/a:b")
	want := filepath.Join(paths.UserLockDir, "app", "cli_a_b.lock")
	if got != want {
		t.Fatalf("AppLockFile = %q, want %q", got, want)
	}
}
