package bridge

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderRunCardKitFacade(t *testing.T) {
	state := NewRunCardState(RunCardStateInput{})
	state = ReduceRunCardState(state, Event{Type: EventText, Delta: strPtr("hello")})

	card := RenderRunCardKit(state, CardRenderOptions{
		SignCallback: func(action string) string { return "signed-" + action },
	})
	if card["schema"] != "2.0" {
		t.Fatalf("schema = %v, want 2.0", card["schema"])
	}
	config := card["config"].(map[string]any)
	if config["streaming_mode"] != true {
		t.Fatalf("streaming_mode = %v, want true", config["streaming_mode"])
	}
	elements := card["body"].(map[string]any)["elements"].([]any)
	button := elements[len(elements)-1].(map[string]any)
	value := button["behaviors"].([]any)[0].(map[string]any)["value"].(map[string]any)
	if value["cmd"] != "stop" || value["bridge_token"] != "signed-stop" {
		t.Fatalf("unexpected callback value: %#v", value)
	}
}

func TestCardKitTemplateFacades(t *testing.T) {
	cards := []CardKitJSON{
		WorkspacesCardKit("/repo", map[string]string{"repo": "/repo"}),
		StatusCardKit(CardKitStatusInfo{
			ProfileName:   "codex",
			CWD:           "/repo",
			AgentName:     "Codex",
			RuntimeAccess: CardKitLabelValue{Label: "access", Value: "workspace"},
			Queue:         &CardKitQueueInfo{Cap: 1},
			OwnerState:    "present",
			Scope:         "oc_group",
			ChatMode:      "group",
		}),
		ResumeCardKit("/repo", []CardKitResumeEntry{{SessionID: "session-1", Preview: "hello", RelTime: "now"}}),
		HelpCardKit("Codex"),
	}
	for i, card := range cards {
		if _, ok := card["schema"]; ok {
			t.Fatalf("card %d unexpectedly has schema: %#v", i, card)
		}
		if _, ok := card["elements"].([]any); !ok {
			t.Fatalf("card %d elements = %T, want []any", i, card["elements"])
		}
	}
}

func TestAccountConfigCardKitFacadesExposeCardKit2Builders(t *testing.T) {
	account := AccountFormCardKit(CardKitAccountFormOptions{
		InitialTenant: CardKitTenantLark,
		PrefillAppID:  "cli_1234567890123456",
		ErrorMessage:  "bad credentials",
	})
	if account["schema"] != "2.0" {
		t.Fatalf("account schema = %v, want 2.0", account["schema"])
	}
	payload := mustMarshalCardKit(t, account)
	if strings.Contains(payload, "super-secret-value") || strings.Contains(payload, "bridge_token") {
		t.Fatalf("account facade leaked sensitive value: %s", payload)
	}
	if !strings.Contains(payload, `"name":"app_secret"`) {
		t.Fatalf("account facade missing app_secret input: %s", payload)
	}
	if strings.Contains(payload, `"name":"app_secret","default_value"`) {
		t.Fatalf("account facade prefilled app_secret: %s", payload)
	}

	config := ConfigFormCardKit(CardKitConfigFormOptions{
		MessageReply:          CardKitMessageReplyCard,
		ShowToolCalls:         true,
		CotMessages:           CardKitCotMessagesBrief,
		MaxConcurrentRuns:     10,
		RunIdleTimeoutMinutes: 0,
		RequireMentionInGroup: true,
		LarkCLIIdentity:       CardKitLarkCLIIdentityBotOnly,
		AllowedUsers:          []string{"ou_user"},
		AllowedChats:          []string{"oc_group_abcdef"},
		Admins:                []string{"ou_admin"},
		KnownChats:            []CardKitKnownChat{{ID: "oc_group_abcdef", Name: "工程群"}},
	})
	if config["schema"] != "2.0" {
		t.Fatalf("config schema = %v, want 2.0", config["schema"])
	}
	configPayload := mustMarshalCardKit(t, config)
	for _, want := range []string{"config.submit", "访问控制", "ou_user", "工程群"} {
		if !strings.Contains(configPayload, want) {
			t.Fatalf("config facade missing %q in %s", want, configPayload)
		}
	}

	for name, card := range map[string]CardKitJSON{
		"account current":   AccountCurrentCardKit(CardKitCurrentInfo{AppID: "cli_1234567890123456", Tenant: CardKitTenantFeishu}),
		"account validate":  AccountValidatingCardKit(),
		"account success":   AccountSuccessCardKit(CardKitCurrentInfo{AppID: "cli_1234567890123456", Tenant: CardKitTenantFeishu}),
		"account failure":   AccountFailureCardKit("bad credentials"),
		"account cancelled": AccountCancelledCardKit(),
		"config saved":      ConfigSavedCardKit(CardKitConfigFormOptions{MessageReply: CardKitMessageReplyText}),
		"config grant":      GroupMsgScopeGrantCardKit("https://example.com/grant", 5),
		"config granted":    GroupMsgScopeGrantedCardKit(),
		"config cancelled":  ConfigCancelledCardKit(),
		"config failed":     ConfigFailedCardKit("write denied"),
	} {
		if card["schema"] != "2.0" {
			t.Fatalf("%s schema = %v, want 2.0", name, card["schema"])
		}
		if strings.Contains(mustMarshalCardKit(t, card), "bridge_token") {
			t.Fatalf("%s leaked bridge_token: %#v", name, card)
		}
	}
}

func mustMarshalCardKit(t *testing.T, card CardKitJSON) string {
	t.Helper()
	payload, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("marshal card: %v", err)
	}
	return string(payload)
}
