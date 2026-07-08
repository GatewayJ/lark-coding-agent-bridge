package carddispatch

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	appintake "github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/intake"
)

const (
	BridgeCallbackMarker       = "__bridge_cb"
	LegacyClaudeCallbackMarker = "__claude_cb"
	BridgeTokenKey             = "bridge_token"
	FormValueKey               = "form_value"
)

var (
	ErrCallbackAuthMissing        = errors.New("callback auth is required")
	ErrCallbackDenied             = errors.New("callback token denied")
	ErrActiveRunLookupRequired    = errors.New("active run lookup is required")
	ErrActiveRunMissing           = errors.New("active run is missing")
	ErrCarrierThreadLookupNeeded  = errors.New("carrier thread lookup is required")
	ErrCarrierThreadLookupFailed  = errors.New("carrier thread lookup failed")
	ErrCarrierThreadIDUnavailable = errors.New("carrier thread id is unavailable")
)

type Outcome string

const (
	OutcomeIgnored     Outcome = "ignored"
	OutcomeCommand     Outcome = "command"
	OutcomeEnqueued    Outcome = "enqueued"
	OutcomeNeedsLookup Outcome = "needs_lookup"
	OutcomeRejected    Outcome = "rejected"
)

type Dispatcher struct {
	Verifier                  CallbackVerifier
	CommandHandler            CommandHandler
	Enqueuer                  PromptEnqueuer
	ActiveRuns                ActiveRunResolver
	CarrierThreads            CarrierThreadResolver
	PolicyFingerprint         string
	PolicyFingerprintForScope func(scope string) string
	Now                       func() time.Time
}

type CommandHandler interface {
	HandleCardCommand(ctx context.Context, req CommandRequest) (any, error)
}

type CommandHandlerFunc func(ctx context.Context, req CommandRequest) (any, error)

func (f CommandHandlerFunc) HandleCardCommand(ctx context.Context, req CommandRequest) (any, error) {
	return f(ctx, req)
}

type PromptEnqueuer interface {
	EnqueueCardPrompt(ctx context.Context, event appintake.NormalizedEvent) error
}

type PromptEnqueuerFunc func(ctx context.Context, event appintake.NormalizedEvent) error

func (f PromptEnqueuerFunc) EnqueueCardPrompt(ctx context.Context, event appintake.NormalizedEvent) error {
	return f(ctx, event)
}

type CarrierThreadResolver interface {
	ResolveCarrierThreadID(ctx context.Context, chatID, messageID string) (string, error)
}

type CarrierThreadResolverFunc func(ctx context.Context, chatID, messageID string) (string, error)

func (f CarrierThreadResolverFunc) ResolveCarrierThreadID(ctx context.Context, chatID, messageID string) (string, error) {
	return f(ctx, chatID, messageID)
}

type ActiveRunResolver interface {
	ActiveRun(ctx context.Context, scope string) (ActiveRun, bool, error)
}

type ActiveRunResolverFunc func(ctx context.Context, scope string) (ActiveRun, bool, error)

func (f ActiveRunResolverFunc) ActiveRun(ctx context.Context, scope string) (ActiveRun, bool, error) {
	return f(ctx, scope)
}

type CallbackVerifier interface {
	VerifyCallback(ctx context.Context, token string, expected CallbackVerifyExpected) CallbackVerifyResult
}

type CallbackVerifierFunc func(ctx context.Context, token string, expected CallbackVerifyExpected) CallbackVerifyResult

func (f CallbackVerifierFunc) VerifyCallback(ctx context.Context, token string, expected CallbackVerifyExpected) CallbackVerifyResult {
	return f(ctx, token, expected)
}

type ActiveRun struct {
	RunID             string
	PolicyFingerprint string
}

type CallbackVerifyExpected struct {
	RunID             string
	Scope             string
	ChatID            string
	OperatorOpenID    string
	Action            string
	PolicyFingerprint string
}

type CallbackVerifyResult struct {
	OK     bool
	Reason string
}

type CommandRequest struct {
	Command    string
	Args       string
	ScopeID    string
	ChatID     string
	ThreadID   string
	ActorID    string
	SenderID   string
	ChatMode   appintake.ChatMode
	Value      map[string]any
	FormValue  map[string]any
	FromCard   bool
	MessageID  string
	EventID    string
	OccurredAt time.Time
}

type Result struct {
	Outcome         Outcome
	Scope           appintake.Scope
	Command         string
	Args            string
	CommandResponse any
	Enqueued        *appintake.NormalizedEvent
	Verified        bool
	RejectReason    string
}

func (d Dispatcher) Dispatch(ctx context.Context, input appintake.CardActionInput) (Result, error) {
	payload := cloneMap(input.ActionValue)
	if len(payload) == 0 {
		return Result{Outcome: OutcomeIgnored}, nil
	}
	if _, legacy := payload[LegacyClaudeCallbackMarker]; legacy {
		return Result{Outcome: OutcomeIgnored}, nil
	}

	scope, err := d.resolveScope(ctx, input)
	if err != nil {
		outcome := OutcomeRejected
		if errors.Is(err, ErrCarrierThreadLookupNeeded) ||
			errors.Is(err, ErrCarrierThreadLookupFailed) ||
			errors.Is(err, ErrCarrierThreadIDUnavailable) {
			outcome = OutcomeNeedsLookup
		}
		return Result{Outcome: outcome, Scope: scope}, err
	}

	cmd := stringField(payload, "cmd")
	signed := isSignedCallback(payload)
	if cmd != "" {
		verified := false
		if signed {
			if err := d.verify(ctx, payload, scope.Key, input, cmd); err != nil {
				return Result{Outcome: OutcomeRejected, Scope: scope, Command: cmd, RejectReason: err.Error()}, err
			}
			verified = true
		}
		if d.CommandHandler == nil {
			return Result{Outcome: OutcomeCommand, Scope: scope, Command: cmd, Verified: verified}, nil
		}
		name, args := commandParts(cmd, payload)
		response, err := d.CommandHandler.HandleCardCommand(ctx, CommandRequest{
			Command:    name,
			Args:       args,
			ScopeID:    scope.Key,
			ChatID:     input.ChatID,
			ThreadID:   scope.ThreadID,
			ActorID:    input.Operator.OpenID,
			SenderID:   input.Operator.OpenID,
			ChatMode:   scope.ChatMode,
			Value:      commandValue(payload),
			FormValue:  cloneMap(input.FormValue),
			FromCard:   true,
			MessageID:  input.MessageID,
			EventID:    input.EventID,
			OccurredAt: eventTime(d.Now, input.CreateTime),
		})
		if err != nil {
			return Result{Outcome: OutcomeCommand, Scope: scope, Command: name, Args: args, Verified: verified}, err
		}
		return Result{
			Outcome:         OutcomeCommand,
			Scope:           scope,
			Command:         name,
			Args:            args,
			CommandResponse: response,
			Verified:        verified,
		}, nil
	}

	if !signed {
		return Result{Outcome: OutcomeIgnored, Scope: scope}, nil
	}
	if err := d.verify(ctx, payload, scope.Key, input, "agent_callback"); err != nil {
		return Result{Outcome: OutcomeRejected, Scope: scope, RejectReason: err.Error()}, err
	}
	event, err := d.syntheticEvent(input, scope, payload)
	if err != nil {
		return Result{Outcome: OutcomeRejected, Scope: scope, Verified: true}, err
	}
	if d.Enqueuer != nil {
		if err := d.Enqueuer.EnqueueCardPrompt(ctx, event); err != nil {
			return Result{Outcome: OutcomeEnqueued, Scope: scope, Enqueued: &event, Verified: true}, err
		}
	}
	return Result{Outcome: OutcomeEnqueued, Scope: scope, Enqueued: &event, Verified: true}, nil
}

func (d Dispatcher) resolveScope(ctx context.Context, input appintake.CardActionInput) (appintake.Scope, error) {
	chatType := input.ChatType
	if chatType == "" {
		chatType = appintake.ChatTypeGroup
	}
	mode := input.ResolvedMode
	if mode == "" {
		if chatType == appintake.ChatTypeP2P {
			mode = appintake.ChatModeP2P
		} else {
			mode = appintake.ChatModeGroup
		}
	}
	threadID := input.ThreadID
	if threadID != "" {
		mode = appintake.ChatModeTopic
	}
	if mode == appintake.ChatModeTopic && threadID == "" && d.CarrierThreads != nil {
		resolved, err := d.CarrierThreads.ResolveCarrierThreadID(ctx, input.ChatID, input.MessageID)
		if err == nil && resolved != "" {
			threadID = resolved
		}
	}
	key := input.ExplicitScope
	parent := ""
	if key == "" {
		key = input.InheritScope
		parent = input.InheritScope
	}
	if key == "" {
		key = input.ChatID
		if threadID != "" {
			key += ":" + threadID
		}
	}
	return appintake.Scope{
		Key:       key,
		Source:    appintake.ScopeSourceCard,
		ChatID:    input.ChatID,
		ChatType:  chatType,
		ChatMode:  mode,
		ThreadID:  threadID,
		ActorID:   input.Operator.OpenID,
		ParentKey: parent,
	}, nil
}

func (d Dispatcher) verify(ctx context.Context, payload map[string]any, scope string, input appintake.CardActionInput, action string) error {
	token := stringField(payload, BridgeTokenKey)
	if d.Verifier == nil || token == "" {
		return ErrCallbackAuthMissing
	}
	if d.ActiveRuns == nil {
		return ErrActiveRunLookupRequired
	}
	active, ok, err := d.ActiveRuns.ActiveRun(ctx, scope)
	if err != nil {
		return err
	}
	if !ok || active.RunID == "" {
		return ErrActiveRunMissing
	}
	fp := active.PolicyFingerprint
	if d.PolicyFingerprintForScope != nil {
		if scoped := d.PolicyFingerprintForScope(scope); scoped != "" {
			fp = scoped
		}
	}
	if fp == "" {
		fp = d.PolicyFingerprint
	}
	result := d.Verifier.VerifyCallback(ctx, token, CallbackVerifyExpected{
		RunID:             active.RunID,
		Scope:             scope,
		ChatID:            input.ChatID,
		OperatorOpenID:    input.Operator.OpenID,
		Action:            action,
		PolicyFingerprint: fp,
	})
	if !result.OK {
		if result.Reason != "" {
			return errors.Join(ErrCallbackDenied, errors.New(result.Reason))
		}
		return ErrCallbackDenied
	}
	return nil
}

func (d Dispatcher) syntheticEvent(input appintake.CardActionInput, scope appintake.Scope, payload map[string]any) (appintake.NormalizedEvent, error) {
	agentPayload := cloneMap(payload)
	delete(agentPayload, BridgeCallbackMarker)
	delete(agentPayload, LegacyClaudeCallbackMarker)
	delete(agentPayload, BridgeTokenKey)
	if input.FormValue != nil {
		agentPayload[FormValueKey] = cloneMap(input.FormValue)
	}
	raw, err := json.Marshal(agentPayload)
	if err != nil {
		return appintake.NormalizedEvent{}, err
	}
	chatType := appintake.ChatTypeGroup
	if scope.ChatMode == appintake.ChatModeP2P {
		chatType = appintake.ChatTypeP2P
	}
	msg := appintake.MessageInput{
		MessageID:      input.MessageID,
		ChatID:         input.ChatID,
		ChatType:       chatType,
		ResolvedMode:   scope.ChatMode,
		ThreadID:       scope.ThreadID,
		Sender:         input.Operator,
		Content:        "[card-click] " + string(raw),
		RawContentType: "card_action",
		CreateTime:     eventTime(d.Now, input.CreateTime),
	}
	event := appintake.NormalizedEvent{
		Kind:    appintake.EventMessage,
		Scope:   appintake.MessageScope(msg),
		Message: &msg,
	}
	event.Scope.Key = scope.Key
	event.Scope.Source = appintake.ScopeSourceCard
	event.Scope.ParentKey = scope.ParentKey
	return event, nil
}

func isSignedCallback(payload map[string]any) bool {
	if _, ok := payload[BridgeCallbackMarker]; ok {
		return true
	}
	return stringField(payload, BridgeTokenKey) != ""
}

func commandParts(cmd string, payload map[string]any) (string, string) {
	parts := strings.Split(cmd, ".")
	name := parts[0]
	sub := strings.Join(parts[1:], " ")
	if sub == "" {
		return name, ""
	}
	arg := stringField(payload, "arg")
	if arg == "" {
		arg = stringField(payload, "name")
	}
	if arg != "" {
		return name, sub + " " + arg
	}
	return name, sub
}

func commandValue(payload map[string]any) map[string]any {
	out := cloneMap(payload)
	delete(out, BridgeCallbackMarker)
	delete(out, LegacyClaudeCallbackMarker)
	delete(out, BridgeTokenKey)
	return out
}

func stringField(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	value, ok := payload[key].(string)
	if !ok {
		return ""
	}
	return value
}

func cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func eventTime(now func() time.Time, input time.Time) time.Time {
	if !input.IsZero() {
		return input
	}
	if now != nil {
		return now()
	}
	return time.Now()
}
