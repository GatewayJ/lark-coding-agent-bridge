package lark

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	larksdk "github.com/larksuite/oapi-sdk-go/v3"
	appcomments "github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/comments"
)

func TestOAPICommentSurfaceResolvesWikiNode(t *testing.T) {
	server := newOAPICommentTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			writeOAPIJSON(t, w, map[string]any{"code": 0, "msg": "ok", "tenant_access_token": "tenant-token", "expire": 7200})
		case "/open-apis/wiki/v2/spaces/get_node":
			switch r.URL.Query().Get("token") {
			case "wiki_token":
				writeOAPIJSON(t, w, map[string]any{"code": 0, "msg": "ok", "data": map[string]any{"node": map[string]any{"obj_token": "doc_token", "obj_type": "docx"}}})
			case "slides_token":
				writeOAPIJSON(t, w, map[string]any{"code": 0, "msg": "ok", "data": map[string]any{"node": map[string]any{"obj_token": "slides_token", "obj_type": "slides"}}})
			case "denied_token":
				writeOAPIJSON(t, w, map[string]any{"code": 999999, "msg": "wiki forbidden"})
			default:
				writeOAPIJSON(t, w, map[string]any{"code": oapiWikiNodeNotFoundCode, "msg": "not wiki"})
			}
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	})
	transport := newOAPICommentTestTransport(t, server)

	target, ok, err := transport.ResolveCommentTarget(context.Background(), "wiki_token", "docx")
	if err != nil {
		t.Fatalf("ResolveCommentTarget wiki error = %v", err)
	}
	if !ok || target.FileToken != "doc_token" || target.FileType != "docx" {
		t.Fatalf("resolved wiki target = %#v ok=%v", target, ok)
	}

	target, ok, err = transport.ResolveCommentTarget(context.Background(), "direct_token", "doc")
	if err != nil {
		t.Fatalf("ResolveCommentTarget direct error = %v", err)
	}
	if !ok || target.FileToken != "direct_token" || target.FileType != "doc" {
		t.Fatalf("direct target = %#v ok=%v", target, ok)
	}

	target, ok, err = transport.ResolveCommentTarget(context.Background(), "denied_token", "docx")
	if err != nil {
		t.Fatalf("ResolveCommentTarget denied wiki error = %v", err)
	}
	if !ok || target.FileToken != "denied_token" || target.FileType != "docx" {
		t.Fatalf("denied wiki target = %#v ok=%v", target, ok)
	}

	_, ok, err = transport.ResolveCommentTarget(context.Background(), "slides_token", "docx")
	if err != nil {
		t.Fatalf("ResolveCommentTarget slides error = %v", err)
	}
	if ok {
		t.Fatalf("slides wiki target should be unsupported")
	}
}

func TestOAPICommentSurfaceFetchFallsBackToPaginatedList(t *testing.T) {
	var listCalls int
	server := newOAPICommentTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			writeOAPIJSON(t, w, map[string]any{"code": 0, "msg": "ok", "tenant_access_token": "tenant-token", "expire": 7200})
		case "/open-apis/drive/v1/files/doc_token/comments/comment_1":
			assertOAPIQuery(t, r, map[string]string{
				"file_type":     "docx",
				"user_id_type":  "open_id",
				"need_reaction": "false",
			})
			writeOAPIJSON(t, w, map[string]any{"code": oapiFileCommentNotFoundCode, "msg": "not exist"})
		case "/open-apis/drive/v1/files/doc_token/comments":
			if r.Method != http.MethodGet {
				t.Fatalf("list method = %s", r.Method)
			}
			listCalls++
			assertOAPIQuery(t, r, map[string]string{
				"file_type":     "docx",
				"user_id_type":  "open_id",
				"need_reaction": "false",
				"page_size":     "100",
			})
			if listCalls == 1 {
				if got := r.URL.Query().Get("page_token"); got != "" {
					t.Fatalf("first page_token = %q", got)
				}
				writeOAPIJSON(t, w, map[string]any{
					"code": 0,
					"msg":  "ok",
					"data": map[string]any{
						"has_more":   true,
						"page_token": "p2",
						"items":      []any{map[string]any{"comment_id": "other"}},
					},
				})
				return
			}
			if got := r.URL.Query().Get("page_token"); got != "p2" {
				t.Fatalf("second page_token = %q", got)
			}
			writeOAPIJSON(t, w, map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": map[string]any{
					"has_more": false,
					"items": []any{map[string]any{
						"comment_id": "comment_1",
						"quote":      "selected quote",
						"is_whole":   false,
						"reply_list": map[string]any{"replies": []any{map[string]any{
							"reply_id": "reply_1",
							"content": map[string]any{"elements": []any{
								map[string]any{"type": "text_run", "text_run": map[string]any{"text": "hello "}},
								map[string]any{"type": "docs_link", "docs_link": map[string]any{"url": "https://example.test/doc"}},
								map[string]any{"type": "person", "person": map[string]any{"user_id": "ou_user"}},
							}},
						}}},
					}},
				},
			})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	})
	transport := newOAPICommentTestTransport(t, server)

	thread, err := transport.FetchComment(context.Background(), appcomments.Target{FileToken: "doc_token", FileType: "docx"}, "comment_1")
	if err != nil {
		t.Fatalf("FetchComment error = %v", err)
	}
	if listCalls != 2 {
		t.Fatalf("list calls = %d, want 2", listCalls)
	}
	if thread.Quote != "selected quote" || thread.IsWhole {
		t.Fatalf("thread metadata = %#v", thread)
	}
	if len(thread.Replies) != 1 || thread.Replies[0].ReplyID != "reply_1" {
		t.Fatalf("replies = %#v", thread.Replies)
	}
	elements := thread.Replies[0].Content.Elements
	if len(elements) != 3 || elements[0].TextRun.Text != "hello " || elements[1].DocsLink.URL != "https://example.test/doc" || elements[2].Person.UserID != "ou_user" {
		t.Fatalf("reply elements = %#v", elements)
	}
}

func TestOAPICommentSurfaceFetchNoAccessFromList(t *testing.T) {
	server := newOAPICommentTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			writeOAPIJSON(t, w, map[string]any{"code": 0, "msg": "ok", "tenant_access_token": "tenant-token", "expire": 7200})
		case "/open-apis/drive/v1/files/doc_token/comments/comment_1":
			writeOAPIJSON(t, w, map[string]any{"code": oapiFileCommentNotFoundCode, "msg": "not exist"})
		case "/open-apis/drive/v1/files/doc_token/comments":
			writeOAPIJSON(t, w, map[string]any{"code": oapiFileCommentNotFoundCode, "msg": "not exist"})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	})
	transport := newOAPICommentTestTransport(t, server)

	_, err := transport.FetchComment(context.Background(), appcomments.Target{FileToken: "doc_token", FileType: "docx"}, "comment_1")
	if !errors.Is(err, appcomments.ErrNoAccess) {
		t.Fatalf("FetchComment err = %v, want ErrNoAccess", err)
	}
}

func TestOAPICommentSurfaceReplyFallsBackToTopLevelAndUpdatesReaction(t *testing.T) {
	var replyCalls int
	var topLevelCalls int
	reactionActions := []string{}
	server := newOAPICommentTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			writeOAPIJSON(t, w, map[string]any{"code": 0, "msg": "ok", "tenant_access_token": "tenant-token", "expire": 7200})
		case "/open-apis/drive/v1/files/doc_token/comments/comment_1/replies":
			if r.Method != http.MethodPost {
				t.Fatalf("reply method = %s", r.Method)
			}
			replyCalls++
			assertOAPIQuery(t, r, map[string]string{"file_type": "docx", "user_id_type": "open_id"})
			body := decodeOAPIBody(t, r)
			if text := bodyText(t, body); text != "plain reply" {
				t.Fatalf("reply body text = %q", text)
			}
			writeOAPIJSON(t, w, map[string]any{"code": oapiWholeDocumentReplyOnlyCode, "msg": "whole doc"})
		case "/open-apis/drive/v1/files/doc_token/comments":
			if r.Method != http.MethodPost {
				t.Fatalf("top-level method = %s", r.Method)
			}
			topLevelCalls++
			assertOAPIQuery(t, r, map[string]string{"file_type": "docx", "user_id_type": "open_id"})
			body := decodeOAPIBody(t, r)
			if text := topLevelBodyText(t, body); text != "plain reply" {
				t.Fatalf("top-level body text = %q", text)
			}
			writeOAPIJSON(t, w, map[string]any{"code": 0, "msg": "ok", "data": map[string]any{"comment_id": "new_comment"}})
		case "/open-apis/drive/v2/files/doc_token/comments/reaction":
			if r.Method != http.MethodPost {
				t.Fatalf("reaction method = %s", r.Method)
			}
			assertOAPIQuery(t, r, map[string]string{"file_type": "docx"})
			body := decodeOAPIBody(t, r)
			reactionActions = append(reactionActions, stringField(t, body, "action"))
			if got := stringField(t, body, "reply_id"); got != "reply_1" {
				t.Fatalf("reaction reply_id = %q", got)
			}
			if got := stringField(t, body, "reaction_type"); got != "Typing" {
				t.Fatalf("reaction type = %q", got)
			}
			writeOAPIJSON(t, w, map[string]any{"code": 0, "msg": "ok"})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	})
	transport := newOAPICommentTestTransport(t, server)
	target := appcomments.Target{FileToken: "doc_token", FileType: "docx"}

	if err := transport.ReplyToComment(context.Background(), target, "comment_1", "plain reply", appcomments.ReplyOptions{}); err != nil {
		t.Fatalf("ReplyToComment error = %v", err)
	}
	if replyCalls != 1 || topLevelCalls != 1 {
		t.Fatalf("reply fallback calls reply=%d topLevel=%d", replyCalls, topLevelCalls)
	}

	added, err := transport.AddReaction(context.Background(), target, "reply_1")
	if err != nil || !added {
		t.Fatalf("AddReaction added=%v err=%v", added, err)
	}
	if err := transport.RemoveReaction(context.Background(), target, "reply_1"); err != nil {
		t.Fatalf("RemoveReaction error = %v", err)
	}
	if got := strings.Join(reactionActions, ","); got != "add,delete" {
		t.Fatalf("reaction actions = %q", got)
	}
}

func TestOAPICommentSurfaceTopLevelReplySkipsThreadProbe(t *testing.T) {
	var topLevelCalls int
	server := newOAPICommentTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			writeOAPIJSON(t, w, map[string]any{"code": 0, "msg": "ok", "tenant_access_token": "tenant-token", "expire": 7200})
		case "/open-apis/drive/v1/files/doc_token/comments":
			if r.Method != http.MethodPost {
				t.Fatalf("top-level method = %s", r.Method)
			}
			topLevelCalls++
			body := decodeOAPIBody(t, r)
			if text := topLevelBodyText(t, body); text != "whole reply" {
				t.Fatalf("top-level body text = %q", text)
			}
			writeOAPIJSON(t, w, map[string]any{"code": 0, "msg": "ok", "data": map[string]any{"comment_id": "new_comment"}})
		case "/open-apis/drive/v1/files/doc_token/comments/comment_1/replies":
			t.Fatalf("top-level reply should not probe thread endpoint")
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	})
	transport := newOAPICommentTestTransport(t, server)

	err := transport.ReplyToComment(context.Background(), appcomments.Target{FileToken: "doc_token", FileType: "docx"}, "comment_1", "whole reply", appcomments.ReplyOptions{TopLevel: true})
	if err != nil {
		t.Fatalf("ReplyToComment top-level error = %v", err)
	}
	if topLevelCalls != 1 {
		t.Fatalf("top-level calls = %d, want 1", topLevelCalls)
	}
}

func newOAPICommentTestServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

func newOAPICommentTestTransport(t *testing.T, server *httptest.Server) *OAPITransport {
	t.Helper()
	appID := "cli_" + strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	client := larksdk.NewClient(appID, "secret",
		larksdk.WithOpenBaseUrl(server.URL),
		larksdk.WithHttpClient(server.Client()),
		larksdk.WithReqTimeout(5*time.Second),
	)
	transport, err := NewOAPITransport(OAPITransportOptions{
		Client:  client,
		channel: &fakeOAPIChannel{},
	})
	if err != nil {
		t.Fatalf("NewOAPITransport error = %v", err)
	}
	return transport
}

func writeOAPIJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("write json error = %v", err)
	}
}

func assertOAPIQuery(t *testing.T, r *http.Request, want map[string]string) {
	t.Helper()
	for key, value := range want {
		if got := r.URL.Query().Get(key); got != value {
			t.Fatalf("query %s = %q, want %q on %s", key, got, value, r.URL.String())
		}
	}
}

func decodeOAPIBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatalf("decode body error = %v", err)
	}
	return body
}

func bodyText(t *testing.T, body map[string]any) string {
	t.Helper()
	content := mapField(t, body, "content")
	return replyContentText(t, content)
}

func topLevelBodyText(t *testing.T, body map[string]any) string {
	t.Helper()
	replyList := mapField(t, body, "reply_list")
	replies := sliceField(t, replyList, "replies")
	if len(replies) != 1 {
		t.Fatalf("top-level replies = %#v", replies)
	}
	reply, ok := replies[0].(map[string]any)
	if !ok {
		t.Fatalf("reply has type %T", replies[0])
	}
	return replyContentText(t, mapField(t, reply, "content"))
}

func replyContentText(t *testing.T, content map[string]any) string {
	t.Helper()
	elements := sliceField(t, content, "elements")
	if len(elements) != 1 {
		t.Fatalf("elements = %#v", elements)
	}
	element, ok := elements[0].(map[string]any)
	if !ok {
		t.Fatalf("element has type %T", elements[0])
	}
	if typ := stringField(t, element, "type"); typ != "text_run" {
		t.Fatalf("element type = %q", typ)
	}
	return stringField(t, mapField(t, element, "text_run"), "text")
}

func mapField(t *testing.T, value map[string]any, field string) map[string]any {
	t.Helper()
	next, ok := value[field].(map[string]any)
	if !ok {
		t.Fatalf("field %s has type %T in %#v", field, value[field], value)
	}
	return next
}

func sliceField(t *testing.T, value map[string]any, field string) []any {
	t.Helper()
	next, ok := value[field].([]any)
	if !ok {
		t.Fatalf("field %s has type %T in %#v", field, value[field], value)
	}
	return next
}

func stringField(t *testing.T, value map[string]any, field string) string {
	t.Helper()
	next, ok := value[field].(string)
	if !ok {
		t.Fatalf("field %s has type %T in %#v", field, value[field], value)
	}
	return next
}
