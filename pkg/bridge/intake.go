package bridge

import (
	"context"
	"time"

	appintake "github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/intake"
)

type LarkEventKind string

const (
	LarkEventMessage    LarkEventKind = "message"
	LarkEventComment    LarkEventKind = "comment"
	LarkEventCardAction LarkEventKind = "cardAction"
	LarkEventReconnect  LarkEventKind = "reconnect"
	LarkEventKeepalive  LarkEventKind = "keepalive"
	LarkEventDisconnect LarkEventKind = "disconnect"
)

type LarkChatType string

const (
	LarkChatTypeP2P   LarkChatType = "p2p"
	LarkChatTypeGroup LarkChatType = "group"
)

type LarkChatMode string

const (
	LarkChatModeP2P   LarkChatMode = "p2p"
	LarkChatModeGroup LarkChatMode = "group"
	LarkChatModeTopic LarkChatMode = "topic"
)

type LarkSenderType string

const (
	LarkSenderTypeUser LarkSenderType = "user"
	LarkSenderTypeBot  LarkSenderType = "bot"
)

type LarkReconnectPhase string

const (
	LarkReconnectReconnecting LarkReconnectPhase = "reconnecting"
	LarkReconnectRecovered    LarkReconnectPhase = "recovered"
	LarkReconnectFailed       LarkReconnectPhase = "failed"
)

type LarkConnectionState string

const (
	LarkConnectionUnknown      LarkConnectionState = "unknown"
	LarkConnectionConnected    LarkConnectionState = "connected"
	LarkConnectionReconnecting LarkConnectionState = "reconnecting"
	LarkConnectionDisconnected LarkConnectionState = "disconnected"
)

type LarkScopeSource string

const (
	LarkScopeSourceIM         LarkScopeSource = "im"
	LarkScopeSourceCard       LarkScopeSource = "card"
	LarkScopeSourceComment    LarkScopeSource = "comment"
	LarkScopeSourceReconnect  LarkScopeSource = "reconnect"
	LarkScopeSourceKeepalive  LarkScopeSource = "keepalive"
	LarkScopeSourceDisconnect LarkScopeSource = "disconnect"
)

type LarkSelfLoopReason string

const (
	LarkSelfLoopNotSelf        LarkSelfLoopReason = ""
	LarkSelfLoopBotOpenID      LarkSelfLoopReason = "bot-open-id"
	LarkSelfLoopBridgeReply    LarkSelfLoopReason = "bridge-reply"
	LarkSelfLoopBridgeMetadata LarkSelfLoopReason = "bridge-metadata"
)

const LarkCurrentThread = appintake.CurrentThread

type LarkActor struct {
	OpenID  string
	UserID  string
	UnionID string
	Name    string
}

type LarkResource struct {
	Kind string
	ID   string
	Name string
	Size int64
	URL  string
}

type LarkMention struct {
	Key    string
	OpenID string
	UserID string
	Name   string
	IsBot  *bool
}

type LarkMessageInput struct {
	MessageID        string
	ChatID           string
	ChatType         LarkChatType
	ResolvedMode     LarkChatMode
	ThreadID         string
	RootID           string
	ParentID         string
	ReplyToMessageID string
	Sender           LarkActor
	SenderType       LarkSenderType
	Content          string
	RawContentType   string
	RawContent       any
	Resources        []LarkResource
	Mentions         []LarkMention
	MentionAll       bool
	MentionedBot     bool
	CreateTime       time.Time
	Metadata         map[string]any
}

type LarkCommentInput struct {
	EventID      string
	FileToken    string
	FileType     string
	CommentID    string
	ReplyID      string
	Operator     LarkActor
	MentionedBot bool

	ExplicitScopeKey string
	InheritScopeKey  string
	BridgeReply      bool
	Metadata         map[string]any
	CreateTime       time.Time
}

type LarkCardActionInput struct {
	EventID       string
	MessageID     string
	ChatID        string
	ChatType      LarkChatType
	ResolvedMode  LarkChatMode
	ThreadID      string
	Operator      LarkActor
	ActionValue   map[string]any
	FormValue     map[string]any
	RawContent    any
	CreateTime    time.Time
	ExplicitScope string
	InheritScope  string
	Metadata      map[string]any
}

type LarkReconnectInput struct {
	Phase               LarkReconnectPhase
	ConsecutiveAttempts int
	Error               string
	At                  time.Time
}

type LarkKeepaliveInput struct {
	State             LarkConnectionState
	ReconnectAttempts int
	NetworkReachable  bool
	ConsecutiveDown   int
	Slept             time.Duration
	At                time.Time
}

type LarkDisconnectInput struct {
	Reason string
	At     time.Time
}

type LarkIntakeScope struct {
	Key       string
	Source    LarkScopeSource
	ChatID    string
	ChatType  LarkChatType
	ChatMode  LarkChatMode
	ThreadID  string
	ActorID   string
	ParentKey string

	FileToken        string
	FileType         string
	CommentID        string
	CommentScopeKey  string
	DocumentScopeKey string
}

type LarkSelfLoopPolicy struct {
	BotOpenID              string
	DropMessagesFromBot    bool
	DropCommentsFromBot    bool
	DropCardActionsFromBot bool
}

type LarkSelfLoopDecision struct {
	Drop   bool
	Reason LarkSelfLoopReason
}

type LarkNormalizedEvent struct {
	Kind LarkEventKind

	Scope LarkIntakeScope
	Self  LarkSelfLoopDecision

	Message    *LarkMessageInput
	Comment    *LarkCommentInput
	CardAction *LarkCardActionInput
	Reconnect  *LarkReconnectInput
	Keepalive  *LarkKeepaliveInput
	Disconnect *LarkDisconnectInput
}

type LarkIntakeBatch struct {
	Scope  LarkIntakeScope
	Events []LarkNormalizedEvent
}

type LarkIntakeBatchHandler func(context.Context, LarkIntakeBatch) error

type LarkIntakeQueue struct {
	inner *appintake.Queue
}

func DefaultLarkSelfLoopPolicy(botOpenID string) LarkSelfLoopPolicy {
	return fromInternalLarkSelfLoopPolicy(appintake.DefaultSelfLoopPolicy(botOpenID))
}

type LarkIntakeNormalizer struct {
	inner appintake.Normalizer
}

func NewLarkIntakeNormalizer(policy LarkSelfLoopPolicy) LarkIntakeNormalizer {
	return LarkIntakeNormalizer{inner: appintake.NewNormalizer(toInternalLarkSelfLoopPolicy(policy))}
}

func (n LarkIntakeNormalizer) NormalizeMessage(input LarkMessageInput) LarkNormalizedEvent {
	return fromInternalLarkNormalizedEvent(n.inner.NormalizeMessage(toInternalLarkMessageInput(input)))
}

func (n LarkIntakeNormalizer) NormalizeComment(input LarkCommentInput) LarkNormalizedEvent {
	return fromInternalLarkNormalizedEvent(n.inner.NormalizeComment(toInternalLarkCommentInput(input)))
}

func (n LarkIntakeNormalizer) NormalizeCardAction(input LarkCardActionInput) LarkNormalizedEvent {
	return fromInternalLarkNormalizedEvent(n.inner.NormalizeCardAction(toInternalLarkCardActionInput(input)))
}

func NormalizeLarkReconnect(input LarkReconnectInput) LarkNormalizedEvent {
	return fromInternalLarkNormalizedEvent(appintake.NormalizeReconnect(toInternalLarkReconnectInput(input)))
}

func NormalizeLarkKeepalive(input LarkKeepaliveInput) LarkNormalizedEvent {
	return fromInternalLarkNormalizedEvent(appintake.NormalizeKeepalive(toInternalLarkKeepaliveInput(input)))
}

func NormalizeLarkDisconnect(input LarkDisconnectInput) LarkNormalizedEvent {
	return fromInternalLarkNormalizedEvent(appintake.NormalizeDisconnect(toInternalLarkDisconnectInput(input)))
}

func LarkMessageScope(input LarkMessageInput) LarkIntakeScope {
	return fromInternalLarkScope(appintake.MessageScope(toInternalLarkMessageInput(input)))
}

func LarkCommentScope(input LarkCommentInput) LarkIntakeScope {
	return fromInternalLarkScope(appintake.CommentScope(toInternalLarkCommentInput(input)))
}

func LarkCardActionScope(input LarkCardActionInput) LarkIntakeScope {
	return fromInternalLarkScope(appintake.CardActionScope(toInternalLarkCardActionInput(input)))
}

func LarkCommentDocumentScopeKey(fileToken string) string {
	return appintake.CommentDocumentScopeKey(fileToken)
}

func LarkCommentScopeKey(fileToken, commentID string) string {
	return appintake.CommentScopeKey(fileToken, commentID)
}

type LarkIntakeQueueOptions struct {
	QuietPeriod  time.Duration
	FlushTimeout time.Duration
	Handler      LarkIntakeBatchHandler
}

func NewLarkIntakeQueue(options LarkIntakeQueueOptions) *LarkIntakeQueue {
	queue := appintake.NewQueue(appintake.QueueOptions{
		QuietPeriod:  options.QuietPeriod,
		FlushTimeout: options.FlushTimeout,
		Handler: func(ctx context.Context, batch appintake.Batch) error {
			if options.Handler == nil {
				return nil
			}
			return options.Handler(ctx, fromInternalLarkBatch(batch))
		},
	})
	return &LarkIntakeQueue{inner: queue}
}

func (q *LarkIntakeQueue) Push(event LarkNormalizedEvent) (int, error) {
	if q == nil || q.inner == nil {
		return 0, nil
	}
	return q.inner.Push(toInternalLarkNormalizedEvent(event))
}

func (q *LarkIntakeQueue) Block(scopeKey string) {
	if q != nil && q.inner != nil {
		q.inner.Block(scopeKey)
	}
}

func (q *LarkIntakeQueue) Unblock(scopeKey string) {
	if q != nil && q.inner != nil {
		q.inner.Unblock(scopeKey)
	}
}

func (q *LarkIntakeQueue) Flush(ctx context.Context, scopeKey string) error {
	if q == nil || q.inner == nil {
		return nil
	}
	return q.inner.Flush(ctx, scopeKey)
}

func (q *LarkIntakeQueue) FlushAll(ctx context.Context) error {
	if q == nil || q.inner == nil {
		return nil
	}
	return q.inner.FlushAll(ctx)
}

func (q *LarkIntakeQueue) Cancel(scopeKey string) []LarkNormalizedEvent {
	if q == nil || q.inner == nil {
		return nil
	}
	return fromInternalLarkEvents(q.inner.Cancel(scopeKey))
}

func (q *LarkIntakeQueue) CancelAll() {
	if q != nil && q.inner != nil {
		q.inner.CancelAll()
	}
}

func (q *LarkIntakeQueue) ScopeKeys() []string {
	if q == nil || q.inner == nil {
		return nil
	}
	return q.inner.ScopeKeys()
}

func (q *LarkIntakeQueue) Close() {
	if q != nil && q.inner != nil {
		q.inner.Close()
	}
}

func FlushLarkIntakeQueue(ctx context.Context, queue *LarkIntakeQueue, scopeKey string) error {
	if queue == nil {
		return nil
	}
	return queue.Flush(ctx, scopeKey)
}

func toInternalLarkNormalizedEvent(event LarkNormalizedEvent) appintake.NormalizedEvent {
	return appintake.NormalizedEvent{
		Kind:       appintake.EventKind(event.Kind),
		Scope:      toInternalLarkScope(event.Scope),
		Self:       toInternalLarkSelfLoopDecision(event.Self),
		Message:    toInternalLarkMessageInputPtr(event.Message),
		Comment:    toInternalLarkCommentInputPtr(event.Comment),
		CardAction: toInternalLarkCardActionInputPtr(event.CardAction),
		Reconnect:  toInternalLarkReconnectInputPtr(event.Reconnect),
		Keepalive:  toInternalLarkKeepaliveInputPtr(event.Keepalive),
		Disconnect: toInternalLarkDisconnectInputPtr(event.Disconnect),
	}
}

func fromInternalLarkNormalizedEvent(event appintake.NormalizedEvent) LarkNormalizedEvent {
	return LarkNormalizedEvent{
		Kind:       LarkEventKind(event.Kind),
		Scope:      fromInternalLarkScope(event.Scope),
		Self:       fromInternalLarkSelfLoopDecision(event.Self),
		Message:    fromInternalLarkMessageInputPtr(event.Message),
		Comment:    fromInternalLarkCommentInputPtr(event.Comment),
		CardAction: fromInternalLarkCardActionInputPtr(event.CardAction),
		Reconnect:  fromInternalLarkReconnectInputPtr(event.Reconnect),
		Keepalive:  fromInternalLarkKeepaliveInputPtr(event.Keepalive),
		Disconnect: fromInternalLarkDisconnectInputPtr(event.Disconnect),
	}
}

func toInternalLarkMessageInput(input LarkMessageInput) appintake.MessageInput {
	out, _ := convertBridgeJSON[appintake.MessageInput](input)
	return out
}

func toInternalLarkMessages(messages []LarkMessageInput) []appintake.MessageInput {
	if len(messages) == 0 {
		return nil
	}
	out := make([]appintake.MessageInput, 0, len(messages))
	for _, message := range messages {
		out = append(out, toInternalLarkMessageInput(message))
	}
	return out
}

func toInternalLarkActor(actor LarkActor) appintake.Actor {
	return appintake.Actor{
		OpenID:  actor.OpenID,
		UserID:  actor.UserID,
		UnionID: actor.UnionID,
		Name:    actor.Name,
	}
}

func fromInternalLarkMessageInput(input appintake.MessageInput) LarkMessageInput {
	out, _ := convertBridgeJSON[LarkMessageInput](input)
	return out
}

func toInternalLarkMessageInputPtr(input *LarkMessageInput) *appintake.MessageInput {
	if input == nil {
		return nil
	}
	out := toInternalLarkMessageInput(*input)
	return &out
}

func fromInternalLarkMessageInputPtr(input *appintake.MessageInput) *LarkMessageInput {
	if input == nil {
		return nil
	}
	out := fromInternalLarkMessageInput(*input)
	return &out
}

func toInternalLarkCommentInput(input LarkCommentInput) appintake.CommentInput {
	out, _ := convertBridgeJSON[appintake.CommentInput](input)
	return out
}

func fromInternalLarkCommentInput(input appintake.CommentInput) LarkCommentInput {
	out, _ := convertBridgeJSON[LarkCommentInput](input)
	return out
}

func toInternalLarkCommentInputPtr(input *LarkCommentInput) *appintake.CommentInput {
	if input == nil {
		return nil
	}
	out := toInternalLarkCommentInput(*input)
	return &out
}

func fromInternalLarkCommentInputPtr(input *appintake.CommentInput) *LarkCommentInput {
	if input == nil {
		return nil
	}
	out := fromInternalLarkCommentInput(*input)
	return &out
}

func toInternalLarkCardActionInput(input LarkCardActionInput) appintake.CardActionInput {
	out, _ := convertBridgeJSON[appintake.CardActionInput](input)
	return out
}

func fromInternalLarkCardActionInput(input appintake.CardActionInput) LarkCardActionInput {
	out, _ := convertBridgeJSON[LarkCardActionInput](input)
	return out
}

func toInternalLarkCardActionInputPtr(input *LarkCardActionInput) *appintake.CardActionInput {
	if input == nil {
		return nil
	}
	out := toInternalLarkCardActionInput(*input)
	return &out
}

func fromInternalLarkCardActionInputPtr(input *appintake.CardActionInput) *LarkCardActionInput {
	if input == nil {
		return nil
	}
	out := fromInternalLarkCardActionInput(*input)
	return &out
}

func toInternalLarkReconnectInput(input LarkReconnectInput) appintake.ReconnectInput {
	return appintake.ReconnectInput{
		Phase:               appintake.ReconnectPhase(input.Phase),
		ConsecutiveAttempts: input.ConsecutiveAttempts,
		Error:               input.Error,
		At:                  input.At,
	}
}

func fromInternalLarkReconnectInput(input appintake.ReconnectInput) LarkReconnectInput {
	return LarkReconnectInput{
		Phase:               LarkReconnectPhase(input.Phase),
		ConsecutiveAttempts: input.ConsecutiveAttempts,
		Error:               input.Error,
		At:                  input.At,
	}
}

func toInternalLarkReconnectInputPtr(input *LarkReconnectInput) *appintake.ReconnectInput {
	if input == nil {
		return nil
	}
	out := toInternalLarkReconnectInput(*input)
	return &out
}

func fromInternalLarkReconnectInputPtr(input *appintake.ReconnectInput) *LarkReconnectInput {
	if input == nil {
		return nil
	}
	out := fromInternalLarkReconnectInput(*input)
	return &out
}

func toInternalLarkKeepaliveInput(input LarkKeepaliveInput) appintake.KeepaliveInput {
	return appintake.KeepaliveInput{
		State:             appintake.ConnectionState(input.State),
		ReconnectAttempts: input.ReconnectAttempts,
		NetworkReachable:  input.NetworkReachable,
		ConsecutiveDown:   input.ConsecutiveDown,
		Slept:             input.Slept,
		At:                input.At,
	}
}

func fromInternalLarkKeepaliveInput(input appintake.KeepaliveInput) LarkKeepaliveInput {
	return LarkKeepaliveInput{
		State:             LarkConnectionState(input.State),
		ReconnectAttempts: input.ReconnectAttempts,
		NetworkReachable:  input.NetworkReachable,
		ConsecutiveDown:   input.ConsecutiveDown,
		Slept:             input.Slept,
		At:                input.At,
	}
}

func toInternalLarkKeepaliveInputPtr(input *LarkKeepaliveInput) *appintake.KeepaliveInput {
	if input == nil {
		return nil
	}
	out := toInternalLarkKeepaliveInput(*input)
	return &out
}

func fromInternalLarkKeepaliveInputPtr(input *appintake.KeepaliveInput) *LarkKeepaliveInput {
	if input == nil {
		return nil
	}
	out := fromInternalLarkKeepaliveInput(*input)
	return &out
}

func toInternalLarkDisconnectInput(input LarkDisconnectInput) appintake.DisconnectInput {
	return appintake.DisconnectInput{Reason: input.Reason, At: input.At}
}

func fromInternalLarkDisconnectInput(input appintake.DisconnectInput) LarkDisconnectInput {
	return LarkDisconnectInput{Reason: input.Reason, At: input.At}
}

func toInternalLarkDisconnectInputPtr(input *LarkDisconnectInput) *appintake.DisconnectInput {
	if input == nil {
		return nil
	}
	out := toInternalLarkDisconnectInput(*input)
	return &out
}

func fromInternalLarkDisconnectInputPtr(input *appintake.DisconnectInput) *LarkDisconnectInput {
	if input == nil {
		return nil
	}
	out := fromInternalLarkDisconnectInput(*input)
	return &out
}

func toInternalLarkScope(scope LarkIntakeScope) appintake.Scope {
	out, _ := convertBridgeJSON[appintake.Scope](scope)
	return out
}

func fromInternalLarkScope(scope appintake.Scope) LarkIntakeScope {
	out, _ := convertBridgeJSON[LarkIntakeScope](scope)
	return out
}

func toInternalLarkSelfLoopPolicy(policy LarkSelfLoopPolicy) appintake.SelfLoopPolicy {
	return appintake.SelfLoopPolicy{
		BotOpenID:              policy.BotOpenID,
		DropMessagesFromBot:    policy.DropMessagesFromBot,
		DropCommentsFromBot:    policy.DropCommentsFromBot,
		DropCardActionsFromBot: policy.DropCardActionsFromBot,
	}
}

func fromInternalLarkSelfLoopPolicy(policy appintake.SelfLoopPolicy) LarkSelfLoopPolicy {
	return LarkSelfLoopPolicy{
		BotOpenID:              policy.BotOpenID,
		DropMessagesFromBot:    policy.DropMessagesFromBot,
		DropCommentsFromBot:    policy.DropCommentsFromBot,
		DropCardActionsFromBot: policy.DropCardActionsFromBot,
	}
}

func toInternalLarkSelfLoopDecision(decision LarkSelfLoopDecision) appintake.SelfLoopDecision {
	return appintake.SelfLoopDecision{
		Drop:   decision.Drop,
		Reason: appintake.SelfLoopReason(decision.Reason),
	}
}

func fromInternalLarkSelfLoopDecision(decision appintake.SelfLoopDecision) LarkSelfLoopDecision {
	return LarkSelfLoopDecision{
		Drop:   decision.Drop,
		Reason: LarkSelfLoopReason(decision.Reason),
	}
}

func fromInternalLarkBatch(batch appintake.Batch) LarkIntakeBatch {
	return LarkIntakeBatch{
		Scope:  fromInternalLarkScope(batch.Scope),
		Events: fromInternalLarkEvents(batch.Events),
	}
}

func fromInternalLarkEvents(events []appintake.NormalizedEvent) []LarkNormalizedEvent {
	if len(events) == 0 {
		return nil
	}
	out := make([]LarkNormalizedEvent, 0, len(events))
	for _, event := range events {
		out = append(out, fromInternalLarkNormalizedEvent(event))
	}
	return out
}
