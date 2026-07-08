package bridge

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var cardDispatchNow = time.UnixMilli(1000)

func TestCardActionDispatcherSignedStopCommand(t *testing.T) {
	auth := newTestCallbackAuth(t, "nonce-stop")
	token := signTestCardToken(t, auth, "stop", "oc_group", "ou_operator")
	handler := &captureCardCommandHandler{response: CommandResponse{Handled: true, Command: "/stop", Kind: CommandResponseStop}}
	dispatcher := NewCardActionDispatcher(CardActionDispatcherOptions{
		Verifier:       testVerifier{auth: auth},
		CommandHandler: handler,
		ActiveRuns:     activeRunsMap{"oc_group": {RunID: "run-active", PolicyFingerprint: "fp-1"}},
	})

	result, err := dispatcher.Dispatch(context.Background(), testCardAction(map[string]any{
		"cmd":              "stop",
		"__bridge_cb":      true,
		"bridge_token":     token,
		"ignored_for_args": "value",
	}, nil))
	if err != nil {
		t.Fatalf("Dispatch returned error: %v", err)
	}
	if result.Outcome != CardDispatchCommand || !result.Verified {
		t.Fatalf("result = %#v", result)
	}
	if handler.calls != 1 || handler.last.Command != "stop" || handler.last.ScopeID != "oc_group" {
		t.Fatalf("command request = %#v calls=%d", handler.last, handler.calls)
	}
	if _, ok := handler.last.Value["bridge_token"]; ok {
		t.Fatalf("command request leaked bridge_token: %#v", handler.last.Value)
	}
	if _, ok := handler.last.Value["__bridge_cb"]; ok {
		t.Fatalf("command request leaked __bridge_cb: %#v", handler.last.Value)
	}
	if handler.last.Value["ignored_for_args"] != "value" {
		t.Fatalf("command request lost payload fields: %#v", handler.last.Value)
	}
}

func TestCardActionDispatcherInvalidTokenRejectsCommand(t *testing.T) {
	auth := newTestCallbackAuth(t, "nonce-invalid")
	token := signTestCardToken(t, auth, "stop", "oc_group", "ou_other")
	handler := &captureCardCommandHandler{}
	dispatcher := NewCardActionDispatcher(CardActionDispatcherOptions{
		Verifier:       testVerifier{auth: auth},
		CommandHandler: handler,
		ActiveRuns:     activeRunsMap{"oc_group": {RunID: "run-active", PolicyFingerprint: "fp-1"}},
	})

	result, err := dispatcher.Dispatch(context.Background(), testCardAction(map[string]any{
		"cmd":          "stop",
		"__bridge_cb":  true,
		"bridge_token": token,
	}, nil))
	if !errors.Is(err, ErrCardCallbackDenied) {
		t.Fatalf("Dispatch error = %v, want callback denied", err)
	}
	if result.Outcome != CardDispatchRejected || handler.calls != 0 {
		t.Fatalf("result = %#v calls=%d", result, handler.calls)
	}
}

func TestCardActionDispatcherMergesFormValueAndDoesNotLeakToken(t *testing.T) {
	auth := newTestCallbackAuth(t, "nonce-agent")
	token := signTestCardToken(t, auth, "agent_callback", "oc_group", "ou_operator")
	queue := &captureCardQueue{}
	dispatcher := NewCardActionDispatcher(CardActionDispatcherOptions{
		Verifier:   testVerifier{auth: auth},
		Enqueuer:   queue,
		ActiveRuns: activeRunsMap{"oc_group": {RunID: "run-active", PolicyFingerprint: "fp-1"}},
	})

	result, err := dispatcher.Dispatch(context.Background(), testCardAction(map[string]any{
		"__bridge_cb":  true,
		"bridge_token": token,
		"choice":       "a",
	}, map[string]any{"note": "from form"}))
	if err != nil {
		t.Fatalf("Dispatch returned error: %v", err)
	}
	if result.Outcome != CardDispatchEnqueued || len(queue.events) != 1 {
		t.Fatalf("result = %#v queued=%d", result, len(queue.events))
	}
	content := queue.events[0].Message.Content
	if content != `[card-click] {"choice":"a","form_value":{"note":"from form"}}` {
		t.Fatalf("content = %q", content)
	}
	if strings.Contains(content, "bridge_token") {
		t.Fatalf("callback payload leaked bridge_token: %q", content)
	}
}

func TestCardActionDispatcherLegacyMarkerIsIgnoredLikeTypeScript(t *testing.T) {
	handler := &captureCardCommandHandler{}
	dispatcher := NewCardActionDispatcher(CardActionDispatcherOptions{
		Verifier:       testVerifier{auth: newTestCallbackAuth(t, "nonce-unused")},
		CommandHandler: handler,
		ActiveRuns:     activeRunsMap{"oc_group": {RunID: "run-active", PolicyFingerprint: "fp-1"}},
	})

	result, err := dispatcher.Dispatch(context.Background(), testCardAction(map[string]any{
		"__claude_cb": true,
		"cmd":         "stop",
	}, nil))
	if err != nil {
		t.Fatalf("Dispatch error = %v, want nil", err)
	}
	if result.Outcome != CardDispatchIgnored || handler.calls != 0 {
		t.Fatalf("result = %#v calls=%d", result, handler.calls)
	}
}

func TestCardActionDispatcherLegacyMarkerWinsOverBridgeCallback(t *testing.T) {
	auth := newTestCallbackAuth(t, "nonce-legacy")
	token := signTestCardToken(t, auth, "agent_callback", "oc_group", "ou_operator")
	queue := &captureCardQueue{}
	dispatcher := NewCardActionDispatcher(CardActionDispatcherOptions{
		Verifier:   testVerifier{auth: auth},
		Enqueuer:   queue,
		ActiveRuns: activeRunsMap{"oc_group": {RunID: "run-active", PolicyFingerprint: "fp-1"}},
	})

	_, err := dispatcher.Dispatch(context.Background(), testCardAction(map[string]any{
		"__claude_cb":  true,
		"__bridge_cb":  true,
		"bridge_token": token,
		"choice":       "legacy",
	}, nil))
	if err != nil {
		t.Fatalf("Dispatch returned error: %v", err)
	}
	if len(queue.events) != 0 {
		t.Fatalf("queued events = %#v", queue.events)
	}
}

func TestCardActionDispatcherTopicCarrierThreadScope(t *testing.T) {
	auth := newTestCallbackAuth(t, "nonce-topic")
	token := signTestCardToken(t, auth, "agent_callback", "oc_group:th_topic", "ou_operator")
	queue := &captureCardQueue{}
	dispatcher := NewCardActionDispatcher(CardActionDispatcherOptions{
		Verifier:   testVerifier{auth: auth},
		Enqueuer:   queue,
		ActiveRuns: activeRunsMap{"oc_group:th_topic": {RunID: "run-active", PolicyFingerprint: "fp-1"}},
		CarrierThreads: CardCarrierThreadResolverFunc(func(_ context.Context, chatID, messageID string) (string, error) {
			if chatID != "oc_group" || messageID != "om_card" {
				t.Fatalf("resolver input = %q %q", chatID, messageID)
			}
			return "th_topic", nil
		}),
	})
	input := testCardAction(map[string]any{
		"__bridge_cb":  true,
		"bridge_token": token,
		"choice":       "a",
	}, nil)
	input.ResolvedMode = LarkChatModeTopic

	result, err := dispatcher.Dispatch(context.Background(), input)
	if err != nil {
		t.Fatalf("Dispatch returned error: %v", err)
	}
	if result.Scope.Key != "oc_group:th_topic" || len(queue.events) != 1 || queue.events[0].Scope.Key != "oc_group:th_topic" {
		t.Fatalf("result scope=%#v queued=%#v", result.Scope, queue.events)
	}
}

func TestCardActionDispatcherTopicResolverMissingFallsBackToChatScopeLikeTypeScript(t *testing.T) {
	handler := &captureCardCommandHandler{}
	dispatcher := NewCardActionDispatcher(CardActionDispatcherOptions{CommandHandler: handler})
	input := testCardAction(map[string]any{"cmd": "status"}, nil)
	input.ResolvedMode = LarkChatModeTopic

	result, err := dispatcher.Dispatch(context.Background(), input)
	if err != nil {
		t.Fatalf("Dispatch error = %v, want nil", err)
	}
	if result.Outcome != CardDispatchCommand || result.Scope.Key != "oc_group" || handler.calls != 1 || handler.last.ScopeID != "oc_group" {
		t.Fatalf("result = %#v", result)
	}
}

type captureCardCommandHandler struct {
	calls    int
	last     CardCommandRequest
	response any
}

func (h *captureCardCommandHandler) HandleCardCommand(_ context.Context, req CardCommandRequest) (any, error) {
	h.calls++
	h.last = req
	return h.response, nil
}

type captureCardQueue struct {
	events []LarkNormalizedEvent
}

func (q *captureCardQueue) EnqueueCardPrompt(_ context.Context, event LarkNormalizedEvent) error {
	q.events = append(q.events, event)
	return nil
}

type activeRunsMap map[string]CardActiveRun

func (m activeRunsMap) ActiveRun(_ context.Context, scope string) (CardActiveRun, bool, error) {
	run, ok := m[scope]
	return run, ok, nil
}

type testVerifier struct {
	auth *CallbackAuth
}

func (v testVerifier) VerifyCallback(_ context.Context, token string, expected CardCallbackVerifyExpected) CardCallbackVerifyResult {
	result := v.auth.Verify(token, CallbackVerifyExpected{
		RunID:             expected.RunID,
		Scope:             expected.Scope,
		ChatID:            expected.ChatID,
		OperatorOpenID:    expected.OperatorOpenID,
		Action:            expected.Action,
		PolicyFingerprint: expected.PolicyFingerprint,
	})
	return CardCallbackVerifyResult{OK: result.OK, Reason: string(result.Reason)}
}

func newTestCallbackAuth(t *testing.T, nonce string) *CallbackAuth {
	t.Helper()
	auth, err := NewCallbackAuth(CallbackAuthOptions{
		Keys:           []CallbackKey{{Version: 1, Secret: "secret-1"}},
		NonceStorePath: filepath.Join(t.TempDir(), "nonces.json"),
		Now:            func() time.Time { return cardDispatchNow },
		CreateNonce:    func() (string, error) { return nonce, nil },
	})
	if err != nil {
		t.Fatalf("NewCallbackAuth returned error: %v", err)
	}
	return auth
}

func signTestCardToken(t *testing.T, auth *CallbackAuth, action, scope, operator string) string {
	t.Helper()
	token, err := auth.Sign(CallbackSignInput{
		RunID:             "run-active",
		Scope:             scope,
		ChatID:            "oc_group",
		OperatorOpenID:    operator,
		Action:            action,
		PolicyFingerprint: "fp-1",
		TTL:               time.Minute,
	})
	if err != nil {
		t.Fatalf("Sign returned error: %v", err)
	}
	return token
}

func testCardAction(value map[string]any, formValue map[string]any) CardActionDispatchInput {
	return CardActionDispatchInput{
		EventID:      "evt-1",
		MessageID:    "om_card",
		ChatID:       "oc_group",
		ChatType:     LarkChatTypeGroup,
		ResolvedMode: LarkChatModeGroup,
		Operator: LarkActor{
			OpenID: "ou_operator",
			Name:   "Operator",
		},
		ActionValue: value,
		FormValue:   formValue,
		CreateTime:  cardDispatchNow,
	}
}
