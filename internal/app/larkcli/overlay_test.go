package larkcli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWithLegacyLarkCliSourceOverlayRestoresOriginalConfig(t *testing.T) {
	root := t.TempDir()
	configFile := filepath.Join(root, "config.json")
	sourceConfigFile := filepath.Join(root, "profiles", "codex", "lark-cli-source", "config.json")
	original := []byte("{\n  \"schemaVersion\": 2,\n  \"activeProfile\": \"codex\",\n  \"profiles\": {}\n}\n")
	source := []byte("{\n  \"accounts\": { \"app\": { \"id\": \"cli_codex\" } }\n}\n")
	mustWriteFile(t, configFile, original)
	mustWriteFile(t, sourceConfigFile, source)

	err := WithLegacyLarkCliSourceOverlay(configFile, sourceConfigFile, func() error {
		got, err := os.ReadFile(configFile)
		if err != nil {
			t.Fatalf("read overlay config: %v", err)
		}
		if string(got) != string(source) {
			t.Fatalf("overlay config = %q, want %q", got, source)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WithLegacyLarkCliSourceOverlay returned error: %v", err)
	}

	got, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("read restored config: %v", err)
	}
	if string(got) != string(original) {
		t.Fatalf("restored config = %q, want %q", got, original)
	}
	if ok, err := HasLegacyLarkCliSourceOverlay(configFile); err != nil || ok {
		t.Fatalf("HasLegacyLarkCliSourceOverlay = %v, %v; want false, nil", ok, err)
	}
}

func TestWithLegacyLarkCliSourceOverlayRestoresWhenCallbackFails(t *testing.T) {
	root := t.TempDir()
	configFile := filepath.Join(root, "config.json")
	sourceConfigFile := filepath.Join(root, "profiles", "codex", "lark-cli-source", "config.json")
	original := []byte("{\"schemaVersion\":2}\n")
	source := []byte("{\"accounts\":{\"app\":{\"id\":\"cli_codex\"}}}\n")
	mustWriteFile(t, configFile, original)
	mustWriteFile(t, sourceConfigFile, source)
	callbackErr := errors.New("callback failed")

	err := WithLegacyLarkCliSourceOverlay(configFile, sourceConfigFile, func() error {
		return callbackErr
	})
	if !errors.Is(err, callbackErr) {
		t.Fatalf("error = %v, want callback error", err)
	}
	got, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("read restored config: %v", err)
	}
	if string(got) != string(original) {
		t.Fatalf("restored config = %q, want %q", got, original)
	}
}

func TestWithLegacyLarkCliSourceOverlayRemovesMissingOriginalConfig(t *testing.T) {
	root := t.TempDir()
	configFile := filepath.Join(root, "config.json")
	sourceConfigFile := filepath.Join(root, "profiles", "codex", "lark-cli-source", "config.json")
	source := []byte("{\"accounts\":{\"app\":{\"id\":\"cli_codex\"}}}\n")
	mustWriteFile(t, sourceConfigFile, source)

	err := WithLegacyLarkCliSourceOverlay(configFile, sourceConfigFile, func() error {
		if _, err := os.Stat(configFile); err != nil {
			t.Fatalf("overlay config should exist during callback: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WithLegacyLarkCliSourceOverlay returned error: %v", err)
	}
	if _, err := os.Stat(configFile); !os.IsNotExist(err) {
		t.Fatalf("configFile should be removed after restore, stat err = %v", err)
	}
}

func TestRecoverLegacyLarkCliSourceOverlayRestoresCrashedOverlay(t *testing.T) {
	root := t.TempDir()
	configFile := filepath.Join(root, "config.json")
	paths := LegacyLarkCliSourceOverlayPaths(configFile)
	original := []byte("{\"schemaVersion\":2,\"activeProfile\":\"codex\",\"profiles\":{}}\n")
	overlay := []byte("{\"accounts\":{\"app\":{\"id\":\"cli_codex\"}}}\n")
	mustWriteFile(t, paths.BackupFile, original)
	mustWriteFile(t, paths.MarkerFile, []byte("{\"hadConfig\":true,\"profile\":\"codex\"}\n"))
	mustWriteFile(t, configFile, overlay)

	if err := RecoverLegacyLarkCliSourceOverlay(configFile); err != nil {
		t.Fatalf("RecoverLegacyLarkCliSourceOverlay returned error: %v", err)
	}
	got, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("read recovered config: %v", err)
	}
	if string(got) != string(original) {
		t.Fatalf("recovered config = %q, want %q", got, original)
	}
	assertMissing(t, paths.BackupFile)
	assertMissing(t, paths.MarkerFile)
}

func mustWriteFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func assertMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("%s should be missing, stat err = %v", path, err)
	}
}
