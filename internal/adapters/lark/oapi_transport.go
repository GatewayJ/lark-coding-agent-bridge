package lark

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	larksdk "github.com/larksuite/oapi-sdk-go/v3"
	larkchannel "github.com/larksuite/oapi-sdk-go/v3/channel"
	larknormalize "github.com/larksuite/oapi-sdk-go/v3/channel/normalize"
	"github.com/larksuite/oapi-sdk-go/v3/channel/outbound"
	channeltypes "github.com/larksuite/oapi-sdk-go/v3/channel/types"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"github.com/larksuite/oapi-sdk-go/v3/scene/registration"
	larkapplication "github.com/larksuite/oapi-sdk-go/v3/service/application/v6"
	larkcardkit "github.com/larksuite/oapi-sdk-go/v3/service/cardkit/v1"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/cardmanaged"
	appcot "github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/cotpresenter"
	appintake "github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/intake"
	appmedia "github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/media"
)

const (
	DefaultOAPIRequestTimeout = 30 * time.Second
	DefaultOAPIStartTimeout   = 40 * time.Second
	DefaultOAPIStreamThrottle = 400 * time.Millisecond

	oapiQuoteCardContentType       = "user_card_content"
	oapiInteractiveCardPlaceholder = "[interactive card]"
	oapiMaxForwardedQuoteMessages  = 50
)

var (
	ErrOAPIAppCredentials = errors.New("lark oapi app id and secret are required")
	ErrOAPIChannel        = errors.New("lark oapi channel is required")
	ErrOAPIClient         = errors.New("lark oapi client is required")
	ErrOAPIStartTimeout   = errors.New("lark oapi channel start timed out")
	ErrOAPIAlreadyStarted = errors.New("lark oapi transport already started; create a new transport to reconnect")
	ErrOAPICardIDMissing  = errors.New("lark oapi card id is missing")
	ErrOAPIMessageMissing = errors.New("lark oapi message is missing")
	ErrOAPIReplyThread    = errors.New("lark oapi replyInThread requires replyTo")
	ErrOAPIThreadIDSend   = errors.New("lark oapi direct threadID send is unsupported; reply to a carrier message instead")
)

type OAPIError struct {
	Operation string
	Code      int
	Message   string
}

func (e *OAPIError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("%s API error: %d %s", e.Operation, e.Code, e.Message)
}

type OAPITransportOptions struct {
	AppID     string
	AppSecret string

	// Tenant selects the public OpenAPI host when Domain is empty. Use "lark"
	// for open.larksuite.com; every other value defaults to open.feishu.cn.
	Tenant string
	Domain string

	Client   *larksdk.Client
	WSClient *larkws.Client

	HTTPClient              larkcore.HttpClient
	RequestTimeout          time.Duration
	StartTimeout            time.Duration
	LogLevel                larkcore.LogLevel
	Logger                  larkcore.Logger
	Headers                 http.Header
	Source                  string
	RegistrationDomain      string
	RegistrationLarkDomain  string
	ClientAssertionProvider larkcore.ClientAssertionProvider
	ChannelOptions          []channeltypes.ChannelOption

	DisableWebSocket   bool
	EnableSDKChatQueue bool

	channel oapiChannel
	now     func() time.Time
}

type OAPITransport struct {
	mu sync.Mutex

	client       *larksdk.Client
	wsClient     *larkws.Client
	channel      oapiChannel
	requestTTL   time.Duration
	startTimeout time.Duration
	startCancel  context.CancelFunc
	ready        chan struct{}
	handler      TransportHandler
	connected    bool
	registered   bool
	started      bool
	now          func() time.Time

	source        string
	regDomain     string
	regLarkDomain string
}

type oapiChannel interface {
	Send(ctx context.Context, input *channeltypes.SendInput) (*channeltypes.SendResult, error)
	OnMessage(handler func(ctx context.Context, msg *channeltypes.NormalizedMessage) error)
	OnComment(handler func(ctx context.Context, event *channeltypes.CommentEvent) error)
	OnCardAction(handler func(ctx context.Context, event *channeltypes.CardActionEvent) error)
	OnReady(handler func())
	OnError(handler func(err error))
	OnReconnecting(handler func())
	OnReconnected(handler func())
	OnDisconnected(handler func())
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	GetBotIdentity(ctx context.Context) *channeltypes.BotIdentity
}

func NewOAPITransport(options OAPITransportOptions) (*OAPITransport, error) {
	client := options.Client
	wsClient := options.WSClient
	channel := options.channel
	if channel == nil {
		if client == nil {
			if !hasOAPICredentials(options) {
				return nil, ErrOAPIAppCredentials
			}
			client = newOAPIClient(options)
		}
		if !options.DisableWebSocket && wsClient == nil {
			if !hasOAPICredentials(options) {
				return nil, ErrOAPIAppCredentials
			}
			wsClient = newOAPIWSClient(options)
		}
		channel = larkchannel.NewChannel(client, wsClient, defaultOAPIChannelOptions(options)...)
	}
	if channel == nil {
		return nil, ErrOAPIChannel
	}
	requestTTL := options.RequestTimeout
	if requestTTL == 0 {
		requestTTL = DefaultOAPIRequestTimeout
	}
	startTimeout := options.StartTimeout
	if startTimeout == 0 {
		startTimeout = DefaultOAPIStartTimeout
	}
	now := options.now
	if now == nil {
		now = time.Now
	}
	return &OAPITransport{
		client:        client,
		wsClient:      wsClient,
		channel:       channel,
		requestTTL:    requestTTL,
		startTimeout:  startTimeout,
		now:           now,
		source:        options.Source,
		regDomain:     options.RegistrationDomain,
		regLarkDomain: options.RegistrationLarkDomain,
	}, nil
}

func newOAPIClient(options OAPITransportOptions) *larksdk.Client {
	reqTimeout := options.RequestTimeout
	if reqTimeout == 0 {
		reqTimeout = DefaultOAPIRequestTimeout
	}
	httpClient := options.HTTPClient
	if httpClient == nil {
		transport, ok := http.DefaultTransport.(*http.Transport)
		if !ok {
			transport = &http.Transport{}
		}
		cloned := transport.Clone()
		cloned.Proxy = http.ProxyFromEnvironment
		httpClient = &http.Client{Timeout: reqTimeout, Transport: cloned}
	}
	opts := []larksdk.ClientOptionFunc{
		larksdk.WithReqTimeout(reqTimeout),
		larksdk.WithHttpClient(httpClient),
	}
	if domain := oapiDomain(options); domain != "" {
		opts = append(opts, larksdk.WithOpenBaseUrl(domain))
	}
	if options.LogLevel != 0 {
		opts = append(opts, larksdk.WithLogLevel(options.LogLevel))
	}
	if options.Logger != nil {
		opts = append(opts, larksdk.WithLogger(options.Logger))
	}
	if options.Headers != nil {
		opts = append(opts, larksdk.WithHeaders(options.Headers))
	}
	if options.Source != "" {
		opts = append(opts, larksdk.WithSource(options.Source))
	}
	if options.ClientAssertionProvider != nil {
		opts = append(opts, larksdk.WithClientAssertionProvider(options.ClientAssertionProvider))
	}
	return larksdk.NewClient(options.AppID, options.AppSecret, opts...)
}

func newOAPIWSClient(options OAPITransportOptions) *larkws.Client {
	opts := []larkws.ClientOption{}
	if domain := oapiDomain(options); domain != "" {
		opts = append(opts, larkws.WithDomain(domain))
	}
	if options.LogLevel != 0 {
		opts = append(opts, larkws.WithLogLevel(options.LogLevel))
	}
	if options.Logger != nil {
		opts = append(opts, larkws.WithLogger(options.Logger))
	}
	if options.Headers != nil {
		opts = append(opts, larkws.WithHeaders(options.Headers))
	}
	if options.Source != "" {
		opts = append(opts, larkws.WithSource(options.Source))
	}
	if options.ClientAssertionProvider != nil {
		opts = append(opts, larkws.WithClientAssertionProvider(options.ClientAssertionProvider))
	}
	return larkws.NewClient(options.AppID, options.AppSecret, opts...)
}

func defaultOAPIChannelOptions(options OAPITransportOptions) []channeltypes.ChannelOption {
	cfg := channeltypes.DefaultChannelConfig()
	requireMention := false
	respondToMentionAll := false
	cfg.Policy.RequireMention = &requireMention
	cfg.Policy.RespondToMentionAll = &respondToMentionAll
	cfg.Policy.DMMode = "open"
	cfg.Outbound.StreamThrottleMs = DefaultOAPIStreamThrottle
	if !options.EnableSDKChatQueue {
		cfg.Safety.Batch.DelayMs = 0
		cfg.Safety.Batch.LongDelayMs = 0
		cfg.Safety.Batch.MaxMessages = 1
	}
	out := []channeltypes.ChannelOption{
		channeltypes.WithPolicyConfig(cfg.Policy),
		channeltypes.WithSafetyConfig(cfg.Safety),
		channeltypes.WithOutboundConfig(cfg.Outbound),
		channeltypes.WithBotIdentityCacheConfig(cfg.BotIdentityCache),
	}
	out = append(out, options.ChannelOptions...)
	return out
}

func oapiDomain(options OAPITransportOptions) string {
	if options.Domain != "" {
		return options.Domain
	}
	if strings.EqualFold(options.Tenant, "lark") {
		return larksdk.LarkBaseUrl
	}
	return larksdk.FeishuBaseUrl
}

func hasOAPICredentials(options OAPITransportOptions) bool {
	return options.AppID != "" && (options.AppSecret != "" || options.ClientAssertionProvider != nil)
}

func (t *OAPITransport) Connect(ctx context.Context, handler TransportHandler) error {
	if t == nil || t.channel == nil {
		return ErrOAPIChannel
	}
	if handler == nil {
		return ErrNilTransport
	}
	ready := make(chan struct{}, 1)
	done := make(chan error, 1)
	startCtx, cancel := context.WithCancel(ctx)

	t.mu.Lock()
	if t.started {
		t.mu.Unlock()
		cancel()
		return ErrOAPIAlreadyStarted
	}
	t.started = true
	t.handler = handler
	t.connected = true
	t.startCancel = cancel
	t.ready = ready
	if !t.registered {
		t.registerCallbacks()
		t.registered = true
	}
	t.mu.Unlock()

	go func() {
		err := t.channel.Start(startCtx)
		done <- err
	}()

	if t.wsClient == nil {
		err := <-done
		if err != nil {
			t.resetFailedStart()
		}
		return err
	}
	timeout := t.startTimeout
	if timeout < 0 {
		return nil
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case err := <-done:
		if err != nil {
			t.resetFailedStart()
		}
		return err
	case <-ready:
		return nil
	case <-timer.C:
		cancel()
		_ = t.channel.Stop(context.Background())
		t.resetFailedStart()
		return ErrOAPIStartTimeout
	case <-ctx.Done():
		cancel()
		_ = t.channel.Stop(context.Background())
		t.resetFailedStart()
		return ctx.Err()
	}
}

func (t *OAPITransport) Disconnect(ctx context.Context) error {
	if t == nil || t.channel == nil {
		return nil
	}
	t.mu.Lock()
	if t.startCancel != nil {
		t.startCancel()
		t.startCancel = nil
	}
	t.ready = nil
	t.connected = false
	t.started = false
	t.handler = nil
	t.mu.Unlock()
	return t.channel.Stop(ctx)
}

func (t *OAPITransport) BotIdentity(ctx context.Context) (BotIdentity, error) {
	if t == nil || t.channel == nil {
		return BotIdentity{}, ErrOAPIChannel
	}
	identity := t.channel.GetBotIdentity(ctx)
	if identity == nil {
		return BotIdentity{}, nil
	}
	return BotIdentity{
		OpenID: identity.OpenID,
		UserID: identity.UserID,
		Name:   identity.Name,
		Raw:    identity,
	}, nil
}

func (t *OAPITransport) CreateBoundChat(ctx context.Context, req CreateBoundChatRequest) (CreatedChat, error) {
	if t == nil || t.client == nil {
		return CreatedChat{}, ErrOAPIClient
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return CreatedChat{}, errors.New("chat name is required")
	}
	inviteOpenID := strings.TrimSpace(req.InviteOpenID)
	if inviteOpenID == "" {
		return CreatedChat{}, errors.New("invite open id is required")
	}
	body := &larkim.CreateChatReqBody{
		Name:       &name,
		UserIdList: []string{inviteOpenID},
	}
	if description := strings.TrimSpace(req.Description); description != "" {
		body.Description = &description
	}
	resp, err := t.client.Im.V1.Chat.Create(ctx, larkim.NewCreateChatReqBuilder().
		UserIdType("open_id").
		Body(body).
		Build())
	if err != nil {
		return CreatedChat{}, err
	}
	if !resp.Success() {
		return CreatedChat{}, oapiCodeError("create chat", resp.Code, resp.Msg)
	}
	if resp.Data == nil || resp.Data.ChatId == nil || *resp.Data.ChatId == "" {
		return CreatedChat{}, errors.New("create chat response missing chat_id")
	}
	createdName := name
	if resp.Data.Name != nil && *resp.Data.Name != "" {
		createdName = *resp.Data.Name
	}
	return CreatedChat{ChatID: *resp.Data.ChatId, Name: createdName}, nil
}

func (t *OAPITransport) SendMessage(ctx context.Context, req SendMessageRequest) (SendResult, error) {
	if t == nil || t.channel == nil {
		return SendResult{}, ErrOAPIChannel
	}
	if err := validateThreadSend(req.Options); err != nil {
		return SendResult{}, err
	}
	if req.Options.ReplyInThread {
		msgType, content, err := directMessageContent(req.Content)
		if err != nil {
			return SendResult{}, err
		}
		return t.sendDirect(ctx, req.ChatID, msgType, content, req.Options)
	}
	input, err := sendInputForMessage(req.ChatID, req.Content, req.Options)
	if err != nil {
		return SendResult{}, err
	}
	result, err := t.channel.Send(ctx, input)
	if err != nil {
		return SendResult{}, err
	}
	return SendResult{MessageID: result.MessageID, Raw: result}, nil
}

func (t *OAPITransport) SendCard(ctx context.Context, req SendCardRequest) (SendResult, error) {
	if t == nil || t.channel == nil {
		return SendResult{}, ErrOAPIChannel
	}
	cardJSON, err := marshalCard(req.Card)
	if err != nil {
		return SendResult{}, err
	}
	if err := validateThreadSend(req.Options); err != nil {
		return SendResult{}, err
	}
	if req.Options.ReplyInThread {
		return t.sendDirect(ctx, req.ChatID, "interactive", cardJSON, req.Options)
	}
	result, err := t.channel.Send(ctx, &channeltypes.SendInput{
		ReceiveID:      req.ChatID,
		Card:           cardJSON,
		ReplyMessageID: req.Options.ReplyTo,
	})
	if err != nil {
		return SendResult{}, err
	}
	return SendResult{MessageID: result.MessageID, Raw: result}, nil
}

func (t *OAPITransport) UpdateCard(ctx context.Context, req UpdateCardRequest) error {
	return t.UpdateRawCard(ctx, req.MessageID, req.Card)
}

func (t *OAPITransport) UpdateMessage(ctx context.Context, req UpdateMessageRequest) error {
	if t == nil || t.client == nil {
		return ErrOAPIClient
	}
	if req.MessageID == "" {
		return ErrOAPIMessageMissing
	}
	content, err := patchMessageContent(req.Content)
	if err != nil {
		return err
	}
	resp, err := t.client.Im.V1.Message.Patch(ctx, larkim.NewPatchMessageReqBuilder().
		MessageId(req.MessageID).
		Body(larkim.NewPatchMessageReqBodyBuilder().Content(content).Build()).
		Build())
	if err != nil {
		return err
	}
	if !resp.Success() {
		return oapiCodeError("patch message", resp.Code, resp.Msg)
	}
	return nil
}

func (t *OAPITransport) AddMessageReaction(ctx context.Context, req MessageReactionRequest) (MessageReactionResult, error) {
	if t == nil || t.client == nil {
		return MessageReactionResult{}, ErrOAPIClient
	}
	if req.MessageID == "" {
		return MessageReactionResult{}, ErrOAPIMessageMissing
	}
	if req.EmojiType == "" {
		return MessageReactionResult{}, errors.New("emoji type is required")
	}
	resp, err := t.client.Im.V1.MessageReaction.Create(ctx, larkim.NewCreateMessageReactionReqBuilder().
		MessageId(req.MessageID).
		Body(larkim.NewCreateMessageReactionReqBodyBuilder().
			ReactionType(larkim.NewEmojiBuilder().EmojiType(req.EmojiType).Build()).
			Build()).
		Build())
	if err != nil {
		return MessageReactionResult{}, err
	}
	if !resp.Success() {
		return MessageReactionResult{}, oapiCodeError("create message reaction", resp.Code, resp.Msg)
	}
	if resp.Data == nil || resp.Data.ReactionId == nil {
		return MessageReactionResult{}, nil
	}
	return MessageReactionResult{ReactionID: *resp.Data.ReactionId}, nil
}

func (t *OAPITransport) DeleteMessageReaction(ctx context.Context, req MessageReactionRequest) error {
	if t == nil || t.client == nil {
		return ErrOAPIClient
	}
	if req.MessageID == "" {
		return ErrOAPIMessageMissing
	}
	if req.ReactionID == "" {
		return errors.New("reaction id is required")
	}
	resp, err := t.client.Im.V1.MessageReaction.Delete(ctx, larkim.NewDeleteMessageReactionReqBuilder().
		MessageId(req.MessageID).
		ReactionId(req.ReactionID).
		Build())
	if err != nil {
		return err
	}
	if !resp.Success() {
		return oapiCodeError("delete message reaction", resp.Code, resp.Msg)
	}
	return nil
}

func (t *OAPITransport) CreateMessageCOT(ctx context.Context, req appcot.CreateRequest) (appcot.Ref, error) {
	if t == nil || t.client == nil {
		return appcot.Ref{}, ErrOAPIClient
	}
	if strings.TrimSpace(req.ReceiveID) == "" {
		return appcot.Ref{}, errors.New("COT receive id is required")
	}
	query := larkcore.QueryParams{}
	query.Set("receive_id_type", "chat_id")
	body := map[string]any{"receive_id": req.ReceiveID}
	if strings.TrimSpace(req.OriginMessageID) != "" {
		body["origin_message_id"] = req.OriginMessageID
	}
	resp, err := t.client.Do(ctx, &larkcore.ApiReq{
		HttpMethod:                http.MethodPost,
		ApiPath:                   "/open-apis/im/v1/message_cot",
		QueryParams:               query,
		Body:                      body,
		SupportedAccessTokenTypes: []larkcore.AccessTokenType{larkcore.AccessTokenTypeTenant},
	})
	if err != nil {
		return appcot.Ref{}, err
	}
	var out struct {
		larkcore.CodeError
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(resp.RawBody, &out); err != nil {
		return appcot.Ref{}, err
	}
	if out.Code != 0 {
		return appcot.Ref{}, oapiCodeError("create message COT", out.Code, out.Msg)
	}
	data := out.Data
	if data == nil {
		data = map[string]any{}
		if err := json.Unmarshal(resp.RawBody, &data); err != nil {
			return appcot.Ref{}, err
		}
	}
	return appcot.Ref{
		COTID:     stringAny(data["cot_id"], data["cotId"]),
		MessageID: stringAny(data["message_id"], data["messageId"]),
	}, nil
}

func (t *OAPITransport) UpdateMessageCOT(ctx context.Context, req appcot.UpdateRequest) error {
	if t == nil || t.client == nil {
		return ErrOAPIClient
	}
	if req.Ref.COTID == "" || req.Ref.MessageID == "" {
		return errors.New("COT ref is required")
	}
	if len(req.Events) == 0 {
		return nil
	}
	resp, err := t.client.Do(ctx, &larkcore.ApiReq{
		HttpMethod: http.MethodPut,
		ApiPath:    "/open-apis/im/v1/message_cot",
		Body: map[string]any{
			"cot_id":     req.Ref.COTID,
			"message_id": req.Ref.MessageID,
			"events":     req.Events,
		},
		SupportedAccessTokenTypes: []larkcore.AccessTokenType{larkcore.AccessTokenTypeTenant},
	})
	if err != nil {
		return err
	}
	return checkOAPIRawCode("update message COT", resp.RawBody)
}

func (t *OAPITransport) CompleteMessageCOT(ctx context.Context, req appcot.CompleteRequest) error {
	if t == nil || t.client == nil {
		return ErrOAPIClient
	}
	if req.Ref.COTID == "" || req.Ref.MessageID == "" {
		return errors.New("COT ref is required")
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		reason = "done"
	}
	query := larkcore.QueryParams{}
	query.Set("message_id", req.Ref.MessageID)
	query.Set("reason", reason)
	resp, err := t.client.Do(ctx, &larkcore.ApiReq{
		HttpMethod:                http.MethodPost,
		ApiPath:                   "/open-apis/im/v1/message_cot/complete/" + url.PathEscape(req.Ref.COTID),
		QueryParams:               query,
		SupportedAccessTokenTypes: []larkcore.AccessTokenType{larkcore.AccessTokenTypeTenant},
	})
	if err != nil {
		return err
	}
	return checkOAPIRawCode("complete message COT", resp.RawBody)
}

func (t *OAPITransport) CreateCard(ctx context.Context, card map[string]any) (string, error) {
	if t == nil || t.client == nil {
		return "", ErrOAPIClient
	}
	cardJSON, err := marshalCard(card)
	if err != nil {
		return "", err
	}
	resp, err := t.client.Cardkit.V1.Card.Create(ctx, larkcardCreateReq(cardJSON))
	if err != nil {
		return "", err
	}
	if !resp.Success() {
		return "", oapiCodeError("create card", resp.Code, resp.Msg)
	}
	if resp.Data == nil || resp.Data.CardId == nil || *resp.Data.CardId == "" {
		return "", ErrOAPICardIDMissing
	}
	return *resp.Data.CardId, nil
}

func (t *OAPITransport) SendCardID(ctx context.Context, recipientID string, cardID string, opts cardmanaged.SendOptions) (string, error) {
	if t == nil || t.channel == nil {
		return "", ErrOAPIChannel
	}
	content, err := json.Marshal(map[string]string{"card_id": cardID})
	if err != nil {
		return "", err
	}
	sendOpts := SendOptions{
		ReplyTo:       opts.ReplyTo,
		ReplyInThread: opts.ReplyInThread,
	}
	if err := validateThreadSend(sendOpts); err != nil {
		return "", err
	}
	if opts.ReplyInThread {
		result, err := t.sendDirect(ctx, recipientID, "interactive", string(content), sendOpts)
		return result.MessageID, err
	}
	result, err := t.channel.Send(ctx, &channeltypes.SendInput{
		ReceiveID:      recipientID,
		Card:           string(content),
		ReplyMessageID: opts.ReplyTo,
	})
	if err != nil {
		return "", err
	}
	return result.MessageID, nil
}

func (t *OAPITransport) SendRawCard(ctx context.Context, recipientID string, card map[string]any, opts cardmanaged.SendOptions) (string, error) {
	result, err := t.SendCard(ctx, SendCardRequest{
		ChatID: recipientID,
		Card:   card,
		Options: SendOptions{
			ReplyTo:       opts.ReplyTo,
			ReplyInThread: opts.ReplyInThread,
		},
	})
	return result.MessageID, err
}

func (t *OAPITransport) UpdateCardByID(ctx context.Context, cardID string, card map[string]any, sequence int) error {
	if t == nil || t.client == nil {
		return ErrOAPIClient
	}
	cardJSON, err := marshalCard(card)
	if err != nil {
		return err
	}
	resp, err := t.client.Cardkit.V1.Card.Update(ctx, larkcardUpdateReq(cardID, cardJSON, sequence))
	if err != nil {
		return err
	}
	if !resp.Success() {
		return oapiCodeError("update card", resp.Code, resp.Msg)
	}
	return nil
}

func (t *OAPITransport) UpdateRawCard(ctx context.Context, messageID string, card map[string]any) error {
	if t == nil || t.client == nil {
		return ErrOAPIClient
	}
	cardJSON, err := marshalCard(card)
	if err != nil {
		return err
	}
	resp, err := t.client.Im.V1.Message.Patch(ctx, larkim.NewPatchMessageReqBuilder().
		MessageId(messageID).
		Body(larkim.NewPatchMessageReqBodyBuilder().Content(cardJSON).Build()).
		Build())
	if err != nil {
		return err
	}
	if !resp.Success() {
		return oapiCodeError("patch card message", resp.Code, resp.Msg)
	}
	return nil
}

func (t *OAPITransport) DownloadResource(ctx context.Context, req appmedia.DownloadRequest) (appmedia.DownloadResult, error) {
	if t == nil || t.client == nil {
		return appmedia.DownloadResult{}, ErrOAPIClient
	}
	resourceType := "file"
	if req.Type == appmedia.DownloadResourceImage {
		resourceType = "image"
	}
	resp, err := t.client.Im.V1.MessageResource.Get(ctx, larkim.NewGetMessageResourceReqBuilder().
		MessageId(req.MessageID).
		FileKey(req.FileKey).
		Type(resourceType).
		Build())
	if err != nil {
		return appmedia.DownloadResult{}, err
	}
	if !resp.Success() {
		return appmedia.DownloadResult{}, oapiCodeError("download message resource", resp.Code, resp.Msg)
	}
	file, err := os.Create(req.DestinationPath)
	if err != nil {
		return appmedia.DownloadResult{}, err
	}
	defer file.Close()
	n, err := io.Copy(file, resp.File)
	if err != nil {
		return appmedia.DownloadResult{}, err
	}
	contentType := ""
	if resp.ApiResp != nil {
		contentType = resp.ApiResp.Header.Get("Content-Type")
	}
	return appmedia.DownloadResult{ContentType: contentType, BytesWritten: n}, nil
}

func (t *OAPITransport) ResolveCarrierThreadID(ctx context.Context, chatID, messageID string) (string, error) {
	if t == nil || t.client == nil {
		return "", ErrOAPIClient
	}
	resp, err := t.client.Im.V1.Message.Get(ctx, larkim.NewGetMessageReqBuilder().
		MessageId(messageID).
		UserIdType("open_id").
		Build())
	if err != nil {
		return "", err
	}
	if !resp.Success() {
		return "", oapiCodeError("get message", resp.Code, resp.Msg)
	}
	if resp.Data == nil || len(resp.Data.Items) == 0 || resp.Data.Items[0] == nil {
		return "", ErrOAPIMessageMissing
	}
	item := resp.Data.Items[0]
	if chatID != "" && item.ChatId != nil && *item.ChatId != "" && *item.ChatId != chatID {
		return "", nil
	}
	if item.ThreadId == nil {
		return "", nil
	}
	return *item.ThreadId, nil
}

func (t *OAPITransport) ResolveLarkQuote(ctx context.Context, target QuoteTarget) (QuoteMessage, bool, error) {
	if t == nil || t.client == nil {
		return QuoteMessage{}, false, ErrOAPIClient
	}
	if target.MessageID == "" {
		return QuoteMessage{}, false, nil
	}
	items, err := t.fetchOAPIQuoteItems(ctx, target.MessageID)
	if err != nil {
		return QuoteMessage{}, false, err
	}
	return t.quoteMessageFromOAPIItems(ctx, items, map[string]struct{}{target.MessageID: struct{}{}})
}

func (t *OAPITransport) fetchOAPIQuoteItems(ctx context.Context, messageID string) ([]*larkim.Message, error) {
	resp, err := t.client.Im.V1.Message.Get(ctx, larkim.NewGetMessageReqBuilder().
		MessageId(messageID).
		UserIdType("open_id").
		CardMsgContentType(oapiQuoteCardContentType).
		Build())
	if err != nil {
		return nil, err
	}
	if !resp.Success() {
		return nil, oapiCodeError("get quoted message", resp.Code, resp.Msg)
	}
	if resp.Data == nil || len(resp.Data.Items) == 0 || resp.Data.Items[0] == nil {
		return nil, nil
	}
	return resp.Data.Items, nil
}

func (t *OAPITransport) HasLarkScope(ctx context.Context, appID string, scope string) (bool, error) {
	if t == nil || t.client == nil {
		return false, ErrOAPIClient
	}
	appID = strings.TrimSpace(appID)
	scope = strings.TrimSpace(scope)
	if appID == "" || scope == "" {
		return false, nil
	}
	resp, err := t.client.Application.Application.Get(ctx, larkapplication.NewGetApplicationReqBuilder().
		AppId(appID).
		Lang("zh_cn").
		UserIdType("open_id").
		Build())
	if err != nil {
		return false, err
	}
	if !resp.Success() {
		return false, oapiCodeError("get application scopes", resp.Code, resp.Msg)
	}
	if resp.Data == nil || resp.Data.App == nil {
		return false, nil
	}
	for _, granted := range resp.Data.App.Scopes {
		if granted != nil && stringValue(granted.Scope) == scope {
			return true, nil
		}
	}
	return false, nil
}

func (t *OAPITransport) FetchLarkOwner(ctx context.Context, appID string) (string, error) {
	if t == nil || t.client == nil {
		return "", ErrOAPIClient
	}
	appID = strings.TrimSpace(appID)
	if appID == "" {
		return "", nil
	}
	resp, err := t.client.Application.Application.Get(ctx, larkapplication.NewGetApplicationReqBuilder().
		AppId(appID).
		Lang("zh_cn").
		UserIdType("open_id").
		Build())
	if err != nil {
		return "", err
	}
	if !resp.Success() {
		return "", oapiCodeError("get application owner", resp.Code, resp.Msg)
	}
	if resp.Data == nil || resp.Data.App == nil {
		return "", errors.New("application owner missing from API response")
	}
	if resp.Data.App.Owner != nil && stringValue(resp.Data.App.Owner.OwnerId) != "" {
		return stringValue(resp.Data.App.Owner.OwnerId), nil
	}
	if creatorID := stringValue(resp.Data.App.CreatorId); creatorID != "" {
		return creatorID, nil
	}
	return "", errors.New("application owner missing from API response")
}

func (t *OAPITransport) ListLarkKnownChats(ctx context.Context, maxPages int) ([]KnownChat, error) {
	if t == nil || t.client == nil {
		return nil, ErrOAPIClient
	}
	if maxPages <= 0 {
		maxPages = 5
	}
	pageToken := ""
	out := []KnownChat{}
	for page := 0; page < maxPages; page++ {
		builder := larkim.NewListChatReqBuilder().
			PageSize(100).
			UserIdType("open_id").
			Types("group")
		if pageToken != "" {
			builder.PageToken(pageToken)
		}
		resp, err := t.client.Im.V1.Chat.List(ctx, builder.Build())
		if err != nil {
			return nil, err
		}
		if !resp.Success() {
			return nil, oapiCodeError("list chats", resp.Code, resp.Msg)
		}
		if resp.Data == nil {
			return out, nil
		}
		for _, item := range resp.Data.Items {
			if item == nil || stringValue(item.ChatId) == "" {
				continue
			}
			name := stringValue(item.Name)
			if name == "" {
				name = "(无名)"
			}
			out = append(out, KnownChat{ID: stringValue(item.ChatId), Name: name})
		}
		if resp.Data.HasMore == nil || !*resp.Data.HasMore || resp.Data.PageToken == nil || *resp.Data.PageToken == "" {
			break
		}
		pageToken = *resp.Data.PageToken
	}
	return out, nil
}

func (t *OAPITransport) RequestLarkScopeGrant(ctx context.Context, req ScopeGrantRequest) (ScopeGrantLink, error) {
	if strings.TrimSpace(req.AppID) == "" {
		return ScopeGrantLink{}, errors.New("lark scope grant app id is required")
	}
	qrCh := make(chan ScopeGrantLink, 1)
	done := make(chan error, 1)
	registrationCtx, cancel := context.WithCancel(ctx)
	go func() {
		defer cancel()
		_, err := registration.RegisterApp(registrationCtx, &registration.Options{
			Source:     oapiFirstNonEmpty(t.source, "lark-channel-bridge"),
			Domain:     t.regDomain,
			LarkDomain: t.regLarkDomain,
			AppID:      req.AppID,
			Addons: &registration.AppAddons{
				Scopes: registration.AppAddonsScopes{Tenant: append([]string(nil), req.TenantScopes...)},
			},
			OnQRCode: func(info *registration.QRCodeInfo) {
				link := ScopeGrantLink{
					URL:       info.URL,
					ExpiresIn: time.Duration(info.ExpireIn) * time.Second,
					Cancel:    cancel,
					Wait: func(waitCtx context.Context) error {
						select {
						case err := <-done:
							return err
						case <-waitCtx.Done():
							cancel()
							return waitCtx.Err()
						}
					},
				}
				select {
				case qrCh <- link:
				default:
				}
			},
		})
		done <- err
	}()
	select {
	case link := <-qrCh:
		return link, nil
	case err := <-done:
		return ScopeGrantLink{}, err
	case <-ctx.Done():
		cancel()
		return ScopeGrantLink{}, ctx.Err()
	}
}

func (t *OAPITransport) quoteMessageFromOAPIItems(ctx context.Context, items []*larkim.Message, visited map[string]struct{}) (QuoteMessage, bool, error) {
	if len(items) == 0 || items[0] == nil {
		return QuoteMessage{}, false, nil
	}
	parent := items[0]
	messageID := stringValue(parent.MessageId)
	if messageID == "" {
		return QuoteMessage{}, false, nil
	}
	msgType := oapiMessageType(parent)
	content, err := t.renderOAPIQuoteContent(ctx, parent, buildOAPIForwardChildren(items), newForwardLimit(), visited)
	if err != nil {
		return QuoteMessage{}, false, err
	}
	return QuoteMessage{
		MessageID:      messageID,
		SenderID:       oapiMessageSenderID(parent),
		SenderName:     oapiMessageSenderName(parent),
		CreatedAt:      oapiMessageCreatedAt(parent),
		RawContentType: msgType,
		Content:        content,
	}, true, nil
}

func (t *OAPITransport) renderOAPIQuoteContent(ctx context.Context, message *larkim.Message, children map[string][]*larkim.Message, remaining *int, visited map[string]struct{}) (string, error) {
	msgType := oapiMessageType(message)
	raw := oapiMessageBodyContent(message)
	switch msgType {
	case "merge_forward":
		return t.renderOAPIForwardedMessages(ctx, message, children, remaining, visited)
	case "interactive":
		flattened, _ := larknormalize.ParseContent(msgType, raw)
		return expandOAPIInteractiveCard(flattened, raw), nil
	default:
		content, _ := larknormalize.ParseContent(msgType, raw)
		return content, nil
	}
}

func (t *OAPITransport) renderOAPIForwardedMessages(ctx context.Context, parent *larkim.Message, children map[string][]*larkim.Message, remaining *int, visited map[string]struct{}) (string, error) {
	parentID := stringValue(parent.MessageId)
	if err := t.ensureOAPIForwardChildren(ctx, parentID, children, visited); err != nil {
		return "", err
	}
	parts := []string{"<forwarded_messages>"}
	for _, child := range children[parentID] {
		if remaining != nil && *remaining <= 0 {
			break
		}
		rendered, err := t.renderOAPIForwardedMessage(ctx, child, children, remaining, visited)
		if err != nil {
			return "", err
		}
		if rendered != "" {
			parts = append(parts, rendered)
		}
	}
	parts = append(parts, "</forwarded_messages>")
	return strings.Join(parts, "\n"), nil
}

func (t *OAPITransport) renderOAPIForwardedMessage(ctx context.Context, message *larkim.Message, children map[string][]*larkim.Message, remaining *int, visited map[string]struct{}) (string, error) {
	if message == nil {
		return "", nil
	}
	if remaining != nil {
		if *remaining <= 0 {
			return "", nil
		}
		*remaining = *remaining - 1
	}
	attrs := oapiForwardedMessageAttrs(message)
	content, err := t.renderOAPIQuoteContent(ctx, message, children, remaining, visited)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("<forwarded_message %s>\n%s\n</forwarded_message>", strings.Join(attrs, " "), content), nil
}

func buildOAPIForwardChildren(items []*larkim.Message) map[string][]*larkim.Message {
	children := make(map[string][]*larkim.Message)
	if len(items) == 0 || items[0] == nil {
		return children
	}
	parentID := stringValue(items[0].MessageId)
	for _, item := range items[1:] {
		if item == nil {
			continue
		}
		upperID := stringValue(item.UpperMessageId)
		if upperID == "" {
			upperID = parentID
		}
		children[upperID] = append(children[upperID], item)
	}
	return children
}

func (t *OAPITransport) ensureOAPIForwardChildren(ctx context.Context, messageID string, children map[string][]*larkim.Message, visited map[string]struct{}) error {
	if messageID == "" || len(children[messageID]) > 0 {
		return nil
	}
	if _, ok := visited[messageID]; ok {
		return nil
	}
	if len(visited) >= oapiMaxForwardedQuoteMessages {
		return nil
	}
	visited[messageID] = struct{}{}
	items, err := t.fetchOAPIQuoteItems(ctx, messageID)
	if err != nil {
		return err
	}
	for parentID, nested := range buildOAPIForwardChildren(items) {
		if parentID == "" || len(nested) == 0 || len(children[parentID]) > 0 {
			continue
		}
		children[parentID] = nested
	}
	return nil
}

func oapiForwardedMessageAttrs(message *larkim.Message) []string {
	msgType := oapiMessageType(message)
	attrs := []string{
		fmt.Sprintf(`id="%s"`, escapeOAPIQuoteAttr(stringValue(message.MessageId))),
		fmt.Sprintf(`type="%s"`, escapeOAPIQuoteAttr(msgType)),
	}
	if senderID := oapiMessageSenderID(message); senderID != "" {
		attrs = append(attrs, fmt.Sprintf(`sender_id="%s"`, escapeOAPIQuoteAttr(senderID)))
	}
	if senderName := oapiMessageSenderName(message); senderName != "" {
		attrs = append(attrs, fmt.Sprintf(`sender_name="%s"`, escapeOAPIQuoteAttr(senderName)))
	}
	if createdAt := oapiMessageCreatedAt(message); createdAt != "" {
		attrs = append(attrs, fmt.Sprintf(`created_at="%s"`, escapeOAPIQuoteAttr(createdAt)))
	}
	return attrs
}

func expandOAPIInteractiveCard(flattened string, rawJSON string) string {
	if rawJSON == "" {
		return flattened
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(rawJSON), &parsed); err == nil {
		if userDSL, ok := parsed["user_dsl"].(string); ok && strings.TrimSpace(userDSL) != "" {
			return wrapOAPIInteractiveCard(userDSL)
		}
		if schema, ok := parsed["schema"].(string); ok && schema == "2.0" {
			return wrapOAPIInteractiveCard(rawJSON)
		}
	}
	if flattened == oapiInteractiveCardPlaceholder {
		return wrapOAPIInteractiveCard(rawJSON)
	}
	return flattened
}

func wrapOAPIInteractiveCard(content string) string {
	return "<interactive_card>\n" + content + "\n</interactive_card>"
}

func oapiFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func oapiMessageType(message *larkim.Message) string {
	if message == nil {
		return "text"
	}
	if msgType := stringValue(message.MsgType); msgType != "" {
		return msgType
	}
	return "text"
}

func oapiMessageBodyContent(message *larkim.Message) string {
	if message == nil || message.Body == nil {
		return ""
	}
	return stringValue(message.Body.Content)
}

func oapiMessageSenderID(message *larkim.Message) string {
	if message == nil || message.Sender == nil {
		return ""
	}
	return stringValue(message.Sender.Id)
}

func oapiMessageSenderName(message *larkim.Message) string {
	if message == nil || message.Sender == nil {
		return ""
	}
	return stringValue(message.Sender.SenderName)
}

func oapiMessageCreatedAt(message *larkim.Message) string {
	if message == nil {
		return ""
	}
	raw := stringValue(message.CreateTime)
	if raw == "" {
		return ""
	}
	ms, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format("2006-01-02T15:04:05.000Z")
}

func newForwardLimit() *int {
	limit := oapiMaxForwardedQuoteMessages
	return &limit
}

func escapeOAPIQuoteAttr(value string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&#39;",
	)
	return replacer.Replace(value)
}

func (t *OAPITransport) registerCallbacks() {
	t.channel.OnMessage(func(ctx context.Context, msg *channeltypes.NormalizedMessage) error {
		return t.emit(ctx, IncomingEvent{
			Kind:    appintake.EventMessage,
			Raw:     msg.RawEvent,
			Message: t.mapMessage(msg),
		})
	})
	t.channel.OnComment(func(ctx context.Context, event *channeltypes.CommentEvent) error {
		return t.emit(ctx, IncomingEvent{
			Kind:    appintake.EventComment,
			Raw:     event.RawEvent,
			Comment: mapComment(event),
		})
	})
	t.channel.OnCardAction(func(ctx context.Context, event *channeltypes.CardActionEvent) error {
		input := t.mapCardAction(ctx, event)
		return t.emit(ctx, IncomingEvent{
			Kind:       appintake.EventCardAction,
			Raw:        event.RawEvent,
			CardAction: input,
		})
	})
	t.channel.OnReady(func() {
		t.signalReady()
	})
	t.channel.OnError(func(err error) {
		_ = t.emit(context.Background(), IncomingEvent{
			Kind: appintake.EventDisconnect,
			Disconnect: &appintake.DisconnectInput{
				Reason: err.Error(),
				At:     t.timeNow(),
			},
		})
	})
	t.channel.OnReconnecting(func() {
		_ = t.emit(context.Background(), IncomingEvent{
			Kind: appintake.EventReconnect,
			Reconnect: &appintake.ReconnectInput{
				Phase: appintake.ReconnectReconnecting,
				At:    t.timeNow(),
			},
		})
	})
	t.channel.OnReconnected(func() {
		_ = t.emit(context.Background(), IncomingEvent{
			Kind: appintake.EventReconnect,
			Reconnect: &appintake.ReconnectInput{
				Phase: appintake.ReconnectRecovered,
				At:    t.timeNow(),
			},
		})
	})
	t.channel.OnDisconnected(func() {
		_ = t.emit(context.Background(), IncomingEvent{
			Kind: appintake.EventDisconnect,
			Disconnect: &appintake.DisconnectInput{
				Reason: "disconnected",
				At:     t.timeNow(),
			},
		})
	})
}

func (t *OAPITransport) emit(ctx context.Context, event IncomingEvent) error {
	t.mu.Lock()
	handler := t.handler
	connected := t.connected
	t.mu.Unlock()
	if !connected || handler == nil {
		return nil
	}
	return handler.HandleLarkTransportEvent(ctx, event)
}

func (t *OAPITransport) signalReady() {
	t.mu.Lock()
	ready := t.ready
	t.mu.Unlock()
	if ready == nil {
		return
	}
	select {
	case ready <- struct{}{}:
	default:
	}
}

func (t *OAPITransport) resetFailedStart() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.started = false
	t.connected = false
	t.handler = nil
	t.ready = nil
	t.startCancel = nil
}

func (t *OAPITransport) mapMessage(msg *channeltypes.NormalizedMessage) *appintake.MessageInput {
	if msg == nil {
		return nil
	}
	threadID, rootID, parentID := messageReplyIDsFromRawEvent(msg.RawEvent)
	chatType := mapChatType(msg.ChatType)
	mode := appintake.ChatModeGroup
	if chatType == appintake.ChatTypeP2P {
		mode = appintake.ChatModeP2P
	}
	if threadID != "" {
		mode = appintake.ChatModeTopic
	}
	return &appintake.MessageInput{
		MessageID:        msg.MessageID,
		ChatID:           msg.ChatID,
		ChatType:         chatType,
		ResolvedMode:     mode,
		ThreadID:         threadID,
		RootID:           rootID,
		ParentID:         parentID,
		ReplyToMessageID: parentID,
		Sender:           appintake.Actor{OpenID: msg.UserID},
		SenderType:       senderTypeFromRawEvent(msg.RawEvent),
		Content:          msg.Content,
		RawContentType:   msg.RawContentType,
		RawContent:       rawMessageContent(msg.RawEvent),
		Resources:        mapOAPIResources(msg.Resources),
		Mentions:         mapOAPIMentions(msg.Mentions),
		MentionAll:       msg.MentionAll,
		MentionedBot:     msg.MentionedBot,
		CreateTime:       unixMillis(msg.CreateTimeMs),
		Metadata: map[string]any{
			"eventId": msg.EventID,
			"raw":     msg.RawEvent,
		},
	}
}

func rawMessageContent(raw any) any {
	rawMap := rawEventMap(raw)
	if rawMap == nil {
		return nil
	}
	for _, path := range [][]string{
		{"event", "message", "content"},
		{"message", "content"},
		{"content"},
	} {
		if value, ok := nestedRawValue(rawMap, path...); ok {
			return value
		}
	}
	return nil
}

func senderTypeFromRawEvent(raw any) appintake.SenderType {
	rawMap := rawEventMap(raw)
	if rawMap == nil {
		return ""
	}
	for _, path := range [][]string{
		{"event", "sender", "sender_type"},
		{"sender", "sender_type"},
	} {
		value, ok := nestedRawValue(rawMap, path...)
		if !ok {
			continue
		}
		switch value {
		case "user":
			return appintake.SenderTypeUser
		case "app", "bot":
			return appintake.SenderTypeBot
		}
	}
	return ""
}

func rawEventMap(raw any) map[string]any {
	switch typed := raw.(type) {
	case map[string]any:
		return typed
	case nil:
		return nil
	default:
		payload, err := json.Marshal(raw)
		if err != nil {
			return nil
		}
		var out map[string]any
		if err := json.Unmarshal(payload, &out); err != nil {
			return nil
		}
		return out
	}
}

func nestedRawValue(input map[string]any, path ...string) (any, bool) {
	var current any = input
	for _, key := range path {
		m, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = m[key]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func mapComment(event *channeltypes.CommentEvent) *appintake.CommentInput {
	if event == nil {
		return nil
	}
	return &appintake.CommentInput{
		EventID:      event.EventID,
		FileToken:    event.FileToken,
		FileType:     event.FileType,
		CommentID:    event.CommentID,
		ReplyID:      event.ReplyID,
		Operator:     mapOperator(event.Operator),
		MentionedBot: event.MentionedBot,
		Metadata: map[string]any{
			"raw": event.RawEvent,
		},
		CreateTime: unixMillis(event.Timestamp),
	}
}

func (t *OAPITransport) mapCardAction(ctx context.Context, event *channeltypes.CardActionEvent) *appintake.CardActionInput {
	if event == nil {
		return nil
	}
	chatID := event.ChatID
	chatType := appintake.ChatTypeGroup
	mode := appintake.ChatModeGroup
	if cardActionLooksP2P(chatID) {
		chatType = appintake.ChatTypeP2P
		mode = appintake.ChatModeP2P
	}
	if chatID == "" && event.Operator.OpenID != "" {
		chatID = event.Operator.OpenID
		chatType = appintake.ChatTypeP2P
		mode = appintake.ChatModeP2P
	}
	threadID := ""
	if chatID != "" && event.MessageID != "" && t.client != nil && chatType != appintake.ChatTypeP2P {
		threadID, _ = t.ResolveCarrierThreadID(ctx, chatID, event.MessageID)
	}
	if threadID != "" {
		mode = appintake.ChatModeTopic
		chatType = appintake.ChatTypeGroup
	}
	return &appintake.CardActionInput{
		EventID:      event.EventID,
		MessageID:    event.MessageID,
		ChatID:       chatID,
		ChatType:     chatType,
		ResolvedMode: mode,
		ThreadID:     threadID,
		Operator: appintake.Actor{
			OpenID: event.Operator.OpenID,
			UserID: event.Operator.UserID,
		},
		ActionValue: event.Action.Value,
		FormValue:   event.Action.FormValue,
		RawContent:  event.RawEvent,
		Metadata: map[string]any{
			"token":        event.Token,
			"host":         event.Host,
			"deliveryType": event.DeliveryType,
			"action":       event.Action,
			"context":      event.Context,
		},
	}
}

func cardActionLooksP2P(chatID string) bool {
	return strings.HasPrefix(chatID, "ou_") || strings.HasPrefix(chatID, "on_") || strings.HasPrefix(chatID, "un_")
}

func (t *OAPITransport) timeNow() time.Time {
	if t == nil || t.now == nil {
		return time.Now()
	}
	return t.now()
}

func sendInputForMessage(recipientID string, content MessageContent, opts SendOptions) (*channeltypes.SendInput, error) {
	input := &channeltypes.SendInput{
		ReceiveID:      recipientID,
		ReplyMessageID: opts.ReplyTo,
	}
	switch {
	case content.Card != nil:
		cardJSON, err := marshalCard(content.Card)
		if err != nil {
			return nil, err
		}
		input.Card = cardJSON
	case content.Markdown != "":
		input.Markdown = content.Markdown
	case content.Text != "":
		input.Text = content.Text
	default:
		return nil, errors.New("lark message content is empty")
	}
	return input, nil
}

func directMessageContent(content MessageContent) (string, string, error) {
	switch {
	case content.Card != nil:
		cardJSON, err := marshalCard(content.Card)
		return "interactive", cardJSON, err
	case content.Markdown != "":
		postJSON, err := larknormalize.SimpleMarkdownToPost("", content.Markdown, nil)
		return "post", postJSON, err
	case content.Text != "":
		textJSON, err := json.Marshal(map[string]string{"text": content.Text})
		return "text", string(textJSON), err
	default:
		return "", "", errors.New("lark message content is empty")
	}
}

func patchMessageContent(content MessageContent) (string, error) {
	switch {
	case content.Markdown != "":
		postJSON, err := larknormalize.SimpleMarkdownToPost("", content.Markdown, nil)
		return postJSON, err
	case content.Text != "":
		textJSON, err := json.Marshal(map[string]string{"text": content.Text})
		return string(textJSON), err
	default:
		return "", errors.New("lark message content is empty")
	}
}

func validateThreadSend(opts SendOptions) error {
	if opts.ThreadID != "" && opts.ReplyTo == "" {
		return ErrOAPIThreadIDSend
	}
	if opts.ReplyInThread && opts.ReplyTo == "" {
		return ErrOAPIReplyThread
	}
	return nil
}

func (t *OAPITransport) sendDirect(ctx context.Context, recipientID string, msgType string, content string, opts SendOptions) (SendResult, error) {
	if t == nil || t.client == nil {
		return SendResult{}, ErrOAPIClient
	}
	if opts.ReplyTo != "" {
		body := larkim.NewReplyMessageReqBodyBuilder().
			MsgType(msgType).
			Content(content)
		if opts.ReplyInThread {
			body.ReplyInThread(true)
		}
		resp, err := t.client.Im.V1.Message.Reply(ctx, larkim.NewReplyMessageReqBuilder().
			MessageId(opts.ReplyTo).
			Body(body.Build()).
			Build())
		if err != nil {
			return SendResult{}, err
		}
		if !resp.Success() {
			return SendResult{}, oapiCodeError("reply message", resp.Code, resp.Msg)
		}
		if resp.Data == nil || resp.Data.MessageId == nil {
			return SendResult{}, ErrOAPIMessageMissing
		}
		return SendResult{MessageID: *resp.Data.MessageId, Raw: resp}, nil
	}

	receiveType, err := outbound.DetectReceiveIdType(recipientID)
	if err != nil {
		return SendResult{}, err
	}
	resp, err := t.client.Im.V1.Message.Create(ctx, larkim.NewCreateMessageReqBuilder().
		ReceiveIdType(string(receiveType)).
		Body(larkim.NewCreateMessageReqBodyBuilder().
			ReceiveId(recipientID).
			MsgType(msgType).
			Content(content).
			Build()).
		Build())
	if err != nil {
		return SendResult{}, err
	}
	if !resp.Success() {
		return SendResult{}, oapiCodeError("create message", resp.Code, resp.Msg)
	}
	if resp.Data == nil || resp.Data.MessageId == nil {
		return SendResult{}, ErrOAPIMessageMissing
	}
	return SendResult{MessageID: *resp.Data.MessageId, Raw: resp}, nil
}

func marshalCard(card map[string]any) (string, error) {
	if card == nil {
		return "", errors.New("lark card is required")
	}
	b, err := json.Marshal(card)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func oapiCodeError(operation string, code int, msg string) error {
	return &OAPIError{Operation: operation, Code: code, Message: msg}
}

func checkOAPIRawCode(operation string, raw []byte) error {
	if len(raw) == 0 {
		return nil
	}
	var out larkcore.CodeError
	if err := json.Unmarshal(raw, &out); err != nil {
		return err
	}
	if out.Code != 0 {
		return oapiCodeError(operation, out.Code, out.Msg)
	}
	return nil
}

func stringAny(values ...any) string {
	for _, value := range values {
		if text, ok := value.(string); ok && text != "" {
			return text
		}
	}
	return ""
}

func larkcardCreateReq(cardJSON string) *larkcardkit.CreateCardReq {
	return larkcardkit.NewCreateCardReqBuilder().
		Body(larkcardkit.NewCreateCardReqBodyBuilder().
			Type("card_json").
			Data(cardJSON).
			Build()).
		Build()
}

func larkcardUpdateReq(cardID string, cardJSON string, sequence int) *larkcardkit.UpdateCardReq {
	body := larkcardkit.NewUpdateCardReqBodyBuilder().
		Card(larkcardkit.NewCardBuilder().
			Type("card_json").
			Data(cardJSON).
			Build())
	if sequence > 0 {
		body.Sequence(sequence)
	}
	return larkcardkit.NewUpdateCardReqBuilder().
		CardId(cardID).
		Body(body.Build()).
		Build()
}

func mapChatType(value string) appintake.ChatType {
	if value == "p2p" {
		return appintake.ChatTypeP2P
	}
	return appintake.ChatTypeGroup
}

func mapOAPIResources(resources []channeltypes.Resource) []appintake.Resource {
	out := make([]appintake.Resource, 0, len(resources))
	for _, resource := range resources {
		out = append(out, appintake.Resource{
			Kind: resource.Type,
			ID:   resource.FileKey,
			Name: resource.FileName,
		})
	}
	return out
}

func mapOAPIMentions(mentions []channeltypes.Mention) []appintake.Mention {
	out := make([]appintake.Mention, 0, len(mentions))
	for _, mention := range mentions {
		isBot := mention.IsBot
		out = append(out, appintake.Mention{
			Key:    mention.Key,
			OpenID: mention.OpenID,
			UserID: mention.UserID,
			Name:   mention.Name,
			IsBot:  &isBot,
		})
	}
	return out
}

func mapOperator(operator channeltypes.OperatorInfo) appintake.Actor {
	return appintake.Actor{
		OpenID:  operator.OpenID,
		UserID:  operator.UserID,
		UnionID: operator.UnionID,
	}
}

func unixMillis(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

func messageReplyIDsFromRawEvent(raw any) (threadID string, rootID string, parentID string) {
	event, ok := raw.(*larkim.P2MessageReceiveV1)
	if !ok || event.Event == nil || event.Event.Message == nil {
		return "", "", ""
	}
	message := event.Event.Message
	return stringValue(message.ThreadId), stringValue(message.RootId), stringValue(message.ParentId)
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
