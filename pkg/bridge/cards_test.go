package bridge

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRunCardFacadeReducesBridgeEvents(t *testing.T) {
	at := time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC)
	state := NewRunCardState(RunCardStateInput{
		Scope: "oc_group",
		CWD:   "/repo",
	})
	state = ReduceRunCardState(state, Event{
		Type:    EventText,
		At:      at,
		RunID:   strPtr("run-1"),
		ScopeID: strPtr("oc_group"),
		Delta:   strPtr("hello"),
	})
	state = ReduceRunCardState(state, Event{Type: EventDone, TerminationReason: TerminationNormal})

	if state.Status != RunCardSucceeded {
		t.Fatalf("status = %s, want %s", state.Status, RunCardSucceeded)
	}
	if state.RunID != "run-1" || state.Scope != "oc_group" || !state.StartedAt.Equal(at) {
		t.Fatalf("event metadata not preserved: %#v", state)
	}
	card := RenderRunCard(state, CardRenderOptions{ShowMetadata: true})
	if card.Summary != "已完成" {
		t.Fatalf("summary = %q, want 已完成", card.Summary)
	}
}

func TestJavaScriptCompatibleRunStateFacade(t *testing.T) {
	state := InitialState()
	if state.Terminal != TerminalRunning || state.Footer != FooterThinking {
		t.Fatalf("InitialState = %#v", state)
	}
	body, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal initial RunState: %v", err)
	}
	if !strings.Contains(string(body), `"blocks":[]`) || !strings.Contains(string(body), `"footer":"thinking"`) {
		t.Fatalf("InitialState JSON = %s, want JS-shaped blocks/footer", body)
	}
	state = Reduce(state, Event{Type: EventText, Delta: strPtr("hello")})
	if len(state.Blocks) != 1 || state.Blocks[0].Content != "hello" || state.Footer != FooterStreaming {
		t.Fatalf("Reduce text state = %#v", state)
	}
	if text := RenderText(state); text == "" || !strings.Contains(text, "hello") {
		t.Fatalf("RenderText = %q, want hello", text)
	}
	card := RenderCard(state, CardRenderOptions{})
	if card["schema"] != "2.0" {
		t.Fatalf("RenderCard = %#v", card)
	}
	state = FinalizeIfRunning(state)
	if state.Terminal != TerminalDone || state.Footer != "" {
		t.Fatalf("FinalizeIfRunning = %#v", state)
	}
	body, err = json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal finalized RunState: %v", err)
	}
	if !strings.Contains(string(body), `"footer":null`) {
		t.Fatalf("finalized RunState JSON = %s, want footer null", body)
	}
	state = MarkInterrupted(InitialState())
	if state.Terminal != TerminalInterrupted {
		t.Fatalf("MarkInterrupted = %#v", state)
	}
	state = MarkIdleTimeout(InitialState(), 7)
	if state.Terminal != TerminalIdleTimeout || state.IdleTimeoutMinutes != 7 {
		t.Fatalf("MarkIdleTimeout = %#v", state)
	}
}

func TestParseCardActionFacade(t *testing.T) {
	parsed := ParseCardAction(CardActionParseInput{
		Value: map[string]any{
			"__bridge_cb":  true,
			"bridge_token": "signed",
			"choice":       "a",
		},
		FormValue: map[string]any{"note": "x"},
	})

	if !parsed.BridgeCallback || parsed.AgentPayload["bridge_token"] != nil {
		t.Fatalf("unexpected parsed action: %#v", parsed)
	}
	if !IsBridgeCardCallback(parsed.Value) {
		t.Fatal("IsBridgeCardCallback = false, want true")
	}
}

func strPtr(s string) *string { return &s }
