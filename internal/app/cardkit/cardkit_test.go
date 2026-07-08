package cardkit

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/cardrender"
)

func TestRenderRunCardKitRunningStopButtonAndCallbackValue(t *testing.T) {
	state := cardrender.Reduce(cardrender.NewRunState(cardrender.RunStateInput{}), cardrender.Event{
		Type:  cardrender.EventThinking,
		Delta: str("checking options"),
	})

	card := normalize(t, RenderRunCardKit(state, cardrender.RenderOptions{
		SignCallback: func(action string) string {
			if action != "stop" {
				t.Fatalf("signed action = %q, want stop", action)
			}
			return "token-for-stop"
		},
	}))

	if got := card["schema"]; got != "2.0" {
		t.Fatalf("schema = %v, want 2.0", got)
	}
	config := asMap(t, card["config"])
	if got := config["streaming_mode"]; got != true {
		t.Fatalf("streaming_mode = %v, want true", got)
	}
	if got := asMap(t, config["summary"])["content"]; got != "思考中" {
		t.Fatalf("summary = %v, want 思考中", got)
	}

	elements := asSlice(t, asMap(t, card["body"])["elements"])
	if len(elements) != 3 {
		t.Fatalf("elements = %d, want reasoning/footer/button: %#v", len(elements), elements)
	}
	panel := asMap(t, elements[0])
	if panel["tag"] != "collapsible_panel" || panel["expanded"] != true {
		t.Fatalf("reasoning panel not expanded: %#v", panel)
	}
	if got := asMap(t, asMap(t, panel["header"])["title"])["content"]; got != "🧠 **思考中**" {
		t.Fatalf("reasoning title = %v", got)
	}
	footer := asMap(t, elements[1])
	if footer["tag"] != "markdown" || footer["text_size"] != "notation" || footer["content"] != "🧠 正在思考" {
		t.Fatalf("footer note mismatch: %#v", footer)
	}
	button := asMap(t, elements[2])
	if button["tag"] != "button" || button["type"] != "danger" {
		t.Fatalf("stop button mismatch: %#v", button)
	}
	value := asMap(t, asMap(t, asSlice(t, button["behaviors"])[0])["value"])
	for key, want := range map[string]any{
		"cmd":          "stop",
		"__bridge_cb":  true,
		"bridge_token": "token-for-stop",
	} {
		if got := value[key]; got != want {
			t.Fatalf("button value[%s] = %v, want %v in %#v", key, got, want, value)
		}
	}
}

func TestRenderRunCardKitTerminalStates(t *testing.T) {
	tests := []struct {
		name         string
		state        cardrender.RunState
		wantSummary  string
		wantContains string
	}{
		{
			name: "succeeded",
			state: reduceEvents(
				cardrender.Event{Type: cardrender.EventText, Delta: str("final answer")},
				cardrender.Event{Type: cardrender.EventDone, TerminationReason: cardrender.TerminationNormal},
			),
			wantSummary:  "已完成",
			wantContains: "final answer",
		},
		{
			name: "failed",
			state: reduceEvents(cardrender.Event{
				Type:              cardrender.EventError,
				Message:           str("process failed"),
				TerminationReason: cardrender.TerminationFailed,
			}),
			wantSummary:  "出错",
			wantContains: "⚠️ agent 失败：process failed",
		},
		{
			name: "timeout",
			state: cardrender.MarkTimeout(reduceEvents(cardrender.Event{
				Type:  cardrender.EventText,
				Delta: str("partial"),
			}), 15),
			wantSummary:  "已超时",
			wantContains: "_⏱ 15 分钟无响应,已自动终止_",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			card := normalize(t, RenderRunCardKit(tt.state, cardrender.RenderOptions{}))
			config := asMap(t, card["config"])
			if got := config["streaming_mode"]; got != false {
				t.Fatalf("streaming_mode = %v, want false", got)
			}
			if got := asMap(t, config["summary"])["content"]; got != tt.wantSummary {
				t.Fatalf("summary = %v, want %s", got, tt.wantSummary)
			}
			bodyJSON, err := json.Marshal(asMap(t, card["body"]))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(bodyJSON), tt.wantContains) {
				t.Fatalf("body missing %q in %s", tt.wantContains, bodyJSON)
			}
			if strings.Contains(string(bodyJSON), `"tag":"button"`) {
				t.Fatalf("terminal card should not render stop button: %s", bodyJSON)
			}
		})
	}
}

func TestRenderRunCardKitPreservesCollapsibleToolPanelSemantics(t *testing.T) {
	state := reduceEvents(
		toolUse("tool-1", "Bash", map[string]any{"command": "pwd"}),
		toolResult("tool-1", "/repo", false),
		toolUse("tool-2", "Read", map[string]any{"file_path": "/repo/a.go"}),
		toolResult("tool-2", "package a", false),
		toolUse("tool-3", "Edit", map[string]any{"file_path": "/repo/a.go"}),
		toolResult("tool-3", "ok", false),
		cardrender.Event{Type: cardrender.EventDone, TerminationReason: cardrender.TerminationNormal},
	)
	card := normalize(t, RenderRunCardKit(state, cardrender.RenderOptions{}))
	elements := asSlice(t, asMap(t, card["body"])["elements"])
	if len(elements) != 1 {
		t.Fatalf("elements = %d, want one collapsed tool summary: %#v", len(elements), elements)
	}
	panel := asMap(t, elements[0])
	if panel["tag"] != "collapsible_panel" || panel["expanded"] != false {
		t.Fatalf("tool summary panel mismatch: %#v", panel)
	}
	if got := asMap(t, panel["border"])["color"]; got != "blue" {
		t.Fatalf("tool summary border = %v, want blue", got)
	}
	if got := asMap(t, asMap(t, panel["header"])["title"])["content"]; got != "☕ **3 个工具调用（已结束）**" {
		t.Fatalf("tool summary title = %v", got)
	}
	body := asMap(t, asSlice(t, panel["elements"])[0])
	if body["tag"] != "markdown" || body["text_size"] != "notation" {
		t.Fatalf("tool summary body markdown mismatch: %#v", body)
	}
	for _, want := range []string{"✅ **Bash** — pwd", "✅ **Read** — /repo/a.go", "✅ **Edit** — /repo/a.go"} {
		if !strings.Contains(body["content"].(string), want) {
			t.Fatalf("tool summary missing %q in %q", want, body["content"])
		}
	}
}

func TestTemplateBuildersMatchTypeScriptTemplateShells(t *testing.T) {
	cards := []JSONCard{
		WorkspacesCard("/repo", map[string]string{"main": "/repo"}),
		StatusCard(StatusInfo{
			ProfileName:   "codex",
			CWD:           "/repo",
			SessionID:     "thread-123456",
			AgentName:     "Codex",
			RuntimeAccess: LabelValue{Label: "access", Value: "workspace"},
			Queue:         &QueueInfo{Active: 1, Waiting: 2, Cap: 3},
			OwnerState:    "present",
			Scope:         "oc_group",
			ChatMode:      "group",
		}),
		ResumeCard("/repo", []ResumeEntry{{SessionID: "session-123456", Preview: "hello", RelTime: "1m ago", LineCount: 3}}),
		HelpCard("Codex"),
	}
	for i, card := range cards {
		normalized := normalize(t, card)
		if _, ok := normalized["schema"]; ok {
			t.Fatalf("card %d unexpectedly has CardKit 2.0 schema: %#v", i, normalized)
		}
		if len(asSlice(t, normalized["elements"])) == 0 {
			t.Fatalf("card %d has no elements", i)
		}
		if _, ok := normalized["body"]; ok {
			t.Fatalf("card %d unexpectedly nested elements under body: %#v", i, normalized)
		}
		if _, ok := asMap(t, normalized["config"])["wide_screen_mode"]; !ok {
			t.Fatalf("card %d missing v1 template config: %#v", i, normalized["config"])
		}
	}
}

func TestTemplateButtonsUseLegacyValueFieldLikeTypeScript(t *testing.T) {
	card := normalize(t, WorkspacesCard("/repo", map[string]string{"main": "/repo"}))
	elements := asSlice(t, card["elements"])
	action := asMap(t, elements[3])
	button := asMap(t, asSlice(t, action["actions"])[0])
	if _, ok := button["behaviors"]; ok {
		t.Fatalf("template button unexpectedly uses CardKit behaviors: %#v", button)
	}
	value := asMap(t, button["value"])
	if value["cmd"] != "ws.use" || value["name"] != "main" {
		t.Fatalf("button value = %#v", value)
	}
}

func TestAccountCardKit2BuildersMatchTypeScriptShapeAndRedactSecrets(t *testing.T) {
	const (
		appID  = "cli_1234567890123456"
		secret = "super-secret-value"
	)

	current := normalize(t, AccountCurrentCard(CurrentInfo{
		AppID:   appID,
		BotName: "codex-bot",
		Tenant:  TenantFeishu,
	}))
	assertCardKit2Summary(t, current, "当前应用")
	currentBody := jsonString(t, current["body"])
	if !strings.Contains(currentBody, "**App ID**: `cli_123456789****56`") {
		t.Fatalf("current card missing masked app id: %s", currentBody)
	}
	if strings.Contains(currentBody, appID) || strings.Contains(currentBody, secret) {
		t.Fatalf("current card leaked raw credential: %s", currentBody)
	}
	currentElements := asSlice(t, asMap(t, current["body"])["elements"])
	changeButton := asMap(t, currentElements[2])
	changeValue := asMap(t, asMap(t, asSlice(t, changeButton["behaviors"])[0])["value"])
	if changeValue["cmd"] != "account.change" {
		t.Fatalf("change button callback = %#v", changeValue)
	}
	if _, ok := changeValue["bridge_token"]; ok {
		t.Fatalf("account callback leaked bridge_token: %#v", changeValue)
	}

	form := normalize(t, AccountFormCard(AccountFormOptions{
		InitialTenant: TenantLark,
		PrefillAppID:  appID,
		ErrorMessage:  "invalid credentials",
	}))
	assertCardKit2Summary(t, form, "更换凭据")
	formPayload := jsonString(t, form)
	if strings.Contains(formPayload, secret) || strings.Contains(formPayload, "bridge_token") {
		t.Fatalf("account form leaked sensitive value: %s", formPayload)
	}
	bodyElements := asSlice(t, asMap(t, form["body"])["elements"])
	if got := asMap(t, bodyElements[0])["content"]; got != "❌ **校验失败**：invalid credentials" {
		t.Fatalf("form error content = %v", got)
	}
	formElement := asMap(t, bodyElements[1])
	if formElement["tag"] != "form" || formElement["name"] != "account_form" {
		t.Fatalf("account form element mismatch: %#v", formElement)
	}
	fields := asSlice(t, formElement["elements"])
	appIDInput := asMap(t, fields[0])
	if appIDInput["default_value"] != appID {
		t.Fatalf("app_id default_value = %v, want %s", appIDInput["default_value"], appID)
	}
	secretInput := asMap(t, fields[1])
	if secretInput["name"] != "app_secret" {
		t.Fatalf("secret input mismatch: %#v", secretInput)
	}
	if _, ok := secretInput["default_value"]; ok {
		t.Fatalf("app_secret must never be prefilled: %#v", secretInput)
	}
	tenantSelect := asMap(t, fields[3])
	if tenantSelect["initial_option"] != "lark" {
		t.Fatalf("tenant initial_option = %v, want lark", tenantSelect["initial_option"])
	}
	buttons := asSlice(t, asMap(t, fields[4])["columns"])
	submit := asMap(t, asSlice(t, asMap(t, buttons[0])["elements"])[0])
	if submit["form_action_type"] != "submit" || callbackCmd(t, submit) != "account.submit" {
		t.Fatalf("submit button mismatch: %#v", submit)
	}
	cancel := asMap(t, asSlice(t, asMap(t, buttons[1])["elements"])[0])
	if callbackCmd(t, cancel) != "account.cancel" {
		t.Fatalf("cancel button mismatch: %#v", cancel)
	}

	assertCardKit2Summary(t, normalize(t, AccountValidatingCard()), "正在校验...")
	success := normalize(t, AccountSuccessCard(CurrentInfo{AppID: appID, Tenant: TenantFeishu}))
	assertCardKit2Summary(t, success, "已保存")
	successBody := jsonString(t, success["body"])
	if !strings.Contains(successBody, "正在用新凭据重连 WebSocket") || strings.Contains(successBody, appID) {
		t.Fatalf("success card body mismatch or leaked app id: %s", successBody)
	}
	assertCardKit2Summary(t, normalize(t, AccountFailureCard("bad credentials")), "校验失败")
	assertCardKit2Summary(t, normalize(t, AccountCancelledCard()), "已取消")
}

func TestConfigCardKit2BuildersMatchTypeScriptShapeAndAccessText(t *testing.T) {
	opts := ConfigFormOptions{
		MessageReply:          MessageReplyCard,
		ShowToolCalls:         false,
		CotMessages:           CotMessagesDetailed,
		MaxConcurrentRuns:     7,
		RunIdleTimeoutMinutes: 15,
		RequireMentionInGroup: false,
		LarkCLIIdentity:       LarkCLIIdentityUserDefault,
		AllowedUsers:          []string{"ou_user_1"},
		AllowedChats:          []string{"oc_123456abcdef", "oc_unknown999999"},
		Admins:                []string{"ou_admin_1"},
		KnownChats:            []KnownChat{{ID: "oc_123456abcdef", Name: "工程群"}},
	}

	card := normalize(t, ConfigFormCard(opts))
	assertCardKit2Summary(t, card, "偏好设置")
	payload := jsonString(t, card)
	for _, want := range []string{
		"控制谁能通过私聊和群聊使用 bot",
		"- **工程群**（...abcdef）",
		"- **(未知群)**（...999999）",
		"`/invite all group`",
	} {
		if !strings.Contains(payload, want) {
			t.Fatalf("config form missing %q in %s", want, payload)
		}
	}
	if strings.Contains(payload, "bridge_token") || strings.Contains(payload, "super-secret-value") {
		t.Fatalf("config form leaked sensitive value: %s", payload)
	}

	bodyElements := asSlice(t, asMap(t, card["body"])["elements"])
	form := asMap(t, bodyElements[2])
	if form["tag"] != "form" || form["name"] != "config_form" {
		t.Fatalf("config form element mismatch: %#v", form)
	}
	fields := asSlice(t, form["elements"])
	checkSelectInitial(t, fields[1], "message_reply", "markdown")
	checkSelectInitial(t, fields[3], "show_tool_calls", "hide")
	checkSelectInitial(t, fields[5], "cot_messages", "detailed")
	if got := asMap(t, fields[7])["default_value"]; got != "7" {
		t.Fatalf("max_concurrent_runs default_value = %v, want 7", got)
	}
	if got := asMap(t, fields[9])["default_value"]; got != "15" {
		t.Fatalf("run_idle_timeout_minutes default_value = %v, want 15", got)
	}
	checkSelectInitial(t, fields[11], "require_mention_in_group", "no")
	checkSelectInitial(t, fields[13], "lark_cli_identity", "user-default")

	panel := asMap(t, fields[15])
	if panel["tag"] != "collapsible_panel" || panel["expanded"] != false {
		t.Fatalf("access panel mismatch: %#v", panel)
	}
	if got := asMap(t, asMap(t, panel["header"])["title"])["content"]; got != "🔒 **访问控制**（点击展开）" {
		t.Fatalf("access panel title = %v", got)
	}
	if got := asMap(t, panel["border"])["color"]; got != "blue" {
		t.Fatalf("access panel border color = %v, want blue", got)
	}
	panelElements := asSlice(t, panel["elements"])
	allowedUsersText := asMap(t, panelElements[2])["content"].(string)
	if !strings.Contains(allowedUsersText, "<at id=\"ou_user_1\"></at>") {
		t.Fatalf("allowed users mention text mismatch: %q", allowedUsersText)
	}
	adminsText := asMap(t, panelElements[6])["content"].(string)
	if !strings.Contains(adminsText, "<at id=\"ou_admin_1\"></at>") {
		t.Fatalf("admins mention text mismatch: %q", adminsText)
	}
	buttons := asSlice(t, asMap(t, fields[16])["columns"])
	submit := asMap(t, asSlice(t, asMap(t, buttons[0])["elements"])[0])
	if submit["form_action_type"] != "submit" || callbackCmd(t, submit) != "config.submit" {
		t.Fatalf("config submit button mismatch: %#v", submit)
	}
	cancel := asMap(t, asSlice(t, asMap(t, buttons[1])["elements"])[0])
	if callbackCmd(t, cancel) != "config.cancel" {
		t.Fatalf("config cancel button mismatch: %#v", cancel)
	}
}

func TestConfigSavedAndScopeStatusCardsMatchTypeScriptText(t *testing.T) {
	opts := ConfigFormOptions{
		MessageReply:          MessageReplyCard,
		ShowToolCalls:         false,
		CotMessages:           CotMessagesBrief,
		MaxConcurrentRuns:     3,
		RunIdleTimeoutMinutes: 0,
		RequireMentionInGroup: true,
		LarkCLIIdentity:       LarkCLIIdentityUserDefault,
		AllowedUsers:          []string{"ou_a", "ou_b"},
		AllowedChats:          []string{},
		Admins:                []string{"ou_admin"},
	}

	saved := normalize(t, ConfigSavedCard(opts))
	assertCardKit2Summary(t, saved, "偏好已保存")
	savedBody := jsonString(t, saved["body"])
	for _, want := range []string{
		"**消息回复方式**:交互卡片",
		"**工具调用显示**:`hide`",
		"**COT 过程消息**:`简略`",
		"**并发上限**:`3`",
		"**run 探活**:`关闭`",
		"**群里需要 @ bot**:`是`",
		"**lark-cli 身份策略**:`允许用户身份`",
		"**允许私聊的用户**:2 项",
		"**允许响应的群**:_(空)_",
		"**管理员**:1 项",
	} {
		if !strings.Contains(savedBody, want) {
			t.Fatalf("saved card missing %q in %s", want, savedBody)
		}
	}

	grantURL := "https://example.com/grant?state=abc"
	grant := normalize(t, GroupMsgScopeGrantCard(grantURL, 9))
	assertCardKit2Summary(t, grant, "需要补授权")
	grantBody := jsonString(t, grant["body"])
	for _, want := range []string{"im:message.group_msg", "约 9 分钟内有效", grantURL, "`/reconnect`"} {
		if !strings.Contains(grantBody, want) {
			t.Fatalf("grant card missing %q in %s", want, grantBody)
		}
	}
	assertCardKit2Summary(t, normalize(t, GroupMsgScopeGrantedCard()), "授权成功")
	assertCardKit2Summary(t, normalize(t, ConfigCancelledCard()), "已取消")
	failed := normalize(t, ConfigFailedCard("write denied"))
	assertCardKit2Summary(t, failed, "保存失败")
	if !strings.Contains(jsonString(t, failed["body"]), "保存失败：write denied") {
		t.Fatalf("failed card body mismatch: %#v", failed)
	}
}

func reduceEvents(events ...cardrender.Event) cardrender.RunState {
	state := cardrender.NewRunState(cardrender.RunStateInput{})
	for _, event := range events {
		state = cardrender.Reduce(state, event)
	}
	return state
}

func toolUse(id string, name string, input any) cardrender.Event {
	return cardrender.Event{Type: cardrender.EventToolUse, ID: str(id), Name: str(name), Input: input}
}

func toolResult(id string, output string, isError bool) cardrender.Event {
	return cardrender.Event{Type: cardrender.EventToolResult, ID: str(id), Output: str(output), IsError: &isError}
}

func normalize(t *testing.T, value any) map[string]any {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(payload, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

func asMap(t *testing.T, value any) map[string]any {
	t.Helper()
	out, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("value is %T, want map[string]any: %#v", value, value)
	}
	return out
}

func asSlice(t *testing.T, value any) []any {
	t.Helper()
	out, ok := value.([]any)
	if !ok {
		t.Fatalf("value is %T, want []any: %#v", value, value)
	}
	return out
}

func jsonString(t *testing.T, value any) string {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(payload)
}

func assertCardKit2Summary(t *testing.T, card map[string]any, want string) {
	t.Helper()
	if got := card["schema"]; got != "2.0" {
		t.Fatalf("schema = %v, want 2.0 in %#v", got, card)
	}
	if _, ok := card["header"]; ok {
		t.Fatalf("CardKit 2.0 card unexpectedly has legacy header: %#v", card)
	}
	body := asMap(t, card["body"])
	if len(asSlice(t, body["elements"])) == 0 {
		t.Fatalf("CardKit 2.0 body has no elements: %#v", body)
	}
	config := asMap(t, card["config"])
	if got := asMap(t, config["summary"])["content"]; got != want {
		t.Fatalf("summary = %v, want %s", got, want)
	}
}

func callbackCmd(t *testing.T, button map[string]any) any {
	t.Helper()
	behavior := asMap(t, asSlice(t, button["behaviors"])[0])
	value := asMap(t, behavior["value"])
	if _, ok := value["bridge_token"]; ok {
		t.Fatalf("callback value leaked bridge_token: %#v", value)
	}
	return value["cmd"]
}

func checkSelectInitial(t *testing.T, value any, name string, initial string) {
	t.Helper()
	selectValue := asMap(t, value)
	if selectValue["tag"] != "select_static" || selectValue["name"] != name {
		t.Fatalf("select %s mismatch: %#v", name, selectValue)
	}
	if selectValue["initial_option"] != initial {
		t.Fatalf("select %s initial_option = %v, want %s", name, selectValue["initial_option"], initial)
	}
}

func str(value string) *string { return &value }
