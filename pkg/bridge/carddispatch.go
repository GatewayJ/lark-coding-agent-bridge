package bridge

import (
	"context"
	"errors"
	"time"

	appdispatch "github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/carddispatch"
	appintake "github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/intake"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/access"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/profile"
)

const (
	BridgeCardCallbackMarker       = appdispatch.BridgeCallbackMarker
	LegacyClaudeCardCallbackMarker = appdispatch.LegacyClaudeCallbackMarker
	BridgeCardTokenKey             = appdispatch.BridgeTokenKey
	BridgeCardFormValueKey         = appdispatch.FormValueKey
)

var (
	ErrCardCallbackAuthMissing        = appdispatch.ErrCallbackAuthMissing
	ErrCardCallbackDenied             = appdispatch.ErrCallbackDenied
	ErrCardActiveRunLookupRequired    = appdispatch.ErrActiveRunLookupRequired
	ErrCardActiveRunMissing           = appdispatch.ErrActiveRunMissing
	ErrCardCarrierThreadLookupNeeded  = appdispatch.ErrCarrierThreadLookupNeeded
	ErrCardCarrierThreadLookupFailed  = appdispatch.ErrCarrierThreadLookupFailed
	ErrCardCarrierThreadIDUnavailable = appdispatch.ErrCarrierThreadIDUnavailable
)

type CardDispatchOutcome string

const (
	CardDispatchIgnored     CardDispatchOutcome = "ignored"
	CardDispatchCommand     CardDispatchOutcome = "command"
	CardDispatchEnqueued    CardDispatchOutcome = "enqueued"
	CardDispatchNeedsLookup CardDispatchOutcome = "needs_lookup"
	CardDispatchRejected    CardDispatchOutcome = "rejected"
)

type CardActionDispatchInput = LarkCardActionInput

type CardActionDispatchResult struct {
	Outcome         CardDispatchOutcome
	Scope           LarkIntakeScope
	Command         string
	Args            string
	CommandResponse any
	Enqueued        *LarkNormalizedEvent
	Verified        bool
	RejectReason    string
}

type CardCommandRequest struct {
	Command    string
	Args       string
	ScopeID    string
	ChatID     string
	ThreadID   string
	ActorID    string
	SenderID   string
	ChatMode   LarkChatMode
	Value      map[string]any
	FormValue  map[string]any
	FromCard   bool
	MessageID  string
	EventID    string
	OccurredAt time.Time
}

type CardActiveRun struct {
	RunID             string
	PolicyFingerprint string
}

type CardCallbackVerifyExpected struct {
	RunID             string
	Scope             string
	ChatID            string
	OperatorOpenID    string
	Action            string
	PolicyFingerprint string
}

type CardCallbackVerifyResult struct {
	OK     bool
	Reason string
}

type CardCommandHandler interface {
	HandleCardCommand(ctx context.Context, req CardCommandRequest) (any, error)
}

type CardCommandHandlerFunc func(ctx context.Context, req CardCommandRequest) (any, error)

func (f CardCommandHandlerFunc) HandleCardCommand(ctx context.Context, req CardCommandRequest) (any, error) {
	return f(ctx, req)
}

type CardPromptEnqueuer interface {
	EnqueueCardPrompt(ctx context.Context, event LarkNormalizedEvent) error
}

type CardPromptEnqueuerFunc func(ctx context.Context, event LarkNormalizedEvent) error

func (f CardPromptEnqueuerFunc) EnqueueCardPrompt(ctx context.Context, event LarkNormalizedEvent) error {
	return f(ctx, event)
}

type CardCarrierThreadResolver interface {
	ResolveCarrierThreadID(ctx context.Context, chatID, messageID string) (string, error)
}

type CardCarrierThreadResolverFunc func(ctx context.Context, chatID, messageID string) (string, error)

func (f CardCarrierThreadResolverFunc) ResolveCarrierThreadID(ctx context.Context, chatID, messageID string) (string, error) {
	return f(ctx, chatID, messageID)
}

type CardActiveRunResolver interface {
	ActiveRun(ctx context.Context, scope string) (CardActiveRun, bool, error)
}

type CardActiveRunResolverFunc func(ctx context.Context, scope string) (CardActiveRun, bool, error)

func (f CardActiveRunResolverFunc) ActiveRun(ctx context.Context, scope string) (CardActiveRun, bool, error) {
	return f(ctx, scope)
}

type CardCallbackVerifier interface {
	VerifyCallback(ctx context.Context, token string, expected CardCallbackVerifyExpected) CardCallbackVerifyResult
}

type CardCallbackVerifierFunc func(ctx context.Context, token string, expected CardCallbackVerifyExpected) CardCallbackVerifyResult

func (f CardCallbackVerifierFunc) VerifyCallback(ctx context.Context, token string, expected CardCallbackVerifyExpected) CardCallbackVerifyResult {
	return f(ctx, token, expected)
}

type CardActionDispatcherOptions struct {
	Verifier                  CardCallbackVerifier
	CommandHandler            CardCommandHandler
	Enqueuer                  CardPromptEnqueuer
	ActiveRuns                CardActiveRunResolver
	CarrierThreads            CardCarrierThreadResolver
	PolicyFingerprint         string
	PolicyFingerprintForScope func(scope string) string
	Now                       func() time.Time
}

type CardActionDispatcher struct {
	inner appdispatch.Dispatcher
}

func NewCardActionDispatcher(options CardActionDispatcherOptions) *CardActionDispatcher {
	return &CardActionDispatcher{inner: appdispatch.Dispatcher{
		Verifier:                  wrapInternalCardCallbackVerifier(options.Verifier),
		CommandHandler:            wrapInternalCardCommandHandler(options.CommandHandler),
		Enqueuer:                  wrapInternalCardPromptEnqueuer(options.Enqueuer),
		ActiveRuns:                wrapInternalCardActiveRuns(options.ActiveRuns),
		CarrierThreads:            wrapInternalCardCarrierThreads(options.CarrierThreads),
		PolicyFingerprint:         options.PolicyFingerprint,
		PolicyFingerprintForScope: options.PolicyFingerprintForScope,
		Now:                       options.Now,
	}}
}

func (d *CardActionDispatcher) Dispatch(ctx context.Context, input CardActionDispatchInput) (CardActionDispatchResult, error) {
	if d == nil {
		return CardActionDispatchResult{}, errors.New("card action dispatcher is nil")
	}
	result, err := d.inner.Dispatch(ctx, toInternalLarkCardActionInput(input))
	return fromInternalCardDispatchResult(result), err
}

type CardActionOptions struct {
	CommandOptions            CommandOptions
	ProfileConfig             *profile.Config
	CommandHandler            CardCommandHandler
	CallbackAuth              *CallbackAuth
	Verifier                  CardCallbackVerifier
	Enqueuer                  CardPromptEnqueuer
	ActiveRuns                CardActiveRunResolver
	CarrierThreads            CardCarrierThreadResolver
	PolicyFingerprint         string
	PolicyFingerprintForScope func(scope string) string
	Now                       func() time.Time
}

func (c *Client) HandleCardAction(ctx context.Context, input CardActionDispatchInput, options CardActionOptions) (CardActionDispatchResult, error) {
	if c == nil {
		return CardActionDispatchResult{}, ErrNilClient
	}
	handler := options.CommandHandler
	if handler == nil {
		handler = clientCardCommandHandler{client: c, options: options.CommandOptions, profileConfig: options.ProfileConfig}
	}
	verifier := options.Verifier
	if verifier == nil && options.CallbackAuth != nil {
		verifier = callbackAuthVerifier{auth: options.CallbackAuth}
	}
	dispatcher := NewCardActionDispatcher(CardActionDispatcherOptions{
		Verifier:                  verifier,
		CommandHandler:            handler,
		Enqueuer:                  options.Enqueuer,
		ActiveRuns:                options.ActiveRuns,
		CarrierThreads:            options.CarrierThreads,
		PolicyFingerprint:         options.PolicyFingerprint,
		PolicyFingerprintForScope: options.PolicyFingerprintForScope,
		Now:                       options.Now,
	})
	return dispatcher.Dispatch(ctx, input)
}

type clientCardCommandHandler struct {
	client        *Client
	options       CommandOptions
	profileConfig *profile.Config
}

func (h clientCardCommandHandler) HandleCardCommand(ctx context.Context, req CardCommandRequest) (any, error) {
	request := CommandRequest{
		Command:   req.Command,
		Args:      req.Args,
		ScopeID:   req.ScopeID,
		ChatID:    req.ChatID,
		ThreadID:  req.ThreadID,
		ActorID:   req.ActorID,
		SenderID:  req.SenderID,
		ChatMode:  CommandChatMode(req.ChatMode),
		Access:    h.accessDecision(req),
		FormValue: req.FormValue,
		FromCard:  req.FromCard,
		MessageID: req.MessageID,
		EventID:   req.EventID,
	}
	if h.profileConfig != nil {
		return h.client.HandleCommandWithProfile(ctx, request, h.options, *h.profileConfig)
	}
	return h.client.HandleCommand(ctx, request, h.options)
}

func (h clientCardCommandHandler) accessDecision(req CardCommandRequest) AccessDecision {
	controls := toInternalRuntimeControls(h.options.RuntimeControls)
	profileConfig := h.client.profile
	if h.profileConfig != nil {
		profileConfig = *h.profileConfig
	}
	if req.ChatMode == LarkChatModeP2P {
		return fromInternalAccessDecision(access.CanUseDM(profileConfig, controls, req.SenderID))
	}
	return fromInternalAccessDecision(access.CanUseGroup(profileConfig, controls, req.ChatID, req.SenderID))
}

type callbackAuthVerifier struct {
	auth *CallbackAuth
}

func (v callbackAuthVerifier) VerifyCallback(_ context.Context, token string, expected CardCallbackVerifyExpected) CardCallbackVerifyResult {
	result := v.auth.Verify(token, CallbackVerifyExpected{
		RunID:             expected.RunID,
		Scope:             expected.Scope,
		ChatID:            expected.ChatID,
		OperatorOpenID:    expected.OperatorOpenID,
		Action:            expected.Action,
		PolicyFingerprint: expected.PolicyFingerprint,
	})
	return CardCallbackVerifyResult{
		OK:     result.OK,
		Reason: string(result.Reason),
	}
}

func NewCardPromptQueueEnqueuer(queue *LarkIntakeQueue) CardPromptEnqueuer {
	return CardPromptEnqueuerFunc(func(_ context.Context, event LarkNormalizedEvent) error {
		if queue == nil {
			return nil
		}
		_, err := queue.Push(event)
		return err
	})
}

func wrapInternalCardCommandHandler(handler CardCommandHandler) appdispatch.CommandHandler {
	if handler == nil {
		return nil
	}
	return appdispatch.CommandHandlerFunc(func(ctx context.Context, req appdispatch.CommandRequest) (any, error) {
		return handler.HandleCardCommand(ctx, fromInternalCardCommandRequest(req))
	})
}

func wrapInternalCardPromptEnqueuer(enqueuer CardPromptEnqueuer) appdispatch.PromptEnqueuer {
	if enqueuer == nil {
		return nil
	}
	return appdispatch.PromptEnqueuerFunc(func(ctx context.Context, event appintake.NormalizedEvent) error {
		return enqueuer.EnqueueCardPrompt(ctx, fromInternalLarkNormalizedEvent(event))
	})
}

func wrapInternalCardCarrierThreads(resolver CardCarrierThreadResolver) appdispatch.CarrierThreadResolver {
	if resolver == nil {
		return nil
	}
	return appdispatch.CarrierThreadResolverFunc(func(ctx context.Context, chatID, messageID string) (string, error) {
		return resolver.ResolveCarrierThreadID(ctx, chatID, messageID)
	})
}

func wrapInternalCardActiveRuns(resolver CardActiveRunResolver) appdispatch.ActiveRunResolver {
	if resolver == nil {
		return nil
	}
	return appdispatch.ActiveRunResolverFunc(func(ctx context.Context, scope string) (appdispatch.ActiveRun, bool, error) {
		run, ok, err := resolver.ActiveRun(ctx, scope)
		return appdispatch.ActiveRun(run), ok, err
	})
}

func wrapInternalCardCallbackVerifier(verifier CardCallbackVerifier) appdispatch.CallbackVerifier {
	if verifier == nil {
		return nil
	}
	return appdispatch.CallbackVerifierFunc(func(ctx context.Context, token string, expected appdispatch.CallbackVerifyExpected) appdispatch.CallbackVerifyResult {
		result := verifier.VerifyCallback(ctx, token, CardCallbackVerifyExpected(expected))
		return appdispatch.CallbackVerifyResult(result)
	})
}

func toInternalCardDispatchResult(result CardActionDispatchResult) appdispatch.Result {
	var enqueued *appintake.NormalizedEvent
	if result.Enqueued != nil {
		converted := toInternalLarkNormalizedEvent(*result.Enqueued)
		enqueued = &converted
	}
	return appdispatch.Result{
		Outcome:         appdispatch.Outcome(result.Outcome),
		Scope:           toInternalLarkScope(result.Scope),
		Command:         result.Command,
		Args:            result.Args,
		CommandResponse: result.CommandResponse,
		Enqueued:        enqueued,
		Verified:        result.Verified,
		RejectReason:    result.RejectReason,
	}
}

func fromInternalCardDispatchResult(result appdispatch.Result) CardActionDispatchResult {
	var enqueued *LarkNormalizedEvent
	if result.Enqueued != nil {
		converted := fromInternalLarkNormalizedEvent(*result.Enqueued)
		enqueued = &converted
	}
	return CardActionDispatchResult{
		Outcome:         CardDispatchOutcome(result.Outcome),
		Scope:           fromInternalLarkScope(result.Scope),
		Command:         result.Command,
		Args:            result.Args,
		CommandResponse: result.CommandResponse,
		Enqueued:        enqueued,
		Verified:        result.Verified,
		RejectReason:    result.RejectReason,
	}
}

func fromInternalCardDispatchResultPtr(result *appdispatch.Result) *CardActionDispatchResult {
	if result == nil {
		return nil
	}
	converted := fromInternalCardDispatchResult(*result)
	return &converted
}

func fromInternalCardCommandRequest(req appdispatch.CommandRequest) CardCommandRequest {
	return CardCommandRequest{
		Command:    req.Command,
		Args:       req.Args,
		ScopeID:    req.ScopeID,
		ChatID:     req.ChatID,
		ThreadID:   req.ThreadID,
		ActorID:    req.ActorID,
		SenderID:   req.SenderID,
		ChatMode:   LarkChatMode(req.ChatMode),
		Value:      req.Value,
		FormValue:  req.FormValue,
		FromCard:   req.FromCard,
		MessageID:  req.MessageID,
		EventID:    req.EventID,
		OccurredAt: req.OccurredAt,
	}
}
