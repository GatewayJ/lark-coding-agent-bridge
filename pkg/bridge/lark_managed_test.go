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
	agentport "github.com/zarazhangrui/lark-coding-agent-bridge/internal/ports/agent"
)

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

	opts := intake.configCardOptions(context.Background(), &CommandConfigView{Snapshot: CommandConfigSnapshotView{
		MessageReply: "markdown",
		CotMessages:  "detailed",
	}})
	if len(opts.KnownChats) != 1 || opts.KnownChats[0].ID != "oc_group" || opts.KnownChats[0].Name != "工程群" {
		t.Fatalf("config known chats = %#v", opts.KnownChats)
	}
}

func TestManagedLarkIntakeAppliesSavedConfigRuntimeOptions(t *testing.T) {
	showTools := true
	intake := newManagedLarkIntake(managedLarkIntakeOptions{
		Managed: LarkManagedOptions{
			MessageReplyMode: LarkReplyCard,
			ShowToolCalls:    &showTools,
			CommandOptions: CommandOptions{
				GlobalIdleTimeout: 5 * time.Minute,
			},
		},
	})

	intake.applySavedConfigRuntime(CommandResponse{
		Kind: CommandResponseConfig,
		Config: &CommandConfigView{
			Saved: true,
			Snapshot: CommandConfigSnapshotView{
				MessageReply:          "text",
				ShowToolCalls:         false,
				CotMessages:           "detailed",
				RunIdleTimeoutMinutes: 17,
			},
		},
	})

	replyMode, showToolCalls, idleTimeout, cotMessages := intake.currentPresentationOptions("oc_group")
	if replyMode != "text" || showToolCalls || idleTimeout != 17*time.Minute || cotMessages != "detailed" {
		t.Fatalf("presentation options = %q %v %s %q", replyMode, showToolCalls, idleTimeout, cotMessages)
	}
	if got := intake.currentCommandOptions().GlobalIdleTimeout; got != 17*time.Minute {
		t.Fatalf("global idle timeout = %s", got)
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
