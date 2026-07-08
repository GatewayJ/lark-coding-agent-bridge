package bridge

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestPackageTelemetryFacadeReportsAndRestores(t *testing.T) {
	adapter := &recordingTelemetry{}
	restore := SetDefaultTelemetry(adapter)
	defer restore()

	ReportEvent(context.Background(), "bridge.test", map[string]any{"phase": "unit"})
	ReportMetric(context.Background(), "bridge.metric", 2.5, map[string]string{"appId": "cli_test"})
	ReportError(context.Background(), errors.New("boom"), map[string]any{"scope": "oc_test"})

	if len(adapter.events) != 1 || adapter.events[0].Name != "bridge.test" {
		t.Fatalf("events = %#v, want bridge.test", adapter.events)
	}
	if adapter.events[0].Level != "info" || adapter.events[0].Phase != "unit" || adapter.events[0].Event != "bridge.test" || adapter.events[0].Ts == "" {
		t.Fatalf("event JS fields = %#v, want level/phase/event/ts", adapter.events[0])
	}
	if len(adapter.metrics) != 1 || adapter.metrics[0].name != "bridge.metric" || adapter.metrics[0].value != 2.5 {
		t.Fatalf("metrics = %#v, want bridge.metric", adapter.metrics)
	}
	if len(adapter.errors) != 1 || adapter.errors[0].Error() != "boom" {
		t.Fatalf("errors = %#v, want boom", adapter.errors)
	}

	restore()
	ReportEvent(context.Background(), "bridge.after-restore", nil)
	if len(adapter.events) != 1 {
		t.Fatalf("event recorded after restore: %#v", adapter.events)
	}
}

func TestRequiredObservabilityEventsMatchJavaScriptContract(t *testing.T) {
	want := []string{
		"run.started",
		"run.completed",
		"run.failed",
		"policy.denied",
		"callback.denied",
		"access.owner_refresh_failed",
		"jsonl.unknown_event",
		"attachment.decision",
		"comment.reply_failed",
	}
	got := RequiredObservabilityEvents()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RequiredObservabilityEvents = %#v, want %#v", got, want)
	}
	got[0] = "mutated"
	if again := RequiredObservabilityEvents(); again[0] != "run.started" {
		t.Fatalf("RequiredObservabilityEvents returned shared backing array: %#v", again)
	}
}

func TestBridgeTelemetryLifecycleSwallowsPanics(t *testing.T) {
	bridge := &Bridge{started: true, profile: "codex", telemetry: panicLifecycleTelemetry{}}
	if err := bridge.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown returned error: %v", err)
	}
	if bridge.started {
		t.Fatalf("bridge stayed started after telemetry panic")
	}
}

func TestBridgeTelemetryLifecycleErrorsDoNotBlockShutdown(t *testing.T) {
	bridge := &Bridge{started: true, profile: "codex", telemetry: errorLifecycleTelemetry{}}
	if err := bridge.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown returned error: %v", err)
	}
	if bridge.started {
		t.Fatalf("bridge stayed started after telemetry error")
	}
}

func TestPackageTelemetryFacadeSwallowsFaultyAdapters(t *testing.T) {
	restore := SetDefaultTelemetry(panicTelemetry{})
	defer restore()

	ReportEvent(context.Background(), "bridge.test", nil)
	ReportMetric(context.Background(), "bridge.metric", 1, nil)
	ReportError(context.Background(), errors.New("boom"), nil)
}

func TestPackageTelemetryFacadeSanitizesExternalFields(t *testing.T) {
	adapter := &recordingTelemetry{}
	restore := SetDefaultTelemetry(adapter)
	defer restore()

	ReportEvent(context.Background(), "bridge.test", map[string]any{
		"appId": "cli_123456789",
		"path":  "/tmp/private/config.json",
		"env":   map[string]any{"TOKEN": "secret"},
	})
	ReportMetric(context.Background(), "bridge.metric", 1, map[string]string{
		"appId":  "cli_123456789",
		"secret": "plain-secret",
	})
	ReportError(context.Background(), errors.New(`failed with {"appSecret":"plain-secret","fileKey":"boxcn123"}`), map[string]any{
		"cwd":   "/repo/private",
		"token": "plain-token",
	})

	if got := adapter.events[0].Fields["appId"]; got != "...456789" {
		t.Fatalf("sanitized appId = %#v", got)
	}
	if got := adapter.events[0].Fields["path"]; got != "[REDACTED_PATH]" {
		t.Fatalf("sanitized path = %#v", got)
	}
	if got := adapter.events[0].Fields["env"]; got != "[REDACTED]" {
		t.Fatalf("sanitized env = %#v", got)
	}
	if got := adapter.metrics[0].tags["secret"]; got != "[REDACTED]" {
		t.Fatalf("sanitized metric secret = %#v", got)
	}
	if strings.Contains(adapter.errors[0].Error(), "plain-secret") || strings.Contains(adapter.errors[0].Error(), "boxcn123") {
		t.Fatalf("error was not sanitized: %q", adapter.errors[0].Error())
	}
	if got := adapter.errorFields[0]["cwd"]; got != "[REDACTED_PATH]" {
		t.Fatalf("sanitized cwd = %#v", got)
	}
	if got := adapter.errorFields[0]["token"]; got != "[REDACTED]" {
		t.Fatalf("sanitized token = %#v", got)
	}
}

func TestTelemetryFactoryTypesCompile(t *testing.T) {
	var factory AdapterFactory = func(meta AdapterMeta) TelemetryAdapter {
		if meta.Version == "" {
			t.Fatal("meta version should be caller-provided")
		}
		return &recordingTelemetry{}
	}
	if factory(AdapterMeta{Version: "test", AppID: "cli_test", Tenant: "feishu", Hostname: "host"}) == nil {
		t.Fatal("factory returned nil adapter")
	}
}

type recordingTelemetry struct {
	events      []TelemetryEvent
	metrics     []recordedMetric
	errors      []error
	errorFields []map[string]any
}

type recordedMetric struct {
	name  string
	value float64
	tags  map[string]string
}

func (r *recordingTelemetry) Emit(_ context.Context, event TelemetryEvent) {
	r.events = append(r.events, event)
}

func (r *recordingTelemetry) RecordError(_ context.Context, err error, fields map[string]any) {
	r.errors = append(r.errors, err)
	r.errorFields = append(r.errorFields, fields)
}

func (r *recordingTelemetry) RecordMetric(_ context.Context, name string, value float64, tags map[string]string) {
	r.metrics = append(r.metrics, recordedMetric{name: name, value: value, tags: tags})
}

func (r *recordingTelemetry) Flush(context.Context) error { return nil }
func (r *recordingTelemetry) Close(context.Context) error { return nil }

type panicTelemetry struct{}

func (panicTelemetry) Emit(context.Context, TelemetryEvent)               { panic("emit") }
func (panicTelemetry) RecordError(context.Context, error, map[string]any) { panic("error") }
func (panicTelemetry) RecordMetric(context.Context, string, float64, map[string]string) {
	panic("metric")
}
func (panicTelemetry) Flush(context.Context) error { return nil }
func (panicTelemetry) Close(context.Context) error { return nil }

type panicLifecycleTelemetry struct{}

func (panicLifecycleTelemetry) Emit(context.Context, TelemetryEvent)               {}
func (panicLifecycleTelemetry) RecordError(context.Context, error, map[string]any) {}
func (panicLifecycleTelemetry) RecordMetric(context.Context, string, float64, map[string]string) {
}
func (panicLifecycleTelemetry) Flush(context.Context) error { panic("flush") }
func (panicLifecycleTelemetry) Close(context.Context) error { panic("close") }

type errorLifecycleTelemetry struct{}

func (errorLifecycleTelemetry) Emit(context.Context, TelemetryEvent)               {}
func (errorLifecycleTelemetry) RecordError(context.Context, error, map[string]any) {}
func (errorLifecycleTelemetry) RecordMetric(context.Context, string, float64, map[string]string) {
}
func (errorLifecycleTelemetry) Flush(context.Context) error { return errors.New("flush failed") }
func (errorLifecycleTelemetry) Close(context.Context) error { return errors.New("close failed") }
