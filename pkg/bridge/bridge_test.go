package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	appintake "github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/intake"
)

func TestBridgeStartWithoutOperationalCapabilityIsExplicitlyUnsupported(t *testing.T) {
	bridge, err := New(Options{
		Home:    t.TempDir(),
		Profile: "codex",
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	err = bridge.Start(context.Background())
	if !errors.Is(err, ErrBridgeStartUnsupported) {
		t.Fatalf("Start error = %v, want ErrBridgeStartUnsupported", err)
	}
}

func TestBridgeAgentOnlyStartStatusShutdown(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	client, err := NewCodexClient(CodexClientOptions{
		Binary:             "codex",
		ProfileStateDir:    root + "/profiles/codex",
		SessionStorePath:   root + "/profiles/codex/sessions.json",
		SessionCatalogPath: root + "/profiles/codex/sessions.json.catalog.json",
		MaxProcesses:       2,
	})
	if err != nil {
		t.Fatalf("NewCodexClient returned error: %v", err)
	}
	bridge, err := New(Options{
		Home:    root,
		Profile: "codex",
		Client:  client,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if err := bridge.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	status, err := bridge.Status(ctx)
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	if !status.Bridge.Started || status.Bridge.Mode != BridgeModeAgent {
		t.Fatalf("bridge status = %#v", status.Bridge)
	}
	if status.AgentName != "Codex CLI" || status.Pool.Cap != 2 {
		t.Fatalf("agent status = %#v", status)
	}
	if status.Runtime != nil || status.Lark.Configured {
		t.Fatalf("agent-only status unexpectedly configured channel: %#v", status)
	}

	if err := bridge.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown returned error: %v", err)
	}
	status, err = bridge.Status(ctx)
	if err != nil {
		t.Fatalf("Status after shutdown returned error: %v", err)
	}
	if status.Bridge.Started || status.Bridge.Mode != "" {
		t.Fatalf("bridge status after shutdown = %#v", status.Bridge)
	}
}

func TestBridgeStartsInjectedLarkTransportThroughRuntimeCoordinator(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	transport := NewFakeLarkTransport(LarkBotIdentity{
		OpenID: "ou_bot",
		UserID: "u_bot",
		Name:   "Codex Bot",
	})
	bridge, err := New(Options{
		Home:          root,
		Profile:       "codex",
		LarkTransport: transport,
		LarkIntake:    LarkIntakeSinkFunc(func(context.Context, LarkNormalizedEvent) error { return nil }),
		AppID:         "cli_bridge_test",
		Tenant:        RuntimeTenantFeishu,
		AgentKind:     RuntimeAgentCodex,
		Version:       "test",
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if err := bridge.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	status, err := bridge.Status(ctx)
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	if !status.Bridge.Started || status.Bridge.Mode != BridgeModeLark {
		t.Fatalf("bridge status = %#v", status.Bridge)
	}
	if status.Runtime == nil || !status.Runtime.Started || len(status.Runtime.Processes) != 1 {
		t.Fatalf("runtime status = %#v", status.Runtime)
	}
	if !status.Lark.Configured || !status.Lark.Started || status.Lark.BotOpenID != "ou_bot" || status.Lark.BotName != "Codex Bot" {
		t.Fatalf("lark status = %#v", status.Lark)
	}
	if status.Runtime.Entry == nil || status.Runtime.Entry.AppID != "cli_bridge_test" || status.Runtime.Entry.BotName != "Codex Bot" {
		t.Fatalf("runtime entry = %#v", status.Runtime.Entry)
	}

	if err := bridge.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown returned error: %v", err)
	}
	status, err = bridge.Status(ctx)
	if err != nil {
		t.Fatalf("Status after shutdown returned error: %v", err)
	}
	if status.Bridge.Started || status.Lark.Started {
		t.Fatalf("status after shutdown = %#v", status)
	}
	if status.Runtime == nil || status.Runtime.Started || len(status.Runtime.Processes) != 0 {
		t.Fatalf("runtime status after shutdown = %#v", status.Runtime)
	}
}

func TestBridgeShutdownFailureKeepsStateRetryable(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	shutdownErr := errors.New("runtime shutdown failed")
	handle := &retryableRuntimeHandle{shutdownErr: shutdownErr, status: RuntimeAdapterStatus{Connected: true, BotName: "Codex"}}
	bridge, err := New(Options{
		Home:    root,
		Profile: "codex",
		RuntimeAdapter: RuntimeAdapterFunc(func(context.Context, RuntimeStartRequest) (RuntimeHandle, error) {
			return handle, nil
		}),
		AppID:     "cli_bridge_test",
		Tenant:    RuntimeTenantFeishu,
		AgentKind: RuntimeAgentCodex,
		Version:   "test",
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if err := bridge.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	if err := bridge.Shutdown(ctx); !errors.Is(err, shutdownErr) {
		t.Fatalf("Shutdown error = %v, want %v", err, shutdownErr)
	}
	status, err := bridge.Status(ctx)
	if err != nil {
		t.Fatalf("Status after failed shutdown returned error: %v", err)
	}
	if !status.Bridge.Started || status.Runtime == nil || !status.Runtime.Started {
		t.Fatalf("status after failed shutdown = %#v, want still started", status)
	}

	handle.shutdownErr = nil
	if err := bridge.Shutdown(ctx); err != nil {
		t.Fatalf("retry Shutdown returned error: %v", err)
	}
	status, err = bridge.Status(ctx)
	if err != nil {
		t.Fatalf("Status after retry shutdown returned error: %v", err)
	}
	if status.Bridge.Started || status.Runtime == nil || status.Runtime.Started {
		t.Fatalf("status after retry shutdown = %#v, want stopped", status)
	}
}

func TestBridgeStartWithLarkTransportRequiresAppID(t *testing.T) {
	bridge, err := New(Options{
		Home:          t.TempDir(),
		Profile:       "codex",
		LarkTransport: NewFakeLarkTransport(LarkBotIdentity{Name: "Codex"}),
		LarkIntake:    LarkIntakeSinkFunc(func(context.Context, LarkNormalizedEvent) error { return nil }),
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	err = bridge.Start(context.Background())
	if !errors.Is(err, ErrBridgeStartMissingAppID) {
		t.Fatalf("Start error = %v, want ErrBridgeStartMissingAppID", err)
	}
}

type retryableRuntimeHandle struct {
	shutdownErr error
	status      RuntimeAdapterStatus
}

func (h *retryableRuntimeHandle) Shutdown(context.Context) error {
	if h.shutdownErr != nil {
		return h.shutdownErr
	}
	h.status.Connected = false
	return nil
}

func (h *retryableRuntimeHandle) Status(context.Context) (RuntimeAdapterStatus, error) {
	return h.status, nil
}

func TestBridgeWithLarkTransportRequiresIntake(t *testing.T) {
	_, err := New(Options{
		Home:          t.TempDir(),
		Profile:       "codex",
		LarkTransport: NewFakeLarkTransport(LarkBotIdentity{Name: "Codex"}),
	})
	if !errors.Is(err, ErrBridgeLarkIntakeRequired) {
		t.Fatalf("New error = %v, want ErrBridgeLarkIntakeRequired", err)
	}
}

func TestBridgeStartAppliesLarkRuntimeContextToClient(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake uses POSIX sh")
	}
	ctx := context.Background()
	root := t.TempDir()
	cwd := t.TempDir()
	binary := writeBridgeFakeCodex(t, `
cat > prompt.txt
printf '%s' "$LARK_CHANNEL_PROFILE" > lark_profile.txt
printf '%s' "$LARK_CHANNEL_CONFIG" > lark_config.txt
printf '%s\n' '{"type":"turn.completed"}'
`)
	client, err := NewCodexClient(CodexClientOptions{
		Binary:             binary,
		ProfileStateDir:    filepath.Join(root, "profiles", "codex"),
		DefaultWorkingDir:  cwd,
		SessionStorePath:   filepath.Join(root, "sessions.json"),
		SessionCatalogPath: filepath.Join(root, "sessions.catalog.json"),
	})
	if err != nil {
		t.Fatalf("NewCodexClient returned error: %v", err)
	}
	transport := NewFakeLarkTransport(LarkBotIdentity{OpenID: "ou_bot_late", Name: "Late Bot"})
	projection := LarkProfileProjectionHookFunc(func(_ context.Context, req LarkProfileProjectionRequest) (LarkProfileProjectionResult, error) {
		return LarkProfileProjectionResult{
			LarkChannelEnv: map[string]string{
				"LARK_CHANNEL":         "1",
				"LARK_CHANNEL_PROFILE": "codex-projected",
				"LARK_CHANNEL_CONFIG":  "/tmp/projected-lark-config.json",
			},
			BotIdentity: req.BotIdentity,
		}, nil
	})
	bridge, err := New(Options{
		Home:                  root,
		Profile:               "codex",
		Client:                client,
		LarkTransport:         transport,
		LarkIntake:            LarkIntakeSinkFunc(func(context.Context, LarkNormalizedEvent) error { return nil }),
		LarkProfileProjection: projection,
		AppID:                 "cli_bridge_test",
		Tenant:                RuntimeTenantFeishu,
		AgentKind:             RuntimeAgentCodex,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if err := bridge.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	t.Cleanup(func() {
		_ = bridge.Shutdown(context.Background())
	})

	run, err := client.Run(ctx, RunInput{
		ScopeID:    "oc_chat",
		Scope:      Scope{Source: SourceIM, ChatID: "oc_chat", ActorID: "ou_user"},
		Prompt:     "hello",
		WorkingDir: cwd,
		Access: client.EvaluateAccess(AccessInput{
			Scope:    Scope{Source: SourceIM, ChatID: "oc_chat", ActorID: "ou_user"},
			ChatMode: LarkChatModeGroup,
			RuntimeControls: RuntimeControls{
				BotOwnerID:        "ou_user",
				OwnerRefreshState: "ok",
			},
		}),
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	events := collectBridgeEvents(t, run)
	if len(events) != 1 || events[0].Type != EventDone {
		t.Fatalf("events = %#v, want done", events)
	}

	prompt := readBridgeFile(t, filepath.Join(cwd, "prompt.txt"))
	for _, fragment := range []string{"ou_bot_late", "Late Bot"} {
		if !strings.Contains(prompt, fragment) {
			t.Fatalf("prompt missing %q in:\n%s", fragment, prompt)
		}
	}
	if got := readBridgeFile(t, filepath.Join(cwd, "lark_profile.txt")); got != "codex-projected" {
		t.Fatalf("LARK_CHANNEL_PROFILE = %q, want codex-projected", got)
	}
	if got := readBridgeFile(t, filepath.Join(cwd, "lark_config.txt")); got != "/tmp/projected-lark-config.json" {
		t.Fatalf("LARK_CHANNEL_CONFIG = %q, want projected config", got)
	}
}

func TestClientRunRejectsUntrustedAllowedAccess(t *testing.T) {
	client, err := NewCodexClient(CodexClientOptions{
		ProfileStateDir:    t.TempDir(),
		DefaultWorkingDir:  t.TempDir(),
		SessionStorePath:   filepath.Join(t.TempDir(), "sessions.json"),
		SessionCatalogPath: filepath.Join(t.TempDir(), "sessions.catalog.json"),
	})
	if err != nil {
		t.Fatalf("NewCodexClient returned error: %v", err)
	}
	_, err = client.Run(context.Background(), RunInput{
		ScopeID:    "oc_chat",
		Scope:      Scope{Source: SourceIM, ChatID: "oc_chat", ActorID: "ou_user"},
		Prompt:     "hello",
		WorkingDir: t.TempDir(),
		Access:     AccessDecision{OK: true, Reason: AccessOwner},
	})
	if !errors.Is(err, ErrUntrustedAccessDecision) {
		t.Fatalf("Run err = %v, want ErrUntrustedAccessDecision", err)
	}
}

func TestBridgeWithClientAndLarkTransportCreatesManagedCommentIntake(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake uses POSIX sh")
	}
	ctx := context.Background()
	root := t.TempDir()
	cwd := t.TempDir()
	binary := writeBridgeFakeCodex(t, `
cat > prompt.txt
printf '%s\n' '{"type":"agent_message","message":"**完成**"}'
printf '%s\n' '{"type":"turn.completed"}'
`)
	client, err := NewCodexClient(CodexClientOptions{
		Binary:             binary,
		ProfileStateDir:    filepath.Join(root, "profiles", "codex"),
		DefaultWorkingDir:  cwd,
		SessionStorePath:   filepath.Join(root, "sessions.json"),
		SessionCatalogPath: filepath.Join(root, "sessions.catalog.json"),
	})
	if err != nil {
		t.Fatalf("NewCodexClient returned error: %v", err)
	}
	transport := NewFakeLarkTransport(LarkBotIdentity{OpenID: "ou_bot", Name: "Bot"})
	comments := newFakeBridgeCommentSurface()
	bridge, err := New(Options{
		Home:          root,
		Profile:       "codex",
		Client:        client,
		LarkTransport: transport,
		LarkComments:  comments,
		AppID:         "cli_bridge_test",
		Tenant:        RuntimeTenantFeishu,
		AgentKind:     RuntimeAgentCodex,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if err := bridge.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	t.Cleanup(func() {
		_ = bridge.Shutdown(context.Background())
	})

	err = transport.Emit(ctx, LarkIncomingEvent{
		Kind: LarkEventComment,
		Comment: &LarkCommentInput{
			EventID:      "evt_comment",
			FileToken:    "doc_token",
			FileType:     "docx",
			CommentID:    "comment_1",
			ReplyID:      "reply_1",
			Operator:     LarkActor{OpenID: "ou_user"},
			MentionedBot: true,
		},
	})
	if err != nil {
		t.Fatalf("Emit comment returned error: %v", err)
	}
	if len(comments.replies) != 1 {
		t.Fatalf("comment replies = %#v, want one", comments.replies)
	}
	if comments.replies[0].text != "完成" || comments.replies[0].commentID != "comment_1" {
		t.Fatalf("comment reply = %#v", comments.replies[0])
	}
	if !strings.Contains(readBridgeFile(t, filepath.Join(cwd, "prompt.txt")), "用户的问题：请总结") {
		t.Fatalf("prompt missing comment question")
	}
}

func TestManagedCommentIntakeDropsBotSelfComment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake uses POSIX sh")
	}
	ctx := context.Background()
	root := t.TempDir()
	cwd := t.TempDir()
	binary := writeBridgeFakeCodex(t, `
cat > prompt.txt
printf '%s\n' '{"type":"turn.completed"}'
`)
	client, err := NewCodexClient(CodexClientOptions{
		Binary:             binary,
		ProfileStateDir:    filepath.Join(root, "profiles", "codex"),
		DefaultWorkingDir:  cwd,
		SessionStorePath:   filepath.Join(root, "sessions.json"),
		SessionCatalogPath: filepath.Join(root, "sessions.catalog.json"),
	})
	if err != nil {
		t.Fatalf("NewCodexClient returned error: %v", err)
	}
	transport := NewFakeLarkTransport(LarkBotIdentity{OpenID: "ou_bot", Name: "Bot"})
	comments := newFakeBridgeCommentSurface()
	bridge, err := New(Options{
		Home:          root,
		Profile:       "codex",
		Client:        client,
		LarkTransport: transport,
		LarkComments:  comments,
		AppID:         "cli_bridge_test",
		Tenant:        RuntimeTenantFeishu,
		AgentKind:     RuntimeAgentCodex,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if err := bridge.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	t.Cleanup(func() {
		_ = bridge.Shutdown(context.Background())
	})

	err = transport.Emit(ctx, LarkIncomingEvent{
		Kind: LarkEventComment,
		Comment: &LarkCommentInput{
			EventID:      "evt_self",
			FileToken:    "doc_token",
			FileType:     "docx",
			CommentID:    "comment_1",
			ReplyID:      "reply_1",
			Operator:     LarkActor{OpenID: "ou_bot"},
			MentionedBot: true,
		},
	})
	if err != nil {
		t.Fatalf("Emit self comment returned error: %v", err)
	}
	if len(comments.replies) != 0 {
		t.Fatalf("self comment replies = %#v, want none", comments.replies)
	}
	if _, err := os.Stat(filepath.Join(cwd, "prompt.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("prompt file stat err = %v, want not exist", err)
	}
}

func TestManagedCommentIntakeUsesProfileDefaultWorkspaceFallback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake uses POSIX sh")
	}
	ctx := context.Background()
	root := t.TempDir()
	binary := writeBridgeFakeCodex(t, `
pwd > cwd.txt
printf '%s\n' '{"type":"agent_message","message":"ok"}'
printf '%s\n' '{"type":"turn.completed"}'
`)
	client, err := NewCodexClient(CodexClientOptions{
		Binary:             binary,
		ProfileStateDir:    filepath.Join(root, "profiles", "codex"),
		SessionStorePath:   filepath.Join(root, "sessions.json"),
		SessionCatalogPath: filepath.Join(root, "sessions.catalog.json"),
	})
	if err != nil {
		t.Fatalf("NewCodexClient returned error: %v", err)
	}
	transport := NewFakeLarkTransport(LarkBotIdentity{OpenID: "ou_bot", Name: "Bot"})
	comments := newFakeBridgeCommentSurface()
	bridge, err := New(Options{
		Home:          root,
		Profile:       "codex",
		Client:        client,
		LarkTransport: transport,
		LarkComments:  comments,
		AppID:         "cli_bridge_test",
		Tenant:        RuntimeTenantFeishu,
		AgentKind:     RuntimeAgentCodex,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if err := bridge.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	t.Cleanup(func() {
		_ = bridge.Shutdown(context.Background())
	})

	if err := transport.Emit(ctx, LarkIncomingEvent{
		Kind: LarkEventComment,
		Comment: &LarkCommentInput{
			EventID:      "evt_comment",
			FileToken:    "doc_token",
			FileType:     "docx",
			CommentID:    "comment_1",
			ReplyID:      "reply_1",
			Operator:     LarkActor{OpenID: "ou_user"},
			MentionedBot: true,
		},
	}); err != nil {
		t.Fatalf("Emit comment returned error: %v", err)
	}
	expectedWorkspace := filepath.Join(root+"-workspaces", "codex", "default")
	expectedRealpath, err := filepath.EvalSymlinks(expectedWorkspace)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", expectedWorkspace, err)
	}
	got := strings.TrimSpace(readBridgeFile(t, filepath.Join(expectedWorkspace, "cwd.txt")))
	if got != expectedRealpath {
		t.Fatalf("managed comment cwd = %q, want %q", got, expectedRealpath)
	}
}

func TestBridgeManagedLarkIntakeRunsIMMessage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake uses POSIX sh")
	}
	ctx := context.Background()
	root := t.TempDir()
	cwd := t.TempDir()
	binary := writeBridgeFakeCodex(t, `
cat > prompt.txt
printf '%s\n' '{"type":"agent_message","message":"pong from codex"}'
printf '%s\n' '{"type":"turn.completed"}'
`)
	client, err := NewCodexClient(CodexClientOptions{
		Binary:             binary,
		ProfileStateDir:    filepath.Join(root, "profiles", "codex"),
		DefaultWorkingDir:  cwd,
		SessionStorePath:   filepath.Join(root, "sessions.json"),
		SessionCatalogPath: filepath.Join(root, "sessions.catalog.json"),
		AllowedUsers:       []string{"ou_user"},
		Admins:             []string{"ou_user"},
	})
	if err != nil {
		t.Fatalf("NewCodexClient returned error: %v", err)
	}
	transport := NewFakeLarkTransport(LarkBotIdentity{OpenID: "ou_bot", Name: "Bot"})
	bridge, err := New(Options{
		Home:          root,
		Profile:       "codex",
		Client:        client,
		LarkTransport: transport,
		LarkManaged: LarkManagedOptions{
			MessageQuietPeriod: time.Millisecond,
		},
		AppID:  "cli_bridge_test",
		Tenant: RuntimeTenantFeishu,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if err := bridge.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	t.Cleanup(func() {
		_ = bridge.Shutdown(context.Background())
	})

	if err := transport.Emit(ctx, LarkIncomingEvent{
		Kind: LarkEventMessage,
		Message: &LarkMessageInput{
			MessageID: "om_user_1",
			ChatID:    "oc_dm",
			ChatType:  LarkChatTypeP2P,
			Sender:    LarkActor{OpenID: "ou_user", Name: "Alice"},
			Content:   "hello from lark",
		},
	}); err != nil {
		t.Fatalf("Emit message returned error: %v", err)
	}
	messages := waitBridgeSentMessages(t, transport, 1)
	if got := messages[0].Content.Markdown; !strings.Contains(got, "pong from codex") {
		t.Fatalf("managed IM reply markdown = %q", got)
	}
	if messages[0].Options.ReplyTo != "om_user_1" {
		t.Fatalf("reply target = %#v", messages[0].Options)
	}
	prompt := readBridgeFile(t, filepath.Join(cwd, "prompt.txt"))
	for _, fragment := range []string{"bridge_context", "hello from lark", "ou_bot", "Alice"} {
		if !strings.Contains(prompt, fragment) {
			t.Fatalf("managed IM prompt missing %q in:\n%s", fragment, prompt)
		}
	}
	status, err := bridge.Status(ctx)
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	if status.Runtime == nil || status.Runtime.Entry == nil || status.Runtime.Entry.AgentKind != RuntimeAgentCodex {
		t.Fatalf("runtime agent kind = %#v", status.Runtime)
	}
}

func TestBridgeManagedLarkIntakeIncludesReplyQuoteInPrompt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake uses POSIX sh")
	}
	ctx := context.Background()
	root := t.TempDir()
	cwd := t.TempDir()
	binary := writeBridgeFakeCodex(t, `
cat > prompt.txt
printf '%s\n' '{"type":"agent_message","message":"quoted"}'
printf '%s\n' '{"type":"turn.completed"}'
`)
	client, err := NewCodexClient(CodexClientOptions{
		Binary:             binary,
		ProfileStateDir:    filepath.Join(root, "profiles", "codex"),
		DefaultWorkingDir:  cwd,
		SessionStorePath:   filepath.Join(root, "sessions.json"),
		SessionCatalogPath: filepath.Join(root, "sessions.catalog.json"),
		AllowedChats:       []string{"oc_group"},
	})
	if err != nil {
		t.Fatalf("NewCodexClient returned error: %v", err)
	}
	targets := make(chan LarkQuoteTarget, 1)
	transport := NewFakeLarkTransport(LarkBotIdentity{OpenID: "ou_bot", Name: "Bot"})
	bridge, err := New(Options{
		Home:          root,
		Profile:       "codex",
		Client:        client,
		LarkTransport: transport,
		LarkManaged: LarkManagedOptions{
			MessageQuietPeriod: time.Millisecond,
			QuoteResolver: LarkQuoteResolverFunc(func(_ context.Context, target LarkQuoteTarget) (BridgePromptQuotedMessage, bool, error) {
				targets <- target
				return BridgePromptQuotedMessage{
					MessageID:      target.MessageID,
					SenderID:       "ou_quote_sender",
					SenderName:     "Quoted",
					CreatedAt:      "2026-05-25T10:00:00.000Z",
					RawContentType: "text",
					Content:        "regular quoted content",
				}, true, nil
			}),
		},
		AppID:  "cli_bridge_test",
		Tenant: RuntimeTenantFeishu,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if err := bridge.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	t.Cleanup(func() {
		_ = bridge.Shutdown(context.Background())
	})

	if err := transport.Emit(ctx, LarkIncomingEvent{
		Kind: LarkEventMessage,
		Message: &LarkMessageInput{
			MessageID:        "om_reply",
			ChatID:           "oc_group",
			ChatType:         LarkChatTypeGroup,
			ResolvedMode:     LarkChatModeGroup,
			RootID:           "om_quote",
			ParentID:         "om_quote",
			ReplyToMessageID: "om_quote",
			Sender:           LarkActor{OpenID: "ou_user", Name: "Alice"},
			Content:          "@Bot inspect this",
			MentionedBot:     true,
		},
	}); err != nil {
		t.Fatalf("Emit message returned error: %v", err)
	}
	_ = waitBridgeSentMessages(t, transport, 1)
	select {
	case target := <-targets:
		if target.MessageID != "om_quote" || target.SourceMessageID != "om_reply" {
			t.Fatalf("quote target = %#v", target)
		}
	default:
		t.Fatal("quote resolver was not called")
	}
	prompt := readBridgeFile(t, filepath.Join(cwd, "prompt.txt"))
	for _, fragment := range []string{"<quoted_messages>", "regular quoted content", "om_quote"} {
		if !strings.Contains(prompt, fragment) {
			t.Fatalf("quote prompt missing %q in:\n%s", fragment, prompt)
		}
	}
}

func TestBridgeManagedLarkIntakeSkipsTopicRootQuote(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake uses POSIX sh")
	}
	ctx := context.Background()
	root := t.TempDir()
	cwd := t.TempDir()
	binary := writeBridgeFakeCodex(t, `
cat > prompt.txt
printf '%s\n' '{"type":"agent_message","message":"topic"}'
printf '%s\n' '{"type":"turn.completed"}'
`)
	client, err := NewCodexClient(CodexClientOptions{
		Binary:             binary,
		ProfileStateDir:    filepath.Join(root, "profiles", "codex"),
		DefaultWorkingDir:  cwd,
		SessionStorePath:   filepath.Join(root, "sessions.json"),
		SessionCatalogPath: filepath.Join(root, "sessions.catalog.json"),
		AllowedChats:       []string{"oc_topic"},
	})
	if err != nil {
		t.Fatalf("NewCodexClient returned error: %v", err)
	}
	targets := make(chan LarkQuoteTarget, 1)
	transport := NewFakeLarkTransport(LarkBotIdentity{OpenID: "ou_bot", Name: "Bot"})
	bridge, err := New(Options{
		Home:          root,
		Profile:       "codex",
		Client:        client,
		LarkTransport: transport,
		LarkManaged: LarkManagedOptions{
			MessageQuietPeriod: time.Millisecond,
			QuoteResolver: LarkQuoteResolverFunc(func(_ context.Context, target LarkQuoteTarget) (BridgePromptQuotedMessage, bool, error) {
				targets <- target
				return BridgePromptQuotedMessage{MessageID: target.MessageID, RawContentType: "text", Content: "topic root content"}, true, nil
			}),
		},
		AppID:  "cli_bridge_test",
		Tenant: RuntimeTenantFeishu,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if err := bridge.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	t.Cleanup(func() {
		_ = bridge.Shutdown(context.Background())
	})

	if err := transport.Emit(ctx, LarkIncomingEvent{
		Kind: LarkEventMessage,
		Message: &LarkMessageInput{
			MessageID:        "om_topic_reply",
			ChatID:           "oc_topic",
			ChatType:         LarkChatTypeGroup,
			ResolvedMode:     LarkChatModeTopic,
			ThreadID:         "omt_topic",
			RootID:           "om_topic_root",
			ParentID:         "om_topic_root",
			ReplyToMessageID: "om_topic_root",
			Sender:           LarkActor{OpenID: "ou_user", Name: "Alice"},
			Content:          "@Bot continue",
			MentionedBot:     true,
		},
	}); err != nil {
		t.Fatalf("Emit message returned error: %v", err)
	}
	_ = waitBridgeSentMessages(t, transport, 1)
	select {
	case target := <-targets:
		t.Fatalf("quote resolver called for topic root: %#v", target)
	default:
	}
	prompt := readBridgeFile(t, filepath.Join(cwd, "prompt.txt"))
	if strings.Contains(prompt, "<quoted_messages>") || strings.Contains(prompt, "topic root content") {
		t.Fatalf("topic root quote leaked into prompt:\n%s", prompt)
	}
	if !strings.Contains(prompt, `"threadId":"omt_topic"`) {
		t.Fatalf("topic prompt missing thread id:\n%s", prompt)
	}
}

func TestBridgeManagedLarkIntakeCanReplyWithCard(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake uses POSIX sh")
	}
	ctx := context.Background()
	root := t.TempDir()
	cwd := t.TempDir()
	binary := writeBridgeFakeCodex(t, `
cat > prompt.txt
printf '%s\n' '{"type":"agent_message","message":"card body"}'
printf '%s\n' '{"type":"turn.completed"}'
`)
	client, err := NewCodexClient(CodexClientOptions{
		Binary:             binary,
		ProfileStateDir:    filepath.Join(root, "profiles", "codex"),
		DefaultWorkingDir:  cwd,
		SessionStorePath:   filepath.Join(root, "sessions.json"),
		SessionCatalogPath: filepath.Join(root, "sessions.catalog.json"),
		AllowedUsers:       []string{"ou_user"},
		Admins:             []string{"ou_user"},
	})
	if err != nil {
		t.Fatalf("NewCodexClient returned error: %v", err)
	}
	transport := NewFakeLarkTransport(LarkBotIdentity{OpenID: "ou_bot", Name: "Bot"})
	bridge, err := New(Options{
		Home:          root,
		Profile:       "codex",
		Client:        client,
		LarkTransport: transport,
		LarkManaged: LarkManagedOptions{
			MessageQuietPeriod: time.Millisecond,
			MessageReplyMode:   LarkReplyCard,
		},
		AppID:  "cli_bridge_test",
		Tenant: RuntimeTenantFeishu,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if err := bridge.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	t.Cleanup(func() {
		_ = bridge.Shutdown(context.Background())
	})

	emitManagedTestMessage(t, ctx, transport, "om_card_reply", "render card")
	cards := waitBridgeSentCards(t, transport, 1)
	updates := waitBridgeUpdatedCards(t, transport, 1)
	if cards[0].Options.ReplyTo != "om_card_reply" {
		t.Fatalf("card reply options = %#v", cards[0].Options)
	}
	if cards[0].Card["schema"] != "2.0" || updates[0].Card["schema"] != "2.0" {
		t.Fatalf("card schema = %#v / %#v", cards[0].Card["schema"], updates[0].Card["schema"])
	}
	if got := transport.SentMessageSnapshot(); len(got) != 0 {
		t.Fatalf("sent messages = %#v, want card-only reply", got)
	}
}

func TestBridgeManagedLarkIntakeDefaultCardStopActionInterruptsRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake uses POSIX sh")
	}
	ctx := context.Background()
	root := t.TempDir()
	cwd := t.TempDir()
	binary := writeBridgeFakeCodex(t, `
cat > prompt.txt
sleep 5
printf '%s\n' '{"type":"agent_message","message":"too late"}'
printf '%s\n' '{"type":"turn.completed"}'
`)
	client, err := NewCodexClient(CodexClientOptions{
		Binary:             binary,
		ProfileStateDir:    filepath.Join(root, "profiles", "codex"),
		DefaultWorkingDir:  cwd,
		SessionStorePath:   filepath.Join(root, "sessions.json"),
		SessionCatalogPath: filepath.Join(root, "sessions.catalog.json"),
		AllowedUsers:       []string{"ou_user"},
	})
	if err != nil {
		t.Fatalf("NewCodexClient returned error: %v", err)
	}
	auth, err := NewCallbackAuth(CallbackAuthOptions{
		Keys:        []CallbackKey{{Version: 1, Secret: "secret-1"}},
		CreateNonce: func() (string, error) { return "nonce-stop-card", nil },
	})
	if err != nil {
		t.Fatalf("NewCallbackAuth returned error: %v", err)
	}
	transport := NewFakeLarkTransport(LarkBotIdentity{OpenID: "ou_bot", Name: "Bot"})
	bridge, err := New(Options{
		Home:          root,
		Profile:       "codex",
		Client:        client,
		LarkTransport: transport,
		LarkManaged: LarkManagedOptions{
			MessageQuietPeriod: time.Millisecond,
			MessageReplyMode:   LarkReplyCard,
			CallbackAuth:       auth,
		},
		AppID:  "cli_bridge_test",
		Tenant: RuntimeTenantFeishu,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if err := bridge.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	t.Cleanup(func() {
		_ = bridge.Shutdown(context.Background())
	})

	emitManagedTestMessage(t, ctx, transport, "om_stop_me", "long run")
	cards := waitBridgeSentCards(t, transport, 1)
	value := findBridgeCallbackValue(t, cards[0].Card)
	if value["cmd"] != "stop" || value[BridgeCardCallbackMarker] != true {
		t.Fatalf("stop callback value = %#v", value)
	}
	if token, _ := value[BridgeCardTokenKey].(string); !strings.HasPrefix(token, "bridge_cb.v1.") {
		t.Fatalf("stop callback token = %#v", value[BridgeCardTokenKey])
	}

	if err := transport.Emit(ctx, LarkIncomingEvent{
		Kind: LarkEventCardAction,
		CardAction: &LarkCardActionInput{
			EventID:     "evt_stop",
			MessageID:   "om_card_stop",
			ChatID:      "oc_dm",
			ChatType:    LarkChatTypeP2P,
			Operator:    LarkActor{OpenID: "ou_user", Name: "Alice"},
			ActionValue: value,
		},
	}); err != nil {
		t.Fatalf("Emit card action returned error: %v", err)
	}
	_ = waitBridgeUpdatedCards(t, transport, 1)
}

func TestBridgeManagedLarkIntakeRejectsUnauthorizedCardAction(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake uses POSIX sh")
	}
	root := t.TempDir()
	cwd := t.TempDir()
	binary := writeBridgeFakeCodex(t, `
cat > prompt.txt
printf '%s\n' '{"type":"turn.completed"}'
`)
	client, err := NewCodexClient(CodexClientOptions{
		Binary:             binary,
		ProfileStateDir:    filepath.Join(root, "profiles", "codex"),
		DefaultWorkingDir:  cwd,
		SessionStorePath:   filepath.Join(root, "sessions.json"),
		SessionCatalogPath: filepath.Join(root, "sessions.catalog.json"),
		AllowedUsers:       []string{"ou_allowed"},
	})
	if err != nil {
		t.Fatalf("NewCodexClient returned error: %v", err)
	}
	intake := newManagedLarkIntake(managedLarkIntakeOptions{
		Client:    client,
		Transport: NewFakeLarkTransport(LarkBotIdentity{OpenID: "ou_bot", Name: "Bot"}),
	})

	result, err := intake.Dispatch(context.Background(), LarkCardActionInput{
		MessageID:   "om_card",
		ChatID:      "oc_dm",
		ChatType:    LarkChatTypeP2P,
		Operator:    LarkActor{OpenID: "ou_denied", Name: "Mallory"},
		ActionValue: map[string]any{"cmd": "status"},
	})
	if err != nil {
		t.Fatalf("Dispatch returned error: %v", err)
	}
	if result.Outcome != CardDispatchRejected || result.RejectReason != string(AccessDeniedUser) {
		t.Fatalf("result = %#v", result)
	}
}

func TestBridgeManagedLarkIntakeSendsCardCommandResponse(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake uses POSIX sh")
	}
	root := t.TempDir()
	cwd := t.TempDir()
	binary := writeBridgeFakeCodex(t, `
cat > prompt.txt
printf '%s\n' '{"type":"turn.completed"}'
`)
	client, err := NewCodexClient(CodexClientOptions{
		Binary:             binary,
		ProfileStateDir:    filepath.Join(root, "profiles", "codex"),
		DefaultWorkingDir:  cwd,
		SessionStorePath:   filepath.Join(root, "sessions.json"),
		SessionCatalogPath: filepath.Join(root, "sessions.catalog.json"),
		AllowedUsers:       []string{"ou_user"},
	})
	if err != nil {
		t.Fatalf("NewCodexClient returned error: %v", err)
	}
	transport := NewFakeLarkTransport(LarkBotIdentity{OpenID: "ou_bot", Name: "Bot"})
	intake := newManagedLarkIntake(managedLarkIntakeOptions{
		Client:    client,
		Transport: transport,
	})

	result, err := intake.Dispatch(context.Background(), LarkCardActionInput{
		MessageID:   "om_card_status",
		ChatID:      "oc_dm",
		ChatType:    LarkChatTypeP2P,
		Operator:    LarkActor{OpenID: "ou_user", Name: "Alice"},
		ActionValue: map[string]any{"cmd": "status"},
	})
	if err != nil {
		t.Fatalf("Dispatch returned error: %v", err)
	}
	if result.Outcome != CardDispatchCommand {
		t.Fatalf("result = %#v", result)
	}
	messages := transport.SentMessageSnapshot()
	if len(messages) != 1 || messages[0].Options.ReplyTo != "om_card_status" || !strings.Contains(messages[0].Content.Markdown, "**profile** codex") {
		t.Fatalf("messages = %#v", messages)
	}
}

func TestBridgeManagedLarkIntakeUpdatesCardCommandResponse(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake uses POSIX sh")
	}
	root := t.TempDir()
	cwd := t.TempDir()
	binary := writeBridgeFakeCodex(t, `
cat > prompt.txt
printf '%s\n' '{"type":"turn.completed"}'
`)
	client, err := NewCodexClient(CodexClientOptions{
		Binary:             binary,
		ProfileStateDir:    filepath.Join(root, "profiles", "codex"),
		DefaultWorkingDir:  cwd,
		SessionStorePath:   filepath.Join(root, "sessions.json"),
		SessionCatalogPath: filepath.Join(root, "sessions.catalog.json"),
		AllowedUsers:       []string{"ou_user"},
		Admins:             []string{"ou_user"},
	})
	if err != nil {
		t.Fatalf("NewCodexClient returned error: %v", err)
	}
	transport := NewFakeLarkTransport(LarkBotIdentity{OpenID: "ou_bot", Name: "Bot"})
	intake := newManagedLarkIntake(managedLarkIntakeOptions{
		Client:    client,
		Transport: transport,
	})

	result, err := intake.Dispatch(context.Background(), LarkCardActionInput{
		MessageID:   "om_config_card",
		ChatID:      "oc_dm",
		ChatType:    LarkChatTypeP2P,
		Operator:    LarkActor{OpenID: "ou_user", Name: "Alice"},
		ActionValue: map[string]any{"cmd": "config.cancel"},
	})
	if err != nil {
		t.Fatalf("Dispatch returned error: %v", err)
	}
	if result.Outcome != CardDispatchCommand {
		t.Fatalf("result = %#v", result)
	}
	updates := transport.UpdatedCardSnapshot()
	if len(updates) != 1 || updates[0].MessageID != "om_config_card" || !strings.Contains(mustBridgeCardJSON(t, updates[0].Card), "已取消") {
		t.Fatalf("updates = %#v", updates)
	}
	if got := transport.SentMessageSnapshot(); len(got) != 0 {
		t.Fatalf("sent messages = %#v, want card update only", got)
	}
}

func TestBridgeManagedLarkIntakeDetachesCardSubmitUntilSettle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake uses POSIX sh")
	}
	root := t.TempDir()
	cwd := t.TempDir()
	binary := writeBridgeFakeCodex(t, `
cat > prompt.txt
printf '%s\n' '{"type":"turn.completed"}'
`)
	client, err := NewCodexClient(CodexClientOptions{
		Binary:             binary,
		ProfileStateDir:    filepath.Join(root, "profiles", "codex"),
		DefaultWorkingDir:  cwd,
		SessionStorePath:   filepath.Join(root, "sessions.json"),
		SessionCatalogPath: filepath.Join(root, "sessions.catalog.json"),
		AllowedUsers:       []string{"ou_user"},
		Admins:             []string{"ou_user"},
	})
	if err != nil {
		t.Fatalf("NewCodexClient returned error: %v", err)
	}
	transport := NewFakeLarkTransport(LarkBotIdentity{OpenID: "ou_bot", Name: "Bot"})
	intake := newManagedLarkIntake(managedLarkIntakeOptions{
		Client:    client,
		Transport: transport,
		Managed:   LarkManagedOptions{CardActionSettle: 40 * time.Millisecond},
	})

	start := time.Now()
	result, err := intake.Dispatch(context.Background(), LarkCardActionInput{
		MessageID:   "om_config_submit",
		ChatID:      "oc_dm",
		ChatType:    LarkChatTypeP2P,
		Operator:    LarkActor{OpenID: "ou_user", Name: "Alice"},
		ActionValue: map[string]any{"cmd": "config.submit"},
		FormValue: map[string]any{
			"message_reply":            "text",
			"show_tool_calls":          "show",
			"cot_messages":             "off",
			"max_concurrent_runs":      "10",
			"run_idle_timeout_minutes": "0",
			"require_mention_in_group": "yes",
			"lark_cli_identity":        "bot-only",
		},
	})
	if err != nil {
		t.Fatalf("Dispatch returned error: %v", err)
	}
	if result.Outcome != CardDispatchCommand || result.Command != "config" || result.Args != "submit" {
		t.Fatalf("result = %#v", result)
	}
	if elapsed := time.Since(start); elapsed > 25*time.Millisecond {
		t.Fatalf("Dispatch blocked for %s", elapsed)
	}
	if updates := transport.UpdatedCardSnapshot(); len(updates) != 0 {
		t.Fatalf("updates before settle = %#v", updates)
	}

	updates := waitBridgeUpdatedCards(t, transport, 1)
	if updates[0].MessageID != "om_config_submit" || !strings.Contains(mustBridgeCardJSON(t, updates[0].Card), "ConfigPath") {
		t.Fatalf("updates = %#v", updates)
	}
}

func TestBridgeManagedLarkIntakeSchedulesAccountReconnectAfterCardUpdate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake uses POSIX sh")
	}
	root := t.TempDir()
	cwd := t.TempDir()
	binary := writeBridgeFakeCodex(t, `
cat > prompt.txt
printf '%s\n' '{"type":"turn.completed"}'
`)
	configPath := filepath.Join(root, "config.json")
	writeBridgeAccountRoot(t, configPath, cwd, binary)
	keystore, err := NewKeystore(KeystoreOptions{
		Paths: KeystorePaths{
			SecretsFile:         filepath.Join(root, "profiles", "codex", "secrets.enc"),
			KeystoreSaltFile:    filepath.Join(root, "profiles", "codex", "secrets.salt"),
			SecretsGetterScript: filepath.Join(root, "secrets-getter"),
		},
		Seed: "test-seed",
	})
	if err != nil {
		t.Fatalf("NewKeystore returned error: %v", err)
	}
	client, err := NewCodexClient(CodexClientOptions{
		Binary:             binary,
		ProfileStateDir:    filepath.Join(root, "profiles", "codex"),
		DefaultWorkingDir:  cwd,
		SessionStorePath:   filepath.Join(root, "sessions.json"),
		SessionCatalogPath: filepath.Join(root, "sessions.catalog.json"),
		AllowedUsers:       []string{"ou_user"},
		Admins:             []string{"ou_user"},
	})
	if err != nil {
		t.Fatalf("NewCodexClient returned error: %v", err)
	}
	reconnected := make(chan bool, 1)
	transport := NewFakeLarkTransport(LarkBotIdentity{OpenID: "ou_bot", Name: "Bot"})
	intake := newManagedLarkIntake(managedLarkIntakeOptions{
		Client:    client,
		Transport: transport,
		Managed: LarkManagedOptions{
			CardActionSettle: 25 * time.Millisecond,
			AccountReconnect: 120 * time.Millisecond,
			CommandOptions: CommandOptions{
				ConfigPath: configPath,
				Keystore:   keystore,
				AccountValidator: CommandAccountValidatorFunc(func(_ context.Context, appID, appSecret, tenant string) (CommandAccountValidationResult, error) {
					if appID != "cli_new" || appSecret != "new-secret" || tenant != "lark" {
						return CommandAccountValidationResult{OK: false, Reason: "bad input"}, nil
					}
					return CommandAccountValidationResult{OK: true, BotName: "Bridge Bot"}, nil
				}),
				Reconnector: CommandReconnectorFunc(func(_ context.Context, wait bool) error {
					reconnected <- wait
					return nil
				}),
			},
		},
	})

	result, err := intake.Dispatch(context.Background(), LarkCardActionInput{
		MessageID:   "om_account_submit",
		ChatID:      "oc_dm",
		ChatType:    LarkChatTypeP2P,
		Operator:    LarkActor{OpenID: "ou_user", Name: "Alice"},
		ActionValue: map[string]any{"cmd": "account.submit"},
		FormValue:   map[string]any{"app_id": "cli_new", "app_secret": "new-secret", "tenant": "lark"},
	})
	if err != nil {
		t.Fatalf("Dispatch returned error: %v", err)
	}
	if result.Outcome != CardDispatchCommand || result.Command != "account" || result.Args != "submit" {
		t.Fatalf("result = %#v", result)
	}
	updates := waitBridgeUpdatedCards(t, transport, 1)
	if updates[0].MessageID != "om_account_submit" || !strings.Contains(mustBridgeCardJSON(t, updates[0].Card), "Bridge Bot") {
		t.Fatalf("updates = %#v", updates)
	}
	select {
	case wait := <-reconnected:
		t.Fatalf("reconnect happened before card update path settled, wait=%v", wait)
	default:
	}
	select {
	case wait := <-reconnected:
		if wait {
			t.Fatalf("account reconnect wait = true, want false")
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for delayed account reconnect")
	}
	snapshot, err := LoadConfig(configPath, ConfigLoadOptions{Profile: "codex"})
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}
	if snapshot.Profile.Accounts.App.ID != "cli_new" {
		t.Fatalf("persisted app id = %q, want cli_new", snapshot.Profile.Accounts.App.ID)
	}
	intake.Close()
}

func TestBridgeManagedLarkIntakeReconnectsWhenAccountCardDeliveryFails(t *testing.T) {
	reconnected := make(chan bool, 1)
	transport := NewFakeLarkTransport(LarkBotIdentity{OpenID: "ou_bot", Name: "Bot"})
	transport.UpdateErr = errors.New("update failed")
	transport.SendErr = errors.New("send failed")
	intake := newManagedLarkIntake(managedLarkIntakeOptions{
		Transport: transport,
		Managed: LarkManagedOptions{
			AccountReconnect: 10 * time.Millisecond,
			CommandOptions: CommandOptions{
				Reconnector: CommandReconnectorFunc(func(_ context.Context, wait bool) error {
					reconnected <- wait
					return nil
				}),
			},
		},
	})
	defer intake.Close()

	err := intake.sendCommandResponse(context.Background(), appintake.MessageInput{
		MessageID:      "om_account_submit",
		ChatID:         "oc_dm",
		RawContentType: "card_action",
	}, appintake.Scope{Key: "oc_dm", ChatMode: appintake.ChatModeP2P}, CommandResponse{
		Kind:    CommandResponseAccount,
		Handled: true,
		Account: &CommandAccountView{Action: "submit", AppID: "cli_new", Tenant: "lark", BotName: "Bridge Bot", Saved: true},
	})
	if err == nil {
		t.Fatalf("sendCommandResponse error = nil, want delivery error")
	}
	select {
	case wait := <-reconnected:
		if wait {
			t.Fatalf("account reconnect wait = true, want false")
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for reconnect after card delivery failure")
	}
}

func TestBridgeManagedLarkIntakeAccountFailureUpdatesOldCardAndSendsRetryForm(t *testing.T) {
	transport := NewFakeLarkTransport(LarkBotIdentity{OpenID: "ou_bot", Name: "Bot"})
	intake := newManagedLarkIntake(managedLarkIntakeOptions{Transport: transport})
	defer intake.Close()

	if err := intake.sendCommandResponse(context.Background(), appintake.MessageInput{
		MessageID:      "om_account_submit",
		ChatID:         "oc_dm",
		RawContentType: "card_action",
	}, appintake.Scope{Key: "oc_dm", ChatMode: appintake.ChatModeP2P}, CommandResponse{
		Kind:    CommandResponseAccount,
		Handled: true,
		Account: &CommandAccountView{
			Action:  "submit",
			AppID:   "cli_new",
			Tenant:  "lark",
			Failure: "App 凭据校验失败",
		},
	}); err != nil {
		t.Fatalf("sendCommandResponse returned error: %v", err)
	}

	updates := waitBridgeUpdatedCards(t, transport, 1)
	if updates[0].MessageID != "om_account_submit" || !strings.Contains(mustBridgeCardJSON(t, updates[0].Card), "App 凭据校验失败") {
		t.Fatalf("failure update = %#v", updates)
	}
	retry := waitBridgeSentCards(t, transport, 1)
	retryJSON := mustBridgeCardJSON(t, retry[0].Card)
	if retry[0].ChatID != "oc_dm" || retry[0].Options.ReplyTo != "om_account_submit" || !strings.Contains(retryJSON, "account.submit") || !strings.Contains(retryJSON, "cli_new") {
		t.Fatalf("retry form = %#v json=%s", retry[0], retryJSON)
	}
	if strings.Contains(retryJSON, "new-secret") || strings.Contains(retryJSON, "App 凭据校验失败") {
		t.Fatalf("retry form leaked sensitive/error state: %s", retryJSON)
	}
}

func TestBridgeManagedLarkIntakeDeduplicatesPendingAccountReconnect(t *testing.T) {
	reconnected := make(chan bool, 2)
	intake := newManagedLarkIntake(managedLarkIntakeOptions{
		Managed: LarkManagedOptions{
			AccountReconnect: 50 * time.Millisecond,
			CommandOptions: CommandOptions{
				Reconnector: CommandReconnectorFunc(func(_ context.Context, wait bool) error {
					reconnected <- wait
					return nil
				}),
			},
		},
	})
	defer intake.Close()

	msg := appintake.MessageInput{MessageID: "om_account_submit", ChatID: "oc_dm"}
	response := CommandResponse{
		Kind:    CommandResponseAccount,
		Handled: true,
		Account: &CommandAccountView{Action: "submit", AppID: "cli_new", Saved: true},
	}
	intake.scheduleAccountReconnect(msg, response)
	intake.scheduleAccountReconnect(msg, response)

	select {
	case <-reconnected:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for first reconnect")
	}
	select {
	case wait := <-reconnected:
		t.Fatalf("duplicate reconnect fired, wait=%v", wait)
	case <-time.After(120 * time.Millisecond):
	}
}

func TestBridgeManagedLarkIntakeCloseCancelsPendingAccountReconnect(t *testing.T) {
	reconnected := make(chan bool, 1)
	intake := newManagedLarkIntake(managedLarkIntakeOptions{
		Managed: LarkManagedOptions{
			AccountReconnect: 200 * time.Millisecond,
			CommandOptions: CommandOptions{
				Reconnector: CommandReconnectorFunc(func(_ context.Context, wait bool) error {
					reconnected <- wait
					return nil
				}),
			},
		},
	})
	intake.scheduleAccountReconnect(appintake.MessageInput{MessageID: "om_account_submit"}, CommandResponse{
		Kind:    CommandResponseAccount,
		Handled: true,
		Account: &CommandAccountView{Action: "submit", AppID: "cli_new", Saved: true},
	})
	intake.Close()

	select {
	case wait := <-reconnected:
		t.Fatalf("reconnect fired after Close, wait=%v", wait)
	case <-time.After(80 * time.Millisecond):
	}
}

func TestBridgeManagedLarkIntakeCloseIsBoundedWhenReconnectorIgnoresContext(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	intake := newManagedLarkIntake(managedLarkIntakeOptions{
		Managed: LarkManagedOptions{
			AccountReconnect: time.Millisecond,
			CloseTimeout:     20 * time.Millisecond,
			CommandOptions: CommandOptions{
				Reconnector: CommandReconnectorFunc(func(context.Context, bool) error {
					close(started)
					<-release
					return nil
				}),
			},
		},
	})
	intake.scheduleAccountReconnect(appintake.MessageInput{MessageID: "om_account_submit"}, CommandResponse{
		Kind:    CommandResponseAccount,
		Handled: true,
		Account: &CommandAccountView{Action: "submit", AppID: "cli_new", Saved: true},
	})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for blocking reconnect")
	}
	start := time.Now()
	intake.Close()
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("Close blocked for %s", elapsed)
	}
	close(release)
}

func TestBridgeManagedLarkIntakePromptsGroupMsgScopeAfterConfigSave(t *testing.T) {
	grantDone := make(chan struct{})
	transport := &bridgeScopeTransport{
		FakeLarkTransport: NewFakeLarkTransport(LarkBotIdentity{OpenID: "ou_bot", Name: "Bot"}),
		check: LarkScopeCheckerFunc(func(_ context.Context, appID string, scope string) (bool, error) {
			if appID != "cli_bridge_test" || scope != LarkGroupMsgScope {
				return false, errors.New("unexpected scope check")
			}
			return false, nil
		}),
		grant: LarkScopeGrantRequesterFunc(func(_ context.Context, req LarkScopeGrantRequest) (LarkScopeGrantLink, error) {
			if req.AppID != "cli_bridge_test" || len(req.TenantScopes) != 1 || req.TenantScopes[0] != LarkGroupMsgScope {
				return LarkScopeGrantLink{}, errors.New("unexpected scope grant request")
			}
			return LarkScopeGrantLink{
				URL:       "https://example.com/grant",
				ExpiresIn: 5 * time.Minute,
				Wait: func(ctx context.Context) error {
					select {
					case <-grantDone:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				},
			}, nil
		}),
	}
	intake := newManagedLarkIntake(managedLarkIntakeOptions{
		Transport: transport,
		AppID:     "cli_bridge_test",
	})
	defer intake.Close()

	if err := intake.sendCommandResponse(context.Background(), appintake.MessageInput{
		MessageID:      "om_config_submit",
		ChatID:         "oc_group",
		RawContentType: "card_action",
	}, appintake.Scope{Key: "oc_group", ChatMode: appintake.ChatModeGroup}, CommandResponse{
		Kind:    CommandResponseConfig,
		Handled: true,
		Config: &CommandConfigView{
			Action: "submit",
			Saved:  true,
			Snapshot: CommandConfigSnapshotView{
				MessageReply:          "markdown",
				CotMessages:           "detailed",
				MaxConcurrentRuns:     1,
				RequireMentionInGroup: false,
				LarkCLIIdentity:       "bot-only",
			},
		},
	}); err != nil {
		t.Fatalf("sendCommandResponse returned error: %v", err)
	}
	updates := waitBridgeUpdatedCards(t, transport.FakeLarkTransport, 1)
	if updates[0].MessageID != "om_config_submit" || !strings.Contains(mustBridgeCardJSON(t, updates[0].Card), "偏好已保存") {
		t.Fatalf("config update = %#v", updates)
	}
	grants := waitBridgeSentCards(t, transport.FakeLarkTransport, 1)
	if grants[0].ChatID != "oc_group" || !strings.Contains(mustBridgeCardJSON(t, grants[0].Card), "im:message.group_msg") {
		t.Fatalf("grant card = %#v", grants)
	}
	close(grantDone)
	updates = waitBridgeUpdatedCards(t, transport.FakeLarkTransport, 2)
	if updates[1].MessageID != "om_fake_1" || !strings.Contains(mustBridgeCardJSON(t, updates[1].Card), "授权成功") {
		t.Fatalf("granted update = %#v", updates[1])
	}
}

func TestBridgeManagedLarkIntakePromptsGroupMsgScopeWhenConfigCardDeliveryFails(t *testing.T) {
	transport := &bridgeFailFirstSendTransport{
		bridgeScopeTransport: &bridgeScopeTransport{
			FakeLarkTransport: NewFakeLarkTransport(LarkBotIdentity{OpenID: "ou_bot", Name: "Bot"}),
			check: LarkScopeCheckerFunc(func(context.Context, string, string) (bool, error) {
				return false, nil
			}),
			grant: LarkScopeGrantRequesterFunc(func(context.Context, LarkScopeGrantRequest) (LarkScopeGrantLink, error) {
				return LarkScopeGrantLink{URL: "https://example.com/grant", ExpiresIn: time.Minute}, nil
			}),
		},
		failSendCount: 1,
	}
	transport.UpdateErr = errors.New("update failed")
	intake := newManagedLarkIntake(managedLarkIntakeOptions{
		Transport: transport,
		AppID:     "cli_bridge_test",
	})
	defer intake.Close()

	err := intake.sendCommandResponse(context.Background(), appintake.MessageInput{
		MessageID:      "om_config_submit",
		ChatID:         "oc_group",
		RawContentType: "card_action",
	}, appintake.Scope{Key: "oc_group", ChatMode: appintake.ChatModeGroup}, CommandResponse{
		Kind:    CommandResponseConfig,
		Handled: true,
		Config: &CommandConfigView{
			Action: "submit",
			Saved:  true,
			Snapshot: CommandConfigSnapshotView{
				MessageReply:          "markdown",
				CotMessages:           "detailed",
				MaxConcurrentRuns:     1,
				RequireMentionInGroup: false,
				LarkCLIIdentity:       "bot-only",
			},
		},
	})
	if err == nil {
		t.Fatalf("sendCommandResponse error = nil, want config card delivery error")
	}
	grants := waitBridgeSentCards(t, transport.FakeLarkTransport, 1)
	if grants[0].ChatID != "oc_group" || !strings.Contains(mustBridgeCardJSON(t, grants[0].Card), "im:message.group_msg") {
		t.Fatalf("grant card = %#v", grants[0])
	}
}

func TestBridgeManagedLarkIntakeClonesDetachedCardActionMaps(t *testing.T) {
	input := LarkCardActionInput{
		ActionValue: map[string]any{"cmd": "account.submit", "nested": map[string]any{"k": "v"}},
		FormValue:   map[string]any{"app_id": "cli_original", "items": []any{map[string]any{"x": "y"}}},
	}
	cloned := cloneManagedCardActionInput(input)

	input.ActionValue["cmd"] = "status"
	input.ActionValue["nested"].(map[string]any)["k"] = "mutated"
	input.FormValue["app_id"] = "cli_mutated"
	input.FormValue["items"].([]any)[0].(map[string]any)["x"] = "mutated"

	if cloned.ActionValue["cmd"] != "account.submit" || cloned.ActionValue["nested"].(map[string]any)["k"] != "v" {
		t.Fatalf("cloned action value mutated: %#v", cloned.ActionValue)
	}
	if cloned.FormValue["app_id"] != "cli_original" || cloned.FormValue["items"].([]any)[0].(map[string]any)["x"] != "y" {
		t.Fatalf("cloned form value mutated: %#v", cloned.FormValue)
	}
}

func TestBridgeManagedLarkIntakeHandlesCommandBeforeRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake uses POSIX sh")
	}
	ctx := context.Background()
	root := t.TempDir()
	cwd := t.TempDir()
	binary := writeBridgeFakeCodex(t, `
cat > prompt.txt
printf '%s\n' '{"type":"agent_message","message":"should not run"}'
printf '%s\n' '{"type":"turn.completed"}'
`)
	client, err := NewCodexClient(CodexClientOptions{
		Binary:             binary,
		ProfileStateDir:    filepath.Join(root, "profiles", "codex"),
		DefaultWorkingDir:  cwd,
		SessionStorePath:   filepath.Join(root, "sessions.json"),
		SessionCatalogPath: filepath.Join(root, "sessions.catalog.json"),
		AllowedUsers:       []string{"ou_user"},
	})
	if err != nil {
		t.Fatalf("NewCodexClient returned error: %v", err)
	}
	transport := NewFakeLarkTransport(LarkBotIdentity{OpenID: "ou_bot", Name: "Bot"})
	bridge, err := New(Options{
		Home:          root,
		Profile:       "codex",
		Client:        client,
		LarkTransport: transport,
		AppID:         "cli_bridge_test",
		Tenant:        RuntimeTenantFeishu,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if err := bridge.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	t.Cleanup(func() {
		_ = bridge.Shutdown(context.Background())
	})

	if err := transport.Emit(ctx, LarkIncomingEvent{
		Kind: LarkEventMessage,
		Message: &LarkMessageInput{
			MessageID: "om_cmd",
			ChatID:    "oc_dm",
			ChatType:  LarkChatTypeP2P,
			Sender:    LarkActor{OpenID: "ou_user"},
			Content:   "/help",
		},
	}); err != nil {
		t.Fatalf("Emit command returned error: %v", err)
	}
	messages := waitBridgeSentMessages(t, transport, 1)
	if !strings.Contains(messages[0].Content.Markdown, "/status") || !strings.Contains(messages[0].Content.Markdown, "/new") {
		t.Fatalf("command reply markdown = %q", messages[0].Content.Markdown)
	}
	if _, err := os.Stat(filepath.Join(cwd, "prompt.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("prompt file stat err = %v, want not exist", err)
	}
}

func TestBridgeManagedLarkIntakeDropsUnmentionedGroupMessage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake uses POSIX sh")
	}
	ctx := context.Background()
	root := t.TempDir()
	cwd := t.TempDir()
	binary := writeBridgeFakeCodex(t, `
cat > prompt.txt
printf '%s\n' '{"type":"agent_message","message":"should not run"}'
printf '%s\n' '{"type":"turn.completed"}'
`)
	client, err := NewCodexClient(CodexClientOptions{
		Binary:             binary,
		ProfileStateDir:    filepath.Join(root, "profiles", "codex"),
		DefaultWorkingDir:  cwd,
		SessionStorePath:   filepath.Join(root, "sessions.json"),
		SessionCatalogPath: filepath.Join(root, "sessions.catalog.json"),
		AllowedChats:       []string{"oc_group"},
	})
	if err != nil {
		t.Fatalf("NewCodexClient returned error: %v", err)
	}
	transport := NewFakeLarkTransport(LarkBotIdentity{OpenID: "ou_bot", Name: "Bot"})
	bridge, err := New(Options{
		Home:          root,
		Profile:       "codex",
		Client:        client,
		LarkTransport: transport,
		LarkManaged: LarkManagedOptions{
			MessageQuietPeriod: time.Millisecond,
		},
		AppID:  "cli_bridge_test",
		Tenant: RuntimeTenantFeishu,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if err := bridge.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	t.Cleanup(func() {
		_ = bridge.Shutdown(context.Background())
	})

	if err := transport.Emit(ctx, LarkIncomingEvent{
		Kind: LarkEventMessage,
		Message: &LarkMessageInput{
			MessageID:    "om_group",
			ChatID:       "oc_group",
			ChatType:     LarkChatTypeGroup,
			ResolvedMode: LarkChatModeGroup,
			Sender:       LarkActor{OpenID: "ou_user"},
			Content:      "quiet chatter",
		},
	}); err != nil {
		t.Fatalf("Emit group message returned error: %v", err)
	}
	time.Sleep(25 * time.Millisecond)
	if got := transport.SentMessageSnapshot(); len(got) != 0 {
		t.Fatalf("sent messages = %#v, want none", got)
	}
	if _, err := os.Stat(filepath.Join(cwd, "prompt.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("prompt file stat err = %v, want not exist", err)
	}
}

func TestBridgeManagedLarkIntakeBlocksQueueDuringActiveRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake uses POSIX sh")
	}
	ctx := context.Background()
	root := t.TempDir()
	cwd := t.TempDir()
	binary := writeBridgeFakeCodex(t, `
prompt=$(cat)
printf '%s\n---prompt---\n' "$prompt" >> prompts.txt
sleep 0.15
printf '%s\n' '{"type":"agent_message","message":"done"}'
printf '%s\n' '{"type":"turn.completed"}'
`)
	client, err := NewCodexClient(CodexClientOptions{
		Binary:             binary,
		ProfileStateDir:    filepath.Join(root, "profiles", "codex"),
		DefaultWorkingDir:  cwd,
		SessionStorePath:   filepath.Join(root, "sessions.json"),
		SessionCatalogPath: filepath.Join(root, "sessions.catalog.json"),
		AllowedUsers:       []string{"ou_user"},
	})
	if err != nil {
		t.Fatalf("NewCodexClient returned error: %v", err)
	}
	transport := NewFakeLarkTransport(LarkBotIdentity{OpenID: "ou_bot", Name: "Bot"})
	bridge, err := New(Options{
		Home:          root,
		Profile:       "codex",
		Client:        client,
		LarkTransport: transport,
		LarkManaged: LarkManagedOptions{
			MessageQuietPeriod: time.Millisecond,
		},
		AppID:  "cli_bridge_test",
		Tenant: RuntimeTenantFeishu,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if err := bridge.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	t.Cleanup(func() {
		_ = bridge.Shutdown(context.Background())
	})

	emitManagedTestMessage(t, ctx, transport, "om_1", "first")
	waitBridgeFile(t, filepath.Join(cwd, "prompts.txt"))
	emitManagedTestMessage(t, ctx, transport, "om_2", "second")
	time.Sleep(10 * time.Millisecond)
	emitManagedTestMessage(t, ctx, transport, "om_3", "third")

	messages := waitBridgeSentMessages(t, transport, 2)
	time.Sleep(250 * time.Millisecond)
	if got := transport.SentMessageSnapshot(); len(got) != 2 {
		t.Fatalf("sent messages after blocked active run = %#v, initially %#v", got, messages)
	}
	prompts := readBridgeFile(t, filepath.Join(cwd, "prompts.txt"))
	if count := strings.Count(prompts, "---prompt---"); count != 2 {
		t.Fatalf("run count from prompts = %d, want 2\n%s", count, prompts)
	}
	if !strings.Contains(prompts, "second") || !strings.Contains(prompts, "third") {
		t.Fatalf("second batch did not merge active-run messages:\n%s", prompts)
	}
}

func TestBridgeManagedLarkIntakeResolvesAttachmentsIntoPrompt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake uses POSIX sh")
	}
	ctx := context.Background()
	root := t.TempDir()
	cwd := t.TempDir()
	binary := writeBridgeFakeCodex(t, `
cat > prompt.txt
printf '%s\n' '{"type":"agent_message","message":"saw attachment"}'
printf '%s\n' '{"type":"turn.completed"}'
`)
	client, err := NewCodexClient(CodexClientOptions{
		Binary:             binary,
		ProfileStateDir:    filepath.Join(root, "profiles", "codex"),
		DefaultWorkingDir:  cwd,
		SessionStorePath:   filepath.Join(root, "sessions.json"),
		SessionCatalogPath: filepath.Join(root, "sessions.catalog.json"),
		AllowedUsers:       []string{"ou_user"},
	})
	if err != nil {
		t.Fatalf("NewCodexClient returned error: %v", err)
	}
	transport := NewFakeLarkTransport(LarkBotIdentity{OpenID: "ou_bot", Name: "Bot"})
	transport.SetResourceDownload("file_key_1", []byte("attachment body"), "text/plain")
	bridge, err := New(Options{
		Home:          root,
		Profile:       "codex",
		Client:        client,
		LarkTransport: transport,
		LarkManaged: LarkManagedOptions{
			MessageQuietPeriod: time.Millisecond,
		},
		AppID:  "cli_bridge_test",
		Tenant: RuntimeTenantFeishu,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if err := bridge.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	t.Cleanup(func() {
		_ = bridge.Shutdown(context.Background())
	})

	if err := transport.Emit(ctx, LarkIncomingEvent{
		Kind: LarkEventMessage,
		Message: &LarkMessageInput{
			MessageID: "om_attach",
			ChatID:    "oc_dm",
			ChatType:  LarkChatTypeP2P,
			Sender:    LarkActor{OpenID: "ou_user"},
			Content:   "please inspect",
			Resources: []LarkResource{{
				Kind: "file",
				ID:   "file_key_1",
				Name: "note.txt",
			}},
		},
	}); err != nil {
		t.Fatalf("Emit attachment message returned error: %v", err)
	}
	_ = waitBridgeSentMessages(t, transport, 1)
	prompt := readBridgeFile(t, filepath.Join(cwd, "prompt.txt"))
	for _, fragment := range []string{"attachments", "om_attach", "accepted", "optional", "text/plain"} {
		if !strings.Contains(prompt, fragment) {
			t.Fatalf("attachment prompt missing %q in:\n%s", fragment, prompt)
		}
	}
}

func TestBridgeManagedLarkIntakeKeepsRejectedAttachmentsOptional(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake uses POSIX sh")
	}
	ctx := context.Background()
	root := t.TempDir()
	cwd := t.TempDir()
	binary := writeBridgeFakeCodex(t, `
cat > prompt.txt
printf '%s\n' '{"type":"agent_message","message":"continued despite attachment"}'
printf '%s\n' '{"type":"turn.completed"}'
`)
	client, err := NewCodexClient(CodexClientOptions{
		Binary:             binary,
		ProfileStateDir:    filepath.Join(root, "profiles", "codex"),
		DefaultWorkingDir:  cwd,
		SessionStorePath:   filepath.Join(root, "sessions.json"),
		SessionCatalogPath: filepath.Join(root, "sessions.catalog.json"),
		AllowedUsers:       []string{"ou_user"},
	})
	if err != nil {
		t.Fatalf("NewCodexClient returned error: %v", err)
	}
	transport := NewFakeLarkTransport(LarkBotIdentity{OpenID: "ou_bot", Name: "Bot"})
	transport.SetResourceDownload("file_key_1", []byte("too large"), "text/plain")
	bridge, err := New(Options{
		Home:          root,
		Profile:       "codex",
		Client:        client,
		LarkTransport: transport,
		LarkManaged: LarkManagedOptions{
			MessageQuietPeriod: time.Millisecond,
		},
		AppID:  "cli_bridge_test",
		Tenant: RuntimeTenantFeishu,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if err := bridge.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	t.Cleanup(func() {
		_ = bridge.Shutdown(context.Background())
	})
	client.profile.Attachments.MaxFileBytes = 1

	if err := transport.Emit(ctx, LarkIncomingEvent{
		Kind: LarkEventMessage,
		Message: &LarkMessageInput{
			MessageID: "om_attach",
			ChatID:    "oc_dm",
			ChatType:  LarkChatTypeP2P,
			Sender:    LarkActor{OpenID: "ou_user"},
			Content:   "please inspect",
			Resources: []LarkResource{{
				Kind: "file",
				ID:   "file_key_1",
				Name: "note.txt",
			}},
		},
	}); err != nil {
		t.Fatalf("Emit attachment message returned error: %v", err)
	}
	messages := waitBridgeSentMessages(t, transport, 1)
	finalContent := messages[0].Content.Markdown + messages[0].Content.Text
	if !strings.Contains(finalContent, "continued despite attachment") {
		t.Fatalf("final reply = %#v", messages[0])
	}
	prompt := readBridgeFile(t, filepath.Join(cwd, "prompt.txt"))
	for _, fragment := range []string{"attachments", "om_attach", "optional", "rejected", "file-too-large"} {
		if !strings.Contains(prompt, fragment) {
			t.Fatalf("attachment prompt missing %q in:\n%s", fragment, prompt)
		}
	}
}

func writeBridgeFakeCodex(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "codex")
	preamble := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then printf '%s\\n' 'codex 0.0.0'; exit 0; fi\n"
	if err := os.WriteFile(path, []byte(preamble+body), 0o700); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	return path
}

func collectBridgeEvents(t *testing.T, run *Run) []Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var events []Event
	eventCh := run.Events(ctx)
	for {
		select {
		case event, ok := <-eventCh:
			if !ok {
				return events
			}
			events = append(events, event)
		case <-ctx.Done():
			t.Fatalf("timed out collecting bridge events")
		}
	}
}

func readBridgeFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func writeBridgeAccountRoot(t *testing.T, path string, cwd string, codexBinary string) {
	t.Helper()
	root := map[string]any{
		"schemaVersion": 2,
		"activeProfile": "codex",
		"preferences":   map[string]any{},
		"profiles": map[string]any{
			"codex": map[string]any{
				"schemaVersion": 2,
				"agentKind":     "codex",
				"accounts": map[string]any{
					"app": map[string]any{
						"id":     "cli_old",
						"secret": "old-secret",
						"tenant": "feishu",
					},
				},
				"access": map[string]any{
					"allowedUsers":          []string{"ou_user"},
					"allowedChats":          []string{},
					"admins":                []string{"ou_user"},
					"requireMentionInGroup": true,
				},
				"workspaces":  map[string]any{"default": cwd},
				"preferences": map[string]any{},
				"codex": map[string]any{
					"binaryPath":       codexBinary,
					"inheritCodexHome": true,
				},
				"larkCli": map[string]any{"identityPreset": "bot-only"},
			},
		},
	}
	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		t.Fatalf("marshal root: %v", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

type bridgeScopeTransport struct {
	*FakeLarkTransport
	check LarkScopeChecker
	grant LarkScopeGrantRequester
}

func (t *bridgeScopeTransport) HasLarkScope(ctx context.Context, appID string, scope string) (bool, error) {
	return t.check.HasLarkScope(ctx, appID, scope)
}

func (t *bridgeScopeTransport) RequestLarkScopeGrant(ctx context.Context, req LarkScopeGrantRequest) (LarkScopeGrantLink, error) {
	return t.grant.RequestLarkScopeGrant(ctx, req)
}

type bridgeFailFirstSendTransport struct {
	*bridgeScopeTransport
	mu            sync.Mutex
	failSendCount int
}

func (t *bridgeFailFirstSendTransport) SendCard(ctx context.Context, req LarkSendCardRequest) (LarkSendResult, error) {
	t.mu.Lock()
	if t.failSendCount > 0 {
		t.failSendCount--
		t.mu.Unlock()
		return LarkSendResult{}, errors.New("send failed once")
	}
	t.mu.Unlock()
	return t.FakeLarkTransport.SendCard(ctx, req)
}

func waitBridgeFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.After(bridgeTestWaitTimeout())
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for %s", path)
		case <-tick.C:
		}
	}
}

func waitBridgeSentMessages(t *testing.T, transport *FakeLarkTransport, count int) []LarkSendMessageRequest {
	t.Helper()
	deadline := time.After(bridgeTestWaitTimeout())
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		messages := transport.SentMessageSnapshot()
		if len(messages) >= count {
			return messages
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for sent messages; got %#v", messages)
		case <-tick.C:
		}
	}
}

func waitBridgeSentCards(t *testing.T, transport *FakeLarkTransport, count int) []LarkSendCardRequest {
	t.Helper()
	deadline := time.After(bridgeTestWaitTimeout())
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		cards := transport.SentCardSnapshot()
		if len(cards) >= count {
			return cards
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for sent cards; got %#v", cards)
		case <-tick.C:
		}
	}
}

func waitBridgeUpdatedCards(t *testing.T, transport *FakeLarkTransport, count int) []LarkUpdateCardRequest {
	t.Helper()
	deadline := time.After(bridgeTestWaitTimeout())
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		cards := transport.UpdatedCardSnapshot()
		if len(cards) >= count {
			return cards
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for updated cards; got %#v", cards)
		case <-tick.C:
		}
	}
}

func bridgeTestWaitTimeout() time.Duration {
	if bridgeTestRaceEnabled() {
		return 5 * time.Second
	}
	return 2 * time.Second
}

func mustBridgeCardJSON(t *testing.T, card map[string]any) string {
	t.Helper()
	data, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("marshal card: %v", err)
	}
	return string(data)
}

func findBridgeCallbackValue(t *testing.T, card map[string]any) map[string]any {
	t.Helper()
	value, ok := findMapWithKey(card, BridgeCardTokenKey)
	if !ok {
		t.Fatalf("card callback token not found in %#v", card)
	}
	return value
}

func findMapWithKey(value any, key string) (map[string]any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		if _, ok := typed[key]; ok {
			return typed, true
		}
		for _, child := range typed {
			if found, ok := findMapWithKey(child, key); ok {
				return found, true
			}
		}
	case []any:
		for _, child := range typed {
			if found, ok := findMapWithKey(child, key); ok {
				return found, true
			}
		}
	}
	return nil, false
}

func emitManagedTestMessage(t *testing.T, ctx context.Context, transport *FakeLarkTransport, messageID string, content string) {
	t.Helper()
	if err := transport.Emit(ctx, LarkIncomingEvent{
		Kind: LarkEventMessage,
		Message: &LarkMessageInput{
			MessageID: messageID,
			ChatID:    "oc_dm",
			ChatType:  LarkChatTypeP2P,
			Sender:    LarkActor{OpenID: "ou_user"},
			Content:   content,
		},
	}); err != nil {
		t.Fatalf("Emit message %s returned error: %v", messageID, err)
	}
}

type fakeBridgeCommentSurface struct {
	target  CommentTarget
	thread  CommentThread
	replies []fakeBridgeCommentReply
}

type fakeBridgeCommentReply struct {
	target    CommentTarget
	commentID string
	text      string
	topLevel  bool
}

func newFakeBridgeCommentSurface() *fakeBridgeCommentSurface {
	return &fakeBridgeCommentSurface{
		thread: CommentThread{
			Replies: []CommentReply{{
				ReplyID: "reply_1",
				Content: CommentReplyContent{Elements: []CommentReplyElement{{
					Type:    "text_run",
					TextRun: &CommentReplyTextRun{Text: "请总结"},
				}}},
			}},
			Quote: "原文",
		},
	}
}

func (s *fakeBridgeCommentSurface) ResolveCommentTarget(_ context.Context, fileToken string, fileType string) (CommentTarget, bool, error) {
	if s.target.FileToken != "" {
		return s.target, true, nil
	}
	return CommentTarget{FileToken: fileToken, FileType: fileType}, true, nil
}

func (s *fakeBridgeCommentSurface) FetchComment(context.Context, CommentTarget, string) (CommentThread, error) {
	return s.thread, nil
}

func (s *fakeBridgeCommentSurface) ReplyToComment(_ context.Context, target CommentTarget, commentID string, text string, opts CommentReplyOptions) error {
	s.replies = append(s.replies, fakeBridgeCommentReply{
		target:    target,
		commentID: commentID,
		text:      text,
		topLevel:  opts.TopLevel,
	})
	return nil
}

func (s *fakeBridgeCommentSurface) AddCommentReaction(context.Context, CommentTarget, string) (bool, error) {
	return false, nil
}

func (s *fakeBridgeCommentSurface) RemoveCommentReaction(context.Context, CommentTarget, string) error {
	return nil
}
