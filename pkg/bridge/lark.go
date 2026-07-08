package bridge

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	channeltypes "github.com/larksuite/oapi-sdk-go/v3/channel/types"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	internallark "github.com/zarazhangrui/lark-coding-agent-bridge/internal/adapters/lark"
	appdispatch "github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/carddispatch"
	appcot "github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/cotpresenter"
	appintake "github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/intake"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/larkcli"
	appmedia "github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/media"
)

var (
	ErrNilLarkTransport          = internallark.ErrNilTransport
	ErrUnsupportedLarkEventKind  = internallark.ErrUnsupportedEventKind
	ErrMissingLarkEventPayload   = internallark.ErrMissingEventPayload
	ErrLarkAdapterNotStarted     = internallark.ErrAdapterNotStarted
	ErrLarkCarrierThreadResolver = internallark.ErrCarrierThreadResolver
	ErrLarkManagedCardTransport  = internallark.ErrManagedCardTransport
	ErrFakeLarkTransportClosed   = internallark.ErrFakeTransportNotConnected
	ErrLarkOAPIAppCredentials    = internallark.ErrOAPIAppCredentials
	ErrLarkOAPIChannel           = internallark.ErrOAPIChannel
	ErrLarkOAPIClient            = internallark.ErrOAPIClient
	ErrLarkOAPIStartTimeout      = internallark.ErrOAPIStartTimeout
	ErrLarkOAPIAlreadyStarted    = internallark.ErrOAPIAlreadyStarted
	ErrLarkOAPICardIDMissing     = internallark.ErrOAPICardIDMissing
	ErrLarkOAPIMessageMissing    = internallark.ErrOAPIMessageMissing
	ErrLarkOAPIReplyThread       = internallark.ErrOAPIReplyThread
	ErrLarkOAPIThreadIDSend      = internallark.ErrOAPIThreadIDSend
)

const (
	DefaultLarkOAPIRequestTimeout = internallark.DefaultOAPIRequestTimeout
	DefaultLarkOAPIStartTimeout   = internallark.DefaultOAPIStartTimeout
	DefaultLarkOAPIStreamThrottle = internallark.DefaultOAPIStreamThrottle
)

type LarkIncomingEvent struct {
	Kind LarkEventKind
	Raw  any

	Message    *LarkMessageInput
	Comment    *LarkCommentInput
	CardAction *LarkCardActionInput
	Reconnect  *LarkReconnectInput
	Keepalive  *LarkKeepaliveInput
	Disconnect *LarkDisconnectInput
}

type LarkTransportHandler interface {
	HandleLarkTransportEvent(ctx context.Context, event LarkIncomingEvent) error
}

type LarkTransport interface {
	Connect(ctx context.Context, handler LarkTransportHandler) error
	Disconnect(ctx context.Context) error
	BotIdentity(ctx context.Context) (LarkBotIdentity, error)
	SendMessage(ctx context.Context, req LarkSendMessageRequest) (LarkSendResult, error)
	SendCard(ctx context.Context, req LarkSendCardRequest) (LarkSendResult, error)
	UpdateCard(ctx context.Context, req LarkUpdateCardRequest) error
}

type LarkCarrierThreadResolver interface {
	ResolveCarrierThreadID(ctx context.Context, chatID, messageID string) (string, error)
}

type LarkIntakeSink interface {
	HandleLarkEvent(ctx context.Context, event LarkNormalizedEvent) error
}

type LarkIntakeSinkFunc func(ctx context.Context, event LarkNormalizedEvent) error

func (f LarkIntakeSinkFunc) HandleLarkEvent(ctx context.Context, event LarkNormalizedEvent) error {
	return f(ctx, event)
}

type LarkCardActionDispatcher interface {
	Dispatch(ctx context.Context, input LarkCardActionInput) (CardActionDispatchResult, error)
}

type LarkProfileProjectionHook interface {
	ProjectLarkProfile(ctx context.Context, req LarkProfileProjectionRequest) (LarkProfileProjectionResult, error)
}

type LarkProfileProjectionHookFunc func(ctx context.Context, req LarkProfileProjectionRequest) (LarkProfileProjectionResult, error)

func (f LarkProfileProjectionHookFunc) ProjectLarkProfile(ctx context.Context, req LarkProfileProjectionRequest) (LarkProfileProjectionResult, error) {
	return f(ctx, req)
}

type LarkBotIdentity struct {
	OpenID  string
	UserID  string
	UnionID string
	Name    string
	Raw     any
}

type LarkMessageContent struct {
	Text     string
	Markdown string
	Card     map[string]any
	Raw      any
}

type LarkSendOptions struct {
	ReplyTo       string
	ReplyInThread bool
	ThreadID      string
	Metadata      map[string]any
}

type LarkSendMessageRequest struct {
	ChatID  string
	Content LarkMessageContent
	Options LarkSendOptions
}

type LarkSendCardRequest struct {
	ChatID  string
	Card    map[string]any
	Options LarkSendOptions
}

type LarkUpdateCardRequest struct {
	MessageID string
	Card      map[string]any
}

type LarkUpdateMessageRequest struct {
	MessageID string
	Content   LarkMessageContent
}

type LarkMessageUpdater interface {
	UpdateMessage(ctx context.Context, req LarkUpdateMessageRequest) error
}

type LarkMessageReactionRequest struct {
	MessageID  string
	EmojiType  string
	ReactionID string
}

type LarkMessageReactionResult struct {
	ReactionID string
}

type LarkMessageReactioner interface {
	AddMessageReaction(ctx context.Context, req LarkMessageReactionRequest) (LarkMessageReactionResult, error)
	DeleteMessageReaction(ctx context.Context, req LarkMessageReactionRequest) error
}

type LarkCOTRef struct {
	COTID     string
	MessageID string
}

type LarkCOTEvent struct {
	EventType string `json:"event_type"`
	Content   string `json:"content"`
	Timestamp int64  `json:"timestamp"`
}

type LarkCOTCreateRequest struct {
	ReceiveID       string
	OriginMessageID string
}

type LarkCOTUpdateRequest struct {
	Ref    LarkCOTRef
	Events []LarkCOTEvent
}

type LarkCOTCompleteRequest struct {
	Ref    LarkCOTRef
	Reason string
}

type LarkCOTClient interface {
	CreateMessageCOT(ctx context.Context, req LarkCOTCreateRequest) (LarkCOTRef, error)
	UpdateMessageCOT(ctx context.Context, req LarkCOTUpdateRequest) error
	CompleteMessageCOT(ctx context.Context, req LarkCOTCompleteRequest) error
}

type LarkSendResult struct {
	MessageID string
	Raw       any
}

type LarkProfileProjectionRequest struct {
	BotIdentity LarkBotIdentity
	StartedAt   time.Time
}

type LarkProfileProjectionResult struct {
	LarkCliSourceConfigFile string
	LarkChannelEnv          map[string]string
	IdentityPolicyApplied   bool
	BotIdentity             LarkBotIdentity
}

type LarkCLIProjectionHookOptions struct {
	Config              LarkCLIAppConfig
	Paths               LarkCLIProjectionPaths
	Env                 LarkCLIEnvContext
	IdentityPreset      LarkCLIIdentityPreset
	ApplyIdentityPolicy bool
	IdentityOptions     LarkCLIIdentityPolicyOptions
}

type LarkAdapterOptions struct {
	Transport LarkTransport
	Intake    LarkIntakeSink

	CardActions                LarkCardActionDispatcher
	ForwardCardPromptsToIntake bool
	SelfLoopPolicy             LarkSelfLoopPolicy
	UseBotIdentityForSelfLoop  *bool
	ProfileProjection          LarkProfileProjectionHook
	Now                        func() time.Time
}

type LarkHandleResult struct {
	Normalized      LarkNormalizedEvent
	CardDispatch    *CardActionDispatchResult
	DroppedSelfLoop bool
}

type LarkOAPIError struct {
	Operation string
	Code      int
	Message   string
}

func (e *LarkOAPIError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("%s API error: %d %s", e.Operation, e.Code, e.Message)
}

func fromInternalLarkError(err error) error {
	if err == nil {
		return nil
	}
	var oapiErr *internallark.OAPIError
	if errors.As(err, &oapiErr) {
		return &LarkOAPIError{
			Operation: oapiErr.Operation,
			Code:      oapiErr.Code,
			Message:   oapiErr.Message,
		}
	}
	return err
}

type LarkAdapter struct {
	inner *internallark.Adapter
}

type OAPILarkTransport struct {
	inner *internallark.OAPITransport
}

type FakeLarkTransport struct {
	inner *internallark.FakeTransport
	mu    sync.RWMutex

	ConnectErr      error
	DisconnectErr   error
	SendErr         error
	UpdateErr       error
	IdentityErr     error
	CreateChatErr   error
	CreateCardErr   error
	SendCardIDErr   error
	SendRawCardErr  error
	UpdateCardIDErr error
	ReactionErr     error
	COTCreateErr    error
	COTUpdateErr    error
	COTCompleteErr  error
}

type OAPILarkTransportOptions struct {
	AppID     string
	AppSecret string
	Tenant    string
	Domain    string
	Source    string

	RequestTimeout         time.Duration
	StartTimeout           time.Duration
	Headers                http.Header
	HTTPClient             larkcore.HttpClient
	LogLevel               larkcore.LogLevel
	Logger                 larkcore.Logger
	RegistrationDomain     string
	RegistrationLarkDomain string

	ClientAssertionProvider larkcore.ClientAssertionProvider
	ChannelOptions          []channeltypes.ChannelOption

	DisableWebSocket   bool
	EnableSDKChatQueue bool
}

const defaultOAPILarkTransportSource = "lark-channel-bridge"

var _ LarkTransport = (*OAPILarkTransport)(nil)
var _ LarkTransport = (*FakeLarkTransport)(nil)
var _ CommandChatCreator = (*OAPILarkTransport)(nil)
var _ CommandChatCreator = (*FakeLarkTransport)(nil)
var _ CommandChatMessenger = (*OAPILarkTransport)(nil)
var _ CommandChatMessenger = (*FakeLarkTransport)(nil)
var _ LarkQuoteResolver = (*OAPILarkTransport)(nil)
var _ LarkScopeChecker = (*OAPILarkTransport)(nil)
var _ LarkScopeGrantRequester = (*OAPILarkTransport)(nil)
var _ LarkRuntimeInfoSource = (*OAPILarkTransport)(nil)
var _ LarkMessageReactioner = (*OAPILarkTransport)(nil)
var _ LarkCOTClient = (*OAPILarkTransport)(nil)

func NewLarkAdapter(options LarkAdapterOptions) (*LarkAdapter, error) {
	adapter, err := internallark.NewAdapter(internallark.AdapterOptions{
		Transport:                  wrapInternalLarkTransport(options.Transport),
		Intake:                     wrapInternalLarkIntake(options.Intake),
		CardActions:                wrapInternalLarkCardDispatcher(options.CardActions),
		ForwardCardPromptsToIntake: options.ForwardCardPromptsToIntake,
		SelfLoopPolicy:             toInternalLarkSelfLoopPolicy(options.SelfLoopPolicy),
		UseBotIdentityForSelfLoop:  options.UseBotIdentityForSelfLoop,
		ProfileProjection:          wrapInternalLarkProfileProjection(options.ProfileProjection),
		Now:                        options.Now,
	})
	if err != nil {
		return nil, err
	}
	return &LarkAdapter{inner: adapter}, nil
}

func NewOAPILarkTransport(options OAPILarkTransportOptions) (*OAPILarkTransport, error) {
	source := options.Source
	if source == "" {
		source = defaultOAPILarkTransportSource
	}
	transport, err := internallark.NewOAPITransport(internallark.OAPITransportOptions{
		AppID:                   options.AppID,
		AppSecret:               options.AppSecret,
		Tenant:                  options.Tenant,
		Domain:                  options.Domain,
		Source:                  source,
		RequestTimeout:          options.RequestTimeout,
		StartTimeout:            options.StartTimeout,
		Headers:                 options.Headers,
		HTTPClient:              options.HTTPClient,
		LogLevel:                options.LogLevel,
		Logger:                  options.Logger,
		RegistrationDomain:      options.RegistrationDomain,
		RegistrationLarkDomain:  options.RegistrationLarkDomain,
		ChannelOptions:          options.ChannelOptions,
		ClientAssertionProvider: options.ClientAssertionProvider,
		DisableWebSocket:        options.DisableWebSocket,
		EnableSDKChatQueue:      options.EnableSDKChatQueue,
	})
	if err != nil {
		return nil, err
	}
	return &OAPILarkTransport{inner: transport}, nil
}

func NewLarkCLIProjectionHook(options LarkCLIProjectionHookOptions) LarkProfileProjectionHook {
	return larkProfileProjectionHook{options: options}
}

func NewFakeLarkTransport(identity LarkBotIdentity) *FakeLarkTransport {
	return &FakeLarkTransport{inner: internallark.NewFakeTransport(toInternalLarkBotIdentity(identity))}
}

func (a *LarkAdapter) Start(ctx context.Context) error {
	if a == nil || a.inner == nil {
		return ErrNilLarkTransport
	}
	return fromInternalLarkError(a.inner.Start(ctx))
}

func (a *LarkAdapter) Disconnect(ctx context.Context) error {
	if a == nil || a.inner == nil {
		return nil
	}
	return a.inner.Disconnect(ctx)
}

func (a *LarkAdapter) BotIdentity() LarkBotIdentity {
	if a == nil || a.inner == nil {
		return LarkBotIdentity{}
	}
	return fromInternalLarkBotIdentity(a.inner.BotIdentity())
}

func (a *LarkAdapter) ProjectionResult() LarkProfileProjectionResult {
	if a == nil || a.inner == nil {
		return LarkProfileProjectionResult{}
	}
	return fromInternalLarkProfileProjectionResult(a.inner.ProjectionResult())
}

func (a *LarkAdapter) Started() bool {
	return a != nil && a.inner != nil && a.inner.Started()
}

func (a *LarkAdapter) HandleLarkTransportEvent(ctx context.Context, event LarkIncomingEvent) error {
	_, err := a.HandleTransportEvent(ctx, event)
	return err
}

func (a *LarkAdapter) HandleTransportEvent(ctx context.Context, event LarkIncomingEvent) (LarkHandleResult, error) {
	if a == nil || a.inner == nil {
		return LarkHandleResult{}, ErrNilLarkTransport
	}
	result, err := a.inner.HandleTransportEvent(ctx, toInternalLarkIncomingEvent(event))
	return fromInternalLarkHandleResult(result), fromInternalLarkError(err)
}

func (a *LarkAdapter) SendMessage(ctx context.Context, req LarkSendMessageRequest) (LarkSendResult, error) {
	if a == nil || a.inner == nil {
		return LarkSendResult{}, ErrNilLarkTransport
	}
	result, err := a.inner.SendMessage(ctx, toInternalLarkSendMessageRequest(req))
	return fromInternalLarkSendResult(result), fromInternalLarkError(err)
}

func (a *LarkAdapter) SendCard(ctx context.Context, req LarkSendCardRequest) (LarkSendResult, error) {
	if a == nil || a.inner == nil {
		return LarkSendResult{}, ErrNilLarkTransport
	}
	result, err := a.inner.SendCard(ctx, toInternalLarkSendCardRequest(req))
	return fromInternalLarkSendResult(result), fromInternalLarkError(err)
}

func (a *LarkAdapter) UpdateCard(ctx context.Context, req LarkUpdateCardRequest) error {
	if a == nil || a.inner == nil {
		return ErrNilLarkTransport
	}
	return fromInternalLarkError(a.inner.UpdateCard(ctx, toInternalLarkUpdateCardRequest(req)))
}

func (a *LarkAdapter) ResolveCarrierThreadID(ctx context.Context, chatID, messageID string) (string, error) {
	if a == nil || a.inner == nil {
		return "", ErrNilLarkTransport
	}
	threadID, err := a.inner.ResolveCarrierThreadID(ctx, chatID, messageID)
	return threadID, fromInternalLarkError(err)
}

func (t *OAPILarkTransport) Connect(ctx context.Context, handler LarkTransportHandler) error {
	if t == nil || t.inner == nil {
		return ErrNilLarkTransport
	}
	return fromInternalLarkError(t.inner.Connect(ctx, internalLarkHandlerAdapter{handler: handler}))
}

func (t *OAPILarkTransport) Disconnect(ctx context.Context) error {
	if t == nil || t.inner == nil {
		return nil
	}
	return t.inner.Disconnect(ctx)
}

func (t *OAPILarkTransport) BotIdentity(ctx context.Context) (LarkBotIdentity, error) {
	if t == nil || t.inner == nil {
		return LarkBotIdentity{}, ErrNilLarkTransport
	}
	identity, err := t.inner.BotIdentity(ctx)
	return fromInternalLarkBotIdentity(identity), fromInternalLarkError(err)
}

func (t *OAPILarkTransport) SendMessage(ctx context.Context, req LarkSendMessageRequest) (LarkSendResult, error) {
	if t == nil || t.inner == nil {
		return LarkSendResult{}, ErrNilLarkTransport
	}
	result, err := t.inner.SendMessage(ctx, toInternalLarkSendMessageRequest(req))
	return fromInternalLarkSendResult(result), fromInternalLarkError(err)
}

func (t *OAPILarkTransport) CreateBoundChat(ctx context.Context, input CommandCreateBoundChatInput) (CommandCreatedChat, error) {
	if t == nil || t.inner == nil {
		return CommandCreatedChat{}, ErrNilLarkTransport
	}
	result, err := t.inner.CreateBoundChat(ctx, toInternalCreateBoundChatRequest(input))
	return fromInternalCreatedChat(result), fromInternalLarkError(err)
}

func (t *OAPILarkTransport) SendMessageToChat(ctx context.Context, chatID string, markdown string) error {
	_, err := t.SendMessage(ctx, LarkSendMessageRequest{
		ChatID: chatID,
		Content: LarkMessageContent{
			Markdown: markdown,
		},
	})
	return err
}

func (t *OAPILarkTransport) SendCard(ctx context.Context, req LarkSendCardRequest) (LarkSendResult, error) {
	if t == nil || t.inner == nil {
		return LarkSendResult{}, ErrNilLarkTransport
	}
	result, err := t.inner.SendCard(ctx, toInternalLarkSendCardRequest(req))
	return fromInternalLarkSendResult(result), fromInternalLarkError(err)
}

func (t *OAPILarkTransport) UpdateCard(ctx context.Context, req LarkUpdateCardRequest) error {
	if t == nil || t.inner == nil {
		return ErrNilLarkTransport
	}
	return fromInternalLarkError(t.inner.UpdateCard(ctx, toInternalLarkUpdateCardRequest(req)))
}

func (t *OAPILarkTransport) UpdateMessage(ctx context.Context, req LarkUpdateMessageRequest) error {
	if t == nil || t.inner == nil {
		return ErrNilLarkTransport
	}
	return fromInternalLarkError(t.inner.UpdateMessage(ctx, toInternalLarkUpdateMessageRequest(req)))
}

func (t *OAPILarkTransport) CreateCard(ctx context.Context, card map[string]any) (string, error) {
	if t == nil || t.inner == nil {
		return "", ErrNilLarkTransport
	}
	cardID, err := t.inner.CreateCard(ctx, card)
	return cardID, fromInternalLarkError(err)
}

func (t *OAPILarkTransport) SendCardID(ctx context.Context, recipientID string, cardID string, opts ManagedCardSendOptions) (string, error) {
	if t == nil || t.inner == nil {
		return "", ErrNilLarkTransport
	}
	messageID, err := t.inner.SendCardID(ctx, recipientID, cardID, toInternalManagedCardSendOptions(opts))
	return messageID, fromInternalLarkError(err)
}

func (t *OAPILarkTransport) SendRawCard(ctx context.Context, recipientID string, card map[string]any, opts ManagedCardSendOptions) (string, error) {
	if t == nil || t.inner == nil {
		return "", ErrNilLarkTransport
	}
	messageID, err := t.inner.SendRawCard(ctx, recipientID, card, toInternalManagedCardSendOptions(opts))
	return messageID, fromInternalLarkError(err)
}

func (t *OAPILarkTransport) UpdateCardByID(ctx context.Context, cardID string, card map[string]any, sequence int) error {
	if t == nil || t.inner == nil {
		return ErrNilLarkTransport
	}
	return fromInternalLarkError(t.inner.UpdateCardByID(ctx, cardID, card, sequence))
}

func (t *OAPILarkTransport) UpdateRawCard(ctx context.Context, messageID string, card map[string]any) error {
	if t == nil || t.inner == nil {
		return ErrNilLarkTransport
	}
	return fromInternalLarkError(t.inner.UpdateRawCard(ctx, messageID, card))
}

func (t *OAPILarkTransport) AddMessageReaction(ctx context.Context, req LarkMessageReactionRequest) (LarkMessageReactionResult, error) {
	if t == nil || t.inner == nil {
		return LarkMessageReactionResult{}, ErrNilLarkTransport
	}
	result, err := t.inner.AddMessageReaction(ctx, toInternalLarkReactionRequest(req))
	return fromInternalLarkReactionResult(result), fromInternalLarkError(err)
}

func (t *OAPILarkTransport) DeleteMessageReaction(ctx context.Context, req LarkMessageReactionRequest) error {
	if t == nil || t.inner == nil {
		return ErrNilLarkTransport
	}
	return fromInternalLarkError(t.inner.DeleteMessageReaction(ctx, toInternalLarkReactionRequest(req)))
}

func (t *OAPILarkTransport) CreateMessageCOT(ctx context.Context, req LarkCOTCreateRequest) (LarkCOTRef, error) {
	if t == nil || t.inner == nil {
		return LarkCOTRef{}, ErrNilLarkTransport
	}
	ref, err := t.inner.CreateMessageCOT(ctx, appcot.CreateRequest(req))
	return LarkCOTRef(ref), fromInternalLarkError(err)
}

func (t *OAPILarkTransport) UpdateMessageCOT(ctx context.Context, req LarkCOTUpdateRequest) error {
	if t == nil || t.inner == nil {
		return ErrNilLarkTransport
	}
	return fromInternalLarkError(t.inner.UpdateMessageCOT(ctx, toInternalLarkCOTUpdateRequest(req)))
}

func (t *OAPILarkTransport) CompleteMessageCOT(ctx context.Context, req LarkCOTCompleteRequest) error {
	if t == nil || t.inner == nil {
		return ErrNilLarkTransport
	}
	return fromInternalLarkError(t.inner.CompleteMessageCOT(ctx, appcot.CompleteRequest{Ref: appcot.Ref(req.Ref), Reason: req.Reason}))
}

func (t *OAPILarkTransport) ResolveCarrierThreadID(ctx context.Context, chatID, messageID string) (string, error) {
	if t == nil || t.inner == nil {
		return "", ErrNilLarkTransport
	}
	threadID, err := t.inner.ResolveCarrierThreadID(ctx, chatID, messageID)
	return threadID, fromInternalLarkError(err)
}

func (t *OAPILarkTransport) ResolveLarkQuote(ctx context.Context, target LarkQuoteTarget) (BridgePromptQuotedMessage, bool, error) {
	if t == nil || t.inner == nil {
		return BridgePromptQuotedMessage{}, false, ErrNilLarkTransport
	}
	message, ok, err := t.inner.ResolveLarkQuote(ctx, toInternalLarkQuoteTarget(target))
	return fromInternalBridgePromptQuotedMessage(message), ok, fromInternalLarkError(err)
}

func (t *OAPILarkTransport) HasLarkScope(ctx context.Context, appID string, scope string) (bool, error) {
	if t == nil || t.inner == nil {
		return false, ErrNilLarkTransport
	}
	ok, err := t.inner.HasLarkScope(ctx, appID, scope)
	return ok, fromInternalLarkError(err)
}

func (t *OAPILarkTransport) FetchLarkOwner(ctx context.Context, appID string) (string, error) {
	if t == nil || t.inner == nil {
		return "", ErrNilLarkTransport
	}
	owner, err := t.inner.FetchLarkOwner(ctx, appID)
	return owner, fromInternalLarkError(err)
}

func (t *OAPILarkTransport) ListLarkKnownChats(ctx context.Context, maxPages int) ([]LarkKnownChatInfo, error) {
	if t == nil || t.inner == nil {
		return nil, ErrNilLarkTransport
	}
	chats, err := t.inner.ListLarkKnownChats(ctx, maxPages)
	if err != nil {
		return nil, fromInternalLarkError(err)
	}
	return fromInternalLarkKnownChats(chats), nil
}

func (t *OAPILarkTransport) RequestLarkScopeGrant(ctx context.Context, req LarkScopeGrantRequest) (LarkScopeGrantLink, error) {
	if t == nil || t.inner == nil {
		return LarkScopeGrantLink{}, ErrNilLarkTransport
	}
	link, err := t.inner.RequestLarkScopeGrant(ctx, toInternalLarkScopeGrantRequest(req))
	if err != nil {
		return LarkScopeGrantLink{}, fromInternalLarkError(err)
	}
	return fromInternalLarkScopeGrantLink(link), nil
}

func (t *OAPILarkTransport) DownloadResource(ctx context.Context, req MediaDownloadRequest) (MediaDownloadResult, error) {
	if t == nil || t.inner == nil {
		return MediaDownloadResult{}, ErrNilLarkTransport
	}
	result, err := t.inner.DownloadResource(ctx, toInternalMediaDownloadRequest(req))
	return fromInternalMediaDownloadResult(result), fromInternalLarkError(err)
}

func (t *FakeLarkTransport) Connect(ctx context.Context, handler LarkTransportHandler) error {
	t.syncErrors()
	return t.inner.Connect(ctx, internalLarkHandlerAdapter{handler: handler})
}

func (t *FakeLarkTransport) Disconnect(ctx context.Context) error {
	t.syncErrors()
	return t.inner.Disconnect(ctx)
}

func (t *FakeLarkTransport) BotIdentity(ctx context.Context) (LarkBotIdentity, error) {
	t.syncErrors()
	identity, err := t.inner.BotIdentity(ctx)
	return fromInternalLarkBotIdentity(identity), err
}

func (t *FakeLarkTransport) Emit(ctx context.Context, event LarkIncomingEvent) error {
	t.syncErrors()
	return t.inner.Emit(ctx, toInternalLarkIncomingEvent(event))
}

func (t *FakeLarkTransport) SendMessage(ctx context.Context, req LarkSendMessageRequest) (LarkSendResult, error) {
	t.syncErrors()
	result, err := t.inner.SendMessage(ctx, toInternalLarkSendMessageRequest(req))
	return fromInternalLarkSendResult(result), err
}

func (t *FakeLarkTransport) CreateBoundChat(ctx context.Context, input CommandCreateBoundChatInput) (CommandCreatedChat, error) {
	t.syncErrors()
	result, err := t.inner.CreateBoundChat(ctx, toInternalCreateBoundChatRequest(input))
	return fromInternalCreatedChat(result), err
}

func (t *FakeLarkTransport) CreatedChatSnapshot() []CommandCreateBoundChatInput {
	in := t.inner.CreatedChatSnapshot()
	out := make([]CommandCreateBoundChatInput, 0, len(in))
	for _, req := range in {
		out = append(out, fromInternalCreateBoundChatRequest(req))
	}
	return out
}

func (t *FakeLarkTransport) SendMessageToChat(ctx context.Context, chatID string, markdown string) error {
	_, err := t.SendMessage(ctx, LarkSendMessageRequest{
		ChatID: chatID,
		Content: LarkMessageContent{
			Markdown: markdown,
		},
	})
	return err
}

func (t *FakeLarkTransport) SentMessageSnapshot() []LarkSendMessageRequest {
	return fromInternalLarkSendMessageRequests(t.inner.SentMessageSnapshot())
}

func (t *FakeLarkTransport) SendCard(ctx context.Context, req LarkSendCardRequest) (LarkSendResult, error) {
	t.syncErrors()
	result, err := t.inner.SendCard(ctx, toInternalLarkSendCardRequest(req))
	return fromInternalLarkSendResult(result), err
}

func (t *FakeLarkTransport) SentCardSnapshot() []LarkSendCardRequest {
	return fromInternalLarkSendCardRequests(t.inner.SentCardSnapshot())
}

func (t *FakeLarkTransport) UpdateCard(ctx context.Context, req LarkUpdateCardRequest) error {
	t.syncErrors()
	return t.inner.UpdateCard(ctx, toInternalLarkUpdateCardRequest(req))
}

func (t *FakeLarkTransport) UpdatedCardSnapshot() []LarkUpdateCardRequest {
	return fromInternalLarkUpdateCardRequests(t.inner.UpdatedCardSnapshot())
}

func (t *FakeLarkTransport) UpdateMessage(ctx context.Context, req LarkUpdateMessageRequest) error {
	t.syncErrors()
	return t.inner.UpdateMessage(ctx, toInternalLarkUpdateMessageRequest(req))
}

func (t *FakeLarkTransport) UpdatedMessageSnapshot() []LarkUpdateMessageRequest {
	return fromInternalLarkUpdateMessageRequests(t.inner.UpdatedMessageSnapshot())
}

func (t *FakeLarkTransport) CreateCard(ctx context.Context, card map[string]any) (string, error) {
	t.syncErrors()
	return t.inner.CreateCard(ctx, card)
}

func (t *FakeLarkTransport) SendCardID(ctx context.Context, recipientID string, cardID string, opts ManagedCardSendOptions) (string, error) {
	t.syncErrors()
	return t.inner.SendCardID(ctx, recipientID, cardID, toInternalManagedCardSendOptions(opts))
}

func (t *FakeLarkTransport) SendRawCard(ctx context.Context, recipientID string, card map[string]any, opts ManagedCardSendOptions) (string, error) {
	t.syncErrors()
	return t.inner.SendRawCard(ctx, recipientID, card, toInternalManagedCardSendOptions(opts))
}

func (t *FakeLarkTransport) UpdateCardByID(ctx context.Context, cardID string, card map[string]any, sequence int) error {
	t.syncErrors()
	return t.inner.UpdateCardByID(ctx, cardID, card, sequence)
}

func (t *FakeLarkTransport) UpdateRawCard(ctx context.Context, messageID string, card map[string]any) error {
	t.syncErrors()
	return t.inner.UpdateRawCard(ctx, messageID, card)
}

func (t *FakeLarkTransport) AddMessageReaction(ctx context.Context, req LarkMessageReactionRequest) (LarkMessageReactionResult, error) {
	t.syncErrors()
	result, err := t.inner.AddMessageReaction(ctx, toInternalLarkReactionRequest(req))
	return fromInternalLarkReactionResult(result), err
}

func (t *FakeLarkTransport) DeleteMessageReaction(ctx context.Context, req LarkMessageReactionRequest) error {
	t.syncErrors()
	return t.inner.DeleteMessageReaction(ctx, toInternalLarkReactionRequest(req))
}

func (t *FakeLarkTransport) AddedReactionSnapshot() []LarkMessageReactionRequest {
	return fromInternalLarkReactionRequests(t.inner.AddedReactionSnapshot())
}

func (t *FakeLarkTransport) DeletedReactionSnapshot() []LarkMessageReactionRequest {
	return fromInternalLarkReactionRequests(t.inner.DeletedReactionSnapshot())
}

func (t *FakeLarkTransport) CreateMessageCOT(ctx context.Context, req LarkCOTCreateRequest) (LarkCOTRef, error) {
	t.syncErrors()
	ref, err := t.inner.CreateMessageCOT(ctx, appcot.CreateRequest(req))
	return LarkCOTRef(ref), err
}

func (t *FakeLarkTransport) UpdateMessageCOT(ctx context.Context, req LarkCOTUpdateRequest) error {
	t.syncErrors()
	return t.inner.UpdateMessageCOT(ctx, toInternalLarkCOTUpdateRequest(req))
}

func (t *FakeLarkTransport) CompleteMessageCOT(ctx context.Context, req LarkCOTCompleteRequest) error {
	t.syncErrors()
	return t.inner.CompleteMessageCOT(ctx, appcot.CompleteRequest{Ref: appcot.Ref(req.Ref), Reason: req.Reason})
}

func (t *FakeLarkTransport) CreatedCOTSnapshot() []LarkCOTCreateRequest {
	in := t.inner.CreatedCOTSnapshot()
	out := make([]LarkCOTCreateRequest, 0, len(in))
	for _, req := range in {
		out = append(out, LarkCOTCreateRequest(req))
	}
	return out
}

func (t *FakeLarkTransport) UpdatedCOTSnapshot() []LarkCOTUpdateRequest {
	in := t.inner.UpdatedCOTSnapshot()
	out := make([]LarkCOTUpdateRequest, 0, len(in))
	for _, req := range in {
		out = append(out, fromInternalLarkCOTUpdateRequest(req))
	}
	return out
}

func (t *FakeLarkTransport) CompletedCOTSnapshot() []LarkCOTCompleteRequest {
	in := t.inner.CompletedCOTSnapshot()
	out := make([]LarkCOTCompleteRequest, 0, len(in))
	for _, req := range in {
		out = append(out, LarkCOTCompleteRequest{Ref: LarkCOTRef(req.Ref), Reason: req.Reason})
	}
	return out
}

func (t *FakeLarkTransport) ResolveCarrierThreadID(ctx context.Context, chatID, messageID string) (string, error) {
	return t.inner.ResolveCarrierThreadID(ctx, chatID, messageID)
}

func (t *FakeLarkTransport) SetCarrierThread(chatID, messageID, threadID string) {
	t.inner.SetCarrierThread(chatID, messageID, threadID)
}

func (t *FakeLarkTransport) DownloadResource(ctx context.Context, req MediaDownloadRequest) (MediaDownloadResult, error) {
	result, err := t.inner.DownloadResource(ctx, toInternalMediaDownloadRequest(req))
	return fromInternalMediaDownloadResult(result), err
}

func (t *FakeLarkTransport) SetResourceDownload(fileKey string, content []byte, contentType string) {
	t.inner.SetResourceDownload(fileKey, content, contentType)
}

func (t *FakeLarkTransport) syncErrors() {
	if t == nil || t.inner == nil {
		return
	}
	t.mu.RLock()
	errors := internallark.FakeTransportErrors{
		ConnectErr:      t.ConnectErr,
		DisconnectErr:   t.DisconnectErr,
		SendErr:         t.SendErr,
		UpdateErr:       t.UpdateErr,
		IdentityErr:     t.IdentityErr,
		CreateChatErr:   t.CreateChatErr,
		CreateCardErr:   t.CreateCardErr,
		SendCardIDErr:   t.SendCardIDErr,
		SendRawCardErr:  t.SendRawCardErr,
		UpdateCardIDErr: t.UpdateCardIDErr,
		ReactionErr:     t.ReactionErr,
		COTCreateErr:    t.COTCreateErr,
		COTUpdateErr:    t.COTUpdateErr,
		COTCompleteErr:  t.COTCompleteErr,
	}
	t.mu.RUnlock()
	t.inner.SetErrors(errors)
}

func (t *OAPILarkTransport) internalCommentSurface() *internallark.OAPITransport {
	if t == nil {
		return nil
	}
	return t.inner
}

func (t *OAPILarkTransport) internalMediaDownloader() appmedia.ResourceDownloader {
	if t == nil {
		return nil
	}
	return t.inner
}

func (t *OAPILarkTransport) internalCOTClient() appcot.Client {
	if t == nil {
		return nil
	}
	return t.inner
}

func (t *FakeLarkTransport) internalMediaDownloader() appmedia.ResourceDownloader {
	if t == nil {
		return nil
	}
	t.syncErrors()
	return t.inner
}

func (t *FakeLarkTransport) internalCOTClient() appcot.Client {
	if t == nil {
		return nil
	}
	t.syncErrors()
	return t.inner
}

func wrapInternalLarkCOTClient(client LarkCOTClient) appcot.Client {
	if client == nil {
		return nil
	}
	if provider, ok := client.(interface{ internalCOTClient() appcot.Client }); ok {
		return provider.internalCOTClient()
	}
	if adapter, ok := client.(publicCOTClientAdapter); ok {
		return adapter.inner
	}
	return internalCOTClientAdapter{client: client}
}

type internalCOTClientAdapter struct {
	client LarkCOTClient
}

func (a internalCOTClientAdapter) CreateMessageCOT(ctx context.Context, req appcot.CreateRequest) (appcot.Ref, error) {
	ref, err := a.client.CreateMessageCOT(ctx, LarkCOTCreateRequest(req))
	return appcot.Ref(ref), err
}

func (a internalCOTClientAdapter) UpdateMessageCOT(ctx context.Context, req appcot.UpdateRequest) error {
	return a.client.UpdateMessageCOT(ctx, fromInternalLarkCOTUpdateRequest(req))
}

func (a internalCOTClientAdapter) CompleteMessageCOT(ctx context.Context, req appcot.CompleteRequest) error {
	return a.client.CompleteMessageCOT(ctx, LarkCOTCompleteRequest{Ref: LarkCOTRef(req.Ref), Reason: req.Reason})
}

type publicCOTClientAdapter struct {
	inner appcot.Client
}

func (a publicCOTClientAdapter) CreateMessageCOT(ctx context.Context, req LarkCOTCreateRequest) (LarkCOTRef, error) {
	ref, err := a.inner.CreateMessageCOT(ctx, appcot.CreateRequest(req))
	return LarkCOTRef(ref), err
}

func (a publicCOTClientAdapter) UpdateMessageCOT(ctx context.Context, req LarkCOTUpdateRequest) error {
	return a.inner.UpdateMessageCOT(ctx, toInternalLarkCOTUpdateRequest(req))
}

func (a publicCOTClientAdapter) CompleteMessageCOT(ctx context.Context, req LarkCOTCompleteRequest) error {
	return a.inner.CompleteMessageCOT(ctx, appcot.CompleteRequest{Ref: appcot.Ref(req.Ref), Reason: req.Reason})
}

type larkProfileProjectionHook struct {
	options LarkCLIProjectionHookOptions
}

func (h larkProfileProjectionHook) ProjectLarkProfile(ctx context.Context, req LarkProfileProjectionRequest) (LarkProfileProjectionResult, error) {
	internalOptions, err := toInternalLarkCLIProjectionHookOptions(h.options)
	if err != nil {
		return LarkProfileProjectionResult{}, err
	}
	result, err := internallark.NewLarkCLIProjectionHook(internalOptions).ProjectLarkProfile(ctx, toInternalLarkProfileProjectionRequest(req))
	return fromInternalLarkProfileProjectionResult(result), err
}

type internalLarkHandlerAdapter struct {
	handler LarkTransportHandler
}

func (a internalLarkHandlerAdapter) HandleLarkTransportEvent(ctx context.Context, event internallark.IncomingEvent) error {
	if a.handler == nil {
		return nil
	}
	return a.handler.HandleLarkTransportEvent(ctx, fromInternalLarkIncomingEvent(event))
}

type internalLarkTransportAdapter struct {
	transport LarkTransport
}

func wrapInternalLarkTransport(transport LarkTransport) internallark.Transport {
	if transport == nil {
		return nil
	}
	if oapi, ok := transport.(*OAPILarkTransport); ok {
		return oapi.inner
	}
	if fake, ok := transport.(*FakeLarkTransport); ok {
		return fake.inner
	}
	return internalLarkTransportAdapter{transport: transport}
}

func (a internalLarkTransportAdapter) Connect(ctx context.Context, handler internallark.TransportHandler) error {
	return a.transport.Connect(ctx, publicLarkHandlerAdapter{handler: handler})
}

func (a internalLarkTransportAdapter) Disconnect(ctx context.Context) error {
	return a.transport.Disconnect(ctx)
}

func (a internalLarkTransportAdapter) BotIdentity(ctx context.Context) (internallark.BotIdentity, error) {
	identity, err := a.transport.BotIdentity(ctx)
	return toInternalLarkBotIdentity(identity), err
}

func (a internalLarkTransportAdapter) SendMessage(ctx context.Context, req internallark.SendMessageRequest) (internallark.SendResult, error) {
	result, err := a.transport.SendMessage(ctx, fromInternalLarkSendMessageRequest(req))
	return toInternalLarkSendResult(result), err
}

func (a internalLarkTransportAdapter) SendCard(ctx context.Context, req internallark.SendCardRequest) (internallark.SendResult, error) {
	result, err := a.transport.SendCard(ctx, fromInternalLarkSendCardRequest(req))
	return toInternalLarkSendResult(result), err
}

func (a internalLarkTransportAdapter) UpdateCard(ctx context.Context, req internallark.UpdateCardRequest) error {
	return a.transport.UpdateCard(ctx, fromInternalLarkUpdateCardRequest(req))
}

type publicLarkHandlerAdapter struct {
	handler internallark.TransportHandler
}

func (a publicLarkHandlerAdapter) HandleLarkTransportEvent(ctx context.Context, event LarkIncomingEvent) error {
	if a.handler == nil {
		return nil
	}
	return a.handler.HandleLarkTransportEvent(ctx, toInternalLarkIncomingEvent(event))
}

type internalLarkIntakeAdapter struct {
	intake LarkIntakeSink
}

func wrapInternalLarkIntake(intake LarkIntakeSink) internallark.IntakeSink {
	if intake == nil {
		return nil
	}
	return internalLarkIntakeAdapter{intake: intake}
}

func (a internalLarkIntakeAdapter) HandleLarkEvent(ctx context.Context, event appintake.NormalizedEvent) error {
	return a.intake.HandleLarkEvent(ctx, fromInternalLarkNormalizedEvent(event))
}

type internalLarkCardDispatcherAdapter struct {
	dispatcher LarkCardActionDispatcher
}

func wrapInternalLarkCardDispatcher(dispatcher LarkCardActionDispatcher) internallark.CardActionDispatcher {
	if dispatcher == nil {
		return nil
	}
	return internalLarkCardDispatcherAdapter{dispatcher: dispatcher}
}

func (a internalLarkCardDispatcherAdapter) Dispatch(ctx context.Context, input appintake.CardActionInput) (appdispatch.Result, error) {
	result, err := a.dispatcher.Dispatch(ctx, fromInternalLarkCardActionInput(input))
	return toInternalCardDispatchResult(result), err
}

type internalLarkProfileProjectionAdapter struct {
	hook LarkProfileProjectionHook
}

func wrapInternalLarkProfileProjection(hook LarkProfileProjectionHook) internallark.ProfileProjectionHook {
	if hook == nil {
		return nil
	}
	return internalLarkProfileProjectionAdapter{hook: hook}
}

func (a internalLarkProfileProjectionAdapter) ProjectLarkProfile(ctx context.Context, req internallark.ProfileProjectionRequest) (internallark.ProfileProjectionResult, error) {
	result, err := a.hook.ProjectLarkProfile(ctx, fromInternalLarkProfileProjectionRequest(req))
	return toInternalLarkProfileProjectionResult(result), err
}

func toInternalLarkIncomingEvent(event LarkIncomingEvent) internallark.IncomingEvent {
	return internallark.IncomingEvent{
		Kind:       appintake.EventKind(event.Kind),
		Raw:        event.Raw,
		Message:    toInternalLarkMessageInputPtr(event.Message),
		Comment:    toInternalLarkCommentInputPtr(event.Comment),
		CardAction: toInternalLarkCardActionInputPtr(event.CardAction),
		Reconnect:  toInternalLarkReconnectInputPtr(event.Reconnect),
		Keepalive:  toInternalLarkKeepaliveInputPtr(event.Keepalive),
		Disconnect: toInternalLarkDisconnectInputPtr(event.Disconnect),
	}
}

func fromInternalLarkIncomingEvent(event internallark.IncomingEvent) LarkIncomingEvent {
	return LarkIncomingEvent{
		Kind:       LarkEventKind(event.Kind),
		Raw:        event.Raw,
		Message:    fromInternalLarkMessageInputPtr(event.Message),
		Comment:    fromInternalLarkCommentInputPtr(event.Comment),
		CardAction: fromInternalLarkCardActionInputPtr(event.CardAction),
		Reconnect:  fromInternalLarkReconnectInputPtr(event.Reconnect),
		Keepalive:  fromInternalLarkKeepaliveInputPtr(event.Keepalive),
		Disconnect: fromInternalLarkDisconnectInputPtr(event.Disconnect),
	}
}

func toInternalLarkBotIdentity(identity LarkBotIdentity) internallark.BotIdentity {
	return internallark.BotIdentity(identity)
}

func fromInternalLarkBotIdentity(identity internallark.BotIdentity) LarkBotIdentity {
	return LarkBotIdentity(identity)
}

func toInternalLarkMessageContent(content LarkMessageContent) internallark.MessageContent {
	return internallark.MessageContent(content)
}

func fromInternalLarkMessageContent(content internallark.MessageContent) LarkMessageContent {
	return LarkMessageContent(content)
}

func toInternalLarkSendOptions(options LarkSendOptions) internallark.SendOptions {
	return internallark.SendOptions(options)
}

func fromInternalLarkSendOptions(options internallark.SendOptions) LarkSendOptions {
	return LarkSendOptions(options)
}

func toInternalLarkSendMessageRequest(req LarkSendMessageRequest) internallark.SendMessageRequest {
	return internallark.SendMessageRequest{
		ChatID:  req.ChatID,
		Content: toInternalLarkMessageContent(req.Content),
		Options: toInternalLarkSendOptions(req.Options),
	}
}

func fromInternalLarkSendMessageRequest(req internallark.SendMessageRequest) LarkSendMessageRequest {
	return LarkSendMessageRequest{
		ChatID:  req.ChatID,
		Content: fromInternalLarkMessageContent(req.Content),
		Options: fromInternalLarkSendOptions(req.Options),
	}
}

func toInternalCreateBoundChatRequest(req CommandCreateBoundChatInput) internallark.CreateBoundChatRequest {
	return internallark.CreateBoundChatRequest{
		Name:         req.Name,
		InviteOpenID: req.InviteOpenID,
		Description:  req.Description,
	}
}

func fromInternalCreateBoundChatRequest(req internallark.CreateBoundChatRequest) CommandCreateBoundChatInput {
	return CommandCreateBoundChatInput{
		Name:         req.Name,
		InviteOpenID: req.InviteOpenID,
		Description:  req.Description,
	}
}

func fromInternalCreatedChat(chat internallark.CreatedChat) CommandCreatedChat {
	return CommandCreatedChat{
		ChatID: chat.ChatID,
		Name:   chat.Name,
	}
}

func toInternalLarkSendCardRequest(req LarkSendCardRequest) internallark.SendCardRequest {
	return internallark.SendCardRequest{
		ChatID:  req.ChatID,
		Card:    req.Card,
		Options: toInternalLarkSendOptions(req.Options),
	}
}

func fromInternalLarkSendCardRequest(req internallark.SendCardRequest) LarkSendCardRequest {
	return LarkSendCardRequest{
		ChatID:  req.ChatID,
		Card:    req.Card,
		Options: fromInternalLarkSendOptions(req.Options),
	}
}

func toInternalLarkUpdateCardRequest(req LarkUpdateCardRequest) internallark.UpdateCardRequest {
	return internallark.UpdateCardRequest(req)
}

func fromInternalLarkUpdateCardRequest(req internallark.UpdateCardRequest) LarkUpdateCardRequest {
	return LarkUpdateCardRequest(req)
}

func toInternalLarkUpdateMessageRequest(req LarkUpdateMessageRequest) internallark.UpdateMessageRequest {
	return internallark.UpdateMessageRequest{
		MessageID: req.MessageID,
		Content:   toInternalLarkMessageContent(req.Content),
	}
}

func fromInternalLarkUpdateMessageRequest(req internallark.UpdateMessageRequest) LarkUpdateMessageRequest {
	return LarkUpdateMessageRequest{
		MessageID: req.MessageID,
		Content:   fromInternalLarkMessageContent(req.Content),
	}
}

func toInternalLarkSendResult(result LarkSendResult) internallark.SendResult {
	return internallark.SendResult(result)
}

func fromInternalLarkSendResult(result internallark.SendResult) LarkSendResult {
	return LarkSendResult(result)
}

func toInternalLarkReactionRequest(req LarkMessageReactionRequest) internallark.MessageReactionRequest {
	return internallark.MessageReactionRequest(req)
}

func fromInternalLarkReactionResult(result internallark.MessageReactionResult) LarkMessageReactionResult {
	return LarkMessageReactionResult(result)
}

func fromInternalLarkReactionRequests(in []internallark.MessageReactionRequest) []LarkMessageReactionRequest {
	out := make([]LarkMessageReactionRequest, 0, len(in))
	for _, req := range in {
		out = append(out, LarkMessageReactionRequest(req))
	}
	return out
}

func toInternalLarkQuoteTarget(target LarkQuoteTarget) internallark.QuoteTarget {
	out, _ := convertBridgeJSON[internallark.QuoteTarget](target)
	return out
}

func toInternalLarkScopeGrantRequest(req LarkScopeGrantRequest) internallark.ScopeGrantRequest {
	return internallark.ScopeGrantRequest(req)
}

func fromInternalLarkScopeGrantLink(link internallark.ScopeGrantLink) LarkScopeGrantLink {
	return LarkScopeGrantLink{
		URL:       link.URL,
		ExpiresIn: link.ExpiresIn,
		Wait:      link.Wait,
		Cancel:    link.Cancel,
	}
}

func fromInternalLarkKnownChats(chats []internallark.KnownChat) []LarkKnownChatInfo {
	if len(chats) == 0 {
		return nil
	}
	out := make([]LarkKnownChatInfo, 0, len(chats))
	for _, chat := range chats {
		out = append(out, LarkKnownChatInfo{ID: chat.ID, Name: chat.Name})
	}
	return out
}

func toInternalLarkProfileProjectionRequest(req LarkProfileProjectionRequest) internallark.ProfileProjectionRequest {
	return internallark.ProfileProjectionRequest{
		BotIdentity: toInternalLarkBotIdentity(req.BotIdentity),
		StartedAt:   req.StartedAt,
	}
}

func fromInternalLarkProfileProjectionRequest(req internallark.ProfileProjectionRequest) LarkProfileProjectionRequest {
	return LarkProfileProjectionRequest{
		BotIdentity: fromInternalLarkBotIdentity(req.BotIdentity),
		StartedAt:   req.StartedAt,
	}
}

func toInternalLarkProfileProjectionResult(result LarkProfileProjectionResult) internallark.ProfileProjectionResult {
	return internallark.ProfileProjectionResult{
		LarkCliSourceConfigFile: result.LarkCliSourceConfigFile,
		LarkChannelEnv:          result.LarkChannelEnv,
		IdentityPolicyApplied:   result.IdentityPolicyApplied,
		BotIdentity:             toInternalLarkBotIdentity(result.BotIdentity),
	}
}

func fromInternalLarkProfileProjectionResult(result internallark.ProfileProjectionResult) LarkProfileProjectionResult {
	return LarkProfileProjectionResult{
		LarkCliSourceConfigFile: result.LarkCliSourceConfigFile,
		LarkChannelEnv:          result.LarkChannelEnv,
		IdentityPolicyApplied:   result.IdentityPolicyApplied,
		BotIdentity:             fromInternalLarkBotIdentity(result.BotIdentity),
	}
}

func toInternalLarkCLIProjectionHookOptions(options LarkCLIProjectionHookOptions) (internallark.LarkCLIProjectionHookOptions, error) {
	cfg, err := toInternalLarkCLIAppConfig(options.Config)
	if err != nil {
		return internallark.LarkCLIProjectionHookOptions{}, err
	}
	return internallark.LarkCLIProjectionHookOptions{
		Config:              cfg,
		Paths:               toInternalLarkCLIProjectionPaths(options.Paths),
		Env:                 toInternalLarkCLIEnvContext(options.Env),
		IdentityPreset:      larkcli.IdentityPreset(options.IdentityPreset),
		ApplyIdentityPolicy: options.ApplyIdentityPolicy,
		IdentityOptions: larkcli.IdentityPolicyOptions{
			Command: options.IdentityOptions.Command,
			Timeout: options.IdentityOptions.Timeout,
			BaseEnv: options.IdentityOptions.BaseEnv,
			Runner:  wrapLarkCLICommandRunner(options.IdentityOptions.Runner),
		},
	}, nil
}

func fromInternalLarkHandleResult(result internallark.HandleResult) LarkHandleResult {
	return LarkHandleResult{
		Normalized:      fromInternalLarkNormalizedEvent(result.Normalized),
		CardDispatch:    fromInternalCardDispatchResultPtr(result.CardDispatch),
		DroppedSelfLoop: result.DroppedSelfLoop,
	}
}

func toInternalLarkCOTUpdateRequest(req LarkCOTUpdateRequest) appcot.UpdateRequest {
	events := make([]appcot.Event, 0, len(req.Events))
	for _, event := range req.Events {
		events = append(events, appcot.Event(event))
	}
	return appcot.UpdateRequest{Ref: appcot.Ref(req.Ref), Events: events}
}

func fromInternalLarkCOTUpdateRequest(req appcot.UpdateRequest) LarkCOTUpdateRequest {
	events := make([]LarkCOTEvent, 0, len(req.Events))
	for _, event := range req.Events {
		events = append(events, LarkCOTEvent(event))
	}
	return LarkCOTUpdateRequest{Ref: LarkCOTRef(req.Ref), Events: events}
}

func fromInternalLarkSendMessageRequests(in []internallark.SendMessageRequest) []LarkSendMessageRequest {
	out := make([]LarkSendMessageRequest, 0, len(in))
	for _, req := range in {
		out = append(out, fromInternalLarkSendMessageRequest(req))
	}
	return out
}

func fromInternalLarkSendCardRequests(in []internallark.SendCardRequest) []LarkSendCardRequest {
	out := make([]LarkSendCardRequest, 0, len(in))
	for _, req := range in {
		out = append(out, fromInternalLarkSendCardRequest(req))
	}
	return out
}

func fromInternalLarkUpdateCardRequests(in []internallark.UpdateCardRequest) []LarkUpdateCardRequest {
	out := make([]LarkUpdateCardRequest, 0, len(in))
	for _, req := range in {
		out = append(out, fromInternalLarkUpdateCardRequest(req))
	}
	return out
}

func fromInternalLarkUpdateMessageRequests(in []internallark.UpdateMessageRequest) []LarkUpdateMessageRequest {
	out := make([]LarkUpdateMessageRequest, 0, len(in))
	for _, req := range in {
		out = append(out, fromInternalLarkUpdateMessageRequest(req))
	}
	return out
}
