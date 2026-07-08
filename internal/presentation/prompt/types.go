package prompt

type BridgePromptSource string

const (
	BridgePromptSourceIM      BridgePromptSource = "im"
	BridgePromptSourceCard    BridgePromptSource = "card"
	BridgePromptSourceComment BridgePromptSource = "comment"
)

type BridgePromptSenderType string

const (
	BridgePromptSenderUser BridgePromptSenderType = "user"
	BridgePromptSenderBot  BridgePromptSenderType = "bot"
)

type AgentBotIdentity struct {
	OpenID string `json:"openId"`
	Name   string `json:"name,omitempty"`
}

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
