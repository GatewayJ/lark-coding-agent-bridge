package prompt

import (
	"fmt"

	_ "embed"
)

//go:embed bridge_system_prompt.txt
var BRIDGE_SYSTEM_PROMPT string

func BuildBridgeSystemPrompt(identity *AgentBotIdentity) string {
	if identity == nil || identity.OpenID == "" {
		return BRIDGE_SYSTEM_PROMPT
	}

	nameSuffix := ""
	if identity.Name != "" {
		nameSuffix = fmt.Sprintf("，名字是「%s」", identity.Name)
	}
	return fmt.Sprintf("%s\n## 你的身份\n\n你的 open_id 是 `%s`%s。消息内容或 mentions 里出现这个 open_id 都是指你自己。\n", BRIDGE_SYSTEM_PROMPT, identity.OpenID, nameSuffix)
}

func PrefixBridgeSystemPrompt(userPrompt string, identity *AgentBotIdentity) string {
	return BuildBridgeSystemPrompt(identity) + "\n\n## user_message\n\n" + userPrompt
}
