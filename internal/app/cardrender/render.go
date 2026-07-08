package cardrender

import (
	"fmt"
	"math"
	"strings"
	"time"
)

const (
	reasoningMax          = 1500
	collapseToolThreshold = 3
	headerSummaryMax      = 80
	bodyFieldMax          = 600
	outputMax             = 1200
	bodyTotalMax          = 2500
)

func RenderCard(state RunState, options RenderOptions) CardView {
	elements := make([]CardElement, 0, len(state.Blocks)+4)
	if options.ShowMetadata {
		if meta := metadataMarkdown(state); meta != "" {
			elements = append(elements, note(meta))
		}
	}

	if state.Reasoning.Content != "" {
		title := "🧠 **思考完成，点击查看**"
		if state.Reasoning.Active {
			title = "🧠 **思考中**"
		}
		elements = append(elements, panel(title, truncate(state.Reasoning.Content, reasoningMax), state.Reasoning.Active, "grey"))
	}

	for _, group := range groupBlocks(state.Blocks) {
		if group.text != nil {
			content := strings.TrimSpace(*group.text)
			if content != "" {
				elements = append(elements, markdown(content))
			}
			continue
		}
		elements = append(elements, renderToolGroup(group.tools, !isActive(state.Status))...)
	}

	switch state.Status {
	case StatusCancelled:
		elements = append(elements, note("_⏹ 已被中断_"))
	case StatusTimeout:
		mins := state.TimeoutMinutes
		elements = append(elements, note(fmt.Sprintf("_⏱ %d 分钟无响应,已自动终止_", mins)))
	case StatusFailed:
		if state.Error != "" {
			elements = append(elements, note("⚠️ agent 失败："+state.Error))
		}
	case StatusSucceeded:
		if len(elements) == 0 || onlyMetadata(elements) {
			elements = append(elements, note("_（未返回内容）_"))
		}
	}

	actions := []Action(nil)
	if isActive(state.Status) {
		if state.Footer != "" {
			elements = append(elements, note(footerText(state.Footer)))
		}
		actions = append(actions, stopAction(options))
	}

	return CardView{
		Schema:    "bridge.card.v1",
		Status:    state.Status,
		Summary:   SummaryText(state),
		Streaming: isActive(state.Status),
		Elements:  elements,
		Actions:   actions,
	}
}

func RenderText(state RunState) TextView {
	parts := make([]string, 0, len(state.Blocks)+3)
	for _, block := range state.Blocks {
		switch block.Kind {
		case BlockText:
			if content := strings.TrimSpace(block.Content); content != "" {
				parts = append(parts, content)
			}
		case BlockTool:
			if block.Tool != nil {
				parts = append(parts, "> "+toolHeaderText(*block.Tool))
			}
		}
	}

	switch state.Status {
	case StatusCancelled:
		parts = append(parts, "_⏹ 已被中断_")
	case StatusTimeout:
		parts = append(parts, fmt.Sprintf("_⏱ %d 分钟无响应,已自动终止_", state.TimeoutMinutes))
	case StatusFailed:
		if state.Error != "" {
			parts = append(parts, "⚠️ agent 失败:"+state.Error)
		}
	default:
		if isActive(state.Status) && state.Footer != "" {
			parts = append(parts, footerLine(state.Footer))
		}
	}

	return TextView{
		Status:  state.Status,
		Summary: SummaryText(state),
		Content: strings.Join(parts, "\n\n"),
	}
}

func SummaryText(state RunState) string {
	switch state.Status {
	case StatusQueued:
		return "排队中"
	case StatusCancelled:
		return "已中断"
	case StatusTimeout:
		return "已超时"
	case StatusFailed:
		return "出错"
	case StatusSucceeded:
		return "已完成"
	}
	switch state.Footer {
	case FooterToolRunning:
		return "正在调用工具"
	case FooterStreaming:
		return "正在输出"
	default:
		return "思考中"
	}
}

type blockGroup struct {
	text  *string
	tools []ToolEntry
}

func groupBlocks(blocks []Block) []blockGroup {
	groups := []blockGroup{}
	toolBuf := []ToolEntry{}
	flushTools := func() {
		if len(toolBuf) == 0 {
			return
		}
		copied := append([]ToolEntry(nil), toolBuf...)
		groups = append(groups, blockGroup{tools: copied})
		toolBuf = nil
	}
	for _, block := range blocks {
		if block.Kind == BlockTool && block.Tool != nil {
			toolBuf = append(toolBuf, *block.Tool)
			continue
		}
		flushTools()
		if block.Kind == BlockText {
			content := block.Content
			groups = append(groups, blockGroup{text: &content})
		}
	}
	flushTools()
	return groups
}

func renderToolGroup(tools []ToolEntry, finalized bool) []CardElement {
	if len(tools) == 0 {
		return nil
	}
	if len(tools) < collapseToolThreshold {
		out := make([]CardElement, 0, len(tools))
		for _, tool := range tools {
			out = append(out, toolPanel(tool, false))
		}
		return out
	}
	if finalized {
		return []CardElement{collapsedToolSummary(tools, true)}
	}
	prior := tools[:len(tools)-1]
	latest := tools[len(tools)-1]
	out := make([]CardElement, 0, 2)
	if len(prior) > 0 {
		out = append(out, collapsedToolSummary(prior, false))
	}
	out = append(out, toolPanel(latest, true))
	return out
}

func toolPanel(tool ToolEntry, expanded bool) CardElement {
	border := "grey"
	if tool.Status == ToolError {
		border = "red"
	}
	body := toolBodyMarkdown(tool)
	if body == "" {
		body = "_无输出_"
	}
	return panel(toolHeaderText(tool), body, expanded, border)
}

func collapsedToolSummary(tools []ToolEntry, finalized bool) CardElement {
	suffix := ""
	if finalized {
		suffix = "（已结束）"
	}
	lines := make([]string, 0, len(tools))
	for _, tool := range tools {
		lines = append(lines, "- "+toolHeaderText(tool))
	}
	return panel(fmt.Sprintf("☕ **%d 个工具调用%s**", len(tools), suffix), strings.Join(lines, "\n"), false, "blue")
}

func toolHeaderText(tool ToolEntry) string {
	icon := "⏳"
	if tool.Status == ToolDone {
		icon = "✅"
	} else if tool.Status == ToolError {
		icon = "❌"
	}
	summary := summarizeInput(tool.Name, tool.Input)
	if summary != "" {
		return fmt.Sprintf("%s **%s** — %s", icon, tool.Name, summary)
	}
	return fmt.Sprintf("%s **%s**", icon, tool.Name)
}

func toolBodyMarkdown(tool ToolEntry) string {
	parts := []string{}
	if input := renderInput(tool); input != "" {
		parts = append(parts, input)
	}
	if tool.Output != "" {
		output := truncate(tool.Output, outputMax)
		if tool.Status == ToolError {
			parts = append(parts, fmt.Sprintf("**Error**\n```\n%s\n```", output))
		} else {
			parts = append(parts, fmt.Sprintf("**Output**\n```\n%s\n```", output))
		}
	} else if tool.Status == ToolRunning {
		parts = append(parts, "_运行中…_")
	}
	body := strings.Join(parts, "\n\n")
	if len(body) <= bodyTotalMax {
		return body
	}
	return body[:bodyTotalMax] + "…\n\n_（body 已截断,完整内容查 `/doctor` 或日志）_"
}

func summarizeInput(name string, input any) string {
	rec, ok := input.(map[string]any)
	if !ok {
		return ""
	}
	pick := func(key string, max int) string {
		raw, ok := rec[key].(string)
		if !ok {
			return ""
		}
		oneLine := strings.Join(strings.Fields(raw), " ")
		return truncate(oneLine, max)
	}
	switch name {
	case "Bash":
		return pick("command", headerSummaryMax)
	case "Read", "Edit", "Write", "NotebookEdit":
		return pick("file_path", headerSummaryMax)
	case "Grep":
		pattern := pick("pattern", 40)
		path := pick("path", 30)
		if path != "" {
			return pattern + " in " + path
		}
		return pattern
	case "Glob":
		return pick("pattern", headerSummaryMax)
	case "WebFetch":
		return pick("url", headerSummaryMax)
	case "WebSearch":
		return pick("query", 60)
	case "Agent", "Task":
		if description := pick("description", headerSummaryMax); description != "" {
			return description
		}
		return pick("subagent_type", headerSummaryMax)
	default:
		for _, key := range []string{"command", "file_path", "path", "query"} {
			if v := pick(key, headerSummaryMax); v != "" {
				return v
			}
		}
		return ""
	}
}

func renderInput(tool ToolEntry) string {
	rec, ok := tool.Input.(map[string]any)
	if !ok {
		return ""
	}
	str := func(key string) string {
		if v, ok := rec[key].(string); ok {
			return v
		}
		return ""
	}
	switch tool.Name {
	case "Bash":
		if cmd := str("command"); cmd != "" {
			return fmt.Sprintf("**Command**\n```bash\n%s\n```", truncate(cmd, bodyFieldMax))
		}
	case "Read", "Edit", "Write", "NotebookEdit":
		if path := str("file_path"); path != "" {
			return fmt.Sprintf("**File** `%s`", path)
		}
	case "Grep":
		lines := []string{}
		if pattern := str("pattern"); pattern != "" {
			lines = append(lines, fmt.Sprintf("**Pattern** `%s`", pattern))
		}
		if path := str("path"); path != "" {
			lines = append(lines, fmt.Sprintf("**Path** `%s`", path))
		}
		return strings.Join(lines, "\n")
	case "WebFetch":
		if url := str("url"); url != "" {
			return "**URL** " + url
		}
	case "WebSearch":
		if query := str("query"); query != "" {
			return fmt.Sprintf("**Query** `%s`", truncate(query, bodyFieldMax))
		}
	}
	return ""
}

func metadataMarkdown(state RunState) string {
	lines := []string{}
	if state.Scope != "" {
		lines = append(lines, "🧭 **scope**: `"+escapeCode(state.Scope)+"`")
	}
	if state.CWD != "" {
		lines = append(lines, "📁 **cwd**: `"+escapeCode(state.CWD)+"`")
	}
	if state.SessionID != "" {
		lines = append(lines, "🔗 **session**: `"+escapeCode(shortID(state.SessionID))+"`")
	}
	if state.ThreadID != "" {
		lines = append(lines, "🧵 **thread**: `"+escapeCode(shortID(state.ThreadID))+"`")
	}
	if elapsed := effectiveElapsed(state); elapsed > 0 {
		lines = append(lines, "⏱ **elapsed**: "+formatDuration(elapsed))
	}
	if !state.Usage.Empty() {
		lines = append(lines, "🪙 **usage**: "+formatUsage(state.Usage))
	}
	if state.LastEvent != "" {
		lines = append(lines, "🧾 **last event**: "+state.LastEvent)
	}
	if state.Error != "" {
		lines = append(lines, "⚠️ **error**: "+state.Error)
	}
	return strings.Join(lines, "\n")
}

func effectiveElapsed(state RunState) time.Duration {
	if state.Elapsed > 0 {
		return state.Elapsed
	}
	if !state.StartedAt.IsZero() && !state.UpdatedAt.IsZero() && state.UpdatedAt.After(state.StartedAt) {
		return state.UpdatedAt.Sub(state.StartedAt)
	}
	return 0
}

func formatDuration(d time.Duration) string {
	seconds := int(math.Round(d.Seconds()))
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	minutes := seconds / 60
	remaining := seconds % 60
	if minutes < 60 {
		if remaining == 0 {
			return fmt.Sprintf("%dm", minutes)
		}
		return fmt.Sprintf("%dm%02ds", minutes, remaining)
	}
	hours := minutes / 60
	minutes = minutes % 60
	return fmt.Sprintf("%dh%02dm", hours, minutes)
}

func formatUsage(usage Usage) string {
	parts := []string{}
	if usage.InputTokens != 0 {
		parts = append(parts, fmt.Sprintf("in %d", usage.InputTokens))
	}
	if usage.OutputTokens != 0 {
		parts = append(parts, fmt.Sprintf("out %d", usage.OutputTokens))
	}
	if usage.CachedInputTokens != 0 {
		parts = append(parts, fmt.Sprintf("cached %d", usage.CachedInputTokens))
	}
	if usage.ReasoningOutputTokens != 0 {
		parts = append(parts, fmt.Sprintf("reasoning %d", usage.ReasoningOutputTokens))
	}
	if usage.CostUSD != nil {
		parts = append(parts, fmt.Sprintf("$%.4f", *usage.CostUSD))
	}
	return strings.Join(parts, ", ")
}

func stopAction(options RenderOptions) Action {
	value := map[string]any{"cmd": "stop"}
	if options.SignCallback != nil {
		if token := options.SignCallback("stop"); token != "" {
			value[bridgeCallbackMarker] = true
			value["bridge_token"] = token
		}
	}
	return Action{
		ID:    "stop",
		Text:  "⏹ 终止",
		Style: ActionDanger,
		Value: value,
	}
}

func markdown(content string) CardElement {
	return CardElement{Kind: ElementMarkdown, Text: content}
}

func note(content string) CardElement {
	return CardElement{Kind: ElementNote, Text: content, TextSize: "notation"}
}

func panel(title string, body string, expanded bool, border string) CardElement {
	return CardElement{
		Kind: ElementPanel,
		Panel: &PanelView{
			Title:    title,
			Body:     body,
			Expanded: expanded,
			Border:   border,
		},
	}
}

func footerText(status FooterStatus) string {
	switch status {
	case FooterThinking:
		return "🧠 正在思考"
	case FooterToolRunning:
		return "🧰 正在调用工具"
	default:
		return "✍️ 正在输出"
	}
}

func footerLine(status FooterStatus) string {
	switch status {
	case FooterThinking:
		return "_🧠 正在思考…_"
	case FooterToolRunning:
		return "_🧰 正在调用工具…_"
	default:
		return "_✍️ 正在输出…_"
	}
}

func isActive(status RunStatus) bool {
	return status == StatusQueued || status == StatusRunning
}

func onlyMetadata(elements []CardElement) bool {
	return len(elements) == 1 && elements[0].Kind == ElementNote && strings.Contains(elements[0].Text, "**scope**")
}

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8] + "…"
}

func escapeCode(s string) string {
	return strings.ReplaceAll(s, "`", "'")
}

func truncate(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
