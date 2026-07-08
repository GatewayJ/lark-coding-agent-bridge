package claudecli

import (
	"encoding/json"
	"math"

	agentport "github.com/zarazhangrui/lark-coding-agent-bridge/internal/ports/agent"
)

type ClaudeFinishReason string

const (
	ClaudeFinishInterrupted ClaudeFinishReason = "interrupted"
	ClaudeFinishTimeout     ClaudeFinishReason = "timeout"
)

type StreamTranslator struct {
	sessionID string
	terminal  bool
}

func NewStreamTranslator() *StreamTranslator {
	return &StreamTranslator{}
}

func TranslateEvent(raw any) []agentport.AgentEvent {
	return NewStreamTranslator().Translate(raw)
}

func (t *StreamTranslator) TranslateLine(line []byte) ([]agentport.AgentEvent, error) {
	var raw any
	if err := json.Unmarshal(line, &raw); err != nil {
		return nil, err
	}
	return t.Translate(raw), nil
}

func (t *StreamTranslator) Translate(raw any) []agentport.AgentEvent {
	record, ok := recordValue(raw)
	if !ok {
		return nil
	}
	eventType, _ := stringValue(record["type"])
	switch eventType {
	case "system":
		subtype, _ := stringValue(record["subtype"])
		if subtype != "init" {
			return nil
		}
		return t.translateSystemInit(record)
	case "assistant":
		return translateAssistant(record)
	case "user":
		return translateUser(record)
	case "result":
		return t.translateResult(record)
	default:
		return nil
	}
}

func (t *StreamTranslator) Finish(reason ClaudeFinishReason) []agentport.AgentEvent {
	if t.terminal {
		return nil
	}
	t.terminal = true
	switch reason {
	case ClaudeFinishInterrupted:
		return []agentport.AgentEvent{doneEvent(t.sessionID, agentport.TerminationInterrupted)}
	case ClaudeFinishTimeout:
		return []agentport.AgentEvent{doneEvent(t.sessionID, agentport.TerminationTimeout)}
	default:
		return nil
	}
}

func (t *StreamTranslator) translateSystemInit(raw map[string]any) []agentport.AgentEvent {
	event := agentport.AgentEvent{Type: agentport.EventSystem}
	if sessionID, ok := stringValue(raw["session_id"]); ok {
		t.sessionID = sessionID
		event.SessionID = stringPtr(sessionID)
	}
	if cwd, ok := stringValue(raw["cwd"]); ok {
		event.CWD = stringPtr(cwd)
	}
	if model, ok := stringValue(raw["model"]); ok {
		event.Model = stringPtr(model)
	}
	return []agentport.AgentEvent{event}
}

func translateAssistant(raw map[string]any) []agentport.AgentEvent {
	content := messageContent(raw)
	if len(content) == 0 {
		return nil
	}
	events := make([]agentport.AgentEvent, 0, len(content))
	for _, item := range content {
		block, ok := recordValue(item)
		if !ok {
			continue
		}
		blockType, _ := stringValue(block["type"])
		switch blockType {
		case "text":
			if text, ok := stringValue(block["text"]); ok && text != "" {
				events = append(events, agentport.AgentEvent{
					Type:  agentport.EventText,
					Delta: stringPtr(text),
				})
			}
		case "thinking":
			if thinking, ok := stringValue(block["thinking"]); ok && thinking != "" {
				events = append(events, agentport.AgentEvent{
					Type:  agentport.EventThinking,
					Delta: stringPtr(thinking),
				})
			}
		case "tool_use":
			id, idOK := stringValue(block["id"])
			name, nameOK := stringValue(block["name"])
			if idOK && id != "" && nameOK && name != "" {
				events = append(events, agentport.AgentEvent{
					Type:  agentport.EventToolUse,
					ID:    stringPtr(id),
					Name:  stringPtr(name),
					Input: block["input"],
				})
			}
		}
	}
	return events
}

func translateUser(raw map[string]any) []agentport.AgentEvent {
	content := messageContent(raw)
	if len(content) == 0 {
		return nil
	}
	events := make([]agentport.AgentEvent, 0, len(content))
	for _, item := range content {
		block, ok := recordValue(item)
		if !ok {
			continue
		}
		blockType, _ := stringValue(block["type"])
		if blockType != "tool_result" {
			continue
		}
		toolUseID, ok := stringValue(block["tool_use_id"])
		if !ok || toolUseID == "" {
			continue
		}
		events = append(events, agentport.AgentEvent{
			Type:    agentport.EventToolResult,
			ID:      stringPtr(toolUseID),
			Output:  toolResultOutput(block),
			IsError: boolPtr(block["is_error"] == true),
		})
	}
	return events
}

func (t *StreamTranslator) translateResult(raw map[string]any) []agentport.AgentEvent {
	t.terminal = true
	events := make([]agentport.AgentEvent, 0, 2)
	if usage, ok := recordValue(raw["usage"]); ok {
		event := agentport.AgentEvent{Type: agentport.EventUsage}
		if value, ok := intValue(usage["input_tokens"]); ok {
			event.InputTokens = intPtr(value)
		}
		if value, ok := intValue(usage["output_tokens"]); ok {
			event.OutputTokens = intPtr(value)
		}
		if value, ok := intValue(usage["cache_read_input_tokens"]); ok {
			event.CachedInputTokens = intPtr(value)
		}
		if value, ok := floatValue(raw["total_cost_usd"]); ok {
			event.CostUSD = floatPtr(value)
		}
		events = append(events, event)
	}
	sessionID, ok := stringValue(raw["session_id"])
	if ok {
		t.sessionID = sessionID
	}
	events = append(events, doneEvent(t.sessionID, agentport.TerminationNormal))
	return events
}

func messageContent(raw map[string]any) []any {
	message, ok := recordValue(raw["message"])
	if !ok {
		return nil
	}
	content, ok := arrayValue(message["content"])
	if !ok {
		return nil
	}
	return content
}

func toolResultOutput(block map[string]any) *string {
	content, exists := block["content"]
	if text, ok := stringValue(content); ok {
		return stringPtr(text)
	}
	if !exists {
		return nil
	}
	bytes, err := json.Marshal(content)
	if err != nil {
		return nil
	}
	return stringPtr(string(bytes))
}

func doneEvent(sessionID string, reason agentport.TerminationReason) agentport.AgentEvent {
	event := agentport.AgentEvent{
		Type:              agentport.EventDone,
		TerminationReason: reason,
	}
	if sessionID != "" {
		event.SessionID = stringPtr(sessionID)
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

func arrayValue(value any) ([]any, bool) {
	array, ok := value.([]any)
	return array, ok
}

func stringValue(value any) (string, bool) {
	valueString, ok := value.(string)
	return valueString, ok
}

func intValue(value any) (int, bool) {
	switch value := value.(type) {
	case int:
		return value, true
	case int64:
		if int64(int(value)) == value {
			return int(value), true
		}
	case float64:
		maxInt := int(^uint(0) >> 1)
		minInt := -maxInt - 1
		if math.Trunc(value) == value && value <= float64(maxInt) && value >= float64(minInt) {
			return int(value), true
		}
	}
	return 0, false
}

func floatValue(value any) (float64, bool) {
	switch value := value.(type) {
	case float64:
		return value, true
	case float32:
		return float64(value), true
	case int:
		return float64(value), true
	case int64:
		return float64(value), true
	default:
		return 0, false
	}
}

func stringPtr(value string) *string {
	return &value
}

func intPtr(value int) *int {
	return &value
}

func floatPtr(value float64) *float64 {
	return &value
}

func boolPtr(value bool) *bool {
	return &value
}
