package session

import "testing"

func TestStoreResumeForRequiresMatchingCWDAndClearPreservesTimeout(t *testing.T) {
	store := NewStore("")
	store.SetIdleTimeoutMinutes("scope-1", 500)
	store.Set("scope-1", "session-1", "/repo")

	if got := store.ResumeFor("scope-1", "/repo"); got != "session-1" {
		t.Fatalf("ResumeFor matching cwd = %q, want session-1", got)
	}
	if got := store.ResumeFor("scope-1", "/other"); got != "" {
		t.Fatalf("ResumeFor stale cwd = %q, want empty", got)
	}
	store.Clear("scope-1")
	if _, ok := store.GetRaw("scope-1"); !ok {
		t.Fatalf("Clear removed idle timeout state")
	}
	if minutes, ok := store.GetIdleTimeoutMinutes("scope-1"); !ok || minutes != 120 {
		t.Fatalf("idle timeout = %d, %v; want clamped 120", minutes, ok)
	}
}
