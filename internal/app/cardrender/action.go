package cardrender

const (
	bridgeCallbackMarker       = "__bridge_cb"
	legacyClaudeCallbackMarker = "__claude_cb"
	formValueKey               = "form_value"
)

type ActionParseInput struct {
	Value     map[string]any
	Raw       any
	FormValue map[string]any
}

type ParsedAction struct {
	Command        string         `json:"command,omitempty"`
	BridgeCallback bool           `json:"bridgeCallback"`
	LegacyCallback bool           `json:"legacyCallback"`
	BridgeToken    string         `json:"bridgeToken,omitempty"`
	Value          map[string]any `json:"value,omitempty"`
	FormValue      map[string]any `json:"formValue,omitempty"`
	AgentPayload   map[string]any `json:"agentPayload,omitempty"`
}

func ParseAction(input ActionParseInput) ParsedAction {
	value := cloneMap(input.Value)
	formValue := cloneMap(input.FormValue)
	if formValue == nil {
		formValue = extractFormValue(input.Raw)
	}

	parsed := ParsedAction{
		Value:     value,
		FormValue: formValue,
	}
	if value == nil {
		return parsed
	}
	if cmd, ok := value["cmd"].(string); ok {
		parsed.Command = cmd
	}
	_, parsed.BridgeCallback = value[bridgeCallbackMarker]
	_, parsed.LegacyCallback = value[legacyClaudeCallbackMarker]
	if token, ok := value["bridge_token"].(string); ok {
		parsed.BridgeToken = token
	}
	if parsed.BridgeCallback {
		agentPayload := cloneMap(value)
		delete(agentPayload, bridgeCallbackMarker)
		delete(agentPayload, "bridge_token")
		if formValue != nil {
			agentPayload[formValueKey] = formValue
		}
		parsed.AgentPayload = agentPayload
	}
	return parsed
}

func IsBridgeCallback(value map[string]any) bool {
	if value == nil {
		return false
	}
	if _, ok := value[bridgeCallbackMarker]; ok {
		return true
	}
	_, ok := value["bridge_token"].(string)
	return ok
}

func IsLegacyCallback(value map[string]any) bool {
	if value == nil {
		return false
	}
	_, ok := value[legacyClaudeCallbackMarker]
	return ok
}

func extractFormValue(raw any) map[string]any {
	root, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	action, ok := root["action"].(map[string]any)
	if !ok {
		return nil
	}
	formValue, ok := action[formValueKey].(map[string]any)
	if !ok {
		return nil
	}
	return cloneMap(formValue)
}

func cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
