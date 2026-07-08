package bridge

import internalprompt "github.com/zarazhangrui/lark-coding-agent-bridge/internal/presentation/prompt"

type BotIdentity struct {
	OpenID string `json:"openId"`
	Name   string `json:"name,omitempty"`
}

type BridgePromptSource string

type BridgePromptSenderType string

type BridgePromptMention struct {
	OpenID string `json:"openId,omitempty"`
	Name   string `json:"name,omitempty"`
	IsBot  *bool  `json:"isBot,omitempty"`
}

type BridgePromptContext struct {
	ChatID     string                 `json:"chatId"`
	ChatType   string                 `json:"chatType"`
	SenderID   string                 `json:"senderId"`
	SenderName string                 `json:"senderName,omitempty"`
	SenderType BridgePromptSenderType `json:"senderType,omitempty"`
	BotOpenID  string                 `json:"botOpenId,omitempty"`
	Mentions   []BridgePromptMention  `json:"mentions,omitempty"`
	ThreadID   string                 `json:"threadId,omitempty"`
	MessageIDs []string               `json:"messageIds,omitempty"`
	Source     BridgePromptSource     `json:"source"`
}

type BridgePromptQuotedMessage struct {
	MessageID      string `json:"messageId"`
	SenderID       string `json:"senderId"`
	SenderName     string `json:"senderName,omitempty"`
	CreatedAt      string `json:"createdAt,omitempty"`
	RawContentType string `json:"rawContentType"`
	Content        string `json:"content"`
}

type BridgePromptInteractiveCard struct {
	MessageID string `json:"messageId,omitempty"`
	Content   any    `json:"content"`
}

type BridgePromptComment struct {
	CommentScopeID  string `json:"commentScopeId"`
	IsWholeDocument bool   `json:"isWholeDocument"`
	DocsLink        string `json:"docsLink,omitempty"`
	Question        string `json:"question"`
	Quote           string `json:"quote,omitempty"`
}

type BridgePromptAttachment struct {
	Path            string `json:"path"`
	Kind            string `json:"kind"`
	Hash            string `json:"hash,omitempty"`
	Size            int64  `json:"size,omitempty"`
	MIME            string `json:"mime,omitempty"`
	SourceMessageID string `json:"sourceMessageId,omitempty"`
	Requiredness    string `json:"requiredness,omitempty"`
	Decision        string `json:"decision,omitempty"`
	RejectionReason string `json:"rejectionReason,omitempty"`
}

type BuildAgentPromptInput struct {
	Context          BridgePromptContext
	Instructions     []string
	UserInput        string
	QuotedMessages   []BridgePromptQuotedMessage
	InteractiveCards []BridgePromptInteractiveCard
	Comment          *BridgePromptComment
	Attachments      []BridgePromptAttachment
}

const (
	BridgePromptSourceIM      BridgePromptSource     = "im"
	BridgePromptSourceCard    BridgePromptSource     = "card"
	BridgePromptSourceComment BridgePromptSource     = "comment"
	BridgePromptSenderUser    BridgePromptSenderType = "user"
	BridgePromptSenderBot     BridgePromptSenderType = "bot"
)

var defaultBridgeAgentInstructions = []string{
	"你在 bridge 进程中运行，普通 lark-cli 会继承 LARK_CHANNEL=1 并进入 bridge-bound 模式。",
	"不要 unset LARK_CHANNEL / LARK_CHANNEL_HOME / LARK_CHANNEL_PROFILE / LARKSUITE_CLI_CONFIG_DIR，也不要用 env -u LARK_CHANNEL 绕回本机普通配置。",
	"Codex bridge 默认使用 danger-full-access 对齐 Claude bridge 的 bypassPermissions 行为，因此 lark-cli 应能像用户本机终端一样访问 keychain。",
	"如果提示 lark-channel context detected but not bound，停止当前操作并请用户重启 bridge 或运行 bridge doctor/preflight；不要改用普通 profile，不要自行 bind，也不要直接读取 config.json 里的账号或密钥。",
}

func DefaultBridgeAgentInstructions() []string {
	out := make([]string, len(defaultBridgeAgentInstructions))
	copy(out, defaultBridgeAgentInstructions)
	return out
}

func BridgeSystemPrompt(identity *BotIdentity) string {
	if identity == nil {
		return internalprompt.BuildBridgeSystemPrompt(nil)
	}
	return internalprompt.BuildBridgeSystemPrompt(&internalprompt.AgentBotIdentity{
		OpenID: identity.OpenID,
		Name:   identity.Name,
	})
}

func PrefixBridgeSystemPrompt(userPrompt string, identity *BotIdentity) string {
	if identity == nil {
		return internalprompt.PrefixBridgeSystemPrompt(userPrompt, nil)
	}
	return internalprompt.PrefixBridgeSystemPrompt(userPrompt, &internalprompt.AgentBotIdentity{
		OpenID: identity.OpenID,
		Name:   identity.Name,
	})
}

func BuildAgentPrompt(input BuildAgentPromptInput) string {
	input.Instructions = mergeDefaultBridgeAgentInstructions(input.Instructions)
	return internalprompt.BuildAgentPrompt(toInternalBuildAgentPromptInput(input))
}

func BuildAgentPromptRaw(input BuildAgentPromptInput) string {
	return internalprompt.BuildAgentPrompt(toInternalBuildAgentPromptInput(input))
}

func toInternalBuildAgentPromptInput(input BuildAgentPromptInput) internalprompt.BuildAgentPromptInput {
	out, _ := convertBridgeJSON[internalprompt.BuildAgentPromptInput](input)
	return out
}

func fromInternalBridgePromptQuotedMessage(input internalprompt.BridgePromptQuotedMessage) BridgePromptQuotedMessage {
	out, _ := convertBridgeJSON[BridgePromptQuotedMessage](input)
	return out
}

func mergeDefaultBridgeAgentInstructions(instructions []string) []string {
	out := DefaultBridgeAgentInstructions()
	for _, instruction := range instructions {
		if !containsString(out, instruction) {
			out = append(out, instruction)
		}
	}
	return out
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
