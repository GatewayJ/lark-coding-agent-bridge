package claudecli

import (
	"encoding/json"
	"reflect"
	"testing"

	agentport "github.com/zarazhangrui/lark-coding-agent-bridge/internal/ports/agent"
)

func TestStreamTranslatorTranslatesSystemInitMetadata(t *testing.T) {
	translator := NewStreamTranslator()
	got := translator.Translate(map[string]any{
		"type":       "system",
		"subtype":    "init",
		"session_id": "sess-1",
		"cwd":        "/repo",
		"model":      "sonnet",
	})
	want := []agentport.AgentEvent{{
		Type:      agentport.EventSystem,
		SessionID: stringPtr("sess-1"),
		CWD:       stringPtr("/repo"),
		Model:     stringPtr("sonnet"),
	}}
	assertEvents(t, got, want)
}

func TestStreamTranslatorTranslatesAssistantBlocksInOrder(t *testing.T) {
	got := TranslateEvent(map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"content": []any{
				map[string]any{"type": "text", "text": "hello"},
				map[string]any{"type": "thinking", "thinking": "checking"},
				map[string]any{"type": "tool_use", "id": "tool-1", "name": "Bash", "input": map[string]any{"command": "pwd"}},
			},
		},
	})
	want := []agentport.AgentEvent{
		{Type: agentport.EventText, Delta: stringPtr("hello")},
		{Type: agentport.EventThinking, Delta: stringPtr("checking")},
		{Type: agentport.EventToolUse, ID: stringPtr("tool-1"), Name: stringPtr("Bash"), Input: map[string]any{"command": "pwd"}},
	}
	assertEvents(t, got, want)
}

func TestStreamTranslatorTranslatesToolResults(t *testing.T) {
	structured := []any{map[string]any{"type": "text", "text": "bad"}}
	got := TranslateEvent(map[string]any{
		"type": "user",
		"message": map[string]any{
			"content": []any{
				map[string]any{"type": "tool_result", "tool_use_id": "tool-1", "content": "ok"},
				map[string]any{"type": "tool_result", "tool_use_id": "tool-2", "content": structured, "is_error": true},
			},
		},
	})
	structuredJSON, err := json.Marshal(structured)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	want := []agentport.AgentEvent{
		{Type: agentport.EventToolResult, ID: stringPtr("tool-1"), Output: stringPtr("ok"), IsError: boolPtr(false)},
		{Type: agentport.EventToolResult, ID: stringPtr("tool-2"), Output: stringPtr(string(structuredJSON)), IsError: boolPtr(true)},
	}
	assertEvents(t, got, want)
}

func TestStreamTranslatorTranslatesUsageBeforeDone(t *testing.T) {
	translator := NewStreamTranslator()
	got := translator.Translate(map[string]any{
		"type":           "result",
		"session_id":     "sess-2",
		"usage":          map[string]any{"input_tokens": 12, "output_tokens": 34, "cache_read_input_tokens": 5},
		"total_cost_usd": 0.1234,
	})
	want := []agentport.AgentEvent{
		{
			Type:              agentport.EventUsage,
			InputTokens:       intPtr(12),
			OutputTokens:      intPtr(34),
			CachedInputTokens: intPtr(5),
			CostUSD:           floatPtr(0.1234),
		},
		{Type: agentport.EventDone, SessionID: stringPtr("sess-2"), TerminationReason: agentport.TerminationNormal},
	}
	assertEvents(t, got, want)
}

func TestStreamTranslatorDoesNotFinishAfterResult(t *testing.T) {
	translator := NewStreamTranslator()
	_ = translator.Translate(map[string]any{"type": "result", "session_id": "sess-2"})
	if got := translator.Finish(ClaudeFinishInterrupted); len(got) != 0 {
		t.Fatalf("Finish after result = %#v, want nil", got)
	}
}

func TestStreamTranslatorIgnoresUnknownAndIncompleteEvents(t *testing.T) {
	cases := []any{
		nil,
		map[string]any{"type": "assistant", "message": map[string]any{"content": []any{map[string]any{"type": "text", "text": ""}}}},
		map[string]any{"type": "assistant", "message": map[string]any{"content": []any{map[string]any{"type": "tool_use", "id": "t"}}}},
		map[string]any{"type": "system", "subtype": "other"},
	}
	for _, tc := range cases {
		if got := TranslateEvent(tc); len(got) != 0 {
			t.Fatalf("TranslateEvent(%#v) = %#v, want nil", tc, got)
		}
	}
}

func assertEvents(t *testing.T, got []agentport.AgentEvent, want []agentport.AgentEvent) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		gotJSON, _ := json.MarshalIndent(got, "", "  ")
		wantJSON, _ := json.MarshalIndent(want, "", "  ")
		t.Fatalf("events mismatch:\nwant %s\n got %s", wantJSON, gotJSON)
	}
}
