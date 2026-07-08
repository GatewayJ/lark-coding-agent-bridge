package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const defaultLogRetentionDays = 30

var logFileRE = regexp.MustCompile(`^bridge-(\d{4})(\d{2})(\d{2})\.jsonl$`)

// JSONLLogger writes bridge log events to bridge-YYYYMMDD.jsonl files.
// It mirrors the JavaScript CLI logger's durable local log shape while staying
// small enough for embedding applications to reuse directly.
type JSONLLogger struct {
	mu            sync.Mutex
	dir           string
	retentionDays int
	now           func() time.Time
	stdout        io.Writer
	stderr        io.Writer
	telemetry     TelemetryAdapter
}

type JSONLLoggerOptions struct {
	Dir           string
	RetentionDays int
	Now           func() time.Time
	Stdout        io.Writer
	Stderr        io.Writer
	Telemetry     TelemetryAdapter
}

// NewJSONLLogger constructs a file-backed logger. An empty Dir keeps the logger
// in console/telemetry-only mode, matching the JS logger before configuration.
func NewJSONLLogger(options JSONLLoggerOptions) *JSONLLogger {
	retentionDays := options.RetentionDays
	if retentionDays <= 0 {
		retentionDays = envLogRetentionDays()
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &JSONLLogger{
		dir:           options.Dir,
		retentionDays: retentionDays,
		now:           now,
		stdout:        options.Stdout,
		stderr:        options.Stderr,
		telemetry:     options.Telemetry,
	}
}

func (l *JSONLLogger) Info(msg string, fields map[string]any) {
	l.emit("info", msg, fields)
}

func (l *JSONLLogger) Warn(msg string, fields map[string]any) {
	l.emit("warn", msg, fields)
}

func (l *JSONLLogger) Error(msg string, fields map[string]any) {
	l.emit("error", msg, fields)
}

func (l *JSONLLogger) Flush() error {
	return nil
}

func (l *JSONLLogger) Close() error {
	return nil
}

// GC removes bridge JSONL log files older than the retention window.
func (l *JSONLLogger) GC() (int, error) {
	if l == nil || l.dir == "" {
		return 0, nil
	}
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	cutoff := l.now().Add(-time.Duration(l.retentionDays) * 24 * time.Hour)
	removed := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		matches := logFileRE.FindStringSubmatch(entry.Name())
		if matches == nil {
			continue
		}
		fileTime, err := time.Parse("20060102", matches[1]+matches[2]+matches[3])
		if err != nil || !fileTime.Before(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(l.dir, entry.Name())); err != nil {
			return removed, err
		}
		removed++
	}
	if removed > 0 {
		l.Info("logger.gc", map[string]any{"removed": removed, "retentionDays": l.retentionDays})
	}
	return removed, nil
}

func (l *JSONLLogger) emit(level string, msg string, fields map[string]any) {
	if l == nil {
		return
	}
	phase, event := logPhaseEvent(msg)
	now := l.now()
	ts := now.Format(time.RFC3339Nano)
	entry := map[string]any{
		"ts":    ts,
		"level": level,
		"phase": phase,
		"event": event,
	}
	if msg != "" {
		entry["msg"] = msg
	}
	for key, value := range fields {
		target := key
		if isReservedLogKey(key) {
			target = "_" + key
		}
		entry[target] = sanitizeTelemetryValue(target, value, false)
	}
	if l.dir != "" {
		l.writeFile(now, entry)
	}
	l.emitTelemetry(level, phase, event, ts, entry)
	l.writeConsole(level, phase, event, fields)
}

func (l *JSONLLogger) writeFile(now time.Time, entry map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := os.MkdirAll(l.dir, 0o700); err != nil {
		return
	}
	path := filepath.Join(l.dir, "bridge-"+now.Format("20060102")+".jsonl")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	line, err := json.Marshal(entry)
	if err != nil {
		return
	}
	_, _ = file.Write(append(line, '\n'))
}

func (l *JSONLLogger) emitTelemetry(level string, phase string, event string, ts string, entry map[string]any) {
	if l.telemetry == nil {
		return
	}
	fields := map[string]any{}
	for key, value := range entry {
		if isTelemetryEnvelopeKey(key) || value == nil {
			continue
		}
		fields[key] = sanitizeTelemetryValue(key, value, true)
	}
	ctx := telemetryContextFromFields(entry)
	payload := TelemetryEvent{
		Name:   phase + "." + event,
		At:     l.now(),
		Fields: fields,
		Level:  level,
		Phase:  phase,
		Event:  event,
		Ctx:    ctx,
		Ts:     ts,
	}
	func() {
		defer ignoreTelemetryPanic()
		l.telemetry.Emit(context.Background(), payload)
	}()
	if level == "error" {
		safeTelemetryRecordError(l.telemetry, context.Background(), fmt.Errorf("%s.%s", phase, event), fields)
	}
}

func (l *JSONLLogger) writeConsole(level string, phase string, event string, fields map[string]any) {
	var writer io.Writer
	switch level {
	case "error":
		writer = l.stderr
	case "warn":
		writer = firstWriter(l.stderr, l.stdout)
	case "info":
		if !stdoutLogAllowed(phase, event) {
			return
		}
		writer = l.stdout
	}
	if writer == nil {
		return
	}
	_, _ = fmt.Fprintf(writer, "[%s] %s.%s %s\n", level, phase, event, consoleFields(fields))
}

func firstWriter(values ...io.Writer) io.Writer {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func consoleFields(fields map[string]any) string {
	if len(fields) == 0 {
		return ""
	}
	safe := sanitizeTelemetryContext(fields)
	data, err := json.Marshal(safe)
	if err != nil {
		return ""
	}
	return string(data)
}

func logPhaseEvent(msg string) (string, string) {
	normalized := strings.TrimSpace(msg)
	if normalized == "" {
		return "bridge", "log"
	}
	normalized = strings.ReplaceAll(normalized, " ", ".")
	parts := strings.Split(normalized, ".")
	if len(parts) == 1 {
		return parts[0], parts[0]
	}
	phase := strings.Join(parts[:len(parts)-1], ".")
	event := parts[len(parts)-1]
	return phase, event
}

func isReservedLogKey(key string) bool {
	switch key {
	case "ts", "level", "phase", "event", "traceId", "chatId", "msgId":
		return true
	default:
		return false
	}
}

func isTelemetryEnvelopeKey(key string) bool {
	switch key {
	case "ts", "level", "phase", "event", "traceId", "chatId", "msgId":
		return true
	default:
		return false
	}
}

func stdoutLogAllowed(phase string, event string) bool {
	switch phase + "." + event {
	case "ws.connected",
		"ws.reconnecting",
		"ws.reconnected",
		"intake.enter",
		"intake.command",
		"run.started",
		"run.completed",
		"run.failed",
		"cot.created",
		"cot.completed",
		"outbound.sent",
		"outbound.markdown-stream-fallback",
		"card.final",
		"bridge.started",
		"bridge.stopped":
		return true
	default:
		return false
	}
}

func envLogRetentionDays() int {
	value := strings.TrimSpace(os.Getenv("LARK_CHANNEL_LOG_DAYS"))
	if value == "" {
		return defaultLogRetentionDays
	}
	var days int
	if _, err := fmt.Sscanf(value, "%d", &days); err != nil || days <= 0 {
		return defaultLogRetentionDays
	}
	return days
}
