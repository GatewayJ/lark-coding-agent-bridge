package intake

import "testing"

func TestMessageScopeUsesMessageThreadAsAuthoritativeTopic(t *testing.T) {
	event := NewNormalizer(SelfLoopPolicy{}).NormalizeMessage(MessageInput{
		MessageID:    "om_1",
		ChatID:       "oc_group",
		ChatType:     ChatTypeGroup,
		ResolvedMode: ChatModeGroup,
		ThreadID:     "omt_topic",
		Sender:       Actor{OpenID: "ou_user"},
		Content:      "hello topic",
	})

	if event.Scope.Key != "oc_group:omt_topic" {
		t.Fatalf("scope key = %q, want topic scope", event.Scope.Key)
	}
	if event.Scope.ChatMode != ChatModeTopic {
		t.Fatalf("chat mode = %q, want topic", event.Scope.ChatMode)
	}
	if event.Scope.ThreadID != "omt_topic" {
		t.Fatalf("thread id = %q", event.Scope.ThreadID)
	}
}

func TestMessageScopeUsesChatIDForNonTopicP2P(t *testing.T) {
	event := NewNormalizer(SelfLoopPolicy{}).NormalizeMessage(MessageInput{
		MessageID:    "om_1",
		ChatID:       "oc_p2p",
		ChatType:     ChatTypeP2P,
		ResolvedMode: ChatModeP2P,
		Sender:       Actor{OpenID: "ou_user"},
		Content:      "hello",
	})

	if event.Scope.Key != "oc_p2p" {
		t.Fatalf("scope key = %q, want JS-compatible chat scope", event.Scope.Key)
	}
	if event.Scope.ChatMode != ChatModeP2P {
		t.Fatalf("chat mode = %q, want p2p", event.Scope.ChatMode)
	}
}

func TestMessageScopeUsesChatIDForNonTopicGroup(t *testing.T) {
	event := NewNormalizer(SelfLoopPolicy{}).NormalizeMessage(MessageInput{
		MessageID:    "om_1",
		ChatID:       "oc_group",
		ChatType:     ChatTypeGroup,
		ResolvedMode: ChatModeGroup,
		Sender:       Actor{OpenID: "ou_user"},
		Content:      "hello",
	})

	if event.Scope.Key != "oc_group" {
		t.Fatalf("scope key = %q, want JS-compatible chat scope", event.Scope.Key)
	}
	if event.Scope.ChatMode != ChatModeGroup {
		t.Fatalf("chat mode = %q, want group", event.Scope.ChatMode)
	}
}

func TestCommentScopeDefaultsToCommentThreadAndExposesDocumentScope(t *testing.T) {
	event := NewNormalizer(SelfLoopPolicy{}).NormalizeComment(CommentInput{
		FileToken: "doc-token",
		FileType:  "docx",
		CommentID: "comment-1",
		ReplyID:   "reply-1",
		Operator:  Actor{OpenID: "ou_user"},
	})

	if event.Scope.Key != CommentScopeKey("doc-token", "comment-1") {
		t.Fatalf("scope key = %q, want comment scope", event.Scope.Key)
	}
	if event.Scope.DocumentScopeKey != CommentDocumentScopeKey("doc-token") {
		t.Fatalf("document scope = %q", event.Scope.DocumentScopeKey)
	}
	if event.Scope.FileType != "docx" || event.Scope.CommentID != "comment-1" {
		t.Fatalf("comment scope metadata = %#v", event.Scope)
	}
}

func TestCommentScopeCanInheritOrExplicitlyBindScope(t *testing.T) {
	inherited := NewNormalizer(SelfLoopPolicy{}).NormalizeComment(CommentInput{
		FileToken:       "doc-token",
		CommentID:       "comment-1",
		Operator:        Actor{OpenID: "ou_user"},
		InheritScopeKey: "oc_group:omt_topic",
	})
	if inherited.Scope.Key != "oc_group:omt_topic" || inherited.Scope.ParentKey != "oc_group:omt_topic" {
		t.Fatalf("inherited scope = %#v", inherited.Scope)
	}

	explicit := NewNormalizer(SelfLoopPolicy{}).NormalizeComment(CommentInput{
		FileToken:        "doc-token",
		CommentID:        "comment-1",
		Operator:         Actor{OpenID: "ou_user"},
		InheritScopeKey:  "oc_group:omt_topic",
		ExplicitScopeKey: "manual-scope",
	})
	if explicit.Scope.Key != "manual-scope" || explicit.Scope.ParentKey != "" {
		t.Fatalf("explicit scope = %#v", explicit.Scope)
	}
}

func TestCardActionScopeInheritsOrBindsTopic(t *testing.T) {
	event := NewNormalizer(SelfLoopPolicy{}).NormalizeCardAction(CardActionInput{
		EventID:      "evt_1",
		MessageID:    "om_card",
		ChatID:       "oc_group",
		ChatType:     ChatTypeGroup,
		ResolvedMode: ChatModeTopic,
		ThreadID:     "omt_topic",
		Operator:     Actor{OpenID: "ou_user"},
		ActionValue:  map[string]any{"cmd": "stop"},
	})
	if event.Scope.Key != "oc_group:omt_topic" {
		t.Fatalf("card scope = %q, want carrier topic scope", event.Scope.Key)
	}

	inherited := NewNormalizer(SelfLoopPolicy{}).NormalizeCardAction(CardActionInput{
		EventID:      "evt_2",
		MessageID:    "om_card",
		ChatID:       "oc_group",
		ChatType:     ChatTypeGroup,
		ResolvedMode: ChatModeGroup,
		Operator:     Actor{OpenID: "ou_user"},
		InheritScope: "oc_group:omt_topic",
	})
	if inherited.Scope.Key != "oc_group:omt_topic" || inherited.Scope.ParentKey != "oc_group:omt_topic" {
		t.Fatalf("inherited card scope = %#v", inherited.Scope)
	}
}

func TestSelfReplyPolicyDropsBotAndBridgeReplies(t *testing.T) {
	normalizer := NewNormalizer(DefaultSelfLoopPolicy("ou_bot"))

	message := normalizer.NormalizeMessage(MessageInput{
		ChatID: "oc_p2p",
		Sender: Actor{OpenID: "ou_bot"},
	})
	if !message.Self.Drop || message.Self.Reason != SelfLoopBotOpenID {
		t.Fatalf("message self decision = %#v", message.Self)
	}

	comment := normalizer.NormalizeComment(CommentInput{
		FileToken:   "doc-token",
		CommentID:   "comment-1",
		Operator:    Actor{OpenID: "ou_user"},
		BridgeReply: true,
	})
	if !comment.Self.Drop || comment.Self.Reason != SelfLoopBridgeReply {
		t.Fatalf("comment self decision = %#v", comment.Self)
	}

	metadata := normalizer.NormalizeComment(CommentInput{
		FileToken: "doc-token",
		CommentID: "comment-1",
		Operator:  Actor{OpenID: "ou_user"},
		Metadata: map[string]any{
			"metadata": map[string]any{"source": "lark-channel-bridge"},
		},
	})
	if !metadata.Self.Drop || metadata.Self.Reason != SelfLoopBridgeMetadata {
		t.Fatalf("metadata self decision = %#v", metadata.Self)
	}
}

func TestLifecycleEventsHaveStableScopes(t *testing.T) {
	if event := NormalizeReconnect(ReconnectInput{Phase: ReconnectReconnecting}); event.Kind != EventReconnect || event.Scope.Key != "reconnect" {
		t.Fatalf("reconnect event = %#v", event)
	}
	if event := NormalizeKeepalive(KeepaliveInput{State: ConnectionConnected}); event.Kind != EventKeepalive || event.Scope.Key != "keepalive" {
		t.Fatalf("keepalive event = %#v", event)
	}
	if event := NormalizeDisconnect(DisconnectInput{Reason: "shutdown"}); event.Kind != EventDisconnect || event.Scope.Key != "disconnect" {
		t.Fatalf("disconnect event = %#v", event)
	}
}
