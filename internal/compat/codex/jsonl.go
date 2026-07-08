package codex

import (
	"encoding/json"
	"fmt"
	"math"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/ports/agent"
)

type CodexFinishReason string

const (
	CodexFinishFailed      CodexFinishReason = "failed"
	CodexFinishInterrupted CodexFinishReason = "interrupted"
	CodexFinishTimeout     CodexFinishReason = "timeout"
)

type ProtocolDriftState struct {
	UnknownEvents int `json:"unknownEvents"`
	Anomalies     int `json:"anomalies"`
}

type JSONLReporter func(event string, fields map[string]any)

type CodexJsonlTranslator struct {
	threadID             string
	terminal             bool
	lastNonTerminalError string
	startedItems         map[string]struct{}
	drift                ProtocolDriftState
	reporter             JSONLReporter
}

type JsonlTranslator = CodexJsonlTranslator

func NewCodexJsonlTranslator() *CodexJsonlTranslator {
	return NewCodexJsonlTranslatorWithReporter(nil)
}

func NewCodexJsonlTranslatorWithReporter(reporter JSONLReporter) *CodexJsonlTranslator {
	return &CodexJsonlTranslator{
		startedItems: make(map[string]struct{}),
		reporter:     reporter,
	}
}

func NewJsonlTranslator() *JsonlTranslator {
	return NewCodexJsonlTranslator()
}

func (t *CodexJsonlTranslator) TranslateLine(line []byte) ([]agent.AgentEvent, error) {
	var raw any
	if err := json.Unmarshal(line, &raw); err != nil {
		return nil, err
	}
	return t.Translate(raw), nil
}

func (t *CodexJsonlTranslator) Translate(raw any) []agent.AgentEvent {
	if t.terminal {
		return nil
	}

	record, ok := recordValue(raw)
	if !ok {
		t.drift.Anomalies++
		return nil
	}
	eventType, ok := stringValue(record["type"])
	if !ok {
		t.drift.Anomalies++
		return nil
	}

	switch eventType {
	case "thread.started":
		return t.translateThreadStarted(record)
	case "turn.started":
		return nil
	case "item.started":
		return t.translateItemStarted(record)
	case "item.completed":
		return t.translateItemCompleted(record)
	case "agent_message":
		return t.translateAgentMessage(record)
	case "turn.completed":
		return t.translateTurnCompleted(record)
	case "turn.failed":
		return t.translateTerminalError(record, "codex turn failed")
	case "error":
		return t.translateNonTerminalError(record, "codex error")
	default:
		t.drift.UnknownEvents++
		t.report("jsonl.unknown_event", map[string]any{"eventType": eventType})
		return nil
	}
}

func (t *CodexJsonlTranslator) Finish(reason CodexFinishReason) []agent.AgentEvent {
	if t.terminal {
		return nil
	}
	t.terminal = true

	switch reason {
	case CodexFinishInterrupted:
		return []agent.AgentEvent{doneEvent(t.threadID, agent.TerminationInterrupted)}
	case CodexFinishTimeout:
		return []agent.AgentEvent{doneEvent(t.threadID, agent.TerminationTimeout)}
	default:
		detail := ""
		if t.lastNonTerminalError != "" {
			detail = ": " + t.lastNonTerminalError
		}
		return []agent.AgentEvent{{
			Type:              agent.EventError,
			Message:           stringPtr(truncate("codex stream ended before a terminal event"+detail, 4096)),
			TerminationReason: agent.TerminationFailed,
		}}
	}
}

func (t *CodexJsonlTranslator) ProtocolDrift() ProtocolDriftState {
	return t.drift
}

func (t *CodexJsonlTranslator) TerminalEmitted() bool {
	return t.terminal
}

func (t *CodexJsonlTranslator) translateThreadStarted(raw map[string]any) []agent.AgentEvent {
	threadID, ok := stringValue(raw["thread_id"])
	if !ok {
		threadID, ok = stringValue(raw["threadId"])
	}
	if !ok || threadID == "" {
		t.drift.Anomalies++
		return nil
	}
	t.threadID = threadID
	return []agent.AgentEvent{{
		Type:     agent.EventSystem,
		ThreadID: stringPtr(threadID),
	}}
}

func (t *CodexJsonlTranslator) translateItemStarted(raw map[string]any) []agent.AgentEvent {
	item, ok := recordValue(raw["item"])
	if !ok || item["type"] != "command_execution" {
		return nil
	}
	id, ok := stringValue(item["id"])
	if !ok || id == "" {
		t.drift.Anomalies++
		return nil
	}
	t.startedItems[id] = struct{}{}

	command, _ := stringValue(item["command"])
	return []agent.AgentEvent{{
		Type:  agent.EventToolUse,
		ID:    stringPtr(id),
		Name:  stringPtr("command_execution"),
		Input: map[string]any{"command": command},
	}}
}

func (t *CodexJsonlTranslator) translateItemCompleted(raw map[string]any) []agent.AgentEvent {
	item, ok := recordValue(raw["item"])
	if !ok {
		return nil
	}

	switch item["type"] {
	case "agent_message":
		message, ok := firstString(item["text"], item["message"])
		if !ok || message == "" {
			return nil
		}
		return []agent.AgentEvent{{
			Type:  agent.EventText,
			Delta: stringPtr(message),
		}}
	case "command_execution":
		return t.translateCommandExecutionCompleted(item)
	default:
		return nil
	}
}

func (t *CodexJsonlTranslator) translateCommandExecutionCompleted(item map[string]any) []agent.AgentEvent {
	id, ok := stringValue(item["id"])
	if !ok || id == "" {
		t.drift.Anomalies++
		return nil
	}
	if _, ok := t.startedItems[id]; !ok {
		t.drift.Anomalies++
	}
	delete(t.startedItems, id)

	output, _ := firstString(item["output"], item["aggregated_output"], item["stdout"])
	exitCode, hasExitCode := numberValue(item["exit_code"])
	isError := hasExitCode && exitCode != 0
	return []agent.AgentEvent{{
		Type:    agent.EventToolResult,
		ID:      stringPtr(id),
		Output:  stringPtr(output),
		IsError: boolPtr(isError),
	}}
}

func (t *CodexJsonlTranslator) translateAgentMessage(raw map[string]any) []agent.AgentEvent {
	message, ok := firstString(raw["message"], raw["text"])
	if !ok || message == "" {
		return nil
	}
	return []agent.AgentEvent{{
		Type:  agent.EventText,
		Delta: stringPtr(message),
	}}
}

func (t *CodexJsonlTranslator) translateTurnCompleted(raw map[string]any) []agent.AgentEvent {
	t.terminal = true
	events := make([]agent.AgentEvent, 0, 2)
	if usage, ok := recordValue(raw["usage"]); ok {
		events = append(events, usageEvent(usage))
	}
	events = append(events, doneEvent(t.threadID, agent.TerminationNormal))
	return events
}

func (t *CodexJsonlTranslator) translateTerminalError(raw map[string]any, fallback string) []agent.AgentEvent {
	t.terminal = true
	return []agent.AgentEvent{{
		Type:              agent.EventError,
		Message:           stringPtr(truncate(errorMessage(raw, fallback), 4096)),
		TerminationReason: agent.TerminationFailed,
	}}
}

func (t *CodexJsonlTranslator) translateNonTerminalError(raw map[string]any, fallback string) []agent.AgentEvent {
	t.lastNonTerminalError = errorMessage(raw, fallback)
	t.report("jsonl.error_event", map[string]any{"message": truncate(t.lastNonTerminalError, 500)})
	return nil
}

func (t *CodexJsonlTranslator) report(event string, fields map[string]any) {
	if t.reporter == nil {
		return
	}
	t.reporter(event, fields)
}

func usageEvent(usage map[string]any) agent.AgentEvent {
	event := agent.AgentEvent{Type: agent.EventUsage}
	if value, ok := numberValue(usage["input_tokens"]); ok {
		event.InputTokens = intPtr(value)
	} else if value, ok := numberValue(usage["inputTokens"]); ok {
		event.InputTokens = intPtr(value)
	}
	if value, ok := numberValue(usage["output_tokens"]); ok {
		event.OutputTokens = intPtr(value)
	} else if value, ok := numberValue(usage["outputTokens"]); ok {
		event.OutputTokens = intPtr(value)
	}
	if value, ok := numberValue(usage["cached_input_tokens"]); ok {
		event.CachedInputTokens = intPtr(value)
	} else if value, ok := numberValue(usage["cachedInputTokens"]); ok {
		event.CachedInputTokens = intPtr(value)
	}
	if value, ok := numberValue(usage["reasoning_output_tokens"]); ok {
		event.ReasoningOutputTokens = intPtr(value)
	} else if value, ok := numberValue(usage["reasoningOutputTokens"]); ok {
		event.ReasoningOutputTokens = intPtr(value)
	}
	return event
}

func doneEvent(threadID string, reason agent.TerminationReason) agent.AgentEvent {
	event := agent.AgentEvent{
		Type:              agent.EventDone,
		TerminationReason: reason,
	}
	if threadID != "" {
		event.ThreadID = stringPtr(threadID)
	}
	return event
}

func recordValue(value any) (map[string]any, bool) {
	record, ok := value.(map[string]any)
	if ok && record != nil {
		return record, true
	}
	return nil, false
}

func stringValue(value any) (string, bool) {
	valueString, ok := value.(string)
	return valueString, ok
}

func firstString(values ...any) (string, bool) {
	for _, value := range values {
		if valueString, ok := stringValue(value); ok {
			return valueString, true
		}
	}
	return "", false
}

func numberValue(value any) (int, bool) {
	switch value := value.(type) {
	case int:
		return value, true
	case int8:
		return int(value), true
	case int16:
		return int(value), true
	case int32:
		return int(value), true
	case int64:
		if int64(int(value)) == value {
			return int(value), true
		}
	case uint:
		if uint(int(value)) == value {
			return int(value), true
		}
	case uint8:
		return int(value), true
	case uint16:
		return int(value), true
	case uint32:
		if uint32(int(value)) == value {
			return int(value), true
		}
	case uint64:
		if uint64(int(value)) == value {
			return int(value), true
		}
	case float64:
		if math.Trunc(value) == value && value >= math.MinInt && value <= math.MaxInt {
			return int(value), true
		}
	case float32:
		floatValue := float64(value)
		if math.Trunc(floatValue) == floatValue && floatValue >= math.MinInt && floatValue <= math.MaxInt {
			return int(value), true
		}
	case json.Number:
		var intValue int64
		if _, err := fmt.Sscan(string(value), &intValue); err == nil && int64(int(intValue)) == intValue {
			return int(intValue), true
		}
	}
	return 0, false
}

func errorMessage(raw map[string]any, fallback string) string {
	if message, ok := stringValue(raw["message"]); ok {
		return message
	}
	if nested, ok := recordValue(raw["error"]); ok {
		if message, ok := stringValue(nested["message"]); ok {
			return message
		}
	}
	if message, ok := stringValue(raw["error"]); ok {
		return message
	}
	return fallback
}

func truncate(value string, max int) string {
	if len(value) > max {
		return value[:max]
	}
	return value
}

func stringPtr(value string) *string {
	return &value
}

func boolPtr(value bool) *bool {
	return &value
}

func intPtr(value int) *int {
	return &value
}
