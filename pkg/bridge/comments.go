package bridge

import (
	"context"
	"errors"
	"time"

	appcomments "github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/comments"
)

var (
	ErrNilCommentSurface = errors.New("comment surface is nil")
	ErrCommentNoAccess   = appcomments.ErrNoAccess
)

type CommentTarget struct {
	FileToken string
	FileType  string
}

type CommentReplyTextRun struct {
	Text string
}

type CommentReplyDocsLink struct {
	URL string
}

type CommentReplyPerson struct {
	UserID string
}

type CommentReplyElement struct {
	Type     string
	TextRun  *CommentReplyTextRun
	DocsLink *CommentReplyDocsLink
	Person   *CommentReplyPerson
}

type CommentReplyContent struct {
	Elements []CommentReplyElement
}

type CommentReply struct {
	ReplyID string
	Content CommentReplyContent
}

type CommentThread struct {
	Replies []CommentReply
	Quote   string
	IsWhole bool
}

type CommentReplyOptions struct {
	TopLevel bool
}

type CommentSurface interface {
	ResolveCommentTarget(ctx context.Context, fileToken string, fileType string) (CommentTarget, bool, error)
	FetchComment(ctx context.Context, target CommentTarget, commentID string) (CommentThread, error)
	ReplyToComment(ctx context.Context, target CommentTarget, commentID string, text string, opts CommentReplyOptions) error
	AddCommentReaction(ctx context.Context, target CommentTarget, replyID string) (bool, error)
	RemoveCommentReaction(ctx context.Context, target CommentTarget, replyID string) error
}

type CommentWorkspaceStore interface {
	CWDFor(scopeID string) string
}

type CommentOptions struct {
	Workspaces              CommentWorkspaceStore
	ManagedDefaultWorkspace string
	StopGrace               time.Duration
	Model                   string
	Nowait                  bool
}

type CommentHandler struct {
	service *appcomments.Service
}

type CommentResultStatus string

const (
	CommentResultSkipped CommentResultStatus = "skipped"
	CommentResultReplied CommentResultStatus = "replied"
)

type CommentWorkspaceFallback struct {
	From   string
	To     string
	Reason string
}

type CommentWorkspaceResult struct {
	OK           bool
	RequestedCWD string
	CWDRealpath  string
	Reason       string
	UserVisible  string
	Fallback     *CommentWorkspaceFallback
}

type CommentResult struct {
	Status           CommentResultStatus
	Reason           string
	CommentScopeKey  string
	DocumentScopeKey string
	ExecutionScopeID string
	SessionScopeID   string
	Target           CommentTarget
	Prompt           string
	Reply            string
	Workspace        CommentWorkspaceResult
}

func NewCommentHandler(client *Client, surface CommentSurface, opts CommentOptions) (*CommentHandler, error) {
	if client == nil {
		return nil, ErrNilClient
	}
	if surface == nil {
		return nil, ErrNilCommentSurface
	}
	service := appcomments.NewService(appcomments.Options{
		Resolver:                bridgeCommentSurface{surface: surface},
		Fetcher:                 bridgeCommentSurface{surface: surface},
		Replier:                 bridgeCommentSurface{surface: surface},
		Reactor:                 bridgeCommentSurface{surface: surface},
		Executor:                appcomments.RunExecutorAdapter{Executor: client.executor},
		Sessions:                client.sessions,
		SessionCatalog:          client.catalog,
		Workspaces:              bridgeCommentWorkspaceStore{inner: opts.Workspaces},
		ProfileConfig:           client.profile,
		Capability:              client.cap,
		ManagedDefaultWorkspace: opts.ManagedDefaultWorkspace,
		StopGraceMs:             int(opts.StopGrace / time.Millisecond),
		Model:                   opts.Model,
		Nowait:                  opts.Nowait,
	})
	return &CommentHandler{service: service}, nil
}

func (h *CommentHandler) Handle(ctx context.Context, input LarkCommentInput) (CommentResult, error) {
	if h == nil || h.service == nil {
		return CommentResult{}, ErrNilClient
	}
	result, err := h.service.Handle(ctx, toInternalLarkCommentInput(input))
	return fromCommentResult(result), err
}

func (c *Client) HandleCommentMention(ctx context.Context, input LarkCommentInput, surface CommentSurface, opts CommentOptions) (CommentResult, error) {
	handler, err := NewCommentHandler(c, surface, opts)
	if err != nil {
		return CommentResult{}, err
	}
	return handler.Handle(ctx, input)
}

func CommentDocumentScopeKey(fileToken string) string {
	return appcomments.DocumentScopeKey(fileToken)
}

func CommentScopeKey(fileToken string, commentID string) string {
	return appcomments.ScopeKey(fileToken, commentID)
}

func CommentDocumentSessionScopeKey(fileToken string) string {
	return appcomments.DocumentSessionScopeKey(fileToken)
}

func LegacyCommentDocumentSessionScopeKey(fileToken string) string {
	return appcomments.LegacyDocumentSessionScopeKey(fileToken)
}

func BuildCommentPrompt(target CommentTarget, question string, quote string, isWhole bool) string {
	return appcomments.BuildPrompt(toAppCommentTarget(target), appcomments.CommentContext{
		Question: question,
		Quote:    quote,
		IsWhole:  isWhole,
	})
}

func StripCommentMarkdown(text string) string {
	return appcomments.StripMarkdown(text)
}

type bridgeCommentSurface struct {
	surface CommentSurface
}

func (s bridgeCommentSurface) ResolveCommentTarget(ctx context.Context, fileToken string, fileType string) (appcomments.Target, bool, error) {
	target, ok, err := s.surface.ResolveCommentTarget(ctx, fileToken, fileType)
	return toAppCommentTarget(target), ok, err
}

func (s bridgeCommentSurface) FetchComment(ctx context.Context, target appcomments.Target, commentID string) (appcomments.Thread, error) {
	thread, err := s.surface.FetchComment(ctx, fromAppCommentTarget(target), commentID)
	return toAppCommentThread(thread), err
}

func (s bridgeCommentSurface) ReplyToComment(ctx context.Context, target appcomments.Target, commentID string, text string, opts appcomments.ReplyOptions) error {
	return s.surface.ReplyToComment(ctx, fromAppCommentTarget(target), commentID, text, CommentReplyOptions{TopLevel: opts.TopLevel})
}

func (s bridgeCommentSurface) AddReaction(ctx context.Context, target appcomments.Target, replyID string) (bool, error) {
	return s.surface.AddCommentReaction(ctx, fromAppCommentTarget(target), replyID)
}

func (s bridgeCommentSurface) RemoveReaction(ctx context.Context, target appcomments.Target, replyID string) error {
	return s.surface.RemoveCommentReaction(ctx, fromAppCommentTarget(target), replyID)
}

type bridgeCommentWorkspaceStore struct {
	inner CommentWorkspaceStore
}

func (s bridgeCommentWorkspaceStore) CWDFor(scopeID string) string {
	if s.inner == nil {
		return ""
	}
	return s.inner.CWDFor(scopeID)
}

func toAppCommentTarget(target CommentTarget) appcomments.Target {
	return appcomments.Target{FileToken: target.FileToken, FileType: target.FileType}
}

func fromAppCommentTarget(target appcomments.Target) CommentTarget {
	return CommentTarget{FileToken: target.FileToken, FileType: target.FileType}
}

func toAppCommentThread(thread CommentThread) appcomments.Thread {
	replies := make([]appcomments.Reply, 0, len(thread.Replies))
	for _, reply := range thread.Replies {
		elements := make([]appcomments.ReplyElement, 0, len(reply.Content.Elements))
		for _, element := range reply.Content.Elements {
			elements = append(elements, toAppCommentElement(element))
		}
		replies = append(replies, appcomments.Reply{
			ReplyID: reply.ReplyID,
			Content: appcomments.ReplyContent{Elements: elements},
		})
	}
	return appcomments.Thread{
		Replies: replies,
		Quote:   thread.Quote,
		IsWhole: thread.IsWhole,
	}
}

func fromAppCommentThread(thread appcomments.Thread) CommentThread {
	replies := make([]CommentReply, 0, len(thread.Replies))
	for _, reply := range thread.Replies {
		elements := make([]CommentReplyElement, 0, len(reply.Content.Elements))
		for _, element := range reply.Content.Elements {
			elements = append(elements, fromAppCommentElement(element))
		}
		replies = append(replies, CommentReply{
			ReplyID: reply.ReplyID,
			Content: CommentReplyContent{Elements: elements},
		})
	}
	return CommentThread{
		Replies: replies,
		Quote:   thread.Quote,
		IsWhole: thread.IsWhole,
	}
}

func toAppCommentElement(element CommentReplyElement) appcomments.ReplyElement {
	out := appcomments.ReplyElement{Type: element.Type}
	if element.TextRun != nil {
		out.TextRun = &appcomments.ReplyTextRun{Text: element.TextRun.Text}
	}
	if element.DocsLink != nil {
		out.DocsLink = &appcomments.ReplyDocsLink{URL: element.DocsLink.URL}
	}
	if element.Person != nil {
		out.Person = &appcomments.ReplyPerson{UserID: element.Person.UserID}
	}
	return out
}

func fromAppCommentElement(element appcomments.ReplyElement) CommentReplyElement {
	out := CommentReplyElement{Type: element.Type}
	if element.TextRun != nil {
		out.TextRun = &CommentReplyTextRun{Text: element.TextRun.Text}
	}
	if element.DocsLink != nil {
		out.DocsLink = &CommentReplyDocsLink{URL: element.DocsLink.URL}
	}
	if element.Person != nil {
		out.Person = &CommentReplyPerson{UserID: element.Person.UserID}
	}
	return out
}

func fromCommentResult(result appcomments.Result) CommentResult {
	return CommentResult{
		Status:           CommentResultStatus(result.Status),
		Reason:           result.Reason,
		CommentScopeKey:  result.CommentScopeKey,
		DocumentScopeKey: result.DocumentScopeKey,
		ExecutionScopeID: result.ExecutionScopeID,
		SessionScopeID:   result.SessionScopeID,
		Target:           fromAppCommentTarget(result.Target),
		Prompt:           result.Prompt,
		Reply:            result.Reply,
		Workspace:        fromCommentWorkspaceResult(result.Workspace),
	}
}

func fromCommentWorkspaceResult(result appcomments.WorkspaceResult) CommentWorkspaceResult {
	out := CommentWorkspaceResult{
		OK:           result.OK,
		RequestedCWD: result.RequestedCWD,
		CWDRealpath:  result.CWDRealpath,
		Reason:       string(result.Reason),
		UserVisible:  result.UserVisible,
	}
	if result.Fallback != nil {
		out.Fallback = &CommentWorkspaceFallback{
			From:   result.Fallback.From,
			To:     result.Fallback.To,
			Reason: result.Fallback.Reason,
		}
	}
	return out
}
