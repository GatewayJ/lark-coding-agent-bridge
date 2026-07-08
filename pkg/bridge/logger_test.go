package bridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestJSONLLoggerWritesStructuredFileAndSanitizes(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 25, 10, 30, 0, 0, time.UTC)
	logger := NewJSONLLogger(JSONLLoggerOptions{
		Dir: dir,
		Now: func() time.Time { return now },
	})

	logger.Info("bridge.started", map[string]any{
		"mode":      "lark",
		"appSecret": "plain-secret",
		"cwd":       "/Users/example/private-repo",
	})

	data, err := os.ReadFile(filepath.Join(dir, "bridge-20260525.jsonl"))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &entry); err != nil {
		t.Fatalf("log line is not JSON: %v\n%s", err, data)
	}
	if entry["level"] != "info" || entry["phase"] != "bridge" || entry["event"] != "started" {
		t.Fatalf("entry envelope = %#v", entry)
	}
	if entry["mode"] != "lark" {
		t.Fatalf("mode = %#v", entry["mode"])
	}
	if entry["appSecret"] != "[REDACTED]" {
		t.Fatalf("appSecret not redacted: %#v", entry["appSecret"])
	}
	if entry["cwd"] != "[REDACTED_PATH]" {
		t.Fatalf("cwd not redacted: %#v", entry["cwd"])
	}
}

func TestJSONLLoggerGCRemovesOldBridgeLogs(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bridge-20260501.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write old log: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bridge-20260525.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write recent log: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "other.log"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write other log: %v", err)
	}
	logger := NewJSONLLogger(JSONLLoggerOptions{
		Dir:           dir,
		RetentionDays: 7,
		Now:           func() time.Time { return time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC) },
	})

	removed, err := logger.GC()
	if err != nil {
		t.Fatalf("GC returned error: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if _, err := os.Stat(filepath.Join(dir, "bridge-20260501.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("old log still exists or stat failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "bridge-20260525.jsonl")); err != nil {
		t.Fatalf("recent log missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "other.log")); err != nil {
		t.Fatalf("non-bridge log missing: %v", err)
	}
}

func TestJSONLLoggerEmitsTelemetryEvents(t *testing.T) {
	adapter := &recordingTelemetry{}
	logger := NewJSONLLogger(JSONLLoggerOptions{
		Telemetry: adapter,
		Now:       func() time.Time { return time.Date(2026, 5, 25, 10, 30, 0, 0, time.UTC) },
	})

	logger.Warn("lark-cli.preflight-failed", map[string]any{"appId": "cli_123456789"})
	logger.Error("agent.failed", map[string]any{"token": "plain-token"})

	if len(adapter.events) != 2 {
		t.Fatalf("events = %#v, want 2", adapter.events)
	}
	if adapter.events[0].Level != "warn" || adapter.events[0].Phase != "lark-cli" || adapter.events[0].Event != "preflight-failed" {
		t.Fatalf("warn telemetry = %#v", adapter.events[0])
	}
	if adapter.events[0].Fields["appId"] != "...456789" {
		t.Fatalf("appId not telemetry-redacted: %#v", adapter.events[0].Fields["appId"])
	}
	if len(adapter.errors) != 1 || adapter.errors[0].Error() != "agent.failed" {
		t.Fatalf("errors = %#v, want agent.failed", adapter.errors)
	}
	if adapter.errorFields[0]["token"] != "[REDACTED]" {
		t.Fatalf("error token not redacted: %#v", adapter.errorFields[0]["token"])
	}
}

func TestJSONLLoggerPreservesUnderscoreEventNames(t *testing.T) {
	adapter := &recordingTelemetry{}
	logger := NewJSONLLogger(JSONLLoggerOptions{Telemetry: adapter})

	logger.Warn("jsonl.unknown_event", map[string]any{"eventType": "future"})

	if len(adapter.events) != 1 {
		t.Fatalf("events = %#v, want one event", adapter.events)
	}
	if adapter.events[0].Name != "jsonl.unknown_event" || adapter.events[0].Phase != "jsonl" || adapter.events[0].Event != "unknown_event" {
		t.Fatalf("telemetry event = %#v", adapter.events[0])
	}
}
