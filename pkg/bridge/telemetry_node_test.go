package bridge

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadTelemetryAdapterLoadsJavaScriptModule(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not available")
	}
	dir := t.TempDir()
	sink := filepath.Join(dir, "telemetry.jsonl")
	modulePath := filepath.Join(dir, "adapter.mjs")
	if err := os.WriteFile(modulePath, []byte(`
import { appendFileSync } from 'node:fs';
const sink = process.env.SINK;
function write(value) {
  appendFileSync(sink, JSON.stringify(value) + '\n');
}
export function createAdapter(meta) {
  write({ kind: 'factory', meta });
  return {
    emit(event) { write({ kind: 'emit', event }); },
    recordError(err, fields) { write({ kind: 'error', err, fields }); },
    recordMetric(name, value, tags) { write({ kind: 'metric', name, value, tags }); },
    flush(timeoutMs) { write({ kind: 'flush', timeoutMs }); },
    close() { write({ kind: 'close' }); },
  };
}
`), 0o600); err != nil {
		t.Fatalf("write module: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	adapter, err := LoadTelemetryAdapter(ctx, AdapterMeta{
		Version:  "test-version",
		AppID:    "cli_123456789",
		Tenant:   "feishu",
		Hostname: "host-1",
	}, TelemetryLoaderOptions{
		Module: (&url.URL{Scheme: "file", Path: modulePath}).String(),
		Env:    []string{"SINK=" + sink},
		Stderr: io.Discard,
	})
	if err != nil {
		t.Fatalf("LoadTelemetryAdapter returned error: %v", err)
	}
	if adapter == nil {
		t.Fatal("adapter is nil")
	}
	adapter.Emit(ctx, TelemetryEvent{Level: "info", Phase: "bridge", Event: "started", Ts: "now"})
	adapter.RecordMetric(ctx, "bridge.metric", 2.5, map[string]string{"appId": "cli_123456789"})
	adapter.RecordError(ctx, os.ErrNotExist, map[string]any{"token": "plain-token"})
	if err := adapter.Flush(ctx); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}
	if err := adapter.Close(ctx); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	records := readTelemetryRecords(t, sink)
	if len(records) < 6 {
		t.Fatalf("records = %#v, want factory/event/metric/error/flush/close", records)
	}
	if records[0]["kind"] != "factory" {
		t.Fatalf("first record = %#v, want factory", records[0])
	}
	meta := records[0]["meta"].(map[string]any)
	if meta["version"] != "test-version" || meta["appId"] != "cli_123456789" || meta["tenant"] != "feishu" {
		t.Fatalf("factory meta = %#v", meta)
	}
	if records[1]["kind"] != "emit" {
		t.Fatalf("emit record = %#v", records[1])
	}
	event := records[1]["event"].(map[string]any)
	if event["level"] != "info" || event["phase"] != "bridge" || event["event"] != "started" {
		t.Fatalf("event payload = %#v", event)
	}
	if records[2]["kind"] != "metric" || records[2]["name"] != "bridge.metric" {
		t.Fatalf("metric record = %#v", records[2])
	}
	tags := records[2]["tags"].(map[string]any)
	if tags["appId"] != "...456789" {
		t.Fatalf("metric appId was not sanitized: %#v", tags["appId"])
	}
	if records[3]["kind"] != "error" {
		t.Fatalf("error record = %#v", records[3])
	}
	fields := records[3]["fields"].(map[string]any)
	if fields["token"] != "[REDACTED]" {
		t.Fatalf("error token was not sanitized: %#v", fields["token"])
	}
}

func readTelemetryRecords(t *testing.T, path string) []map[string]any {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open sink: %v", err)
	}
	defer file.Close()
	var records []map[string]any
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var record map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("decode record: %v\n%s", err, scanner.Text())
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan records: %v", err)
	}
	return records
}
