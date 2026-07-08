package codex

import (
	"strings"
	"testing"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/ports/agent"
)

func TestJsonlTranslatorHappyPath(t *testing.T) {
	translator := NewJsonlTranslator()

	assertEvents(t, translator.Translate(map[string]any{"type": "thread.started", "thread_id": "thread-1"}), []agent.AgentEvent{
		{Type: agent.EventSystem, ThreadID: stringPtr("thread-1")},
	})
	assertEvents(t, translator.Translate(map[string]any{"type": "turn.started"}), nil)
	assertEvents(t, translator.Translate(map[string]any{
		"type": "item.started",
		"item": map[string]any{
			"id":      "cmd-1",
			"type":    "command_execution",
			"command": "pwd",
		},
	}), []agent.AgentEvent{
		{
			Type:  agent.EventToolUse,
			ID:    stringPtr("cmd-1"),
			Name:  stringPtr("command_execution"),
			Input: map[string]any{"command": "pwd"},
		},
	})
	assertEvents(t, translator.Translate(map[string]any{
		"type": "item.completed",
		"item": map[string]any{
			"id":        "cmd-1",
			"type":      "command_execution",
			"output":    "/repo\n",
			"exit_code": 0,
		},
	}), []agent.AgentEvent{
		{
			Type:    agent.EventToolResult,
			ID:      stringPtr("cmd-1"),
			Output:  stringPtr("/repo\n"),
			IsError: boolPtr(false),
		},
	})
	assertEvents(t, translator.Translate(map[string]any{
		"type": "item.completed",
		"item": map[string]any{
			"id":   "msg-1",
			"type": "agent_message",
			"text": "hello from item",
		},
	}), []agent.AgentEvent{
		{Type: agent.EventText, Delta: stringPtr("hello from item")},
	})
	assertEvents(t, translator.Translate(map[string]any{"type": "agent_message", "message": "hello"}), []agent.AgentEvent{
		{Type: agent.EventText, Delta: stringPtr("hello")},
	})
	assertEvents(t, translator.Translate(map[string]any{"type": "turn.completed"}), []agent.AgentEvent{
		{Type: agent.EventDone, ThreadID: stringPtr("thread-1"), TerminationReason: agent.TerminationNormal},
	})
}

func TestJsonlTranslatorNonTerminalErrorThenEOF(t *testing.T) {
	translator := NewJsonlTranslator()

	assertEvents(t, translator.Translate(map[string]any{"type": "error", "message": "transport failed"}), nil)
	if translator.TerminalEmitted() {
		t.Fatal("TerminalEmitted = true, want false")
	}
	assertEvents(t, translator.Finish(CodexFinishFailed), []agent.AgentEvent{
		{
			Type:              agent.EventError,
			Message:           stringPtr("codex stream ended before a terminal event: transport failed"),
			TerminationReason: agent.TerminationFailed,
		},
	})
	assertEvents(t, translator.Finish(CodexFinishFailed), nil)
}

func TestJsonlTranslatorUnknownAndAnomalyDrift(t *testing.T) {
	var reports []string
	translator := NewCodexJsonlTranslatorWithReporter(func(event string, fields map[string]any) {
		reports = append(reports, event)
		if event == "jsonl.unknown_event" && fields["eventType"] != "unknown.future" {
			t.Fatalf("unknown event fields = %#v", fields)
		}
	})

	assertEvents(t, translator.Translate(map[string]any{"type": "unknown.future", "value": 1}), nil)
	assertEvents(t, translator.Translate(map[string]any{
		"type": "item.completed",
		"item": map[string]any{
			"id":        "cmd-late",
			"type":      "command_execution",
			"stdout":    "late",
			"exit_code": 1,
		},
	}), []agent.AgentEvent{
		{
			Type:    agent.EventToolResult,
			ID:      stringPtr("cmd-late"),
			Output:  stringPtr("late"),
			IsError: boolPtr(true),
		},
	})

	want := ProtocolDriftState{UnknownEvents: 1, Anomalies: 1}
	if got := translator.ProtocolDrift(); got != want {
		t.Fatalf("ProtocolDrift = %#v, want %#v", got, want)
	}
	if len(reports) != 1 || reports[0] != "jsonl.unknown_event" {
		t.Fatalf("reports = %#v, want one jsonl.unknown_event", reports)
	}
}

func TestJsonlTranslatorReportsNonTerminalErrors(t *testing.T) {
	var reports []string
	var fields []map[string]any
	translator := NewCodexJsonlTranslatorWithReporter(func(event string, eventFields map[string]any) {
		reports = append(reports, event)
		fields = append(fields, eventFields)
	})

	assertEvents(t, translator.Translate(map[string]any{"type": "error", "message": "transport failed"}), nil)
	if len(reports) != 1 || reports[0] != "jsonl.error_event" {
		t.Fatalf("reports = %#v, want one jsonl.error_event", reports)
	}
	if fields[0]["message"] != "transport failed" {
		t.Fatalf("error report fields = %#v", fields[0])
	}
}

func TestJsonlTranslatorInterruptedFinish(t *testing.T) {
	translator := NewJsonlTranslator()

	translator.Translate(map[string]any{"type": "thread.started", "threadId": "thread-stop"})
	assertEvents(t, translator.Finish(CodexFinishInterrupted), []agent.AgentEvent{
		{Type: agent.EventDone, ThreadID: stringPtr("thread-stop"), TerminationReason: agent.TerminationInterrupted},
	})

	timedOut := NewCodexJsonlTranslator()
	timedOut.Translate(map[string]any{"type": "thread.started", "threadId": "thread-timeout"})
	assertEvents(t, timedOut.Finish(CodexFinishTimeout), []agent.AgentEvent{
		{Type: agent.EventDone, ThreadID: stringPtr("thread-timeout"), TerminationReason: agent.TerminationTimeout},
	})
}

func TestJsonlTranslatorUsageFields(t *testing.T) {
	translator := NewJsonlTranslator()
	translator.Translate(map[string]any{"type": "thread.started", "thread_id": "thread-1"})

	assertEvents(t, translator.Translate(map[string]any{
		"type": "turn.completed",
		"usage": map[string]any{
			"input_tokens":            float64(12),
			"output_tokens":           34,
			"cached_input_tokens":     5,
			"reasoning_output_tokens": 7,
		},
	}), []agent.AgentEvent{
		{
			Type:                  agent.EventUsage,
			InputTokens:           intPtr(12),
			OutputTokens:          intPtr(34),
			CachedInputTokens:     intPtr(5),
			ReasoningOutputTokens: intPtr(7),
		},
		{Type: agent.EventDone, ThreadID: stringPtr("thread-1"), TerminationReason: agent.TerminationNormal},
	})
}

func TestJsonlTranslatorDuplicateTerminalIgnored(t *testing.T) {
	translator := NewJsonlTranslator()

	assertEvents(t, translator.Translate(map[string]any{
		"type":  "turn.failed",
		"error": map[string]any{"message": "command denied"},
	}), []agent.AgentEvent{
		{
			Type:              agent.EventError,
			Message:           stringPtr("command denied"),
			TerminationReason: agent.TerminationFailed,
		},
	})
	assertEvents(t, translator.Translate(map[string]any{"type": "turn.completed"}), nil)
	assertEvents(t, translator.Translate(map[string]any{"type": "agent_message", "message": "too late"}), nil)
	assertEvents(t, translator.Finish(CodexFinishFailed), nil)
}

func TestJsonlTranslatorTranslateLine(t *testing.T) {
	translator := NewJsonlTranslator()

	got, err := translator.TranslateLine([]byte(`{"type":"thread.started","thread_id":"thread-json"}`))
	if err != nil {
		t.Fatalf("TranslateLine returned error: %v", err)
	}
	assertEvents(t, got, []agent.AgentEvent{
		{Type: agent.EventSystem, ThreadID: stringPtr("thread-json")},
	})

	_, err = translator.TranslateLine([]byte(`{`))
	if err == nil {
		t.Fatal("TranslateLine returned nil error for invalid JSON")
	}
}

func TestJsonlTranslatorTerminalErrorAndTruncation(t *testing.T) {
	translator := NewJsonlTranslator()
	longMessage := strings.Repeat("x", 5000)

	got := translator.Translate(map[string]any{
		"type":    "turn.failed",
		"message": longMessage,
	})
	if len(got) != 1 || got[0].Message == nil {
		t.Fatalf("Translate returned %#v, want one error with message", got)
	}
	if len(*got[0].Message) != 4096 {
		t.Fatalf("message length = %d, want 4096", len(*got[0].Message))
	}
}

func assertEvents(t *testing.T, got []agent.AgentEvent, want []agent.AgentEvent) {
	t.Helper()
	if !eventsEqual(got, want) {
		t.Fatalf("events mismatch\nwant: %#v\n got: %#v", want, got)
	}
}
