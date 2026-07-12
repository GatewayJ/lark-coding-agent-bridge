package bridge

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	appcot "github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/cotpresenter"
	appimpresenter "github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/impresenter"
	appintake "github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/intake"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/profile"
	agentport "github.com/zarazhangrui/lark-coding-agent-bridge/internal/ports/agent"
)

func writeManagedRuntimeConfig(t *testing.T, allowedChats []string, requireMention bool) string {
	t.Helper()
	root := t.TempDir()
	_, err := BootstrapProfileConfig(BootstrapProfileOptions{
		RootDir:          root,
		Profile:          "codex",
		AgentKind:        ConfigAgentCodex,
		AppID:            "cli_bridge_test",
		AppSecret:        PlainSecret("secret"),
		DefaultWorkspace: root,
		Access: ConfigProfileAccess{
			AllowedChats: append([]string(nil), allowedChats...),
		},
		RequireMention: &requireMention,
	})
	if err != nil {
		t.Fatalf("BootstrapProfileConfig returned error: %v", err)
	}
	return filepath.Join(root, "config.json")
}

func updateManagedRuntimeAccess(t *testing.T, configPath string, mutate func(access *ConfigProfileAccess)) {
	t.Helper()
	snapshot, err := LoadConfig(configPath, ConfigLoadOptions{Profile: "codex", AgentKind: ConfigAgentCodex})
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}
	prof, ok := snapshot.Root.Profiles["codex"]
	if !ok {
		t.Fatalf("config profile codex missing")
	}
	mutate(&prof.Access)
	snapshot.Root.Profiles["codex"] = prof
	if err := SaveConfig(configPath, snapshot.Root); err != nil {
		t.Fatalf("SaveConfig returned error: %v", err)
	}
}

func updateManagedRuntimeProfile(t *testing.T, configPath string, mutate func(profile *ConfigProfile)) {
	t.Helper()
	snapshot, err := LoadConfig(configPath, ConfigLoadOptions{Profile: "codex", AgentKind: ConfigAgentCodex})
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}
	prof, ok := snapshot.Root.Profiles["codex"]
	if !ok {
		t.Fatalf("config profile codex missing")
	}
	mutate(&prof)
	snapshot.Root.Profiles["codex"] = prof
	if err := SaveConfig(configPath, snapshot.Root); err != nil {
		t.Fatalf("SaveConfig returned error: %v", err)
	}
}

func TestManagedLarkIntakeDefaultsQuoteResolverFromTransport(t *testing.T) {
	transport := &quoteResolvingTransport{
		FakeLarkTransport: NewFakeLarkTransport(LarkBotIdentity{}),
		content:           "transport quote",
	}
	intake := newManagedLarkIntake(managedLarkIntakeOptions{
		Transport: transport,
		Managed:   LarkManagedOptions{},
	})

	msg := LarkMessageInput{
		MessageID:        "om_reply",
		ChatID:           "oc_group",
		ChatType:         LarkChatTypeGroup,
		ResolvedMode:     LarkChatModeGroup,
		ParentID:         "om_quote",
		ReplyToMessageID: "om_quote",
	}
	quotes := intake.resolveQuotedMessages(context.Background(), toInternalLarkScope(LarkMessageScope(msg)), toInternalLarkMessages([]LarkMessageInput{msg}))
	if len(quotes) != 1 || quotes[0].Content != "transport quote" || !transport.called {
		t.Fatalf("quotes = %#v transport.called=%v", quotes, transport.called)
	}
}

func TestManagedLarkIntakeExplicitQuoteResolverWinsOverTransportDefault(t *testing.T) {
	transport := &quoteResolvingTransport{
		FakeLarkTransport: NewFakeLarkTransport(LarkBotIdentity{}),
		content:           "transport quote",
	}
	intake := newManagedLarkIntake(managedLarkIntakeOptions{
		Transport: transport,
		Managed: LarkManagedOptions{
			QuoteResolver: LarkQuoteResolverFunc(func(context.Context, LarkQuoteTarget) (BridgePromptQuotedMessage, bool, error) {
				return BridgePromptQuotedMessage{MessageID: "om_quote", RawContentType: "text", Content: "explicit quote"}, true, nil
			}),
		},
	})

	msg := LarkMessageInput{
		MessageID:        "om_reply",
		ChatID:           "oc_group",
		ChatType:         LarkChatTypeGroup,
		ResolvedMode:     LarkChatModeGroup,
		ParentID:         "om_quote",
		ReplyToMessageID: "om_quote",
	}
	quotes := intake.resolveQuotedMessages(context.Background(), toInternalLarkScope(LarkMessageScope(msg)), toInternalLarkMessages([]LarkMessageInput{msg}))
	if len(quotes) != 1 || quotes[0].Content != "explicit quote" || transport.called {
		t.Fatalf("quotes = %#v transport.called=%v", quotes, transport.called)
	}
}

func TestManagedLarkIntakeResolvesNonRootTopicReplyQuote(t *testing.T) {
	targets := make(chan LarkQuoteTarget, 1)
	intake := newManagedLarkIntake(managedLarkIntakeOptions{
		Transport: NewFakeLarkTransport(LarkBotIdentity{}),
		Managed: LarkManagedOptions{
			QuoteResolver: LarkQuoteResolverFunc(func(_ context.Context, target LarkQuoteTarget) (BridgePromptQuotedMessage, bool, error) {
				targets <- target
				return BridgePromptQuotedMessage{MessageID: target.MessageID, RawContentType: "text", Content: "topic parent content"}, true, nil
			}),
		},
	})

	msg := LarkMessageInput{
		MessageID:        "om_reply",
		ChatID:           "oc_topic",
		ChatType:         LarkChatTypeGroup,
		ResolvedMode:     LarkChatModeTopic,
		ThreadID:         "omt_topic",
		RootID:           "om_topic_root",
		ParentID:         "om_topic_parent",
		ReplyToMessageID: "om_topic_parent",
	}
	quotes := intake.resolveQuotedMessages(context.Background(), toInternalLarkScope(LarkMessageScope(msg)), toInternalLarkMessages([]LarkMessageInput{msg}))
	if len(quotes) != 1 || quotes[0].Content != "topic parent content" {
		t.Fatalf("quotes = %#v", quotes)
	}
	select {
	case target := <-targets:
		if target.MessageID != "om_topic_parent" || target.ThreadID != "omt_topic" || target.RootID != "om_topic_root" || target.ParentID != "om_topic_parent" {
			t.Fatalf("quote target = %#v", target)
		}
	default:
		t.Fatal("quote resolver was not called")
	}
}

func TestManagedLarkIntakeDefaultsRuntimeInfoFromTransport(t *testing.T) {
	transport := &runtimeInfoTransport{
		FakeLarkTransport: NewFakeLarkTransport(LarkBotIdentity{}),
		owner:             "ou_owner",
		chats:             []LarkKnownChatInfo{{ID: "oc_group", Name: "工程群"}},
	}
	intake := newManagedLarkIntake(managedLarkIntakeOptions{
		Transport: transport,
		AppID:     "cli_bridge_test",
		Managed:   LarkManagedOptions{},
	})
	defer intake.Close()

	intake.refreshRuntimeInfo(context.Background())

	options := intake.currentCommandOptions()
	if options.RuntimeControls.BotOwnerID != "ou_owner" || options.RuntimeControls.OwnerRefreshState != "ok" || options.RuntimeControls.OwnerRefreshedAt == 0 || options.RuntimeControls.OwnerRefreshError != "" {
		t.Fatalf("runtime controls = %#v", options.RuntimeControls)
	}
	if len(options.KnownChats) != 1 || options.KnownChats[0].ID != "oc_group" || options.KnownChats[0].Name != "工程群" {
		t.Fatalf("known chats = %#v", options.KnownChats)
	}
	if options.ChatCreator != transport {
		t.Fatalf("chat creator = %#v, want transport", options.ChatCreator)
	}
	if transport.ownerAppID != "cli_bridge_test" || transport.ownerCalls != 1 || transport.chatCalls != 1 || transport.maxPages != 5 {
		t.Fatalf("runtime info calls ownerAppID=%q ownerCalls=%d chatCalls=%d maxPages=%d", transport.ownerAppID, transport.ownerCalls, transport.chatCalls, transport.maxPages)
	}
}

func TestManagedLarkIntakeKeepsCachedOwnerWhenRefreshReturnsEmpty(t *testing.T) {
	transport := &runtimeInfoTransport{
		FakeLarkTransport: NewFakeLarkTransport(LarkBotIdentity{}),
		owner:             "",
	}
	intake := newManagedLarkIntake(managedLarkIntakeOptions{
		Transport: transport,
		AppID:     "cli_bridge_test",
		Managed: LarkManagedOptions{CommandOptions: CommandOptions{
			RuntimeControls: RuntimeControls{
				BotOwnerID:        "ou_cached",
				OwnerRefreshState: "ok",
			},
		}},
	})

	intake.refreshRuntimeOwner(context.Background())

	options := intake.currentCommandOptions()
	if options.RuntimeControls.BotOwnerID != "ou_cached" {
		t.Fatalf("cached owner was cleared: %#v", options.RuntimeControls)
	}
	if options.RuntimeControls.OwnerRefreshState != "failed" || !strings.Contains(options.RuntimeControls.OwnerRefreshError, "owner missing") {
		t.Fatalf("runtime controls = %#v", options.RuntimeControls)
	}
}

func TestManagedLarkIntakeSeedsInitialOwnerOpenID(t *testing.T) {
	intake := newManagedLarkIntake(managedLarkIntakeOptions{
		Managed: LarkManagedOptions{InitialOwnerOpenID: " ou_seed "},
	})

	options := intake.currentCommandOptions()
	if options.RuntimeControls.BotOwnerID != "ou_seed" ||
		options.RuntimeControls.OwnerRefreshState != "ok" ||
		options.RuntimeControls.OwnerRefreshedAt == 0 ||
		options.RuntimeControls.OwnerRefreshError != "" {
		t.Fatalf("runtime controls = %#v", options.RuntimeControls)
	}
}

func TestManagedLarkIntakeUsesFetchedOwnerForAccess(t *testing.T) {
	client, err := NewCodexClient(CodexClientOptions{
		Binary:             "codex",
		ProfileStateDir:    t.TempDir(),
		SessionStorePath:   filepath.Join(t.TempDir(), "sessions.json"),
		SessionCatalogPath: filepath.Join(t.TempDir(), "sessions.catalog.json"),
	})
	if err != nil {
		t.Fatalf("NewCodexClient returned error: %v", err)
	}
	transport := &runtimeInfoTransport{
		FakeLarkTransport: NewFakeLarkTransport(LarkBotIdentity{}),
		owner:             "ou_owner",
	}
	intake := newManagedLarkIntake(managedLarkIntakeOptions{
		Client:    client,
		Transport: transport,
		AppID:     "cli_bridge_test",
	})
	defer intake.Close()
	if err := intake.Start(context.Background()); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	if err := intake.HandleLarkEvent(context.Background(), LarkNormalizedEvent{
		Kind: LarkEventMessage,
		Scope: LarkIntakeScope{
			Key:      "oc_dm",
			ChatMode: LarkChatModeP2P,
		},
		Message: &LarkMessageInput{
			MessageID: "om_help",
			ChatID:    "oc_dm",
			ChatType:  LarkChatTypeP2P,
			Sender:    LarkActor{OpenID: "ou_owner"},
			Content:   "/help",
		},
	}); err != nil {
		t.Fatalf("HandleLarkEvent returned error: %v", err)
	}

	messages := transport.SentMessageSnapshot()
	if len(messages) != 1 || !strings.Contains(messages[0].Content.Markdown, "/status") {
		t.Fatalf("sent messages = %#v", messages)
	}
}

func TestManagedLarkIntakeRuntimeInfoRefreshDoesNotBlockEvents(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{})
	transport := &blockingRuntimeInfoTransport{
		FakeLarkTransport: NewFakeLarkTransport(LarkBotIdentity{}),
		release:           release,
		entered:           entered,
	}
	intake := newManagedLarkIntake(managedLarkIntakeOptions{
		Transport: transport,
		AppID:     "cli_bridge_test",
	})

	start := time.Now()
	err := intake.HandleLarkEvent(context.Background(), LarkNormalizedEvent{
		Kind: LarkEventMessage,
		Scope: LarkIntakeScope{
			Key:      "oc_dm",
			ChatMode: LarkChatModeP2P,
		},
		Message: &LarkMessageInput{
			MessageID: "om_help",
			ChatID:    "oc_dm",
			ChatType:  LarkChatTypeP2P,
			Sender:    LarkActor{OpenID: "ou_owner"},
			Content:   "/help",
		},
	})
	if !errors.Is(err, ErrNilClient) {
		t.Fatalf("HandleLarkEvent error = %v, want ErrNilClient", err)
	}
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("HandleLarkEvent blocked on runtime refresh for %s", elapsed)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatalf("runtime refresh did not start")
	}
	close(release)
	intake.Close()
}

func TestManagedLarkIntakeUsesFetchedKnownChatsInConfigCard(t *testing.T) {
	transport := &runtimeInfoTransport{
		FakeLarkTransport: NewFakeLarkTransport(LarkBotIdentity{}),
		chats:             []LarkKnownChatInfo{{ID: "oc_group", Name: "工程群"}},
	}
	intake := newManagedLarkIntake(managedLarkIntakeOptions{
		Transport: transport,
		AppID:     "cli_bridge_test",
	})
	defer intake.Close()

	opts, err := intake.configCardOptions(context.Background(), &CommandConfigView{Snapshot: CommandConfigSnapshotView{
		MessageReply: "markdown",
		CotMessages:  "detailed",
	}})
	if err != nil {
		t.Fatalf("configCardOptions returned error: %v", err)
	}
	if len(opts.KnownChats) != 1 || opts.KnownChats[0].ID != "oc_group" || opts.KnownChats[0].Name != "工程群" {
		t.Fatalf("config known chats = %#v", opts.KnownChats)
	}
}

func TestManagedLarkIntakeReadsPresentationOptionsFromConfigFile(t *testing.T) {
	configPath := writeManagedRuntimeConfig(t, []string{"oc_group"}, true)
	client, err := NewCodexClient(CodexClientOptions{Binary: "codex", ProfileStateDir: t.TempDir(), SessionStorePath: filepath.Join(t.TempDir(), "sessions.json"), SessionCatalogPath: filepath.Join(t.TempDir(), "sessions.catalog.json")})
	if err != nil {
		t.Fatalf("NewCodexClient returned error: %v", err)
	}
	intake := newManagedLarkIntake(managedLarkIntakeOptions{Client: client, Managed: LarkManagedOptions{CommandOptions: CommandOptions{ProfileName: "codex", ConfigPath: configPath}}})
	updateManagedRuntimeProfile(t, configPath, func(profile *ConfigProfile) {
		profile.Preferences = map[string]any{
			"messageReply":          "text",
			"messageReplyMigrated":  true,
			"showToolCalls":         false,
			"cotMessages":           "detailed",
			"runIdleTimeoutMinutes": 17,
		}
	})

	runtime, err := intake.loadRuntimeConfig()
	if err != nil {
		t.Fatalf("loadRuntimeConfig returned error: %v", err)
	}
	replyMode, showToolCalls, idleTimeout, cotMessages := intake.presentationOptions(runtime, "oc_group")
	if replyMode != "text" || showToolCalls || idleTimeout != 17*time.Minute || cotMessages != "detailed" {
		t.Fatalf("presentation options = %q %v %s %q", replyMode, showToolCalls, idleTimeout, cotMessages)
	}
}

func TestManagedLarkIntakeReadsAllowedChatsFromConfigFile(t *testing.T) {
	configPath := writeManagedRuntimeConfig(t, nil, true)
	client, err := NewCodexClient(CodexClientOptions{
		Binary:             "codex",
		ProfileStateDir:    t.TempDir(),
		SessionStorePath:   filepath.Join(t.TempDir(), "sessions.json"),
		SessionCatalogPath: filepath.Join(t.TempDir(), "sessions.catalog.json"),
	})
	if err != nil {
		t.Fatalf("NewCodexClient returned error: %v", err)
	}
	intake := newManagedLarkIntake(managedLarkIntakeOptions{
		Client: client,
		Managed: LarkManagedOptions{CommandOptions: CommandOptions{
			ProfileName: "codex",
			ConfigPath:  configPath,
		}},
	})
	msg := appintake.MessageInput{
		ChatID:   "oc_group",
		ChatType: appintake.ChatTypeGroup,
		Sender:   appintake.Actor{OpenID: "ou_user"},
	}

	runtime, err := intake.loadRuntimeConfig()
	if err != nil {
		t.Fatalf("loadRuntimeConfig returned error: %v", err)
	}
	decision := intake.messageAccessDecisionWithProfile(msg, runtime.profile)
	if decision.OK || decision.Reason != AccessDeniedChat {
		t.Fatalf("decision before config update = %#v, want denied chat", decision)
	}

	updateManagedRuntimeAccess(t, configPath, func(access *ConfigProfileAccess) {
		access.AllowedChats = []string{"oc_group"}
	})

	runtime, err = intake.loadRuntimeConfig()
	if err != nil {
		t.Fatalf("loadRuntimeConfig returned error: %v", err)
	}
	decision = intake.messageAccessDecisionWithProfile(msg, runtime.profile)
	if !decision.OK || decision.Reason != AccessAllowedChat {
		t.Fatalf("decision after config update = %#v, want allowed chat", decision)
	}
}

func TestManagedLarkIntakeReadsRequireMentionFromConfigFile(t *testing.T) {
	configPath := writeManagedRuntimeConfig(t, []string{"oc_group"}, true)
	client, err := NewCodexClient(CodexClientOptions{
		Binary:             "codex",
		ProfileStateDir:    t.TempDir(),
		SessionStorePath:   filepath.Join(t.TempDir(), "sessions.json"),
		SessionCatalogPath: filepath.Join(t.TempDir(), "sessions.catalog.json"),
	})
	if err != nil {
		t.Fatalf("NewCodexClient returned error: %v", err)
	}
	intake := newManagedLarkIntake(managedLarkIntakeOptions{
		Client: client,
		Managed: LarkManagedOptions{CommandOptions: CommandOptions{
			ProfileName: "codex",
			ConfigPath:  configPath,
		}},
	})

	runtime, err := intake.loadRuntimeConfig()
	if err != nil {
		t.Fatalf("loadRuntimeConfig returned error: %v", err)
	}
	if !runtime.profile.Access.RequireMentionInGroup {
		t.Fatalf("RequireMentionInGroup before config update = false, want true")
	}

	updateManagedRuntimeAccess(t, configPath, func(access *ConfigProfileAccess) {
		access.RequireMentionInGroup = false
	})

	runtime, err = intake.loadRuntimeConfig()
	if err != nil {
		t.Fatalf("loadRuntimeConfig returned error: %v", err)
	}
	if runtime.profile.Access.RequireMentionInGroup {
		t.Fatalf("RequireMentionInGroup after config update = true, want false")
	}
}

func TestManagedLarkIntakeReportsConfigReadFailureToFeishu(t *testing.T) {
	client, err := NewCodexClient(CodexClientOptions{Binary: "codex", ProfileStateDir: t.TempDir(), SessionStorePath: filepath.Join(t.TempDir(), "sessions.json"), SessionCatalogPath: filepath.Join(t.TempDir(), "sessions.catalog.json")})
	if err != nil {
		t.Fatalf("NewCodexClient returned error: %v", err)
	}
	transport := NewFakeLarkTransport(LarkBotIdentity{})
	intake := newManagedLarkIntake(managedLarkIntakeOptions{
		Client:    client,
		Transport: transport,
		Managed:   LarkManagedOptions{CommandOptions: CommandOptions{ProfileName: "codex", ConfigPath: filepath.Join(t.TempDir(), "missing.json")}},
	})
	event := appintake.NormalizedEvent{
		Scope: appintake.Scope{Key: "ou_user", ChatMode: appintake.ChatModeP2P},
		Message: &appintake.MessageInput{
			MessageID: "om_config_error",
			ChatID:    "ou_user",
			ChatType:  appintake.ChatTypeP2P,
			Content:   "hello",
		},
	}
	if err := intake.handleMessage(context.Background(), event); err != nil {
		t.Fatalf("handleMessage returned error: %v", err)
	}
	sent := transport.SentMessageSnapshot()
	if len(sent) != 1 || !strings.Contains(sent[0].Content.Markdown, "服务配置读取失败") {
		t.Fatalf("config read failure reply = %#v", sent)
	}
}

func TestManagedLarkIntakeUsesFreshConfigForAdminCommand(t *testing.T) {
	configPath := writeManagedRuntimeConfig(t, nil, true)
	updateManagedRuntimeProfile(t, configPath, func(profile *ConfigProfile) {
		profile.Access.Admins = []string{"ou_admin"}
	})
	client, err := NewCodexClient(CodexClientOptions{Binary: "codex", ProfileStateDir: t.TempDir(), SessionStorePath: filepath.Join(t.TempDir(), "sessions.json"), SessionCatalogPath: filepath.Join(t.TempDir(), "sessions.catalog.json")})
	if err != nil {
		t.Fatalf("NewCodexClient returned error: %v", err)
	}
	transport := NewFakeLarkTransport(LarkBotIdentity{})
	intake := newManagedLarkIntake(managedLarkIntakeOptions{
		Client:    client,
		Transport: transport,
		Managed:   LarkManagedOptions{CommandOptions: CommandOptions{ProfileName: "codex", ConfigPath: configPath}},
	})
	event := appintake.NormalizedEvent{
		Scope: appintake.Scope{Key: "oc_target", ChatMode: appintake.ChatModeGroup},
		Message: &appintake.MessageInput{
			MessageID:    "om_invite",
			ChatID:       "oc_target",
			ChatType:     appintake.ChatTypeGroup,
			Content:      "/invite group",
			MentionedBot: true,
			Sender:       appintake.Actor{OpenID: "ou_admin"},
		},
	}
	if err := intake.handleMessage(context.Background(), event); err != nil {
		t.Fatalf("handleMessage returned error: %v", err)
	}
	snapshot, err := LoadConfig(configPath, ConfigLoadOptions{Profile: "codex", AgentKind: ConfigAgentCodex})
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}
	if !containsString(snapshot.Root.Profiles["codex"].Access.AllowedChats, "oc_target") {
		t.Fatalf("allowed chats = %#v, want oc_target", snapshot.Root.Profiles["codex"].Access.AllowedChats)
	}
}

func TestManagedLarkIntakeDefersMergeForwardExpansionUntilAfterAccess(t *testing.T) {
	configPath := writeManagedRuntimeConfig(t, nil, true)
	client, err := NewCodexClient(CodexClientOptions{Binary: "codex", ProfileStateDir: t.TempDir(), SessionStorePath: filepath.Join(t.TempDir(), "sessions.json"), SessionCatalogPath: filepath.Join(t.TempDir(), "sessions.catalog.json")})
	if err != nil {
		t.Fatalf("NewCodexClient returned error: %v", err)
	}
	resolved := 0
	intake := newManagedLarkIntake(managedLarkIntakeOptions{
		Client: client,
		Managed: LarkManagedOptions{
			CommandOptions: CommandOptions{ProfileName: "codex", ConfigPath: configPath},
			QuoteResolver: LarkQuoteResolverFunc(func(context.Context, LarkQuoteTarget) (BridgePromptQuotedMessage, bool, error) {
				resolved++
				return BridgePromptQuotedMessage{Content: "expanded"}, true, nil
			}),
		},
	})
	event := appintake.NormalizedEvent{
		Scope: appintake.Scope{Key: "oc_denied", ChatMode: appintake.ChatModeGroup},
		Message: &appintake.MessageInput{
			MessageID:      "om_forward",
			ChatID:         "oc_denied",
			ChatType:       appintake.ChatTypeGroup,
			RawContentType: "merge_forward",
			Content:        "Merged and Forwarded Message",
			MentionedBot:   true,
			Sender:         appintake.Actor{OpenID: "ou_user"},
		},
	}
	if err := intake.handleMessage(context.Background(), event); err != nil {
		t.Fatalf("handleMessage returned error: %v", err)
	}
	if resolved != 0 {
		t.Fatalf("merge_forward resolver calls = %d, want 0 before access", resolved)
	}
}

func TestManagedLarkIntakeResolvesAuthorizedMergeForward(t *testing.T) {
	intake := newManagedLarkIntake(managedLarkIntakeOptions{Managed: LarkManagedOptions{
		QuoteResolver: LarkQuoteResolverFunc(func(_ context.Context, target LarkQuoteTarget) (BridgePromptQuotedMessage, bool, error) {
			if target.MessageID != "om_forward" {
				t.Fatalf("target message id = %q", target.MessageID)
			}
			return BridgePromptQuotedMessage{Content: "<forwarded_messages>expanded</forwarded_messages>"}, true, nil
		}),
	}})
	messages := intake.resolveMergedForwardMessages(context.Background(), appintake.Scope{Key: "oc_allowed"}, []appintake.MessageInput{{MessageID: "om_forward", ChatID: "oc_allowed", RawContentType: "merge_forward", Content: "Merged and Forwarded Message"}})
	if len(messages) != 1 || !strings.Contains(messages[0].Content, "expanded") {
		t.Fatalf("resolved messages = %#v", messages)
	}
}

func TestClientCardCommandHandlerUsesProvidedProfile(t *testing.T) {
	client, err := NewCodexClient(CodexClientOptions{Binary: "codex", ProfileStateDir: t.TempDir(), SessionStorePath: filepath.Join(t.TempDir(), "sessions.json"), SessionCatalogPath: filepath.Join(t.TempDir(), "sessions.catalog.json")})
	if err != nil {
		t.Fatalf("NewCodexClient returned error: %v", err)
	}
	runtimeProfile := profile.DefaultConfig(profile.AgentCodex)
	runtimeProfile.Access.Admins = []string{"ou_admin"}
	handler := clientCardCommandHandler{client: client, profileConfig: &runtimeProfile}
	decision := handler.accessDecision(CardCommandRequest{ChatMode: LarkChatModeGroup, ChatID: "oc_group", SenderID: "ou_admin"})
	if !decision.OK || decision.Reason != AccessAllowedAdmin {
		t.Fatalf("card action decision = %#v, want allowed admin", decision)
	}
}

func TestManagedLarkIntakePresentRunPublishesCOTAndFinalOnlyReply(t *testing.T) {
	transport := NewFakeLarkTransport(LarkBotIdentity{})
	intake := &managedLarkIntake{transport: transport, cotClient: wrapInternalLarkCOTClient(transport)}
	toolID := "tool-1"
	toolName := "command_execution"
	text := "final answer"
	output := "secret output"

	_, err := intake.presentRun(context.Background(), managedPresentInput{
		Run: bridgeTestRun{events: []agentport.AgentEvent{
			{Type: agentport.EventToolUse, ID: &toolID, Name: &toolName, Input: map[string]any{"command": "cat secret"}},
			{Type: agentport.EventToolResult, ID: &toolID, Output: &output},
			{Type: agentport.EventText, Delta: &text},
			{Type: agentport.EventDone, TerminationReason: agentport.TerminationNormal},
		}},
		ChatID:        "oc_chat",
		Options:       appimpresenter.SendOptions{ReplyTo: "om_origin"},
		ReplyMode:     appimpresenter.ReplyMarkdown,
		COTMessages:   appcot.ModeBrief,
		RunID:         "run-1",
		ScopeID:       "oc_chat",
		OriginMessage: "om_origin",
		InputPreview:  "run",
	})
	if err != nil {
		t.Fatalf("presentRun returned error: %v", err)
	}
	if len(transport.CreatedCOTSnapshot()) != 1 || len(transport.UpdatedCOTSnapshot()) == 0 || len(transport.CompletedCOTSnapshot()) != 1 {
		t.Fatalf("cot create/update/complete = %#v %#v %#v", transport.CreatedCOTSnapshot(), transport.UpdatedCOTSnapshot(), transport.CompletedCOTSnapshot())
	}
	messages := transport.SentMessageSnapshot()
	if len(messages) != 1 {
		t.Fatalf("sent messages = %#v", messages)
	}
	body := messages[0].Content.Markdown
	if !strings.Contains(body, "final answer") || strings.Contains(body, "secret output") || strings.Contains(body, "command_execution") {
		t.Fatalf("final markdown = %q", body)
	}
}

func TestManagedLarkIntakePresentRunSendsCOTDegradedNoticeAndFinalReply(t *testing.T) {
	transport := NewFakeLarkTransport(LarkBotIdentity{})
	transport.COTUpdateErr = errors.New("field validation failed")
	intake := &managedLarkIntake{transport: transport, cotClient: wrapInternalLarkCOTClient(transport)}
	text := "final answer"

	_, err := intake.presentRun(context.Background(), managedPresentInput{
		Run: bridgeTestRun{events: []agentport.AgentEvent{
			{Type: agentport.EventText, Delta: &text},
			{Type: agentport.EventDone, TerminationReason: agentport.TerminationNormal},
		}},
		ChatID:        "oc_chat",
		Options:       appimpresenter.SendOptions{ReplyTo: "om_origin"},
		ReplyMode:     appimpresenter.ReplyMarkdown,
		COTMessages:   appcot.ModeBrief,
		RunID:         "run-degraded",
		ScopeID:       "oc_chat",
		OriginMessage: "om_origin",
		InputPreview:  "run",
	})
	if err != nil {
		t.Fatalf("presentRun returned error: %v", err)
	}
	if len(transport.CompletedCOTSnapshot()) != 0 {
		t.Fatalf("completed COTs = %#v, want none after update failure", transport.CompletedCOTSnapshot())
	}
	messages := transport.SentMessageSnapshot()
	if len(messages) != 2 {
		t.Fatalf("sent messages = %#v, want degraded notice plus final reply", messages)
	}
	if !strings.Contains(messages[0].Content.Markdown, "COT 过程消息更新失败") {
		t.Fatalf("degraded notice = %q", messages[0].Content.Markdown)
	}
	if !strings.Contains(messages[1].Content.Markdown, "final answer") {
		t.Fatalf("final reply = %q", messages[1].Content.Markdown)
	}
}

func TestManagedLarkIntakeInjectsCurrentInteractiveCardsIntoPrompt(t *testing.T) {
	intake := newManagedLarkIntake(managedLarkIntakeOptions{
		Transport: NewFakeLarkTransport(LarkBotIdentity{OpenID: "ou_bot"}),
	})
	prompt := intake.buildMessagePrompt(context.Background(), toInternalLarkMessages([]LarkMessageInput{{
		MessageID:      "om_card",
		ChatID:         "oc_group",
		ChatType:       LarkChatTypeGroup,
		Sender:         LarkActor{OpenID: "ou_user", Name: "Alice"},
		RawContentType: "interactive",
		RawContent:     `{"schema":"2.0","body":{"elements":[]}}`,
	}}), nil, nil)

	for _, needle := range []string{
		"<interactive_cards>",
		`"messageId":"om_card"`,
		`"schema":"2.0"`,
	} {
		if !strings.Contains(prompt, needle) {
			t.Fatalf("prompt missing %q:\n%s", needle, prompt)
		}
	}
}

func TestManagedLarkIntakeSkipsEmptyInteractiveCardRawContent(t *testing.T) {
	intake := newManagedLarkIntake(managedLarkIntakeOptions{
		Transport: NewFakeLarkTransport(LarkBotIdentity{OpenID: "ou_bot"}),
	})
	prompt := intake.buildMessagePrompt(context.Background(), toInternalLarkMessages([]LarkMessageInput{{
		MessageID:      "om_card",
		ChatID:         "oc_group",
		ChatType:       LarkChatTypeGroup,
		Sender:         LarkActor{OpenID: "ou_user", Name: "Alice"},
		RawContentType: "interactive",
		RawContent:     "",
		Content:        "empty card",
	}}), nil, nil)

	if strings.Contains(prompt, "<interactive_cards>") {
		t.Fatalf("prompt unexpectedly contains interactive cards:\n%s", prompt)
	}
}

func TestManagedLarkIntakeAddsSenderMetadataAndStripsAttachmentRefs(t *testing.T) {
	intake := newManagedLarkIntake(managedLarkIntakeOptions{
		Transport: NewFakeLarkTransport(LarkBotIdentity{OpenID: "ou_bridge"}),
	})
	humanMention := false
	botMention := true
	prompt := intake.buildMessagePrompt(context.Background(), toInternalLarkMessages([]LarkMessageInput{
		{
			MessageID:      "om_human",
			ChatID:         "oc_group",
			ChatType:       LarkChatTypeGroup,
			Sender:         LarkActor{OpenID: "ou_alice", Name: "Alice"},
			SenderType:     "user",
			Content:        "see ![note](file_key_1)\n<file key=\"file_key_2\">",
			RawContentType: "text",
			Resources: []LarkResource{
				{Kind: "file", ID: "file_key_1", Name: "note.txt"},
				{Kind: "file", ID: "file_key_2", Name: "other.txt"},
			},
			Mentions: []LarkMention{
				{OpenID: "ou_bob", Name: "Bob", IsBot: &humanMention},
				{OpenID: "ou_helper", Name: "Helper", IsBot: &botMention},
			},
		},
		{
			MessageID:      "om_bot",
			ChatID:         "oc_group",
			ChatType:       LarkChatTypeGroup,
			Sender:         LarkActor{OpenID: "ou_helper", Name: "Helper"},
			SenderType:     "bot",
			Content:        "部署完成",
			RawContentType: "text",
			Mentions: []LarkMention{
				{OpenID: "ou_helper", Name: "Helper", IsBot: &botMention},
			},
		},
	}), nil, nil)

	for _, needle := range []string{
		`"senderType":"user"`,
		`"mentions":[`,
		`"openId":"ou_bob"`,
		`"isBot":false`,
		`"openId":"ou_helper"`,
		`"isBot":true`,
		`[Alice (user)]: see`,
		`[Helper (bot)]: 部署完成`,
	} {
		if !strings.Contains(prompt, needle) {
			t.Fatalf("prompt missing %q:\n%s", needle, prompt)
		}
	}
	for _, forbidden := range []string{"file_key_1", "file_key_2", "![note]", "<file"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("prompt still contains attachment ref %q:\n%s", forbidden, prompt)
		}
	}
}

func TestManagedPromptMentionsDedupesOpenIDOtherwiseNameAndKey(t *testing.T) {
	human := false
	mentions := managedPromptMentions(toInternalLarkMessages([]LarkMessageInput{{
		Mentions: []LarkMention{
			{Key: "@_same_1", Name: "Alex", IsBot: &human},
			{Key: "@_same_2", Name: "Alex", IsBot: &human},
			{Key: "@_ignored", OpenID: "ou_same", Name: "Bot One"},
			{Key: "@_also_ignored", OpenID: "ou_same", Name: "Bot Alias"},
		},
	}}), "")
	if len(mentions) != 3 {
		t.Fatalf("mentions = %#v, want 3 after JS-style dedupe", mentions)
	}
	if mentions[0].Name != "Alex" || mentions[1].Name != "Alex" || mentions[2].OpenID != "ou_same" {
		t.Fatalf("mentions = %#v", mentions)
	}
}

func TestManagedLarkIntakeAddsAndCleansWorkingReactionForNonCardReplies(t *testing.T) {
	transport := NewFakeLarkTransport(LarkBotIdentity{})
	intake := newManagedLarkIntake(managedLarkIntakeOptions{Transport: transport})
	defer intake.Close()

	reaction := intake.addWorkingReaction("om_source", appimpresenter.ReplyMarkdown, appcot.ModeOff)
	select {
	case id := <-reaction:
		if id == "" {
			t.Fatalf("reaction id is empty")
		}
	case <-time.After(time.Second):
		t.Fatalf("reaction add did not complete")
	}
	added := transport.AddedReactionSnapshot()
	if len(added) != 1 || added[0].MessageID != "om_source" || added[0].EmojiType != "Typing" {
		t.Fatalf("added reactions = %#v", added)
	}

	intake.scheduleWorkingReactionCleanup("om_source", closedReaction("reaction_fake_1"))
	waitForCondition(t, time.Second, func() bool {
		deleted := transport.DeletedReactionSnapshot()
		return len(deleted) == 1 && deleted[0].MessageID == "om_source" && deleted[0].ReactionID == "reaction_fake_1"
	})
}

func TestManagedLarkIntakeSkipsWorkingReactionForCardReplies(t *testing.T) {
	transport := NewFakeLarkTransport(LarkBotIdentity{})
	intake := newManagedLarkIntake(managedLarkIntakeOptions{Transport: transport})
	defer intake.Close()

	if reaction := intake.addWorkingReaction("om_source", appimpresenter.ReplyCard, appcot.ModeOff); reaction != nil {
		t.Fatalf("card reply reaction = %#v, want nil", reaction)
	}
	if added := transport.AddedReactionSnapshot(); len(added) != 0 {
		t.Fatalf("added reactions = %#v", added)
	}
}

func TestManagedLarkIntakeSkipsWorkingReactionWhenCOTIsEnabled(t *testing.T) {
	if managedWorkingReactionEnabled(appimpresenter.ReplyMarkdown, appcot.ModeBrief) {
		t.Fatalf("working reaction enabled for COT brief, want false")
	}
	if managedWorkingReactionEnabled(appimpresenter.ReplyMarkdown, appcot.ModeDetailed) {
		t.Fatalf("working reaction enabled for COT detailed, want false")
	}
	if !managedWorkingReactionEnabled(appimpresenter.ReplyMarkdown, appcot.ModeOff) {
		t.Fatalf("working reaction disabled for markdown without COT, want true")
	}
}

func TestManagedLarkIntakeCancelsScopeGrantWhenGrantCardSendFails(t *testing.T) {
	cancelled := make(chan struct{})
	var cancelOnce sync.Once
	transport := &bridgeScopeTransport{
		FakeLarkTransport: NewFakeLarkTransport(LarkBotIdentity{}),
		check: LarkScopeCheckerFunc(func(context.Context, string, string) (bool, error) {
			return false, nil
		}),
		grant: LarkScopeGrantRequesterFunc(func(context.Context, LarkScopeGrantRequest) (LarkScopeGrantLink, error) {
			return LarkScopeGrantLink{
				URL:       "https://example.com/grant",
				ExpiresIn: time.Minute,
				Cancel: func() {
					cancelOnce.Do(func() { close(cancelled) })
				},
			}, nil
		}),
	}
	transport.SendErr = errors.New("send failed")
	intake := newManagedLarkIntake(managedLarkIntakeOptions{
		Transport: transport,
		AppID:     "cli_bridge_test",
	})
	defer intake.Close()

	intake.promptGroupMsgScopeIfMissing(context.Background(), "oc_group")

	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatalf("scope grant link was not cancelled after grant card send failed")
	}
}

func closedReaction(id string) <-chan string {
	out := make(chan string, 1)
	out <- id
	close(out)
	return out
}

func waitForCondition(t *testing.T, timeout time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ok() {
		t.Fatalf("condition was not met within %s", timeout)
	}
}

type bridgeTestRun struct {
	events []agentport.AgentEvent
}

func (r bridgeTestRun) Events(context.Context) <-chan agentport.AgentEvent {
	out := make(chan agentport.AgentEvent, len(r.events))
	for _, event := range r.events {
		out <- event
	}
	close(out)
	return out
}

type quoteResolvingTransport struct {
	*FakeLarkTransport
	content string
	called  bool
}

func (t *quoteResolvingTransport) ResolveLarkQuote(_ context.Context, target LarkQuoteTarget) (BridgePromptQuotedMessage, bool, error) {
	t.called = true
	return BridgePromptQuotedMessage{
		MessageID:      target.MessageID,
		RawContentType: "text",
		Content:        t.content,
	}, true, nil
}

type runtimeInfoTransport struct {
	*FakeLarkTransport
	owner      string
	chats      []LarkKnownChatInfo
	ownerAppID string
	ownerCalls int
	chatCalls  int
	maxPages   int
}

func (t *runtimeInfoTransport) FetchLarkOwner(_ context.Context, appID string) (string, error) {
	t.ownerAppID = appID
	t.ownerCalls++
	return t.owner, nil
}

func (t *runtimeInfoTransport) ListLarkKnownChats(_ context.Context, maxPages int) ([]LarkKnownChatInfo, error) {
	t.chatCalls++
	t.maxPages = maxPages
	return append([]LarkKnownChatInfo(nil), t.chats...), nil
}

type blockingRuntimeInfoTransport struct {
	*FakeLarkTransport
	release chan struct{}
	entered chan struct{}
	once    sync.Once
}

func (t *blockingRuntimeInfoTransport) FetchLarkOwner(ctx context.Context, appID string) (string, error) {
	t.once.Do(func() { close(t.entered) })
	select {
	case <-t.release:
		return "ou_owner", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (t *blockingRuntimeInfoTransport) ListLarkKnownChats(context.Context, int) ([]LarkKnownChatInfo, error) {
	return nil, nil
}
