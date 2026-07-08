package bridge

import (
	"context"
	"time"
)

// Options configures the stable operational facade. Construction stays small:
// profile bootstrap, credentials, and real Feishu/Lark SDK wiring continue to
// live in the compatibility/profile layers or in caller-provided adapters.
type Options struct {
	Home    string
	Profile string

	Logger    Logger
	Telemetry TelemetryAdapter

	Client       *Client
	CodexClient  *CodexClientOptions
	ClaudeClient *ClaudeClientOptions

	LarkTransport              LarkTransport
	LarkAdapter                *LarkAdapter
	LarkIntake                 LarkIntakeSink
	LarkCardActions            LarkCardActionDispatcher
	LarkComments               CommentSurface
	LarkCommentOptions         CommentOptions
	LarkManaged                LarkManagedOptions
	LarkProfileProjection      LarkProfileProjectionHook
	ForwardCardPromptsToIntake bool
	RuntimeAdapter             RuntimeAdapter
	AppID                      string
	Tenant                     RuntimeTenant
	AgentKind                  RuntimeAgentKind
	Version                    string
	ConfigPath                 string
}

type LarkManagedOptions struct {
	MessageQuietPeriod  time.Duration
	FlushTimeout        time.Duration
	MessageReplyMode    LarkReplyMode
	ShowToolCalls       *bool
	CommandOptions      CommandOptions
	InitialOwnerOpenID  string
	QuoteResolver       LarkQuoteResolver
	CallbackAuth        *CallbackAuth
	CallbackTTL         time.Duration
	CardActionSettle    time.Duration
	AccountReconnect    time.Duration
	CloseTimeout        time.Duration
	ScopeChecker        LarkScopeChecker
	ScopeGrant          LarkScopeGrantRequester
	Reactioner          LarkMessageReactioner
	COTClient           LarkCOTClient
	CotMessages         LarkCotMessagesMode
	RuntimeInfo         LarkRuntimeInfoSource
	InfoRefreshInterval time.Duration
}

type LarkReplyMode string
type LarkCotMessagesMode string

const (
	LarkGroupMsgScope = "im:message.group_msg"

	LarkReplyMarkdown LarkReplyMode = "markdown"
	LarkReplyText     LarkReplyMode = "text"
	LarkReplyCard     LarkReplyMode = "card"

	LarkCotMessagesOff      LarkCotMessagesMode = "off"
	LarkCotMessagesBrief    LarkCotMessagesMode = "brief"
	LarkCotMessagesDetailed LarkCotMessagesMode = "detailed"
)

type LarkScopeChecker interface {
	HasLarkScope(ctx context.Context, appID string, scope string) (bool, error)
}

type LarkScopeCheckerFunc func(ctx context.Context, appID string, scope string) (bool, error)

func (f LarkScopeCheckerFunc) HasLarkScope(ctx context.Context, appID string, scope string) (bool, error) {
	return f(ctx, appID, scope)
}

type LarkScopeGrantRequest struct {
	AppID        string
	TenantScopes []string
}

type LarkScopeGrantLink struct {
	URL       string
	ExpiresIn time.Duration
	Wait      func(ctx context.Context) error
	Cancel    func()
}

type LarkScopeGrantRequester interface {
	RequestLarkScopeGrant(ctx context.Context, req LarkScopeGrantRequest) (LarkScopeGrantLink, error)
}

type LarkScopeGrantRequesterFunc func(ctx context.Context, req LarkScopeGrantRequest) (LarkScopeGrantLink, error)

func (f LarkScopeGrantRequesterFunc) RequestLarkScopeGrant(ctx context.Context, req LarkScopeGrantRequest) (LarkScopeGrantLink, error) {
	return f(ctx, req)
}

type LarkKnownChatInfo struct {
	ID   string
	Name string
}

type LarkRuntimeInfoSource interface {
	FetchLarkOwner(ctx context.Context, appID string) (string, error)
	ListLarkKnownChats(ctx context.Context, maxPages int) ([]LarkKnownChatInfo, error)
}

type LarkQuoteTarget struct {
	MessageID       string
	ChatID          string
	ChatType        LarkChatType
	ResolvedMode    LarkChatMode
	ThreadID        string
	RootID          string
	ParentID        string
	SourceMessageID string
}

type LarkQuoteResolver interface {
	ResolveLarkQuote(ctx context.Context, target LarkQuoteTarget) (BridgePromptQuotedMessage, bool, error)
}

type LarkQuoteResolverFunc func(ctx context.Context, target LarkQuoteTarget) (BridgePromptQuotedMessage, bool, error)

func (f LarkQuoteResolverFunc) ResolveLarkQuote(ctx context.Context, target LarkQuoteTarget) (BridgePromptQuotedMessage, bool, error) {
	return f(ctx, target)
}

type Logger interface {
	Info(msg string, fields map[string]any)
	Warn(msg string, fields map[string]any)
	Error(msg string, fields map[string]any)
}

type TelemetryEvent struct {
	Name   string         `json:"name,omitempty"`
	At     time.Time      `json:"at,omitempty"`
	Fields map[string]any `json:"fields,omitempty"`

	Level string     `json:"level,omitempty"`
	Phase string     `json:"phase,omitempty"`
	Event string     `json:"event,omitempty"`
	Ctx   LogContext `json:"ctx,omitempty"`
	Ts    string     `json:"ts,omitempty"`
}

type TelemetryAdapter interface {
	Emit(ctx context.Context, event TelemetryEvent)
	RecordError(ctx context.Context, err error, fields map[string]any)
	RecordMetric(ctx context.Context, name string, value float64, tags map[string]string)
	Flush(ctx context.Context) error
	Close(ctx context.Context) error
}

type LogContext struct {
	TraceID string `json:"traceId,omitempty"`
	ChatID  string `json:"chatId,omitempty"`
	MsgID   string `json:"msgId,omitempty"`
}

type AdapterMeta struct {
	Version  string `json:"version"`
	AppID    string `json:"appId,omitempty"`
	Tenant   string `json:"tenant,omitempty"`
	Hostname string `json:"hostname,omitempty"`
}

type AdapterFactory func(AdapterMeta) TelemetryAdapter
