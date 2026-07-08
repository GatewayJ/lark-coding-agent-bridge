package cardrender

import (
	"strings"
	"testing"
	"time"
)

func TestReduceRunStateTracksCodexEventsAndMetadata(t *testing.T) {
	start := time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC)
	state := NewRunState(RunStateInput{
		RunID:     "run-1",
		Scope:     "oc_group:th_topic",
		CWD:       "/repo",
		SessionID: "session-abcdef",
		ThreadID:  "thread-123456",
		StartedAt: start,
	})

	state = Reduce(state, Event{Type: EventThinking, Delta: str("checking"), At: start})
	state = Reduce(state, Event{Type: EventText, Delta: str("answer "), At: start.Add(time.Second)})
	state = Reduce(state, Event{Type: EventText, Delta: str("body"), At: start.Add(2 * time.Second)})
	state = Reduce(state, Event{
		Type:  EventToolUse,
		ID:    str("tool-1"),
		Name:  str("Bash"),
		Input: map[string]any{"command": "git status --short"},
	})
	state = Reduce(state, Event{
		Type:    EventToolResult,
		ID:      str("tool-1"),
		Output:  str("clean"),
		IsError: boolPtr(false),
	})
	state = Reduce(state, Event{
		Type:                  EventUsage,
		InputTokens:           intPtr(10),
		OutputTokens:          intPtr(5),
		CachedInputTokens:     intPtr(2),
		ReasoningOutputTokens: intPtr(1),
	})
	state = Reduce(state, Event{Type: EventDone, TerminationReason: TerminationNormal, At: start.Add(65 * time.Second)})

	if state.Status != StatusSucceeded {
		t.Fatalf("status = %s, want %s", state.Status, StatusSucceeded)
	}
	if state.Blocks[0].Content != "answer body" || state.Blocks[0].Streaming {
		t.Fatalf("text block not merged/finalized: %#v", state.Blocks[0])
	}
	if got := state.Usage.TotalTokens(); got != 18 {
		t.Fatalf("total tokens = %d, want 18", got)
	}

	card := RenderCard(state, RenderOptions{ShowMetadata: true})
	serialized := renderCardText(card)
	for _, want := range []string{
		"🧭 **scope**: `oc_group:th_topic`",
		"📁 **cwd**: `/repo`",
		"🔗 **session**: `session-…`",
		"⏱ **elapsed**: 1m05s",
		"🪙 **usage**: in 10, out 5, cached 2, reasoning 1",
		"✅ **Bash** — git status --short",
	} {
		if !strings.Contains(serialized, want) {
			t.Fatalf("card missing %q in:\n%s", want, serialized)
		}
	}
	if card.Summary != "已完成" || card.Streaming {
		t.Fatalf("card summary/streaming = %q/%v", card.Summary, card.Streaming)
	}
}

func TestRenderRunningCardInjectsSignedStopAction(t *testing.T) {
	state := NewRunState(RunStateInput{Scope: "oc_group"})
	state = Reduce(state, Event{Type: EventThinking, Delta: str("work")})

	card := RenderCard(state, RenderOptions{
		SignCallback: func(action string) string {
			if action != "stop" {
				t.Fatalf("signed action = %q, want stop", action)
			}
			return "token-for-stop"
		},
	})

	if card.Summary != "思考中" || !card.Streaming {
		t.Fatalf("summary/streaming = %q/%v", card.Summary, card.Streaming)
	}
	if len(card.Actions) != 1 {
		t.Fatalf("actions = %d, want 1", len(card.Actions))
	}
	value := card.Actions[0].Value
	if value["cmd"] != "stop" || value["__bridge_cb"] != true || value["bridge_token"] != "token-for-stop" {
		t.Fatalf("unexpected stop value: %#v", value)
	}
}

func TestRenderRunningCardSkipsSignedMarkerWhenSigningReturnsEmpty(t *testing.T) {
	state := NewRunState(RunStateInput{Scope: "oc_group"})
	state = Reduce(state, Event{Type: EventThinking, Delta: str("work")})

	card := RenderCard(state, RenderOptions{SignCallback: func(string) string { return "" }})

	if len(card.Actions) != 1 {
		t.Fatalf("actions = %d, want 1", len(card.Actions))
	}
	value := card.Actions[0].Value
	if value["cmd"] != "stop" || value["__bridge_cb"] != nil || value["bridge_token"] != nil {
		t.Fatalf("unexpected unsigned stop value: %#v", value)
	}
}

func TestRenderTerminalTextForErrorTimeoutAndCancel(t *testing.T) {
	failed := Reduce(NewRunState(RunStateInput{}), Event{
		Type:              EventError,
		Message:           str("process failed"),
		TerminationReason: TerminationFailed,
	})
	if got := RenderText(failed).Content; !strings.Contains(got, "⚠️ agent 失败:process failed") {
		t.Fatalf("failed text = %q", got)
	}

	timeout := MarkTimeout(Reduce(NewRunState(RunStateInput{}), Event{Type: EventText, Delta: str("partial")}), 15)
	if got := RenderText(timeout).Content; !strings.Contains(got, "_⏱ 15 分钟无响应,已自动终止_") {
		t.Fatalf("timeout text = %q", got)
	}

	cancelled := MarkCancelled(Reduce(NewRunState(RunStateInput{}), Event{Type: EventText, Delta: str("partial")}))
	if got := RenderText(cancelled).Content; !strings.Contains(got, "_⏹ 已被中断_") {
		t.Fatalf("cancelled text = %q", got)
	}
}

func TestRenderTextOmitsCardOnlyMetadata(t *testing.T) {
	state := NewRunState(RunStateInput{
		Scope:     "oc_group:th_topic",
		CWD:       "/repo",
		SessionID: "session-abcdef",
	})
	state = Reduce(state, Event{Type: EventText, Delta: str("answer")})
	state = Reduce(state, Event{
		Type:  EventToolUse,
		ID:    str("tool-1"),
		Name:  str("Bash"),
		Input: map[string]any{"command": "git status --short"},
	})

	text := RenderText(state).Content
	for _, forbidden := range []string{"scope", "/repo", "session-"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("RenderText leaked metadata %q in:\n%s", forbidden, text)
		}
	}
	for _, want := range []string{"answer", "> ⏳ **Bash** — git status --short"} {
		if !strings.Contains(text, want) {
			t.Fatalf("RenderText missing %q in:\n%s", want, text)
		}
	}
}

func TestTerminationReasonMapping(t *testing.T) {
	cases := []struct {
		reason TerminationReason
		want   RunStatus
	}{
		{TerminationNormal, StatusSucceeded},
		{TerminationInterrupted, StatusCancelled},
		{TerminationTimeout, StatusTimeout},
		{TerminationFailed, StatusFailed},
	}
	for _, tc := range cases {
		got := Reduce(NewRunState(RunStateInput{}), Event{
			Type:              EventDone,
			TerminationReason: tc.reason,
		}).Status
		if got != tc.want {
			t.Fatalf("done %q status = %s, want %s", tc.reason, got, tc.want)
		}
	}
}

func TestPublicRunStatusesAreRenderable(t *testing.T) {
	cases := []struct {
		name    string
		state   RunState
		status  RunStatus
		summary string
	}{
		{
			name:    "queued",
			state:   NewRunState(RunStateInput{}),
			status:  StatusQueued,
			summary: "排队中",
		},
		{
			name:    "running",
			state:   Reduce(NewRunState(RunStateInput{}), Event{Type: EventText, Delta: str("hi")}),
			status:  StatusRunning,
			summary: "正在输出",
		},
		{
			name:    "succeeded",
			state:   Reduce(NewRunState(RunStateInput{}), Event{Type: EventDone, TerminationReason: TerminationNormal}),
			status:  StatusSucceeded,
			summary: "已完成",
		},
		{
			name: "failed",
			state: Reduce(NewRunState(RunStateInput{}), Event{
				Type:              EventError,
				Message:           str("boom"),
				TerminationReason: TerminationFailed,
			}),
			status:  StatusFailed,
			summary: "出错",
		},
		{
			name:    "cancelled",
			state:   MarkCancelled(NewRunState(RunStateInput{})),
			status:  StatusCancelled,
			summary: "已中断",
		},
		{
			name:    "timeout",
			state:   MarkTimeout(NewRunState(RunStateInput{}), 10),
			status:  StatusTimeout,
			summary: "已超时",
		},
	}
	for _, tc := range cases {
		card := RenderCard(tc.state, RenderOptions{})
		if card.Status != tc.status || card.Summary != tc.summary {
			t.Fatalf("%s card = %s/%q, want %s/%q", tc.name, card.Status, card.Summary, tc.status, tc.summary)
		}
	}
}

func renderCardText(card CardView) string {
	var b strings.Builder
	b.WriteString(card.Summary)
	for _, element := range card.Elements {
		b.WriteString("\n")
		b.WriteString(element.Text)
		if element.Panel != nil {
			b.WriteString("\n")
			b.WriteString(element.Panel.Title)
			b.WriteString("\n")
			b.WriteString(element.Panel.Body)
		}
	}
	return b.String()
}

func str(s string) *string { return &s }

func intPtr(v int) *int { return &v }

func boolPtr(v bool) *bool { return &v }
