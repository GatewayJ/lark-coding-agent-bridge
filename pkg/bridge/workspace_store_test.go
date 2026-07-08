package bridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestFileWorkspaceStorePersistsJSCompatibleFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles", "codex", "workspaces.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	initial := `{
  "chats": {
    "chat-old": {
      "cwd": "/repo/old"
    }
  },
  "named": {
    "main": "/repo/main"
  }
}
`
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	store, err := NewFileWorkspaceStore(path)
	if err != nil {
		t.Fatalf("NewFileWorkspaceStore returned error: %v", err)
	}
	if got := store.CWDFor("chat-old"); got != "/repo/old" {
		t.Fatalf("CWDFor = %q, want /repo/old", got)
	}
	if got := store.GetNamed("main"); got != "/repo/main" {
		t.Fatalf("GetNamed = %q, want /repo/main", got)
	}

	if err := store.SetCWD("chat-new", "/repo/new"); err != nil {
		t.Fatalf("SetCWD returned error: %v", err)
	}
	cwds := store.ListCWDs()
	if cwds["chat-old"] != "/repo/old" || cwds["chat-new"] != "/repo/new" {
		t.Fatalf("ListCWDs = %#v", cwds)
	}
	if removed, err := store.RemoveCWD("chat-old"); err != nil || !removed {
		t.Fatalf("RemoveCWD returned removed=%v err=%v", removed, err)
	}
	if err := store.SaveNamed("feature", "/repo/feature"); err != nil {
		t.Fatalf("SaveNamed returned error: %v", err)
	}
	if removed, err := store.RemoveNamed("main"); err != nil || !removed {
		t.Fatalf("RemoveNamed returned false")
	}
	if err := store.LastError(); err != nil {
		t.Fatalf("LastError = %v", err)
	}

	reopened, err := NewFileWorkspaceStore(path)
	if err != nil {
		t.Fatalf("reopen NewFileWorkspaceStore returned error: %v", err)
	}
	if got := reopened.CWDFor("chat-new"); got != "/repo/new" {
		t.Fatalf("reopened CWDFor = %q, want /repo/new", got)
	}
	if got := reopened.CWDFor("chat-old"); got != "" {
		t.Fatalf("reopened removed CWDFor = %q, want empty", got)
	}
	if got := reopened.GetNamed("feature"); got != "/repo/feature" {
		t.Fatalf("reopened GetNamed = %q, want /repo/feature", got)
	}
	if got := reopened.GetNamed("main"); got != "" {
		t.Fatalf("reopened removed named = %q, want empty", got)
	}

	var raw struct {
		Chats map[string]struct {
			CWD string `json:"cwd"`
		} `json:"chats"`
		Named map[string]string `json:"named"`
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("persisted JSON is invalid: %v", err)
	}
	if raw.Chats["chat-new"].CWD != "/repo/new" || raw.Chats["chat-old"].CWD != "" || raw.Named["feature"] != "/repo/feature" {
		t.Fatalf("persisted JSON = %#v", raw)
	}
}

func TestFileWorkspaceStoreMissingFileStartsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile", "workspaces.json")
	store, err := NewFileWorkspaceStore(path)
	if err != nil {
		t.Fatalf("NewFileWorkspaceStore returned error: %v", err)
	}
	if got := store.CWDFor("missing"); got != "" {
		t.Fatalf("CWDFor = %q, want empty", got)
	}
	if err := store.SaveNamed("main", "/repo"); err != nil {
		t.Fatalf("SaveNamed returned error: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Stat persisted file returned error: %v", err)
	}
}

func TestFileWorkspaceStoreRejectsInvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspaces.json")
	if err := os.WriteFile(path, []byte("{bad json"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	if _, err := NewFileWorkspaceStore(path); err == nil {
		t.Fatalf("NewFileWorkspaceStore error = nil, want invalid JSON error")
	}
}

func TestFileWorkspaceStorePropagatesPersistErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspaces.json")
	store, err := NewFileWorkspaceStore(path)
	if err != nil {
		t.Fatalf("NewFileWorkspaceStore returned error: %v", err)
	}

	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("file"), 0o600); err != nil {
		t.Fatalf("WriteFile blocker returned error: %v", err)
	}
	store.path = filepath.Join(blocker, "workspaces.json")

	if err := store.SetCWD("chat-1", "/repo"); err == nil {
		t.Fatalf("SetCWD error = nil, want persist error")
	}
	if got := store.CWDFor("chat-1"); got != "" {
		t.Fatalf("CWDFor after failed SetCWD = %q, want empty", got)
	}
	if err := store.SaveNamed("main", "/repo"); err == nil {
		t.Fatalf("SaveNamed error = nil, want persist error")
	}
	if got := store.GetNamed("main"); got != "" {
		t.Fatalf("GetNamed after failed SaveNamed = %q, want empty", got)
	}

	store.path = path
	if err := store.SaveNamed("main", "/repo"); err != nil {
		t.Fatalf("SaveNamed recovery returned error: %v", err)
	}
	store.path = filepath.Join(blocker, "workspaces.json")
	removed, err := store.RemoveNamed("main")
	if err == nil {
		t.Fatalf("RemoveNamed error = nil, want persist error")
	}
	if removed {
		t.Fatalf("RemoveNamed removed = true, want false on persist error")
	}
	if got := store.GetNamed("main"); got != "/repo" {
		t.Fatalf("GetNamed after failed RemoveNamed = %q, want /repo", got)
	}
	if lastErr := store.LastError(); lastErr == nil {
		t.Fatalf("LastError = nil, want persist error")
	}
}
