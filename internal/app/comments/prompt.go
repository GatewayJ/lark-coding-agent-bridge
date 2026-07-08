package comments

import (
	"regexp"
	"strings"
)

type Target struct {
	FileToken string
	FileType  string
}

type ReplyTextRun struct {
	Text string
}

type ReplyDocsLink struct {
	URL string
}

type ReplyPerson struct {
	UserID string
}

type ReplyElement struct {
	Type     string
	TextRun  *ReplyTextRun
	DocsLink *ReplyDocsLink
	Person   *ReplyPerson
}

type ReplyContent struct {
	Elements []ReplyElement
}

type Reply struct {
	ReplyID string
	Content ReplyContent
}

type Thread struct {
	Replies []Reply
	Quote   string
	IsWhole bool
}

type CommentContext struct {
	Question      string
	Quote         string
	IsWhole       bool
	TargetReplyID string
}

type ExtractQuestionInput struct {
	ReplyID string
	Replies []Reply
}

type ExtractQuestionResult struct {
	Question      string
	TargetReplyID string
}

func ExtractCommentQuestionFromReplies(input ExtractQuestionInput) (ExtractQuestionResult, bool) {
	var target *Reply
	if input.ReplyID != "" {
		for i := range input.Replies {
			if input.Replies[i].ReplyID == input.ReplyID {
				target = &input.Replies[i]
				break
			}
		}
	}
	if target == nil && len(input.Replies) > 0 {
		target = &input.Replies[len(input.Replies)-1]
	}
	if target == nil {
		return ExtractQuestionResult{}, false
	}

	var builder strings.Builder
	for _, element := range target.Content.Elements {
		switch element.Type {
		case "text_run":
			if element.TextRun != nil {
				builder.WriteString(element.TextRun.Text)
			}
		case "docs_link":
			if element.DocsLink != nil {
				builder.WriteString(element.DocsLink.URL)
			}
		}
	}
	return ExtractQuestionResult{
		Question:      strings.TrimSpace(builder.String()),
		TargetReplyID: target.ReplyID,
	}, true
}

func BuildPrompt(target Target, ctx CommentContext) string {
	docURL := "https://feishu.cn/" + target.FileType + "/" + target.FileToken
	parts := []string{
		"我在飞书云文档里被 @了。文档信息：",
		"- 链接：" + docURL,
		"- file_token：" + target.FileToken,
		"- 类型：" + target.FileType,
	}
	if ctx.IsWhole {
		parts = append(parts, "- 评论范围：全文评论（针对整篇）")
	} else {
		parts = append(parts, "- 评论范围：行内评论（针对选中文字）")
	}
	if ctx.Quote != "" {
		parts = append(parts, "", "用户选中的原文：\n> "+strings.ReplaceAll(ctx.Quote, "\n", "\n> "))
	}
	parts = append(parts,
		"",
		"用户的问题："+ctx.Question,
		"",
		commentReadInstruction(target),
		"",
		"评论回复由 bridge 负责：不要调用云文档评论或回复接口，也不要给评论添加或删除 reaction；最终答案直接用纯文本交给 bridge。",
		"",
		"回复要求：直接用纯文本，不要 markdown（不要 ** __ # - * > ` 之类的标记），不要代码块；不要输出内部思考、内部分析、读取步骤、工具调用过程或工具日志。若用户要求解释依据，只说明用户可见的依据和结论。云文档评论框不渲染 markdown，会原样显示这些符号。",
	)
	return strings.Join(parts, "\n")
}

func commentReadInstruction(target Target) string {
	switch target.FileType {
	case "doc", "docx":
		return "读取文档内容：优先使用当前 docs v2 读取命令：\n" +
			"  `lark-cli docs +fetch --api-version v2 --doc " + target.FileToken + " --doc-format markdown`\n" +
			"如果本机 lark-cli 不支持上述参数，不要在同一错误上反复重试；使用当前可用的等价读取命令读取同一 file_token。"
	case "sheet":
		return "读取表格内容：这是 sheet 类型，不要使用 docs +fetch。请按当前可用的表格读取工具或本机 lark-cli 支持的表格读取命令读取同一 file_token；如果命令参数不兼容，不要在同一错误上反复重试。"
	default:
		return "读取文件内容：这是 file 类型，不要使用 docs +fetch。请按当前可用的云空间文件工具或本机 lark-cli 支持的文件读取/下载命令处理同一 file_token；如果命令参数不兼容，不要在同一错误上反复重试。"
	}
}

var (
	headingPattern      = regexp.MustCompile(`(?m)^#{1,6}\s+`)
	boldStarPattern     = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	boldUndersPattern   = regexp.MustCompile(`__([^_]+)__`)
	italicStarPattern   = regexp.MustCompile(`\*([^*\n]+)\*`)
	italicUndersPattern = regexp.MustCompile(`_([^_\n]+)_`)
	inlineCodePattern   = regexp.MustCompile("`([^`]+)`")
	listBulletPattern   = regexp.MustCompile(`(?m)^[-*]\s+`)
	blockquotePattern   = regexp.MustCompile(`(?m)^>\s?`)
	fenceOpenPattern    = regexp.MustCompile("```[a-zA-Z]*\n?")
)

func StripMarkdown(text string) string {
	text = headingPattern.ReplaceAllString(text, "")
	text = boldStarPattern.ReplaceAllString(text, "$1")
	text = boldUndersPattern.ReplaceAllString(text, "$1")
	text = italicStarPattern.ReplaceAllString(text, "$1")
	text = italicUndersPattern.ReplaceAllString(text, "$1")
	text = inlineCodePattern.ReplaceAllString(text, "$1")
	text = listBulletPattern.ReplaceAllString(text, "")
	text = blockquotePattern.ReplaceAllString(text, "")
	text = fenceOpenPattern.ReplaceAllString(text, "")
	return strings.ReplaceAll(text, "```", "")
}
