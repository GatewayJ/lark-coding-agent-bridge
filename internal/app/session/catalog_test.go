package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/capability"
)

func TestCatalogActiveForRequiresValidAgentEntry(t *testing.T) {
	catalog := NewCatalog("")
	identity := CatalogIdentity{
		ScopeID:           "scope-1",
		AgentID:           capability.IDCodex,
		CWDRealpath:       "/repo",
		PolicyFingerprint: "fp",
	}

	if _, err := catalog.UpsertActive(UpsertCatalogInput{
		CatalogIdentity: identity,
		Now:             time.UnixMilli(1000),
		ThreadID:        "thread-1",
	}); err != nil {
		t.Fatalf("UpsertActive returned error: %v", err)
	}
	entry, ok := catalog.ActiveFor(identity)
	if !ok || entry.ThreadID != "thread-1" {
		t.Fatalf("ActiveFor = %#v, %v; want codex thread", entry, ok)
	}

	if _, err := catalog.UpsertActive(UpsertCatalogInput{
		CatalogIdentity: identity,
		SessionID:       "bad-claude-session",
	}); err == nil {
		t.Fatalf("UpsertActive accepted invalid codex identity")
	}
}

func TestCatalogPersistsAndLoads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.catalog.json")
	catalog := NewCatalog(path)
	identity := CatalogIdentity{
		ScopeID:           "scope-1",
		AgentID:           capability.IDClaude,
		CWDRealpath:       "/repo",
		PolicyFingerprint: "fp",
	}
	if _, err := catalog.UpsertActive(UpsertCatalogInput{
		CatalogIdentity: identity,
		Now:             time.UnixMilli(1000),
		SessionID:       "session-1",
	}); err != nil {
		t.Fatalf("UpsertActive returned error: %v", err)
	}

	loaded := NewCatalog(path)
	if err := loaded.Load(); err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	entry, ok := loaded.ActiveFor(identity)
	if !ok || entry.SessionID != "session-1" {
		t.Fatalf("loaded ActiveFor = %#v, %v; want session-1", entry, ok)
	}
}

func TestCatalogLoadDamagedFileClearsAndContinues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.catalog.json")
	if err := os.WriteFile(path, []byte("{bad json"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	catalog := NewCatalog(path)
	if err := catalog.Load(); err != nil {
		t.Fatalf("Load returned error for damaged catalog: %v", err)
	}
	if entries := catalog.Entries(); len(entries) != 0 {
		t.Fatalf("Entries = %#v, want cleared damaged catalog", entries)
	}
}
