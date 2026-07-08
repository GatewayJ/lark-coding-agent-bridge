package lark

import (
	"context"
	"errors"
	"testing"
	"time"

	appdispatch "github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/carddispatch"
	appintake "github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/intake"
)

func TestAdapterNormalizesMessageIntoIntake(t *testing.T) {
	ctx := context.Background()
	transport := NewFakeTransport(BotIdentity{OpenID: "ou_bot", Name: "bot"})
	sink := &captureIntakeSink{}
	adapter, err := NewAdapter(AdapterOptions{
		Transport:      transport,
		Intake:         sink,
		SelfLoopPolicy: appintake.DefaultSelfLoopPolicy(""),
	})
	if err != nil {
		t.Fatalf("NewAdapter returned error: %v", err)
	}
	if err := adapter.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	err = transport.Emit(ctx, IncomingEvent{
		Kind: appintake.EventMessage,
		Message: &appintake.MessageInput{
			MessageID:    "om_1",
			ChatID:       "oc_group",
			ChatType:     appintake.ChatTypeGroup,
			ThreadID:     "th_topic",
			Sender:       appintake.Actor{OpenID: "ou_user", Name: "user"},
			Content:      "hello",
			MentionedBot: true,
			CreateTime:   time.UnixMilli(1000),
		},
	})
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	if len(sink.events) != 1 {
		t.Fatalf("events = %d, want 1", len(sink.events))
	}
	got := sink.events[0]
	if got.Kind != appintake.EventMessage || got.Scope.Key != "oc_group:th_topic" || got.Scope.ChatMode != appintake.ChatModeTopic {
		t.Fatalf("normalized event = %#v", got)
	}
}

func TestAdapterDropsSelfLoopUsingConnectedBotIdentity(t *testing.T) {
	ctx := context.Background()
	transport := NewFakeTransport(BotIdentity{OpenID: "ou_bot"})
	sink := &captureIntakeSink{}
	adapter, err := NewAdapter(AdapterOptions{
		Transport:      transport,
		Intake:         sink,
		SelfLoopPolicy: appintake.DefaultSelfLoopPolicy(""),
	})
	if err != nil {
		t.Fatalf("NewAdapter returned error: %v", err)
	}
	if err := adapter.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	result, err := adapter.HandleTransportEvent(ctx, IncomingEvent{
		Kind: appintake.EventMessage,
		Message: &appintake.MessageInput{
			MessageID: "om_self",
			ChatID:    "oc_group",
			ChatType:  appintake.ChatTypeGroup,
			Sender:    appintake.Actor{OpenID: "ou_bot"},
			Content:   "bridge reply",
		},
	})
	if err != nil {
		t.Fatalf("HandleTransportEvent returned error: %v", err)
	}
	if !result.DroppedSelfLoop || result.Normalized.Self.Reason != appintake.SelfLoopBotOpenID {
		t.Fatalf("self-loop result = %#v", result)
	}
	if len(sink.events) != 0 {
		t.Fatalf("self-loop event reached intake: %#v", sink.events)
	}
}

func TestAdapterRoutesBridgeCallbackThroughCardDispatch(t *testing.T) {
	ctx := context.Background()
	transport := NewFakeTransport(BotIdentity{OpenID: "ou_bot"})
	sink := &captureIntakeSink{}
	var verified appdispatch.CallbackVerifyExpected
	dispatcher := appdispatch.Dispatcher{
		Verifier: appdispatch.CallbackVerifierFunc(func(_ context.Context, token string, expected appdispatch.CallbackVerifyExpected) appdispatch.CallbackVerifyResult {
			verified = expected
			return appdispatch.CallbackVerifyResult{OK: token == "token-1"}
		}),
		ActiveRuns: appdispatch.ActiveRunResolverFunc(func(_ context.Context, scope string) (appdispatch.ActiveRun, bool, error) {
			if scope != "oc_group" {
				t.Fatalf("active run scope = %q", scope)
			}
			return appdispatch.ActiveRun{RunID: "run-1", PolicyFingerprint: "fp-1"}, true, nil
		}),
	}
	adapter, err := NewAdapter(AdapterOptions{
		Transport:                  transport,
		Intake:                     sink,
		CardActions:                dispatcher,
		ForwardCardPromptsToIntake: true,
	})
	if err != nil {
		t.Fatalf("NewAdapter returned error: %v", err)
	}

	result, err := adapter.HandleTransportEvent(ctx, IncomingEvent{
		Kind: appintake.EventCardAction,
		CardAction: &appintake.CardActionInput{
			EventID:     "evt_1",
			MessageID:   "om_card",
			ChatID:      "oc_group",
			ChatType:    appintake.ChatTypeGroup,
			Operator:    appintake.Actor{OpenID: "ou_user", Name: "user"},
			ActionValue: map[string]any{appdispatch.BridgeCallbackMarker: true, appdispatch.BridgeTokenKey: "token-1", "choice": "a"},
			FormValue:   map[string]any{"note": "from form"},
			CreateTime:  time.UnixMilli(2000),
		},
	})
	if err != nil {
		t.Fatalf("HandleTransportEvent returned error: %v", err)
	}
	if result.CardDispatch == nil || result.CardDispatch.Outcome != appdispatch.OutcomeEnqueued || !result.CardDispatch.Verified {
		t.Fatalf("card dispatch result = %#v", result.CardDispatch)
	}
	if verified.RunID != "run-1" || verified.Action != "agent_callback" || verified.OperatorOpenID != "ou_user" {
		t.Fatalf("verified callback = %#v", verified)
	}
	if len(sink.events) != 1 {
		t.Fatalf("events = %d, want synthetic prompt", len(sink.events))
	}
	got := sink.events[0]
	if got.Kind != appintake.EventMessage || got.Scope.Source != appintake.ScopeSourceCard {
		t.Fatalf("synthetic event = %#v", got)
	}
	if got.Message == nil || got.Message.RawContentType != "card_action" || got.Message.Content != `[card-click] {"choice":"a","form_value":{"note":"from form"}}` {
		t.Fatalf("synthetic message = %#v", got.Message)
	}
}

func TestAdapterOutboundPortsUseTransport(t *testing.T) {
	ctx := context.Background()
	transport := NewFakeTransport(BotIdentity{})
	adapter, err := NewAdapter(AdapterOptions{Transport: transport})
	if err != nil {
		t.Fatalf("NewAdapter returned error: %v", err)
	}

	msgResult, err := adapter.SendMessage(ctx, SendMessageRequest{
		ChatID:  "oc_group",
		Content: MessageContent{Text: "hello"},
		Options: SendOptions{ReplyTo: "om_parent"},
	})
	if err != nil {
		t.Fatalf("SendMessage returned error: %v", err)
	}
	cardResult, err := adapter.SendCard(ctx, SendCardRequest{
		ChatID: "oc_group",
		Card:   map[string]any{"schema": "2.0"},
	})
	if err != nil {
		t.Fatalf("SendCard returned error: %v", err)
	}
	if err := adapter.UpdateCard(ctx, UpdateCardRequest{MessageID: cardResult.MessageID, Card: map[string]any{"updated": true}}); err != nil {
		t.Fatalf("UpdateCard returned error: %v", err)
	}
	if msgResult.MessageID == "" || cardResult.MessageID == "" {
		t.Fatalf("message ids = %q %q", msgResult.MessageID, cardResult.MessageID)
	}
	if len(transport.SentMessages) != 1 || transport.SentMessages[0].Options.ReplyTo != "om_parent" {
		t.Fatalf("sent messages = %#v", transport.SentMessages)
	}
	if len(transport.SentCards) != 1 || len(transport.UpdatedCards) != 1 {
		t.Fatalf("sent cards = %#v updates=%#v", transport.SentCards, transport.UpdatedCards)
	}
}

func TestAdapterStartDisconnectsTransportWhenIntakeStartFails(t *testing.T) {
	ctx := context.Background()
	transport := NewFakeTransport(BotIdentity{OpenID: "ou_bot", Name: "bot"})
	wantErr := errors.New("intake start failed")
	adapter, err := NewAdapter(AdapterOptions{
		Transport: transport,
		Intake:    failingStartIntake{err: wantErr},
	})
	if err != nil {
		t.Fatalf("NewAdapter returned error: %v", err)
	}

	if err := adapter.Start(ctx); !errors.Is(err, wantErr) {
		t.Fatalf("Start error = %v, want %v", err, wantErr)
	}
	if adapter.Started() {
		t.Fatal("adapter started after failing intake")
	}
	if err := transport.Emit(ctx, IncomingEvent{Kind: appintake.EventKeepalive, Keepalive: &appintake.KeepaliveInput{}}); !errors.Is(err, ErrFakeTransportNotConnected) {
		t.Fatalf("Emit after failed Start error = %v, want ErrFakeTransportNotConnected", err)
	}
}

type captureIntakeSink struct {
	events []appintake.NormalizedEvent
}

func (s *captureIntakeSink) HandleLarkEvent(_ context.Context, event appintake.NormalizedEvent) error {
	s.events = append(s.events, event)
	return nil
}

type failingStartIntake struct {
	err error
}

func (s failingStartIntake) Start(context.Context) error {
	return s.err
}

func (s failingStartIntake) HandleLarkEvent(context.Context, appintake.NormalizedEvent) error {
	return nil
}
