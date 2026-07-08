package lark

import (
	"context"

	larkdrivev1 "github.com/larksuite/oapi-sdk-go/v3/service/drive/v1"
	larkdrivev2 "github.com/larksuite/oapi-sdk-go/v3/service/drive/v2"
	larkwiki "github.com/larksuite/oapi-sdk-go/v3/service/wiki/v2"
	appcomments "github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/comments"
)

const (
	oapiWikiNodeNotFoundCode       = 131005
	oapiWikiEndpointNotWikiCode    = 99991672
	oapiFileCommentNotFoundCode    = 1069307
	oapiWholeDocumentReplyOnlyCode = 1069302

	oapiCommentUserIDType     = "open_id"
	oapiCommentReactionTyping = "Typing"
	oapiCommentListPageSize   = 100
	oapiCommentMaxListPages   = 10
)

func (t *OAPITransport) ResolveCommentTarget(ctx context.Context, fileToken string, fileType string) (appcomments.Target, bool, error) {
	base := appcomments.Target{FileToken: fileToken, FileType: fileType}
	if !isSupportedOAPICommentFileType(fileType) {
		return appcomments.Target{}, false, nil
	}
	if t == nil || t.client == nil {
		return appcomments.Target{}, false, ErrOAPIClient
	}

	resp, err := t.client.Wiki.V2.Space.GetNode(ctx, larkwiki.NewGetNodeSpaceReqBuilder().
		Token(fileToken).
		Build())
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return appcomments.Target{}, false, ctxErr
		}
		return base, true, nil
	}
	if !resp.Success() {
		return base, true, nil
	}
	if resp.Data == nil || resp.Data.Node == nil || resp.Data.Node.ObjToken == nil || resp.Data.Node.ObjType == nil {
		return base, true, nil
	}
	target := appcomments.Target{FileToken: *resp.Data.Node.ObjToken, FileType: *resp.Data.Node.ObjType}
	if target.FileToken == "" || target.FileType == "" {
		return base, true, nil
	}
	if !isSupportedOAPICommentFileType(target.FileType) {
		return appcomments.Target{}, false, nil
	}
	return target, true, nil
}

func (t *OAPITransport) FetchComment(ctx context.Context, target appcomments.Target, commentID string) (appcomments.Thread, error) {
	if t == nil || t.client == nil {
		return appcomments.Thread{}, ErrOAPIClient
	}
	resp, err := t.client.Drive.V1.FileComment.Get(ctx, larkdrivev1.NewGetFileCommentReqBuilder().
		FileToken(target.FileToken).
		CommentId(commentID).
		FileType(target.FileType).
		UserIdType(oapiCommentUserIDType).
		NeedReaction(false).
		Build())
	if err != nil {
		return appcomments.Thread{}, err
	}
	if resp.Success() {
		if resp.Data == nil {
			return appcomments.Thread{}, nil
		}
		return oapiCommentThreadFromFields(resp.Data.Quote, resp.Data.IsWhole, resp.Data.ReplyList), nil
	}
	if resp.Code != oapiFileCommentNotFoundCode {
		return appcomments.Thread{}, oapiCodeError("get file comment", resp.Code, resp.Msg)
	}
	return t.findCommentFromList(ctx, target, commentID)
}

func (t *OAPITransport) ReplyToComment(ctx context.Context, target appcomments.Target, commentID string, text string, opts appcomments.ReplyOptions) error {
	if t == nil || t.client == nil {
		return ErrOAPIClient
	}
	if opts.TopLevel {
		return t.replyTopLevel(ctx, target, text)
	}
	resp, err := t.client.Drive.V1.FileCommentReply.Create(ctx, larkdrivev1.NewCreateFileCommentReplyReqBuilder().
		FileToken(target.FileToken).
		CommentId(commentID).
		FileType(target.FileType).
		UserIdType(oapiCommentUserIDType).
		Body(larkdrivev1.NewCreateFileCommentReplyReqBodyBuilder().
			Content(oapiTextReplyContent(text)).
			Build()).
		Build())
	if err != nil {
		return err
	}
	if resp.Success() {
		return nil
	}
	if resp.Code == oapiWholeDocumentReplyOnlyCode {
		return t.replyTopLevel(ctx, target, text)
	}
	return oapiCodeError("create file comment reply", resp.Code, resp.Msg)
}

func (t *OAPITransport) AddReaction(ctx context.Context, target appcomments.Target, replyID string) (bool, error) {
	if t == nil || t.client == nil {
		return false, ErrOAPIClient
	}
	err := t.updateCommentReaction(ctx, target, replyID, larkdrivev2.ActionAdd)
	if err != nil {
		return false, err
	}
	return true, nil
}

func (t *OAPITransport) RemoveReaction(ctx context.Context, target appcomments.Target, replyID string) error {
	if t == nil || t.client == nil {
		return ErrOAPIClient
	}
	return t.updateCommentReaction(ctx, target, replyID, larkdrivev2.ActionDelete)
}

func (t *OAPITransport) findCommentFromList(ctx context.Context, target appcomments.Target, commentID string) (appcomments.Thread, error) {
	pageToken := ""
	for page := 0; page < oapiCommentMaxListPages; page++ {
		builder := larkdrivev1.NewListFileCommentReqBuilder().
			FileToken(target.FileToken).
			FileType(target.FileType).
			UserIdType(oapiCommentUserIDType).
			NeedReaction(false).
			PageSize(oapiCommentListPageSize)
		if pageToken != "" {
			builder.PageToken(pageToken)
		}
		resp, err := t.client.Drive.V1.FileComment.List(ctx, builder.Build())
		if err != nil {
			return appcomments.Thread{}, err
		}
		if !resp.Success() {
			if resp.Code == oapiFileCommentNotFoundCode {
				return appcomments.Thread{}, appcomments.ErrNoAccess
			}
			return appcomments.Thread{}, oapiCodeError("list file comments", resp.Code, resp.Msg)
		}
		if resp.Data == nil {
			return appcomments.Thread{}, nil
		}
		for _, item := range resp.Data.Items {
			if item == nil || item.CommentId == nil || *item.CommentId != commentID {
				continue
			}
			return oapiCommentThreadFromFields(item.Quote, item.IsWhole, item.ReplyList), nil
		}
		if resp.Data.HasMore == nil || !*resp.Data.HasMore || resp.Data.PageToken == nil || *resp.Data.PageToken == "" {
			break
		}
		pageToken = *resp.Data.PageToken
	}
	return appcomments.Thread{}, nil
}

func (t *OAPITransport) replyTopLevel(ctx context.Context, target appcomments.Target, text string) error {
	resp, err := t.client.Drive.V1.FileComment.Create(ctx, larkdrivev1.NewCreateFileCommentReqBuilder().
		FileToken(target.FileToken).
		FileType(target.FileType).
		UserIdType(oapiCommentUserIDType).
		FileComment(larkdrivev1.NewFileCommentBuilder().
			ReplyList(larkdrivev1.NewReplyListBuilder().
				Replies([]*larkdrivev1.FileCommentReply{
					larkdrivev1.NewFileCommentReplyBuilder().
						Content(oapiTextReplyContent(text)).
						Build(),
				}).
				Build()).
			Build()).
		Build())
	if err != nil {
		return err
	}
	if !resp.Success() {
		return oapiCodeError("create top-level file comment", resp.Code, resp.Msg)
	}
	return nil
}

func (t *OAPITransport) updateCommentReaction(ctx context.Context, target appcomments.Target, replyID string, action string) error {
	resp, err := t.client.Drive.V2.CommentReaction.UpdateReaction(ctx, larkdrivev2.NewUpdateReactionCommentReactionReqBuilder().
		FileToken(target.FileToken).
		FileType(target.FileType).
		Body(larkdrivev2.NewUpdateReactionCommentReactionReqBodyBuilder().
			Action(action).
			ReplyId(replyID).
			ReactionType(oapiCommentReactionTyping).
			Build()).
		Build())
	if err != nil {
		return err
	}
	if !resp.Success() {
		return oapiCodeError("update comment reaction", resp.Code, resp.Msg)
	}
	return nil
}

func oapiTextReplyContent(text string) *larkdrivev1.ReplyContent {
	return larkdrivev1.NewReplyContentBuilder().
		Elements([]*larkdrivev1.ReplyElement{
			larkdrivev1.NewReplyElementBuilder().
				Type("text_run").
				TextRun(larkdrivev1.NewTextRunBuilder().Text(text).Build()).
				Build(),
		}).
		Build()
}

func oapiCommentThreadFromFields(quote *string, isWhole *bool, replyList *larkdrivev1.ReplyList) appcomments.Thread {
	thread := appcomments.Thread{}
	if quote != nil {
		thread.Quote = *quote
	}
	if isWhole != nil {
		thread.IsWhole = *isWhole
	}
	if replyList == nil {
		return thread
	}
	thread.Replies = make([]appcomments.Reply, 0, len(replyList.Replies))
	for _, reply := range replyList.Replies {
		if reply == nil {
			continue
		}
		thread.Replies = append(thread.Replies, oapiCommentReply(reply))
	}
	return thread
}

func oapiCommentReply(reply *larkdrivev1.FileCommentReply) appcomments.Reply {
	out := appcomments.Reply{}
	if reply.ReplyId != nil {
		out.ReplyID = *reply.ReplyId
	}
	if reply.Content == nil {
		return out
	}
	out.Content.Elements = make([]appcomments.ReplyElement, 0, len(reply.Content.Elements))
	for _, element := range reply.Content.Elements {
		if element == nil {
			continue
		}
		out.Content.Elements = append(out.Content.Elements, oapiCommentElement(element))
	}
	return out
}

func oapiCommentElement(element *larkdrivev1.ReplyElement) appcomments.ReplyElement {
	out := appcomments.ReplyElement{}
	if element.Type != nil {
		out.Type = *element.Type
	}
	if element.TextRun != nil && element.TextRun.Text != nil {
		out.TextRun = &appcomments.ReplyTextRun{Text: *element.TextRun.Text}
	}
	if element.DocsLink != nil && element.DocsLink.Url != nil {
		out.DocsLink = &appcomments.ReplyDocsLink{URL: *element.DocsLink.Url}
	}
	if element.Person != nil && element.Person.UserId != nil {
		out.Person = &appcomments.ReplyPerson{UserID: *element.Person.UserId}
	}
	return out
}

func isSupportedOAPICommentFileType(fileType string) bool {
	switch fileType {
	case "doc", "docx", "sheet", "file":
		return true
	default:
		return false
	}
}
