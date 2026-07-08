package cotpresenter

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	agentport "github.com/zarazhangrui/lark-coding-agent-bridge/internal/ports/agent"
)

func TestConsumeEventsPublishesBriefToolSummaries(t *testing.T) {
	client := &fakeCOTClient{}
	publisher := NewPublisher(PublisherOptions{
		Client:          client,
		ChatID:          "oc_chat",
		OriginMessageID: "om_origin",
		RunID:           "run-1",
		Scope:           "oc_chat:omt_topic",
		InputPreview:    "draw a bear",
		UpdateThrottle:  time.Hour,
		Now:             fixedCOTNow,
	})
	if !publisher.Start(context.Background()) {
		t.Fatalf("Start returned false")
	}

	err := ConsumeEvents(context.Background(), cotEventStream(
		textEvent("我会先生成图片。"),
		toolUseEvent("tool-1", "command_execution", map[string]any{"command": "echo bear"}),
		toolResultEvent("tool-1", "ok", false),
		textEvent("图片已经生成。"),
		agentport.AgentEvent{Type: agentport.EventDone, TerminationReason: agentport.TerminationNormal},
	), publisher, ModeBrief)
	if err != nil {
		t.Fatalf("ConsumeEvents returned error: %v", err)
	}

	events := client.allEvents()
	types := cotEventTypes(events)
	assertCOTContains(t, types, "TEXT_MESSAGE_START")
	assertCOTContains(t, types, "TEXT_MESSAGE_CONTENT")
	assertCOTContains(t, types, "TEXT_MESSAGE_END")
	assertCOTContains(t, types, "TOOL_CALL_START")
	assertCOTContains(t, types, "TOOL_CALL_RESULT")
	assertCOTNotContains(t, types, "TOOL_CALL_ARGS")

	var deltas []string
	for _, event := range events {
		if event.EventType != "TEXT_MESSAGE_CONTENT" {
			continue
		}
		var content map[string]any
		if err := json.Unmarshal([]byte(event.Content), &content); err != nil {
			t.Fatalf("content is not json: %v", err)
		}
		deltas = append(deltas, content["delta"].(string))
	}
	if strings.Join(deltas, "|") != "我会先生成图片。|图片已经生成。" {
		t.Fatalf("text deltas = %#v", deltas)
	}
	if got := client.completedReasons(); len(got) != 1 || got[0] != "done" {
		t.Fatalf("completed reasons = %#v", got)
	}
}

func TestConsumeEventsPublishesDetailedToolArgsAndOutput(t *testing.T) {
	client := &fakeCOTClient{}
	publisher := NewPublisher(PublisherOptions{
		Client:         client,
		ChatID:         "oc_chat",
		RunID:          "run-2",
		Scope:          "oc_chat",
		InputPreview:   "run",
		UpdateThrottle: time.Hour,
		Now:            fixedCOTNow,
	})
	if !publisher.Start(context.Background()) {
		t.Fatalf("Start returned false")
	}

	err := ConsumeEvents(context.Background(), cotEventStream(
		toolUseEvent("tool-1", "command_execution", map[string]any{"command": "pwd"}),
		toolResultEvent("tool-1", "workspace", false),
		agentport.AgentEvent{Type: agentport.EventDone, TerminationReason: agentport.TerminationNormal},
	), publisher, ModeDetailed)
	if err != nil {
		t.Fatalf("ConsumeEvents returned error: %v", err)
	}

	events := client.allEvents()
	assertCOTContains(t, cotEventTypes(events), "TOOL_CALL_ARGS")
	result := findCOTEvent(events, "TOOL_CALL_RESULT")
	var content map[string]any
	if err := json.Unmarshal([]byte(result.Content), &content); err != nil {
		t.Fatalf("tool result content is not json: %v", err)
	}
	if content["content"] != "workspace" {
		t.Fatalf("tool result content = %#v", content)
	}
}

func TestConsumeEventsMarksPublisherDegradedWhenUpdateFails(t *testing.T) {
	client := &fakeCOTClient{updateErr: errors.New("field validation failed")}
	publisher := NewPublisher(PublisherOptions{
		Client:         client,
		ChatID:         "oc_chat",
		RunID:          "run-degraded",
		Scope:          "oc_chat",
		InputPreview:   "run",
		UpdateThrottle: time.Hour,
		Now:            fixedCOTNow,
	})
	if !publisher.Start(context.Background()) {
		t.Fatalf("Start returned false")
	}

	err := ConsumeEvents(context.Background(), cotEventStream(
		textEvent("working"),
		agentport.AgentEvent{Type: agentport.EventDone, TerminationReason: agentport.TerminationNormal},
	), publisher, ModeBrief)
	if err != nil {
		t.Fatalf("ConsumeEvents returned error: %v", err)
	}
	if !publisher.Disabled() {
		t.Fatalf("publisher disabled = false, want true")
	}
	if got := publisher.DegradedReason(); got != "field validation failed" {
		t.Fatalf("degraded reason = %q", got)
	}
	if got := client.completedReasons(); len(got) != 0 {
		t.Fatalf("completed reasons = %#v, want none", got)
	}
}

func TestPublisherIgnoresCompleteFailure(t *testing.T) {
	client := &fakeCOTClient{completeErr: errors.New("complete failed")}
	publisher := NewPublisher(PublisherOptions{
		Client:         client,
		ChatID:         "oc_chat",
		RunID:          "run-complete",
		Scope:          "oc_chat",
		UpdateThrottle: time.Hour,
		Now:            fixedCOTNow,
	})
	if !publisher.Start(context.Background()) {
		t.Fatalf("Start returned false")
	}
	if err := publisher.Finish(context.Background(), "done"); err != nil {
		t.Fatalf("Finish returned error: %v", err)
	}
}

type fakeCOTClient struct {
	createErr   error
	updateErr   error
	completeErr error

	creates   []CreateRequest
	updates   []UpdateRequest
	completes []CompleteRequest
}

func (c *fakeCOTClient) CreateMessageCOT(_ context.Context, req CreateRequest) (Ref, error) {
	if c.createErr != nil {
		return Ref{}, c.createErr
	}
	c.creates = append(c.creates, req)
	return Ref{COTID: "cot_fake", MessageID: "om_cot_fake"}, nil
}

func (c *fakeCOTClient) UpdateMessageCOT(_ context.Context, req UpdateRequest) error {
	if c.updateErr != nil {
		return c.updateErr
	}
	copied := req
	copied.Events = append([]Event(nil), req.Events...)
	c.updates = append(c.updates, copied)
	return nil
}

func (c *fakeCOTClient) CompleteMessageCOT(_ context.Context, req CompleteRequest) error {
	if c.completeErr != nil {
		return c.completeErr
	}
	c.completes = append(c.completes, req)
	return nil
}

func (c *fakeCOTClient) allEvents() []Event {
	var out []Event
	for _, update := range c.updates {
		out = append(out, update.Events...)
	}
	return out
}

func (c *fakeCOTClient) completedReasons() []string {
	out := make([]string, 0, len(c.completes))
	for _, complete := range c.completes {
		out = append(out, complete.Reason)
	}
	return out
}

func cotEventStream(events ...agentport.AgentEvent) <-chan agentport.AgentEvent {
	out := make(chan agentport.AgentEvent, len(events))
	for _, event := range events {
		out <- event
	}
	close(out)
	return out
}

func textEvent(delta string) agentport.AgentEvent {
	return agentport.AgentEvent{Type: agentport.EventText, Delta: &delta}
}

func toolUseEvent(id string, name string, input any) agentport.AgentEvent {
	return agentport.AgentEvent{Type: agentport.EventToolUse, ID: &id, Name: &name, Input: input}
}

func toolResultEvent(id string, output string, isError bool) agentport.AgentEvent {
	return agentport.AgentEvent{Type: agentport.EventToolResult, ID: &id, Output: &output, IsError: &isError}
}

func cotEventTypes(events []Event) []string {
	out := make([]string, 0, len(events))
	for _, event := range events {
		out = append(out, event.EventType)
	}
	return out
}

func findCOTEvent(events []Event, eventType string) Event {
	for _, event := range events {
		if event.EventType == eventType {
			return event
		}
	}
	return Event{}
}

func assertCOTContains(t *testing.T, values []string, want string) {
	t.Helper()
	for _, value := range values {
		if value == want {
			return
		}
	}
	t.Fatalf("%q not found in %#v", want, values)
}

func assertCOTNotContains(t *testing.T, values []string, want string) {
	t.Helper()
	for _, value := range values {
		if value == want {
			t.Fatalf("%q unexpectedly found in %#v", want, values)
		}
	}
}

func fixedCOTNow() time.Time {
	return time.UnixMilli(1234)
}
