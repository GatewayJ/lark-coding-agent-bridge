package bridge

import (
	"context"

	appcomments "github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/comments"
)

func NewOAPICommentSurface(transport *OAPILarkTransport) (CommentSurface, error) {
	if transport == nil || transport.inner == nil {
		return nil, ErrNilLarkTransport
	}
	return oapiCommentSurface{transport: transport}, nil
}

type oapiCommentSurface struct {
	transport *OAPILarkTransport
}

var _ CommentSurface = oapiCommentSurface{}

func (s oapiCommentSurface) ResolveCommentTarget(ctx context.Context, fileToken string, fileType string) (CommentTarget, bool, error) {
	target, ok, err := s.transport.inner.ResolveCommentTarget(ctx, fileToken, fileType)
	return fromAppCommentTarget(target), ok, fromInternalLarkError(err)
}

func (s oapiCommentSurface) FetchComment(ctx context.Context, target CommentTarget, commentID string) (CommentThread, error) {
	thread, err := s.transport.inner.FetchComment(ctx, toAppCommentTarget(target), commentID)
	return fromAppCommentThread(thread), fromInternalLarkError(err)
}

func (s oapiCommentSurface) ReplyToComment(ctx context.Context, target CommentTarget, commentID string, text string, opts CommentReplyOptions) error {
	return fromInternalLarkError(s.transport.inner.ReplyToComment(ctx, toAppCommentTarget(target), commentID, text, appcomments.ReplyOptions{TopLevel: opts.TopLevel}))
}

func (s oapiCommentSurface) AddCommentReaction(ctx context.Context, target CommentTarget, replyID string) (bool, error) {
	ok, err := s.transport.inner.AddReaction(ctx, toAppCommentTarget(target), replyID)
	return ok, fromInternalLarkError(err)
}

func (s oapiCommentSurface) RemoveCommentReaction(ctx context.Context, target CommentTarget, replyID string) error {
	return fromInternalLarkError(s.transport.inner.RemoveReaction(ctx, toAppCommentTarget(target), replyID))
}
