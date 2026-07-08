package impresenter

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/cardrender"
	agentport "github.com/zarazhangrui/lark-coding-agent-bridge/internal/ports/agent"
)

func TestPresentTextModeSendsFinalMarkdownOnly(t *testing.T) {
	ch := &fakeChannel{}
	run := fakeRun([]agentport.AgentEvent{
		textEvent("hello"),
		{Type: agentport.EventDone},
	})

	state, err := Present(context.Background(), Input{
		Run:       run,
		Channel:   ch,
		ChatID:    "oc_chat",
		ReplyMode: ReplyText,
		Options:   SendOptions{ReplyTo: "om_parent"},
	})
	if err != nil {
		t.Fatalf("Present returned error: %v", err)
	}
	if state.Status != "succeeded" {
		t.Fatalf("state = %#v", state)
	}
	if len(ch.messages) != 1 || ch.messages[0].Content.Markdown != "hello" || ch.messages[0].Content.Text != "" {
		t.Fatalf("messages = %#v", ch.messages)
	}
	if len(ch.cards) != 0 {
		t.Fatalf("cards = %#v", ch.cards)
	}
}

func TestPresentCardModeSendsRunCard(t *testing.T) {
	ch := &fakeChannel{}
	run := fakeRun([]agentport.AgentEvent{
		textEvent("hello card"),
		{Type: agentport.EventDone},
	})

	_, err := Present(context.Background(), Input{
		Run:       run,
		Channel:   ch,
		ChatID:    "oc_chat",
		ReplyMode: ReplyCard,
		Options:   SendOptions{ReplyTo: "om_parent"},
	})
	if err != nil {
		t.Fatalf("Present returned error: %v", err)
	}
	if len(ch.cards) != 1 || ch.cards[0].Card["schema"] != "2.0" {
		t.Fatalf("cards = %#v", ch.cards)
	}
	if len(ch.updates) != 1 || ch.updates[0].MessageID != "card-message-1" || ch.updates[0].Card["schema"] != "2.0" {
		t.Fatalf("updates = %#v", ch.updates)
	}
	if !strings.Contains(mustCardBody(ch.updates[0].Card), "hello card") {
		t.Fatalf("card body = %#v", ch.updates[0].Card)
	}
	if len(ch.messages) != 0 {
		t.Fatalf("messages = %#v", ch.messages)
	}
}

func TestPresentCardModeStreamsThrottledUpdates(t *testing.T) {
	ch := &fakeChannel{}
	run := delayedRun{
		{event: textEvent("hello")},
		{after: 15 * time.Millisecond, event: textEvent(" live")},
		{event: agentport.AgentEvent{Type: agentport.EventDone}},
	}

	_, err := Present(context.Background(), Input{
		Run:            run,
		Channel:        ch,
		ChatID:         "oc_chat",
		ReplyMode:      ReplyCard,
		StreamThrottle: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Present returned error: %v", err)
	}
	if len(ch.cards) != 1 {
		t.Fatalf("cards = %#v", ch.cards)
	}
	if len(ch.updates) < 2 {
		t.Fatalf("updates = %#v, want streaming update plus final update", ch.updates)
	}
	if !strings.Contains(mustCardBody(ch.updates[0].Card), "hello live") {
		t.Fatalf("first streaming update body = %#v", ch.updates[0].Card)
	}
	if !strings.Contains(mustCardBody(ch.updates[len(ch.updates)-1].Card), "hello live") {
		t.Fatalf("final update body = %#v", ch.updates[len(ch.updates)-1].Card)
	}
}

func TestPresentCardModeCanHideToolCalls(t *testing.T) {
	ch := &fakeChannel{}
	run := fakeRun([]agentport.AgentEvent{
		toolUseEvent("tool-1", "Bash"),
		toolResultEvent("tool-1", "secret output"),
		textEvent("final answer"),
		{Type: agentport.EventDone},
	})

	state, err := Present(context.Background(), Input{
		Run:           run,
		Channel:       ch,
		ChatID:        "oc_chat",
		ReplyMode:     ReplyCard,
		HideToolCalls: true,
	})
	if err != nil {
		t.Fatalf("Present returned error: %v", err)
	}
	if len(state.Blocks) != 2 {
		t.Fatalf("state blocks = %#v, want original tool and text blocks", state.Blocks)
	}
	if len(ch.updates) != 1 {
		t.Fatalf("updates = %#v", ch.updates)
	}
	body := mustCardBody(ch.updates[0].Card)
	if strings.Contains(body, "Bash") || strings.Contains(body, "secret output") || !strings.Contains(body, "final answer") {
		t.Fatalf("card body = %q", body)
	}
}

func TestPresentTextModeCanHideToolCalls(t *testing.T) {
	ch := &fakeChannel{}
	run := fakeRun([]agentport.AgentEvent{
		toolUseEvent("tool-1", "Bash"),
		toolResultEvent("tool-1", "secret output"),
		textEvent("final answer"),
		{Type: agentport.EventDone},
	})

	_, err := Present(context.Background(), Input{
		Run:           run,
		Channel:       ch,
		ChatID:        "oc_chat",
		ReplyMode:     ReplyText,
		HideToolCalls: true,
	})
	if err != nil {
		t.Fatalf("Present returned error: %v", err)
	}
	if len(ch.messages) != 1 {
		t.Fatalf("messages = %#v", ch.messages)
	}
	body := ch.messages[0].Content.Markdown
	if strings.Contains(body, "Bash") || strings.Contains(body, "secret output") || !strings.Contains(body, "final answer") {
		t.Fatalf("message body = %q", body)
	}
}

func TestPresentCardModeCanDeferAndSendFinalAnswerOnly(t *testing.T) {
	ch := &fakeChannel{}
	run := fakeRun([]agentport.AgentEvent{
		toolUseEvent("tool-1", "Bash"),
		toolResultEvent("tool-1", "secret output"),
		textEvent("final answer"),
		{Type: agentport.EventDone},
	})
	hookCalled := false

	_, err := Present(context.Background(), Input{
		Run:             run,
		Channel:         ch,
		ChatID:          "oc_chat",
		ReplyMode:       ReplyCard,
		DeferUntilDone:  true,
		FinalAnswerOnly: true,
		BeforeFinal: func(context.Context, cardrender.RunState) error {
			hookCalled = true
			if len(ch.cards) != 0 || len(ch.updates) != 0 {
				t.Fatalf("BeforeFinal saw outbound cards=%d updates=%d", len(ch.cards), len(ch.updates))
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Present returned error: %v", err)
	}
	if !hookCalled {
		t.Fatalf("BeforeFinal was not called")
	}
	if len(ch.cards) != 1 || len(ch.updates) != 0 {
		t.Fatalf("cards=%#v updates=%#v", ch.cards, ch.updates)
	}
	body := mustCardBody(ch.cards[0].Card)
	if strings.Contains(body, "Bash") || strings.Contains(body, "secret output") || !strings.Contains(body, "final answer") {
		t.Fatalf("final card body = %q", body)
	}
}

func TestPresentMarkdownModeStreamsThrottledUpdates(t *testing.T) {
	ch := &fakeChannel{}
	run := delayedRun{
		{event: textEvent("hello")},
		{after: 15 * time.Millisecond, event: textEvent(" live")},
		{event: agentport.AgentEvent{Type: agentport.EventDone}},
	}

	_, err := Present(context.Background(), Input{
		Run:            run,
		Channel:        ch,
		ChatID:         "oc_chat",
		ReplyMode:      ReplyMarkdown,
		StreamThrottle: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Present returned error: %v", err)
	}
	if len(ch.messages) != 1 || !strings.Contains(ch.messages[0].Content.Markdown, "hello") {
		t.Fatalf("messages = %#v", ch.messages)
	}
	if len(ch.messageUpdates) < 2 {
		t.Fatalf("message updates = %#v, want streaming update plus final update", ch.messageUpdates)
	}
	if ch.messageUpdates[0].MessageID != "message-1" || !strings.Contains(ch.messageUpdates[0].Content.Markdown, "hello live") {
		t.Fatalf("first message update = %#v", ch.messageUpdates[0])
	}
	if !strings.Contains(ch.messageUpdates[len(ch.messageUpdates)-1].Content.Markdown, "hello live") {
		t.Fatalf("final message update = %#v", ch.messageUpdates[len(ch.messageUpdates)-1])
	}
}

func TestPresentMarkdownModeFallsBackToNewMessageWhenFinalUpdateFails(t *testing.T) {
	ch := &fakeChannel{messageUpdateErr: errors.New("patch denied")}
	run := fakeRun([]agentport.AgentEvent{
		textEvent("final answer"),
		{Type: agentport.EventDone},
	})

	_, err := Present(context.Background(), Input{
		Run:       run,
		Channel:   ch,
		ChatID:    "oc_chat",
		ReplyMode: ReplyMarkdown,
	})
	if err != nil {
		t.Fatalf("Present returned error: %v", err)
	}
	if len(ch.messageUpdates) == 0 {
		t.Fatalf("message updates = %#v, want attempted update", ch.messageUpdates)
	}
	if len(ch.messages) != 2 {
		t.Fatalf("messages = %#v, want initial stream plus fallback final", ch.messages)
	}
	if !strings.Contains(ch.messages[1].Content.Markdown, "final answer") {
		t.Fatalf("fallback final message = %#v", ch.messages[1])
	}
}

func TestPresentMarkdownModeStopsStreamingAfterUpdateFails(t *testing.T) {
	ch := &fakeChannel{messageUpdateErr: errors.New("message cannot be updated")}
	run := delayedRun{
		{event: textEvent("first")},
		{after: 15 * time.Millisecond, event: textEvent(" second")},
		{after: 15 * time.Millisecond, event: textEvent(" third")},
		{event: agentport.AgentEvent{Type: agentport.EventDone}},
	}

	_, err := Present(context.Background(), Input{
		Run:            run,
		Channel:        ch,
		ChatID:         "oc_chat",
		ReplyMode:      ReplyMarkdown,
		StreamThrottle: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Present returned error: %v", err)
	}
	if len(ch.messageUpdates) != 1 {
		t.Fatalf("message updates = %#v, want one failed streaming update", ch.messageUpdates)
	}
	if len(ch.messages) != 2 {
		t.Fatalf("messages = %#v, want initial stream plus fallback final", ch.messages)
	}
	if !strings.Contains(ch.messages[1].Content.Markdown, "first second third") {
		t.Fatalf("fallback final message = %#v", ch.messages[1])
	}
}

func TestPresentStopsRunOnIdleTimeout(t *testing.T) {
	ch := &fakeChannel{}
	run := newIdleBlockingRun(textEvent("partial"))

	state, err := Present(context.Background(), Input{
		Run:         run,
		Channel:     ch,
		ChatID:      "oc_chat",
		ReplyMode:   ReplyText,
		IdleTimeout: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Present returned error: %v", err)
	}
	if state.Status != cardrender.StatusTimeout || state.TimeoutMinutes != 1 {
		t.Fatalf("state = %#v, want timeout", state)
	}
	select {
	case <-run.stopped:
	default:
		t.Fatalf("run was not stopped after idle timeout")
	}
	if len(ch.messages) != 1 || !strings.Contains(ch.messages[0].Content.Markdown, "无响应") {
		t.Fatalf("messages = %#v", ch.messages)
	}
}

func TestPresentIdleTimeoutPausesWhileToolIsInFlight(t *testing.T) {
	ch := &fakeChannel{}
	run := delayedRun{
		{event: toolUseEvent("tool-1", "lark-cli")},
		{after: 30 * time.Millisecond, event: toolResultEvent("tool-1", "ok")},
		{event: textEvent("done")},
		{event: agentport.AgentEvent{Type: agentport.EventDone}},
	}

	state, err := Present(context.Background(), Input{
		Run:         run,
		Channel:     ch,
		ChatID:      "oc_chat",
		ReplyMode:   ReplyText,
		IdleTimeout: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Present returned error: %v", err)
	}
	if state.Status != cardrender.StatusSucceeded {
		t.Fatalf("state = %#v, want succeeded", state)
	}
	if len(ch.messages) != 1 || !strings.Contains(ch.messages[0].Content.Markdown, "done") {
		t.Fatalf("messages = %#v", ch.messages)
	}
}

func TestPresentCardModeSendsInitialStopToken(t *testing.T) {
	ch := &fakeChannel{}

	_, err := Present(context.Background(), Input{
		Run:       fakeRun([]agentport.AgentEvent{{Type: agentport.EventDone}}),
		Channel:   ch,
		ChatID:    "oc_chat",
		ReplyMode: ReplyCard,
		RenderOptions: cardRenderOptions(func(action string) string {
			if action != "stop" {
				t.Fatalf("action = %q, want stop", action)
			}
			return "signed-token"
		}),
	})
	if err != nil {
		t.Fatalf("Present returned error: %v", err)
	}
	if len(ch.cards) != 1 {
		t.Fatalf("cards = %#v", ch.cards)
	}
	if !strings.Contains(flattenCard(ch.cards[0].Card), "signed-token") {
		t.Fatalf("initial card missing token: %#v", ch.cards[0].Card)
	}
}

type fakeRun []agentport.AgentEvent

func (r fakeRun) Events(context.Context) <-chan agentport.AgentEvent {
	out := make(chan agentport.AgentEvent, len(r))
	for _, event := range r {
		out <- event
	}
	close(out)
	return out
}

type delayedRun []delayedEvent

type delayedEvent struct {
	after time.Duration
	event agentport.AgentEvent
}

func (r delayedRun) Events(ctx context.Context) <-chan agentport.AgentEvent {
	out := make(chan agentport.AgentEvent)
	go func() {
		defer close(out)
		for _, item := range r {
			if item.after > 0 {
				timer := time.NewTimer(item.after)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
			select {
			case <-ctx.Done():
				return
			case out <- item.event:
			}
		}
	}()
	return out
}

type idleBlockingRun struct {
	events  chan agentport.AgentEvent
	stopped chan struct{}
	once    sync.Once
}

func newIdleBlockingRun(events ...agentport.AgentEvent) *idleBlockingRun {
	run := &idleBlockingRun{
		events:  make(chan agentport.AgentEvent, len(events)),
		stopped: make(chan struct{}),
	}
	for _, event := range events {
		run.events <- event
	}
	return run
}

func (r *idleBlockingRun) Events(context.Context) <-chan agentport.AgentEvent {
	return r.events
}

func (r *idleBlockingRun) Stop(context.Context) error {
	r.once.Do(func() { close(r.stopped) })
	return nil
}

type fakeChannel struct {
	messages         []SendMessageRequest
	cards            []SendCardRequest
	updates          []UpdateCardRequest
	messageUpdates   []UpdateMessageRequest
	messageUpdateErr error
}

func (c *fakeChannel) SendMessage(_ context.Context, req SendMessageRequest) (SendMessageResult, error) {
	c.messages = append(c.messages, req)
	return SendMessageResult{MessageID: "message-1"}, nil
}

func (c *fakeChannel) SendCard(_ context.Context, req SendCardRequest) (SendCardResult, error) {
	c.cards = append(c.cards, req)
	return SendCardResult{MessageID: "card-message-1"}, nil
}

func (c *fakeChannel) UpdateCard(_ context.Context, req UpdateCardRequest) error {
	c.updates = append(c.updates, req)
	return nil
}

func (c *fakeChannel) UpdateMessage(_ context.Context, req UpdateMessageRequest) error {
	c.messageUpdates = append(c.messageUpdates, req)
	return c.messageUpdateErr
}

func textEvent(delta string) agentport.AgentEvent {
	return agentport.AgentEvent{Type: agentport.EventText, Delta: &delta}
}

func toolUseEvent(id string, name string) agentport.AgentEvent {
	return agentport.AgentEvent{Type: agentport.EventToolUse, ID: &id, Name: &name}
}

func toolResultEvent(id string, output string) agentport.AgentEvent {
	return agentport.AgentEvent{Type: agentport.EventToolResult, ID: &id, Output: &output}
}

func cardRenderOptions(sign func(string) string) cardrender.RenderOptions {
	return cardrender.RenderOptions{SignCallback: sign}
}

func mustCardBody(card map[string]any) string {
	body, _ := card["body"].(map[string]any)
	elements, _ := body["elements"].([]any)
	return strings.TrimSpace(strings.Join(flattenStrings(elements), "\n"))
}

func flattenCard(card map[string]any) string {
	return strings.Join(flattenStrings(card), "\n")
}

func flattenStrings(value any) []string {
	switch typed := value.(type) {
	case []any:
		var out []string
		for _, item := range typed {
			out = append(out, flattenStrings(item)...)
		}
		return out
	case map[string]any:
		var out []string
		for _, item := range typed {
			out = append(out, flattenStrings(item)...)
		}
		return out
	case string:
		return []string{typed}
	default:
		return nil
	}
}
