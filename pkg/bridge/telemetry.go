package bridge

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sync"
	"time"
)

var defaultTelemetry struct {
	mu      sync.RWMutex
	adapter TelemetryAdapter
}

var (
	rawPayloadTelemetryKeys = map[string]struct{}{
		"prompt": {}, "stdout": {}, "stderr": {}, "env": {}, "environment": {}, "proxy": {},
	}
	resourceTelemetryKeys = map[string]struct{}{"fileKey": {}, "sourceFileKey": {}}
	idTelemetryKeys       = map[string]struct{}{
		"chatId": {}, "senderId": {}, "sender": {}, "openId": {}, "operatorId": {}, "userId": {},
		"msgId": {}, "messageId": {}, "sourceMessageId": {}, "sessionId": {}, "threadId": {},
		"docToken": {}, "fileToken": {}, "fileKey": {}, "sourceFileKey": {}, "commentId": {},
		"rootCommentId": {}, "replyId": {}, "reactionId": {}, "scope": {}, "appId": {},
	}
	credentialJSONFieldRE        = regexp.MustCompile(`(?i)("(?:secret|app_secret|appSecret|token|access_token|tenant_access_token|app_access_token|authorization)"\s*:\s*")[^"]*(")`)
	escapedCredentialJSONFieldRE = regexp.MustCompile(`(?i)(\\"(?:secret|app_secret|appSecret|token|access_token|tenant_access_token|app_access_token|authorization)\\"\s*:\s*\\")[^\\]*(\\")`)
	resourceJSONFieldRE          = regexp.MustCompile(`(?i)("(?:fileKey|sourceFileKey|file_key|source_file_key|imageKey|image_key|mediaKey|media_key)"\s*:\s*")[^"]*(")`)
	escapedResourceJSONFieldRE   = regexp.MustCompile(`(?i)(\\"(?:fileKey|sourceFileKey|file_key|source_file_key|imageKey|image_key|mediaKey|media_key)\\"\s*:\s*\\")[^\\]*(\\")`)
)

type TelemetryAdapterFunc func(context.Context, TelemetryEvent)

func (f TelemetryAdapterFunc) Emit(ctx context.Context, event TelemetryEvent) {
	if f != nil {
		f(ctx, event)
	}
}

func (f TelemetryAdapterFunc) RecordError(context.Context, error, map[string]any) {}

func (f TelemetryAdapterFunc) RecordMetric(context.Context, string, float64, map[string]string) {}

func (f TelemetryAdapterFunc) Flush(context.Context) error { return nil }

func (f TelemetryAdapterFunc) Close(context.Context) error { return nil }

type NoopTelemetryAdapter struct{}

func (NoopTelemetryAdapter) Emit(context.Context, TelemetryEvent) {}

func (NoopTelemetryAdapter) RecordError(context.Context, error, map[string]any) {}

func (NoopTelemetryAdapter) RecordMetric(context.Context, string, float64, map[string]string) {}

func (NoopTelemetryAdapter) Flush(context.Context) error { return nil }

func (NoopTelemetryAdapter) Close(context.Context) error { return nil }

var requiredObservabilityEvents = []string{
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

// RequiredObservabilityEvents returns the event names that production
// observability adapters should preserve. The list mirrors the JavaScript SDK
// contract.
func RequiredObservabilityEvents() []string {
	return append([]string(nil), requiredObservabilityEvents...)
}

// SetDefaultTelemetry installs the package-level telemetry adapter used by
// ReportEvent, ReportMetric, and ReportError. It returns a restore function so
// tests and embedding hosts can scope the global change.
func SetDefaultTelemetry(adapter TelemetryAdapter) func() {
	defaultTelemetry.mu.Lock()
	previous := defaultTelemetry.adapter
	defaultTelemetry.adapter = adapter
	defaultTelemetry.mu.Unlock()
	return func() {
		defaultTelemetry.mu.Lock()
		defaultTelemetry.adapter = previous
		defaultTelemetry.mu.Unlock()
	}
}

// ReportEvent emits a package-level telemetry event. Like the JavaScript helper,
// it is a no-op when no adapter is installed and never lets telemetry failures
// affect bridge behavior.
func ReportEvent(ctx context.Context, name string, fields map[string]any) {
	adapter := currentDefaultTelemetry()
	if adapter == nil {
		return
	}
	safeTelemetryEmit(adapter, ctx, name, fields)
}

// ReportMetric records a package-level numeric metric. It is safe to call even
// when telemetry is disabled or the installed adapter is faulty.
func ReportMetric(ctx context.Context, name string, value float64, tags map[string]string) {
	adapter := currentDefaultTelemetry()
	if adapter == nil {
		return
	}
	safeTelemetryMetric(adapter, ctx, name, value, tags)
}

// ReportError records a package-level telemetry error. It is safe to call even
// when telemetry is disabled or the installed adapter is faulty.
func ReportError(ctx context.Context, err error, fields map[string]any) {
	if err == nil {
		return
	}
	adapter := currentDefaultTelemetry()
	if adapter == nil {
		return
	}
	safeTelemetryRecordError(adapter, ctx, err, fields)
}

func currentDefaultTelemetry() TelemetryAdapter {
	defaultTelemetry.mu.RLock()
	adapter := defaultTelemetry.adapter
	defaultTelemetry.mu.RUnlock()
	return adapter
}

func ignoreTelemetryPanic() {
	_ = recover()
}

func safeTelemetryEmit(adapter TelemetryAdapter, ctx context.Context, name string, fields map[string]any) {
	if adapter == nil {
		return
	}
	defer ignoreTelemetryPanic()
	adapter.Emit(ctx, buildTelemetryEvent("info", name, fields))
}

func safeTelemetryRecordError(adapter TelemetryAdapter, ctx context.Context, err error, fields map[string]any) {
	if adapter == nil || err == nil {
		return
	}
	defer ignoreTelemetryPanic()
	adapter.RecordError(ctx, sanitizeTelemetryError(err), sanitizeTelemetryContext(fields))
}

func safeTelemetryMetric(adapter TelemetryAdapter, ctx context.Context, name string, value float64, tags map[string]string) {
	if adapter == nil {
		return
	}
	defer ignoreTelemetryPanic()
	adapter.RecordMetric(ctx, name, value, sanitizeMetricTags(tags))
}

func safeTelemetryFlush(adapter TelemetryAdapter, ctx context.Context) (err error) {
	if adapter == nil {
		return nil
	}
	defer func() {
		if recover() != nil {
			err = nil
		}
	}()
	return adapter.Flush(ctx)
}

func safeTelemetryClose(adapter TelemetryAdapter, ctx context.Context) (err error) {
	if adapter == nil {
		return nil
	}
	defer func() {
		if recover() != nil {
			err = nil
		}
	}()
	return adapter.Close(ctx)
}

func buildTelemetryEvent(level string, name string, fields map[string]any) TelemetryEvent {
	now := time.Now()
	sanitized := sanitizeTelemetryContext(fields)
	phase, event := telemetryPhaseEvent(name, sanitized)
	return TelemetryEvent{
		Name:   name,
		At:     now,
		Fields: sanitized,
		Level:  level,
		Phase:  phase,
		Event:  event,
		Ctx:    telemetryContextFromFields(sanitized),
		Ts:     now.Format(time.RFC3339Nano),
	}
}

func telemetryPhaseEvent(name string, fields map[string]any) (string, string) {
	if value, ok := fields["phase"].(string); ok && value != "" {
		return value, name
	}
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '.' {
			return name[:i], name[i+1:]
		}
	}
	return name, name
}

func telemetryContextFromFields(fields map[string]any) LogContext {
	return LogContext{
		TraceID: telemetryStringField(fields, "traceId"),
		ChatID:  telemetryStringField(fields, "chatId"),
		MsgID:   telemetryStringField(fields, "msgId"),
	}
}

func telemetryStringField(fields map[string]any, key string) string {
	value, _ := fields[key].(string)
	return value
}

func sanitizeTelemetryContext(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = sanitizeTelemetryValue(key, value, true)
	}
	return out
}

func sanitizeMetricTags(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = fmt.Sprint(sanitizeTelemetryValue(key, value, true))
	}
	return out
}

func sanitizeTelemetryError(err error) error {
	if err == nil {
		return nil
	}
	return errors.New(fmt.Sprint(sanitizeTelemetryValue("err", err.Error(), true)))
}

func sanitizeTelemetryValue(key string, value any, redactIDs bool) any {
	normalizedKey := key
	if len(normalizedKey) > 0 && normalizedKey[0] == '_' {
		normalizedKey = normalizedKey[1:]
	}
	if _, ok := rawPayloadTelemetryKeys[normalizedKey]; ok {
		return "[REDACTED]"
	}
	if regexp.MustCompile(`(?i)token|secret|authorization`).MatchString(normalizedKey) {
		return "[REDACTED]"
	}
	if regexp.MustCompile(`(?i)attachment.*path|media.*path|^(cwd|cwdRealpath|path|absPath)$`).MatchString(normalizedKey) {
		return "[REDACTED_PATH]"
	}
	if _, ok := resourceTelemetryKeys[normalizedKey]; ok {
		return "[REDACTED_RESOURCE]"
	}
	if redactIDs {
		if _, ok := idTelemetryKeys[normalizedKey]; ok {
			return redactTelemetryID(value)
		}
	}
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for nestedKey, nestedValue := range typed {
			out[nestedKey] = sanitizeTelemetryValue(nestedKey, nestedValue, redactIDs)
		}
		return out
	case map[string]string:
		out := make(map[string]string, len(typed))
		for nestedKey, nestedValue := range typed {
			out[nestedKey] = fmt.Sprint(sanitizeTelemetryValue(nestedKey, nestedValue, redactIDs))
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = sanitizeTelemetryValue(key, item, redactIDs)
		}
		return out
	case []string:
		out := make([]string, len(typed))
		for i, item := range typed {
			out[i] = fmt.Sprint(sanitizeTelemetryValue(key, item, redactIDs))
		}
		return out
	case string:
		return redactTelemetryText(typed)
	default:
		return value
	}
}

func redactTelemetryID(value any) any {
	text, ok := value.(string)
	if !ok || len(text) <= 6 {
		return value
	}
	return "..." + text[len(text)-6:]
}

func redactTelemetryText(text string) string {
	text = credentialJSONFieldRE.ReplaceAllString(text, `${1}[REDACTED]${2}`)
	text = escapedCredentialJSONFieldRE.ReplaceAllString(text, `${1}[REDACTED]${2}`)
	text = resourceJSONFieldRE.ReplaceAllString(text, `${1}[REDACTED_RESOURCE]${2}`)
	text = escapedResourceJSONFieldRE.ReplaceAllString(text, `${1}[REDACTED_RESOURCE]${2}`)
	const max = 4096
	if len(text) > max {
		return text[:max] + "...[truncated]"
	}
	return text
}
