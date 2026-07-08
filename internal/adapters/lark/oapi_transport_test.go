package lark

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	channeltypes "github.com/larksuite/oapi-sdk-go/v3/channel/types"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/cardmanaged"
	appcot "github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/cotpresenter"
	appintake "github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/intake"
)

func TestOAPITransportSendMessageAndCardIDUseChannelSendContract(t *testing.T) {
	channel := &fakeOAPIChannel{}
	transport, err := NewOAPITransport(OAPITransportOptions{channel: channel})
	if err != nil {
		t.Fatalf("NewOAPITransport error = %v", err)
	}

	if _, err := transport.SendMessage(context.Background(), SendMessageRequest{
		ChatID: "oc_chat",
		Content: MessageContent{
			Markdown: "hello **world**",
		},
		Options: SendOptions{ReplyTo: "om_parent"},
	}); err != nil {
		t.Fatalf("SendMessage error = %v", err)
	}
	if len(channel.sends) != 1 {
		t.Fatalf("sends = %d, want 1", len(channel.sends))
	}
	first := channel.sends[0]
	if first.ReceiveID != "oc_chat" || first.Markdown != "hello **world**" || first.ReplyMessageID != "om_parent" {
		t.Fatalf("first send = %#v", first)
	}

	messageID, err := transport.SendCardID(context.Background(), "ou_user", "card_123", cardmanaged.SendOptions{
		ReplyTo: "om_card_parent",
	})
	if err != nil {
		t.Fatalf("SendCardID error = %v", err)
	}
	if messageID == "" {
		t.Fatalf("messageID is empty")
	}
	second := channel.sends[1]
	if second.ReceiveID != "ou_user" || second.ReplyMessageID != "om_card_parent" {
		t.Fatalf("second send = %#v", second)
	}
	var content map[string]string
	if err := json.Unmarshal([]byte(second.Card), &content); err != nil {
		t.Fatalf("card id content is not json: %v; raw=%s", err, second.Card)
	}
	if content["card_id"] != "card_123" {
		t.Fatalf("card_id content = %#v", content)
	}
}

func TestOAPITransportUpdateMessagePatchesMarkdownContent(t *testing.T) {
	server := newOAPICommentTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			writeOAPIJSON(t, w, map[string]any{"code": 0, "msg": "ok", "tenant_access_token": "tenant-token", "expire": 7200})
		case "/open-apis/im/v1/messages/om_stream":
			if r.Method != http.MethodPatch {
				t.Fatalf("method = %s, want PATCH", r.Method)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("patch body is not json: %v", err)
			}
			content, _ := body["content"].(string)
			if !strings.Contains(content, "hello") || !strings.Contains(content, "world") {
				t.Fatalf("patch content = %q", content)
			}
			writeOAPIJSON(t, w, map[string]any{"code": 0, "msg": "ok"})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	})
	transport := newOAPICommentTestTransport(t, server)

	if err := transport.UpdateMessage(context.Background(), UpdateMessageRequest{
		MessageID: "om_stream",
		Content:   MessageContent{Markdown: "hello **world**"},
	}); err != nil {
		t.Fatalf("UpdateMessage returned error: %v", err)
	}
}

func TestOAPITransportCreateBoundChat(t *testing.T) {
	var sawCreate bool
	server := newOAPICommentTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			writeOAPIJSON(t, w, map[string]any{"code": 0, "msg": "ok", "tenant_access_token": "tenant-token", "expire": 7200})
		case "/open-apis/im/v1/chats":
			sawCreate = true
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", r.Method)
			}
			assertOAPIQuery(t, r, map[string]string{"user_id_type": "open_id"})
			body := decodeOAPIBody(t, r)
			if body["name"] != "Pairing Room" || body["description"] != "work" {
				t.Fatalf("create chat body = %#v", body)
			}
			users, ok := body["user_id_list"].([]any)
			if !ok || len(users) != 1 || users[0] != "ou_user" {
				t.Fatalf("create chat users = %#v", body["user_id_list"])
			}
			writeOAPIJSON(t, w, map[string]any{"code": 0, "msg": "ok", "data": map[string]any{"chat_id": "oc_new", "name": "Pairing Room"}})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	})
	transport := newOAPICommentTestTransport(t, server)

	created, err := transport.CreateBoundChat(context.Background(), CreateBoundChatRequest{
		Name:         "Pairing Room",
		InviteOpenID: "ou_user",
		Description:  "work",
	})
	if err != nil {
		t.Fatalf("CreateBoundChat returned error: %v", err)
	}
	if !sawCreate || created.ChatID != "oc_new" || created.Name != "Pairing Room" {
		t.Fatalf("created = %#v sawCreate=%v", created, sawCreate)
	}
}

func TestOAPITransportMessageReactionCreateAndDelete(t *testing.T) {
	server := newOAPICommentTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			writeOAPIJSON(t, w, map[string]any{"code": 0, "msg": "ok", "tenant_access_token": "tenant-token", "expire": 7200})
		case "/open-apis/im/v1/messages/om_source/reactions":
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", r.Method)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("reaction body is not json: %v", err)
			}
			reactionType, _ := body["reaction_type"].(map[string]any)
			if reactionType["emoji_type"] != "Typing" {
				t.Fatalf("reaction body = %#v", body)
			}
			writeOAPIJSON(t, w, map[string]any{"code": 0, "msg": "ok", "data": map[string]any{"reaction_id": "reaction_1"}})
		case "/open-apis/im/v1/messages/om_source/reactions/reaction_1":
			if r.Method != http.MethodDelete {
				t.Fatalf("method = %s, want DELETE", r.Method)
			}
			writeOAPIJSON(t, w, map[string]any{"code": 0, "msg": "ok"})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	})
	transport := newOAPICommentTestTransport(t, server)

	result, err := transport.AddMessageReaction(context.Background(), MessageReactionRequest{MessageID: "om_source", EmojiType: "Typing"})
	if err != nil {
		t.Fatalf("AddMessageReaction returned error: %v", err)
	}
	if result.ReactionID != "reaction_1" {
		t.Fatalf("reaction result = %#v", result)
	}
	if err := transport.DeleteMessageReaction(context.Background(), MessageReactionRequest{MessageID: "om_source", ReactionID: result.ReactionID}); err != nil {
		t.Fatalf("DeleteMessageReaction returned error: %v", err)
	}
}

func TestOAPITransportMessageCOTCreateUpdateComplete(t *testing.T) {
	var sawCreate, sawUpdate, sawComplete bool
	server := newOAPICommentTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			writeOAPIJSON(t, w, map[string]any{"code": 0, "msg": "ok", "tenant_access_token": "tenant-token", "expire": 7200})
		case "/open-apis/im/v1/message_cot":
			switch r.Method {
			case http.MethodPost:
				sawCreate = true
				assertOAPIQuery(t, r, map[string]string{"receive_id_type": "chat_id"})
				body := decodeOAPIBody(t, r)
				if body["receive_id"] != "oc_chat" || body["origin_message_id"] != "om_origin" {
					t.Fatalf("create body = %#v", body)
				}
				writeOAPIJSON(t, w, map[string]any{"code": 0, "msg": "ok", "data": map[string]any{"cot_id": "cot_1", "message_id": "om_cot"}})
			case http.MethodPut:
				sawUpdate = true
				body := decodeOAPIBody(t, r)
				if body["cot_id"] != "cot_1" || body["message_id"] != "om_cot" {
					t.Fatalf("update body = %#v", body)
				}
				events, ok := body["events"].([]any)
				if !ok || len(events) != 1 {
					t.Fatalf("update events = %#v", body["events"])
				}
				event, ok := events[0].(map[string]any)
				if !ok || event["event_type"] != "RUN_STARTED" || event["content"] == "" {
					t.Fatalf("update event = %#v", events[0])
				}
				writeOAPIJSON(t, w, map[string]any{"code": 0, "msg": "ok"})
			default:
				t.Fatalf("unexpected message_cot method %s", r.Method)
			}
		case "/open-apis/im/v1/message_cot/complete/cot_1":
			sawComplete = true
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", r.Method)
			}
			assertOAPIQuery(t, r, map[string]string{"message_id": "om_cot", "reason": "done"})
			writeOAPIJSON(t, w, map[string]any{"code": 0, "msg": "ok"})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	})
	transport := newOAPICommentTestTransport(t, server)

	ref, err := transport.CreateMessageCOT(context.Background(), appcot.CreateRequest{
		ReceiveID:       "oc_chat",
		OriginMessageID: "om_origin",
	})
	if err != nil {
		t.Fatalf("CreateMessageCOT returned error: %v", err)
	}
	if ref.COTID != "cot_1" || ref.MessageID != "om_cot" {
		t.Fatalf("ref = %#v", ref)
	}
	err = transport.UpdateMessageCOT(context.Background(), appcot.UpdateRequest{
		Ref: ref,
		Events: []appcot.Event{{
			EventType: "RUN_STARTED",
			Content:   `{"runId":"run-1"}`,
			Timestamp: 1234,
		}},
	})
	if err != nil {
		t.Fatalf("UpdateMessageCOT returned error: %v", err)
	}
	if err := transport.CompleteMessageCOT(context.Background(), appcot.CompleteRequest{Ref: ref, Reason: "done"}); err != nil {
		t.Fatalf("CompleteMessageCOT returned error: %v", err)
	}
	if !sawCreate || !sawUpdate || !sawComplete {
		t.Fatalf("saw create/update/complete = %v/%v/%v", sawCreate, sawUpdate, sawComplete)
	}
}

func TestOAPITransportMapMessagePreservesRawInteractiveContent(t *testing.T) {
	transport := &OAPITransport{}
	rawContent := `{"schema":"2.0","body":{"elements":[]}}`
	got := transport.mapMessage(&channeltypes.NormalizedMessage{
		EventID:        "evt_1",
		MessageID:      "om_card",
		ChatID:         "oc_group",
		ChatType:       "group",
		UserID:         "ou_user",
		Content:        "[interactive card]",
		RawContentType: "interactive",
		RawEvent: map[string]any{
			"event": map[string]any{
				"message": map[string]any{
					"content": rawContent,
				},
				"sender": map[string]any{
					"sender_type": "app",
				},
			},
		},
		Mentions: []channeltypes.Mention{{Key: "@_user_1", UserID: "u_other_bot", OpenID: "ou_other_bot", Name: "Other Bot", IsBot: true}},
	})
	if got == nil || got.RawContent != rawContent {
		t.Fatalf("RawContent = %#v, want %q", got, rawContent)
	}
	if got.SenderType != appintake.SenderTypeBot {
		t.Fatalf("SenderType = %q, want bot", got.SenderType)
	}
	if len(got.Mentions) != 1 || got.Mentions[0].Key != "@_user_1" || got.Mentions[0].UserID != "u_other_bot" || got.Mentions[0].IsBot == nil || !*got.Mentions[0].IsBot {
		t.Fatalf("mentions = %#v, want bot mention", got.Mentions)
	}
}

func TestOAPITransportMapsMessageRawThreadIDIntoTopicScope(t *testing.T) {
	channel := &fakeOAPIChannel{}
	now := time.Unix(100, 0)
	transport, err := NewOAPITransport(OAPITransportOptions{
		channel: channel,
		now:     func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewOAPITransport error = %v", err)
	}
	handler := &recordingLarkHandler{}
	if err := transport.Connect(context.Background(), handler); err != nil {
		t.Fatalf("Connect error = %v", err)
	}

	threadID := "omt_topic"
	raw := &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Message: &larkim.EventMessage{ThreadId: &threadID},
		},
	}
	if err := channel.message(context.Background(), &channeltypes.NormalizedMessage{
		EventID:        "evt_1",
		MessageID:      "om_1",
		ChatID:         "oc_chat",
		ChatType:       "group",
		UserID:         "ou_sender",
		Content:        "hello",
		RawContentType: "text",
		MentionedBot:   true,
		CreateTimeMs:   1234,
		RawEvent:       raw,
		Resources: []channeltypes.Resource{{
			Type:     "file",
			FileKey:  "file_1",
			FileName: "a.txt",
		}},
		Mentions: []channeltypes.Mention{{OpenID: "ou_bot", Name: "bot"}},
	}); err != nil {
		t.Fatalf("message callback error = %v", err)
	}
	if len(handler.events) != 1 {
		t.Fatalf("events = %d, want 1", len(handler.events))
	}
	event := handler.events[0]
	if event.Kind != appintake.EventMessage || event.Message == nil {
		t.Fatalf("event = %#v", event)
	}
	if event.Message.ThreadID != "omt_topic" || event.Message.ResolvedMode != appintake.ChatModeTopic {
		t.Fatalf("message scope = thread %q mode %q", event.Message.ThreadID, event.Message.ResolvedMode)
	}
	if event.Message.Resources[0].ID != "file_1" || event.Message.Mentions[0].OpenID != "ou_bot" {
		t.Fatalf("message resources/mentions = %#v %#v", event.Message.Resources, event.Message.Mentions)
	}
}

func TestOAPITransportMapsCommentCardActionAndLifecycle(t *testing.T) {
	channel := &fakeOAPIChannel{}
	now := time.Unix(200, 0)
	transport, err := NewOAPITransport(OAPITransportOptions{
		channel: channel,
		now:     func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewOAPITransport error = %v", err)
	}
	handler := &recordingLarkHandler{}
	if err := transport.Connect(context.Background(), handler); err != nil {
		t.Fatalf("Connect error = %v", err)
	}

	if err := channel.comment(context.Background(), &channeltypes.CommentEvent{
		EventID:      "evt_comment",
		FileToken:    "doccn_1",
		FileType:     "docx",
		CommentID:    "comment_1",
		ReplyID:      "reply_1",
		MentionedBot: true,
		Operator:     channeltypes.OperatorInfo{OpenID: "ou_operator"},
		Timestamp:    1000,
	}); err != nil {
		t.Fatalf("comment callback error = %v", err)
	}
	if err := channel.cardAction(context.Background(), &channeltypes.CardActionEvent{
		EventID:   "evt_card",
		MessageID: "om_card",
		ChatID:    "oc_chat",
		Operator:  channeltypes.CardActionOperator{OpenID: "ou_clicker"},
		Action: channeltypes.CardActionPayload{
			Value:     map[string]any{"cmd": "ws.list"},
			FormValue: map[string]any{"cwd": "/tmp"},
		},
	}); err != nil {
		t.Fatalf("card callback error = %v", err)
	}
	channel.reconnecting()
	channel.reconnected()
	channel.disconnected()

	if len(handler.events) != 5 {
		t.Fatalf("events = %d, want 5: %#v", len(handler.events), handler.events)
	}
	if handler.events[0].Comment == nil || handler.events[0].Comment.Operator.OpenID != "ou_operator" {
		t.Fatalf("comment event = %#v", handler.events[0])
	}
	card := handler.events[1].CardAction
	if card == nil || card.ActionValue["cmd"] != "ws.list" || card.FormValue["cwd"] != "/tmp" {
		t.Fatalf("card event = %#v", handler.events[1])
	}
	if handler.events[2].Reconnect == nil || handler.events[2].Reconnect.Phase != appintake.ReconnectReconnecting {
		t.Fatalf("reconnecting event = %#v", handler.events[2])
	}
	if handler.events[3].Reconnect == nil || handler.events[3].Reconnect.Phase != appintake.ReconnectRecovered {
		t.Fatalf("reconnected event = %#v", handler.events[3])
	}
	if handler.events[4].Disconnect == nil || handler.events[4].Disconnect.Reason != "disconnected" {
		t.Fatalf("disconnect event = %#v", handler.events[4])
	}
}

func TestOAPITransportMapsP2PCardAction(t *testing.T) {
	transport, err := NewOAPITransport(OAPITransportOptions{channel: &fakeOAPIChannel{}})
	if err != nil {
		t.Fatalf("NewOAPITransport error = %v", err)
	}
	card := transport.mapCardAction(context.Background(), &channeltypes.CardActionEvent{
		EventID:   "evt_card_dm",
		MessageID: "om_card_dm",
		ChatID:    "ou_clicker",
		Operator:  channeltypes.CardActionOperator{OpenID: "ou_clicker"},
		Action:    channeltypes.CardActionPayload{Value: map[string]any{"cmd": "status"}},
	})
	if card == nil || card.ChatID != "ou_clicker" || card.ChatType != appintake.ChatTypeP2P || card.ResolvedMode != appintake.ChatModeP2P {
		t.Fatalf("card action = %#v", card)
	}

	card = transport.mapCardAction(context.Background(), &channeltypes.CardActionEvent{
		EventID:   "evt_card_dm_empty",
		MessageID: "om_card_dm_empty",
		Operator:  channeltypes.CardActionOperator{OpenID: "ou_fallback"},
		Action:    channeltypes.CardActionPayload{Value: map[string]any{"cmd": "status"}},
	})
	if card == nil || card.ChatID != "ou_fallback" || card.ChatType != appintake.ChatTypeP2P || card.ResolvedMode != appintake.ChatModeP2P {
		t.Fatalf("fallback card action = %#v", card)
	}
}

func TestOAPITransportRejectsAmbiguousThreadSendOptions(t *testing.T) {
	transport, err := NewOAPITransport(OAPITransportOptions{channel: &fakeOAPIChannel{}})
	if err != nil {
		t.Fatalf("NewOAPITransport error = %v", err)
	}
	_, err = transport.SendMessage(context.Background(), SendMessageRequest{
		ChatID:  "oc_chat",
		Content: MessageContent{Text: "hello"},
		Options: SendOptions{ReplyInThread: true},
	})
	if !errors.Is(err, ErrOAPIReplyThread) {
		t.Fatalf("ReplyInThread error = %v, want %v", err, ErrOAPIReplyThread)
	}
	_, err = transport.SendMessage(context.Background(), SendMessageRequest{
		ChatID:  "oc_chat",
		Content: MessageContent{Text: "hello"},
		Options: SendOptions{ThreadID: "omt_topic"},
	})
	if !errors.Is(err, ErrOAPIThreadIDSend) {
		t.Fatalf("ThreadID error = %v, want %v", err, ErrOAPIThreadIDSend)
	}
}

func TestOAPITransportResolveLarkQuoteTextInteractiveAndMergeForward(t *testing.T) {
	cardJSON := `{"schema":"2.0","body":{"elements":[{"tag":"markdown","content":"real card"}]}}`
	server := newOAPICommentTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			writeOAPIJSON(t, w, map[string]any{"code": 0, "msg": "ok", "tenant_access_token": "tenant-token", "expire": 7200})
		case "/open-apis/im/v1/messages/om_text":
			assertOAPIQuoteQuery(t, r)
			writeOAPIQuoteMessage(t, w,
				oapiQuoteTestMessage("om_text", "text", `{"text":"hello quote"}`, "", "ou_sender", "Alice", "1000"),
			)
		case "/open-apis/im/v1/messages/om_card":
			assertOAPIQuoteQuery(t, r)
			writeOAPIQuoteMessage(t, w,
				oapiQuoteTestMessage("om_card", "interactive", cardJSON, "", "ou_card", "Card Sender", "2000"),
			)
		case "/open-apis/im/v1/messages/om_forward":
			assertOAPIQuoteQuery(t, r)
			writeOAPIQuoteMessage(t, w,
				oapiQuoteTestMessage("om_forward", "merge_forward", "Merged and Forwarded Message", "", "ou_parent", "Parent", "3000"),
				oapiQuoteTestMessage("om_child_text", "text", `{"text":"forwarded text"}`, "om_forward", "ou_child", "Child", "4000"),
				oapiQuoteTestMessage("om_child_card", "interactive", cardJSON, "om_forward", "ou_card", "Card Sender", "5000"),
				oapiQuoteTestMessage("om_nested", "merge_forward", "Nested Forward", "om_forward", "ou_nested", "Nested", "6000"),
			)
		case "/open-apis/im/v1/messages/om_nested":
			assertOAPIQuoteQuery(t, r)
			writeOAPIQuoteMessage(t, w,
				oapiQuoteTestMessage("om_nested", "merge_forward", "Nested Forward", "", "ou_nested", "Nested", "6000"),
				oapiQuoteTestMessage("om_grandchild", "text", `{"text":"nested forwarded text"}`, "om_nested", "ou_grand", "Grand", "7000"),
			)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	})
	transport := newOAPICommentTestTransport(t, server)

	text, ok, err := transport.ResolveLarkQuote(context.Background(), QuoteTarget{MessageID: "om_text"})
	if err != nil {
		t.Fatalf("ResolveLarkQuote text error = %v", err)
	}
	if !ok || text.Content != "hello quote" || text.SenderID != "ou_sender" || text.SenderName != "Alice" || text.CreatedAt != "1970-01-01T00:00:01.000Z" || text.RawContentType != "text" {
		t.Fatalf("text quote = %#v ok=%v", text, ok)
	}

	card, ok, err := transport.ResolveLarkQuote(context.Background(), QuoteTarget{MessageID: "om_card"})
	if err != nil {
		t.Fatalf("ResolveLarkQuote card error = %v", err)
	}
	if !ok || card.RawContentType != "interactive" || !strings.Contains(card.Content, "<interactive_card>") || !strings.Contains(card.Content, cardJSON) {
		t.Fatalf("card quote = %#v ok=%v", card, ok)
	}

	forward, ok, err := transport.ResolveLarkQuote(context.Background(), QuoteTarget{MessageID: "om_forward"})
	if err != nil {
		t.Fatalf("ResolveLarkQuote forward error = %v", err)
	}
	for _, fragment := range []string{
		"<forwarded_messages>",
		`<forwarded_message id="om_child_text"`,
		"forwarded text",
		`<forwarded_message id="om_child_card"`,
		"<interactive_card>",
		cardJSON,
		`<forwarded_message id="om_nested"`,
		`<forwarded_message id="om_grandchild"`,
		"nested forwarded text",
		"</forwarded_messages>",
	} {
		if !ok || !strings.Contains(forward.Content, fragment) {
			t.Fatalf("forward quote missing %q in %#v ok=%v", fragment, forward, ok)
		}
	}
	if forward.RawContentType != "merge_forward" {
		t.Fatalf("forward raw type = %q", forward.RawContentType)
	}
}

func TestOAPITransportHasLarkScope(t *testing.T) {
	server := newOAPICommentTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			writeOAPIJSON(t, w, map[string]any{"code": 0, "msg": "ok", "tenant_access_token": "tenant-token", "expire": 7200})
		case "/open-apis/application/v6/applications/cli_scope":
			assertOAPIQuery(t, r, map[string]string{"lang": "zh_cn", "user_id_type": "open_id"})
			writeOAPIJSON(t, w, map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": map[string]any{"app": map[string]any{"scopes": []any{
					map[string]any{"scope": GroupMsgScope},
					map[string]any{"scope": "im:message"},
				}}},
			})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	})
	transport := newOAPICommentTestTransport(t, server)

	has, err := transport.HasLarkScope(context.Background(), "cli_scope", GroupMsgScope)
	if err != nil {
		t.Fatalf("HasLarkScope returned error: %v", err)
	}
	if !has {
		t.Fatalf("HasLarkScope = false, want true")
	}
	has, err = transport.HasLarkScope(context.Background(), "cli_scope", "missing.scope")
	if err != nil {
		t.Fatalf("HasLarkScope missing returned error: %v", err)
	}
	if has {
		t.Fatalf("HasLarkScope missing = true, want false")
	}
}

func TestOAPITransportRequestLarkScopeGrant(t *testing.T) {
	server := newOAPICommentTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/v1/app/registration" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm returned error: %v", err)
		}
		switch r.Form.Get("action") {
		case "begin":
			writeOAPIJSON(t, w, map[string]any{
				"device_code":               "device-1",
				"verification_uri_complete": "https://example.com/verify",
				"expire_in":                 60,
				"interval":                  1,
			})
		case "poll":
			if r.Form.Get("device_code") != "device-1" {
				t.Fatalf("poll device_code = %q", r.Form.Get("device_code"))
			}
			writeOAPIJSON(t, w, map[string]any{
				"client_id":     "cli_scope",
				"client_secret": "secret",
				"user_info":     map[string]any{"open_id": "ou_user", "tenant_brand": "feishu"},
			})
		default:
			t.Fatalf("registration action = %q", r.Form.Get("action"))
		}
	})
	transport, err := NewOAPITransport(OAPITransportOptions{
		channel:            &fakeOAPIChannel{},
		RegistrationDomain: server.URL,
		Source:             "test",
	})
	if err != nil {
		t.Fatalf("NewOAPITransport returned error: %v", err)
	}

	link, err := transport.RequestLarkScopeGrant(context.Background(), ScopeGrantRequest{
		AppID:        "cli_scope",
		TenantScopes: []string{GroupMsgScope},
	})
	if err != nil {
		t.Fatalf("RequestLarkScopeGrant returned error: %v", err)
	}
	if link.URL == "" || !strings.Contains(link.URL, "clientID=cli_scope") || !strings.Contains(link.URL, "addons=") || link.ExpiresIn != time.Minute {
		t.Fatalf("scope grant link = %#v", link)
	}
	if link.Wait == nil {
		t.Fatalf("scope grant Wait is nil")
	}
	if err := link.Wait(context.Background()); err != nil {
		t.Fatalf("scope grant Wait returned error: %v", err)
	}
}

func TestOAPITransportRejectsRepeatedConnectOnSameInstance(t *testing.T) {
	transport, err := NewOAPITransport(OAPITransportOptions{channel: &fakeOAPIChannel{}})
	if err != nil {
		t.Fatalf("NewOAPITransport error = %v", err)
	}
	handler := &recordingLarkHandler{}
	if err := transport.Connect(context.Background(), handler); err != nil {
		t.Fatalf("first Connect error = %v", err)
	}
	err = transport.Connect(context.Background(), handler)
	if !errors.Is(err, ErrOAPIAlreadyStarted) {
		t.Fatalf("second Connect error = %v, want %v", err, ErrOAPIAlreadyStarted)
	}
}

func TestOAPITransportCanReconnectAfterDisconnectOrFailedStart(t *testing.T) {
	channel := &fakeOAPIChannel{}
	transport, err := NewOAPITransport(OAPITransportOptions{channel: channel})
	if err != nil {
		t.Fatalf("NewOAPITransport error = %v", err)
	}
	handler := &recordingLarkHandler{}
	if err := transport.Connect(context.Background(), handler); err != nil {
		t.Fatalf("first Connect error = %v", err)
	}
	if err := transport.Disconnect(context.Background()); err != nil {
		t.Fatalf("Disconnect error = %v", err)
	}
	if err := transport.Connect(context.Background(), handler); err != nil {
		t.Fatalf("reconnect after disconnect error = %v", err)
	}

	failedChannel := &fakeOAPIChannel{startErr: errors.New("boom")}
	failed, err := NewOAPITransport(OAPITransportOptions{channel: failedChannel})
	if err != nil {
		t.Fatalf("NewOAPITransport failed transport error = %v", err)
	}
	if err := failed.Connect(context.Background(), handler); err == nil {
		t.Fatalf("failed Connect returned nil")
	}
	failedChannel.startErr = nil
	if err := failed.Connect(context.Background(), handler); err != nil {
		t.Fatalf("Connect after failed start error = %v", err)
	}
}

func TestOAPIDefaultStartTimeoutCoversRequestTimeout(t *testing.T) {
	if DefaultOAPIStartTimeout < DefaultOAPIRequestTimeout {
		t.Fatalf("start timeout %s is shorter than request timeout %s", DefaultOAPIStartTimeout, DefaultOAPIRequestTimeout)
	}
}

func TestOAPITransportConstructorRequiresCredentialsWithoutInjectedChannel(t *testing.T) {
	_, err := NewOAPITransport(OAPITransportOptions{})
	if !errors.Is(err, ErrOAPIAppCredentials) {
		t.Fatalf("NewOAPITransport error = %v, want %v", err, ErrOAPIAppCredentials)
	}
}

func assertOAPIQuoteQuery(t *testing.T, r *http.Request) {
	t.Helper()
	assertOAPIQuery(t, r, map[string]string{
		"user_id_type":          "open_id",
		"card_msg_content_type": oapiQuoteCardContentType,
	})
}

func writeOAPIQuoteMessage(t *testing.T, w http.ResponseWriter, items ...map[string]any) {
	t.Helper()
	values := make([]any, 0, len(items))
	for _, item := range items {
		values = append(values, item)
	}
	writeOAPIJSON(t, w, map[string]any{
		"code": 0,
		"msg":  "ok",
		"data": map[string]any{"items": values},
	})
}

func oapiQuoteTestMessage(messageID, msgType, content, upperID, senderID, senderName, createTime string) map[string]any {
	message := map[string]any{
		"message_id":  messageID,
		"msg_type":    msgType,
		"create_time": createTime,
		"sender": map[string]any{
			"id":          senderID,
			"id_type":     "open_id",
			"sender_name": senderName,
		},
		"body": map[string]any{"content": content},
	}
	if upperID != "" {
		message["upper_message_id"] = upperID
	}
	return message
}

type fakeOAPIChannel struct {
	sends []channeltypes.SendInput

	onMessage       func(context.Context, *channeltypes.NormalizedMessage) error
	onComment       func(context.Context, *channeltypes.CommentEvent) error
	onCardAction    func(context.Context, *channeltypes.CardActionEvent) error
	onReady         func()
	onError         func(error)
	onReconnecting  func()
	onReconnected   func()
	onDisconnected  func()
	startErr        error
	stopErr         error
	botIdentity     *channeltypes.BotIdentity
	nextMessageSeq  int
	startWasInvoked bool
}

func (c *fakeOAPIChannel) Send(_ context.Context, input *channeltypes.SendInput) (*channeltypes.SendResult, error) {
	c.nextMessageSeq++
	c.sends = append(c.sends, *input)
	return &channeltypes.SendResult{MessageID: "om_fake"}, nil
}

func (c *fakeOAPIChannel) OnMessage(handler func(context.Context, *channeltypes.NormalizedMessage) error) {
	c.onMessage = handler
}

func (c *fakeOAPIChannel) OnComment(handler func(context.Context, *channeltypes.CommentEvent) error) {
	c.onComment = handler
}

func (c *fakeOAPIChannel) OnCardAction(handler func(context.Context, *channeltypes.CardActionEvent) error) {
	c.onCardAction = handler
}

func (c *fakeOAPIChannel) OnReady(handler func()) {
	c.onReady = handler
}

func (c *fakeOAPIChannel) OnError(handler func(error)) {
	c.onError = handler
}

func (c *fakeOAPIChannel) OnReconnecting(handler func()) {
	c.onReconnecting = handler
}

func (c *fakeOAPIChannel) OnReconnected(handler func()) {
	c.onReconnected = handler
}

func (c *fakeOAPIChannel) OnDisconnected(handler func()) {
	c.onDisconnected = handler
}

func (c *fakeOAPIChannel) Start(context.Context) error {
	c.startWasInvoked = true
	if c.onReady != nil {
		c.onReady()
	}
	return c.startErr
}

func (c *fakeOAPIChannel) Stop(context.Context) error {
	return c.stopErr
}

func (c *fakeOAPIChannel) GetBotIdentity(context.Context) *channeltypes.BotIdentity {
	return c.botIdentity
}

func (c *fakeOAPIChannel) message(ctx context.Context, msg *channeltypes.NormalizedMessage) error {
	return c.onMessage(ctx, msg)
}

func (c *fakeOAPIChannel) comment(ctx context.Context, event *channeltypes.CommentEvent) error {
	return c.onComment(ctx, event)
}

func (c *fakeOAPIChannel) cardAction(ctx context.Context, event *channeltypes.CardActionEvent) error {
	return c.onCardAction(ctx, event)
}

func (c *fakeOAPIChannel) reconnecting() {
	c.onReconnecting()
}

func (c *fakeOAPIChannel) reconnected() {
	c.onReconnected()
}

func (c *fakeOAPIChannel) disconnected() {
	c.onDisconnected()
}

type recordingLarkHandler struct {
	events []IncomingEvent
}

func (h *recordingLarkHandler) HandleLarkTransportEvent(_ context.Context, event IncomingEvent) error {
	h.events = append(h.events, event)
	return nil
}
