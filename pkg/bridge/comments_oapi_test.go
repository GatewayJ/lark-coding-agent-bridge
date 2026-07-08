package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewOAPICommentSurfaceRejectsNilTransport(t *testing.T) {
	surface, err := NewOAPICommentSurface(nil)
	if !errors.Is(err, ErrNilLarkTransport) {
		t.Fatalf("NewOAPICommentSurface err = %v, want %v", err, ErrNilLarkTransport)
	}
	if surface != nil {
		t.Fatalf("surface = %#v, want nil", surface)
	}
}

func TestOAPICommentSurfacePublicFacadeFetchesComment(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			writeBridgeOAPIJSON(t, w, map[string]any{"code": 0, "msg": "ok", "tenant_access_token": "tenant-token", "expire": 7200})
		case "/open-apis/drive/v1/files/doc_token/comments/comment_1":
			if r.URL.Query().Get("file_type") != "docx" || r.URL.Query().Get("user_id_type") != "open_id" {
				t.Fatalf("query = %s", r.URL.RawQuery)
			}
			writeBridgeOAPIJSON(t, w, map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": map[string]any{
					"quote":    "quote",
					"is_whole": true,
					"reply_list": map[string]any{"replies": []any{map[string]any{
						"reply_id": "reply_1",
						"content": map[string]any{"elements": []any{map[string]any{
							"type":     "text_run",
							"text_run": map[string]any{"text": "question"},
						}}},
					}}},
				},
			})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	t.Cleanup(server.Close)

	transport, err := NewOAPILarkTransport(OAPILarkTransportOptions{
		AppID:            "cli_public_comment_test",
		AppSecret:        "secret",
		Domain:           server.URL,
		RequestTimeout:   5 * time.Second,
		DisableWebSocket: true,
	})
	if err != nil {
		t.Fatalf("NewOAPILarkTransport returned error: %v", err)
	}
	surface, err := NewOAPICommentSurface(transport)
	if err != nil {
		t.Fatalf("NewOAPICommentSurface returned error: %v", err)
	}
	thread, err := surface.FetchComment(context.Background(), CommentTarget{FileToken: "doc_token", FileType: "docx"}, "comment_1")
	if err != nil {
		t.Fatalf("FetchComment returned error: %v", err)
	}
	if thread.Quote != "quote" || !thread.IsWhole || len(thread.Replies) != 1 {
		t.Fatalf("thread = %#v", thread)
	}
	if got := thread.Replies[0].Content.Elements[0].TextRun.Text; got != "question" {
		t.Fatalf("reply text = %q", got)
	}
}

func TestOAPICommentSurfaceMapsInternalOAPIErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			writeBridgeOAPIJSON(t, w, map[string]any{"code": 0, "msg": "ok", "tenant_access_token": "tenant-token", "expire": 7200})
		case "/open-apis/drive/v1/files/doc_token/comments/comment_1":
			writeBridgeOAPIJSON(t, w, map[string]any{"code": 999, "msg": "comment denied"})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	t.Cleanup(server.Close)

	transport, err := NewOAPILarkTransport(OAPILarkTransportOptions{
		AppID:            "cli_public_comment_error_test",
		AppSecret:        "secret",
		Domain:           server.URL,
		RequestTimeout:   5 * time.Second,
		DisableWebSocket: true,
	})
	if err != nil {
		t.Fatalf("NewOAPILarkTransport returned error: %v", err)
	}
	surface, err := NewOAPICommentSurface(transport)
	if err != nil {
		t.Fatalf("NewOAPICommentSurface returned error: %v", err)
	}
	_, err = surface.FetchComment(context.Background(), CommentTarget{FileToken: "doc_token", FileType: "docx"}, "comment_1")
	var publicErr *LarkOAPIError
	if !errors.As(err, &publicErr) {
		t.Fatalf("FetchComment error = %T %[1]v, want LarkOAPIError", err)
	}
	if publicErr.Code != 999 || publicErr.Message != "comment denied" {
		t.Fatalf("public error = %#v", publicErr)
	}
}

func writeBridgeOAPIJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("write json error = %v", err)
	}
}
