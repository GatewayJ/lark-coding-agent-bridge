package cardrender

import "testing"

func TestParseActionMergesCardKit20FormValueIntoAgentPayload(t *testing.T) {
	parsed := ParseAction(ActionParseInput{
		Value: map[string]any{
			"__bridge_cb":  true,
			"bridge_token": "signed",
			"choice":       "a",
		},
		Raw: map[string]any{
			"action": map[string]any{
				"form_value": map[string]any{"note": "from form"},
			},
		},
	})

	if !parsed.BridgeCallback {
		t.Fatal("BridgeCallback = false, want true")
	}
	if parsed.BridgeToken != "signed" {
		t.Fatalf("BridgeToken = %q, want signed", parsed.BridgeToken)
	}
	if _, ok := parsed.AgentPayload["__bridge_cb"]; ok {
		t.Fatalf("agent payload leaked marker: %#v", parsed.AgentPayload)
	}
	if _, ok := parsed.AgentPayload["bridge_token"]; ok {
		t.Fatalf("agent payload leaked token: %#v", parsed.AgentPayload)
	}
	form, ok := parsed.AgentPayload["form_value"].(map[string]any)
	if !ok || form["note"] != "from form" {
		t.Fatalf("form value not merged: %#v", parsed.AgentPayload)
	}
}

func TestParseActionKeepsCommandAndLegacyMarkerEntry(t *testing.T) {
	parsed := ParseAction(ActionParseInput{
		Value: map[string]any{
			"__claude_cb": true,
			"cmd":         "stop",
		},
	})

	if parsed.Command != "stop" {
		t.Fatalf("Command = %q, want stop", parsed.Command)
	}
	if !parsed.LegacyCallback {
		t.Fatal("LegacyCallback = false, want true")
	}
	if IsBridgeCallback(parsed.Value) {
		t.Fatal("legacy callback should not be treated as bridge callback")
	}
	if !IsLegacyCallback(parsed.Value) {
		t.Fatal("legacy marker was not detected")
	}
}
