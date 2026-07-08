package prompt

import (
	"strings"
	"testing"
)

func TestBridgeSystemPromptContainsBridgeRules(t *testing.T) {
	needles := []string{
		"lark-cli",
		"LARKSUITE_CLI_CONFIG_DIR",
		"飞书 OAuth 授权",
		"lark-cli auth login --device-code",
		"只有被真实 @",
		"默认不要 @ 其他 bot",
		"botOpenId",
	}

	for _, needle := range needles {
		if !strings.Contains(BRIDGE_SYSTEM_PROMPT, needle) {
			t.Fatalf("BRIDGE_SYSTEM_PROMPT missing %q", needle)
		}
	}
}

func TestBuildBridgeSystemPromptAppendsIdentity(t *testing.T) {
	if got := BuildBridgeSystemPrompt(nil); got != BRIDGE_SYSTEM_PROMPT {
		t.Fatal("BuildBridgeSystemPrompt(nil) did not return base prompt")
	}

	got := BuildBridgeSystemPrompt(&AgentBotIdentity{
		OpenID: "ou_bot_self",
		Name:   "尼莫",
	})
	if !strings.HasPrefix(got, BRIDGE_SYSTEM_PROMPT) {
		t.Fatal("identity prompt does not start with base prompt")
	}
	for _, needle := range []string{"ou_bot_self", "尼莫", "你的 open_id"} {
		if !strings.Contains(got, needle) {
			t.Fatalf("identity prompt missing %q", needle)
		}
	}
}

func TestPrefixBridgeSystemPrompt(t *testing.T) {
	got := PrefixBridgeSystemPrompt("hello world", &AgentBotIdentity{OpenID: "ou_bot_self"})
	if !strings.Contains(got, "ou_bot_self") {
		t.Fatal("prefixed prompt missing identity")
	}
	if strings.Index(got, "ou_bot_self") > strings.Index(got, "## user_message") {
		t.Fatal("identity appears after user message heading")
	}
	if !strings.HasSuffix(got, "hello world") {
		t.Fatal("prefixed prompt does not end with user prompt")
	}
}
