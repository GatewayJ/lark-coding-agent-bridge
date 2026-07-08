package lark

import (
	"context"
	"time"

	appdispatch "github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/carddispatch"
	appintake "github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/intake"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/larkcli"
	internalprompt "github.com/zarazhangrui/lark-coding-agent-bridge/internal/presentation/prompt"
)

const GroupMsgScope = "im:message.group_msg"

type IncomingEvent struct {
	Kind appintake.EventKind
	Raw  any

	Message    *appintake.MessageInput
	Comment    *appintake.CommentInput
	CardAction *appintake.CardActionInput
	Reconnect  *appintake.ReconnectInput
	Keepalive  *appintake.KeepaliveInput
	Disconnect *appintake.DisconnectInput
}

type TransportHandler interface {
	HandleLarkTransportEvent(ctx context.Context, event IncomingEvent) error
}

type Transport interface {
	Connect(ctx context.Context, handler TransportHandler) error
	Disconnect(ctx context.Context) error
	BotIdentity(ctx context.Context) (BotIdentity, error)
	SendMessage(ctx context.Context, req SendMessageRequest) (SendResult, error)
	SendCard(ctx context.Context, req SendCardRequest) (SendResult, error)
	UpdateCard(ctx context.Context, req UpdateCardRequest) error
}

type CarrierThreadResolver interface {
	ResolveCarrierThreadID(ctx context.Context, chatID, messageID string) (string, error)
}

type QuoteMessage = internalprompt.BridgePromptQuotedMessage

type QuoteTarget struct {
	MessageID       string
	ChatID          string
	ChatType        appintake.ChatType
	ResolvedMode    appintake.ChatMode
	ThreadID        string
	RootID          string
	ParentID        string
	SourceMessageID string
}

type QuoteResolver interface {
	ResolveLarkQuote(ctx context.Context, target QuoteTarget) (QuoteMessage, bool, error)
}

type ScopeChecker interface {
	HasLarkScope(ctx context.Context, appID string, scope string) (bool, error)
}

type ScopeCheckerFunc func(ctx context.Context, appID string, scope string) (bool, error)

func (f ScopeCheckerFunc) HasLarkScope(ctx context.Context, appID string, scope string) (bool, error) {
	return f(ctx, appID, scope)
}

type ScopeGrantRequest struct {
	AppID        string
	TenantScopes []string
}

type ScopeGrantLink struct {
	URL       string
	ExpiresIn time.Duration
	Wait      func(ctx context.Context) error
	Cancel    func()
}

type ScopeGrantRequester interface {
	RequestLarkScopeGrant(ctx context.Context, req ScopeGrantRequest) (ScopeGrantLink, error)
}

type ScopeGrantRequesterFunc func(ctx context.Context, req ScopeGrantRequest) (ScopeGrantLink, error)

func (f ScopeGrantRequesterFunc) RequestLarkScopeGrant(ctx context.Context, req ScopeGrantRequest) (ScopeGrantLink, error) {
	return f(ctx, req)
}

type KnownChat struct {
	ID   string
	Name string
}

type RuntimeInfoSource interface {
	FetchLarkOwner(ctx context.Context, appID string) (string, error)
	ListLarkKnownChats(ctx context.Context, maxPages int) ([]KnownChat, error)
}

type CreateBoundChatRequest struct {
	Name         string
	InviteOpenID string
	Description  string
}

type CreatedChat struct {
	ChatID string
	Name   string
}

type BoundChatCreator interface {
	CreateBoundChat(ctx context.Context, req CreateBoundChatRequest) (CreatedChat, error)
}

type IntakeSink interface {
	HandleLarkEvent(ctx context.Context, event appintake.NormalizedEvent) error
}

type IntakeSinkFunc func(ctx context.Context, event appintake.NormalizedEvent) error

func (f IntakeSinkFunc) HandleLarkEvent(ctx context.Context, event appintake.NormalizedEvent) error {
	return f(ctx, event)
}

type CardActionDispatcher interface {
	Dispatch(ctx context.Context, input appintake.CardActionInput) (appdispatch.Result, error)
}

type ProfileProjectionHook interface {
	ProjectLarkProfile(ctx context.Context, req ProfileProjectionRequest) (ProfileProjectionResult, error)
}

type ProfileProjectionHookFunc func(ctx context.Context, req ProfileProjectionRequest) (ProfileProjectionResult, error)

func (f ProfileProjectionHookFunc) ProjectLarkProfile(ctx context.Context, req ProfileProjectionRequest) (ProfileProjectionResult, error) {
	return f(ctx, req)
}

type BotIdentity struct {
	OpenID  string
	UserID  string
	UnionID string
	Name    string
	Raw     any
}

type MessageContent struct {
	Text     string
	Markdown string
	Card     map[string]any
	Raw      any
}

type SendOptions struct {
	ReplyTo       string
	ReplyInThread bool
	ThreadID      string
	Metadata      map[string]any
}

type SendMessageRequest struct {
	ChatID  string
	Content MessageContent
	Options SendOptions
}

type SendCardRequest struct {
	ChatID  string
	Card    map[string]any
	Options SendOptions
}

type UpdateCardRequest struct {
	MessageID string
	Card      map[string]any
}

type UpdateMessageRequest struct {
	MessageID string
	Content   MessageContent
}

type MessageUpdater interface {
	UpdateMessage(ctx context.Context, req UpdateMessageRequest) error
}

type MessageReactionRequest struct {
	MessageID  string
	EmojiType  string
	ReactionID string
}

type MessageReactionResult struct {
	ReactionID string
}

type MessageReactioner interface {
	AddMessageReaction(ctx context.Context, req MessageReactionRequest) (MessageReactionResult, error)
	DeleteMessageReaction(ctx context.Context, req MessageReactionRequest) error
}

type SendResult struct {
	MessageID string
	Raw       any
}

type ProfileProjectionRequest struct {
	BotIdentity BotIdentity
	StartedAt   time.Time
}

type ProfileProjectionResult struct {
	LarkCliSourceConfigFile string
	LarkChannelEnv          map[string]string
	IdentityPolicyApplied   bool
	BotIdentity             BotIdentity
}

type LarkCLIProjectionHookOptions struct {
	Config              larkcli.AppConfig
	Paths               larkcli.ProjectionPaths
	Env                 larkcli.EnvContext
	IdentityPreset      larkcli.IdentityPreset
	ApplyIdentityPolicy bool
	IdentityOptions     larkcli.IdentityPolicyOptions
}
