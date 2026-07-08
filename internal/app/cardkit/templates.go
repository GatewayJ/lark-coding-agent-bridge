package cardkit

import (
	"fmt"
	"strings"
)

type ButtonSpec struct {
	Text  string
	Value map[string]any
	Style string
}

type StatusInfo struct {
	ProfileName         string
	CWD                 string
	SessionID           string
	EmptySessionText    string
	SessionStale        bool
	AgentName           string
	RuntimeAccess       LabelValue
	LarkCLIStatus       string
	ActiveRun           bool
	ActiveScopes        []string
	ActiveCommentScopes []string
	Queue               *QueueInfo
	OwnerState          string
	Scope               string
	ChatMode            string
}

type LabelValue struct {
	Label string
	Value string
}

type QueueInfo struct {
	Active  int
	Waiting int
	Cap     int
}

type ResumeEntry struct {
	SessionID string
	DisplayID string
	Preview   string
	RelTime   string
	LineCount int
	Detail    string
	Current   bool
}

func WorkspacesCard(current string, named map[string]string) JSONCard {
	entries := sortedEntries(named)
	elements := []any{divMD(fmt.Sprintf("当前 cwd：`%s`", escapeCode(orUnset(current))))}
	if len(entries) == 0 {
		elements = append(elements, hr(), divMD("暂无命名工作目录。"), divMD("💡 发送 `/ws save <name>` 把当前 cwd 存为命名工作目录"))
		return shell("📂 工作目录", elements)
	}
	elements = append(elements, hr())
	for i, entry := range entries {
		marker := ""
		if entry.value == current {
			marker = "  ← 当前"
		}
		elements = append(elements, divMD(fmt.Sprintf("**%s** → `%s`%s", escapeMD(entry.key), escapeCode(entry.value), marker)))
		elements = append(elements, actions([]ButtonSpec{
			{Text: "切换到此处", Value: map[string]any{"cmd": "ws.use", "name": entry.key}, Style: "primary"},
			{Text: "删除", Value: map[string]any{"cmd": "ws.remove", "name": entry.key}, Style: "danger"},
		}))
		if i < len(entries)-1 {
			elements = append(elements, hr())
		}
	}
	return shell("📂 工作目录", elements)
}

func StatusCard(info StatusInfo) JSONCard {
	sessionLine := info.EmptySessionText
	if sessionLine == "" {
		sessionLine = "(无)"
	}
	if info.SessionID != "" {
		sessionLine = "`" + shortID(info.SessionID) + "`"
		if info.SessionStale {
			sessionLine += " ⚠️ 旧 cwd，下一条会新建"
		}
	}
	scopeLine := "`" + escapeCode(info.Scope) + "`"
	if info.ChatMode == "topic" {
		scopeLine += " _（话题独立 session）_"
	}
	cwdLine := "(未设置)"
	if info.CWD != "" {
		cwdLine = "`" + escapeCode(info.CWD) + "`"
	}
	queueLine := "unknown"
	if info.Queue != nil {
		queueLine = fmt.Sprintf("%d/%d active, %d waiting", info.Queue.Active, info.Queue.Cap, info.Queue.Waiting)
	}

	lines := []string{
		"🧭 **scope**: " + scopeLine,
		"🧩 **profile**: " + escapeMD(info.ProfileName),
		"📁 **cwd**: " + cwdLine,
		"🔗 **session**: " + sessionLine,
		"🤖 **agent**: " + escapeMD(info.AgentName),
		"🛡 **" + escapeMD(info.RuntimeAccess.Label) + "**: " + escapeMD(info.RuntimeAccess.Value),
	}
	if info.LarkCLIStatus != "" {
		lines = append(lines, "🔐 **lark-cli**: "+escapeMD(info.LarkCLIStatus))
	}
	lines = append(lines, "🏃 **active run**: "+yesNo(info.ActiveRun))
	if len(info.ActiveScopes) > 0 {
		lines = append(lines, "🏃 **active scopes**: "+joinCode(info.ActiveScopes))
	}
	if len(info.ActiveCommentScopes) > 0 {
		lines = append(lines, "📝 **comment runs**: "+joinCode(info.ActiveCommentScopes))
	}
	lines = append(lines, "🚦 **queue**: "+queueLine, "👤 **owner API**: "+escapeMD(info.OwnerState))

	return shell("📊 当前状态", []any{
		divMD(strings.Join(lines, "\n")),
		hr(),
		actions([]ButtonSpec{
			{Text: "🆕 新会话", Value: map[string]any{"cmd": "new"}, Style: "primary"},
			{Text: "🔁 恢复会话", Value: map[string]any{"cmd": "resume"}},
			{Text: "📂 工作目录", Value: map[string]any{"cmd": "ws.list"}},
			{Text: "💡 帮助", Value: map[string]any{"cmd": "help"}},
		}),
	})
}

func ResumeCard(cwd string, entries []ResumeEntry) JSONCard {
	elements := []any{divMD(fmt.Sprintf("当前 cwd：`%s`", escapeCode(cwd)))}
	if len(entries) == 0 {
		elements = append(elements, hr(), divMD("此 cwd 下没有历史会话。"))
		return shell("🔁 恢复历史会话", elements)
	}
	elements = append(elements, hr())
	for i, entry := range entries {
		marker := ""
		if entry.Current {
			marker = "  ← 当前"
		}
		detail := entry.Detail
		if detail == "" {
			detail = fmt.Sprintf("%d 条", entry.LineCount)
		}
		displayID := entry.DisplayID
		if displayID == "" {
			displayID = entry.SessionID
		}
		elements = append(elements, divMD(fmt.Sprintf("**%d.** %s%s\n`%s` · %s · %s", i+1, escapeMD(entry.Preview), marker, shortID(displayID), entry.RelTime, escapeMD(detail))))
		text := "▸ 恢复此会话"
		style := "primary"
		if entry.Current {
			text = "已是当前会话"
			style = "default"
		}
		elements = append(elements, actions([]ButtonSpec{{Text: text, Value: map[string]any{"cmd": "resume.use", "arg": entry.SessionID}, Style: style}}))
		if i < len(entries)-1 {
			elements = append(elements, hr())
		}
	}
	return shell("🔁 恢复历史会话", elements)
}

func HelpCard(agentName string) JSONCard {
	if agentName == "" {
		agentName = "Agent"
	}
	escapedAgentName := escapeMD(agentName)
	return shell("💡 使用帮助", []any{
		divMD(strings.Join([]string{
			"**命令列表**",
			"",
			"- `/new` `/reset` — 清空当前 chat 的会话",
			"- `/new chat [name]` — 新建群+新会话，自动拉你进群",
			"- `/resume [N]` — 列出并恢复历史会话（最多 N 条）",
			"- `/cd <path>` — 切换工作目录（会重置 session）",
			"- `/ws list|save <name>|use <name>|remove <name>` — 工作目录",
			"- `/account` — 查看当前应用；`/account change` 换 appId/secret 并重连",
			"- `/config` — 调整偏好、访问控制和 lark-cli 身份策略",
			"- `/status` — 当前状态",
			"- `/stop` — 结束当前正在跑的任务（也可点卡片底部 ⏹ 终止 按钮）",
			"- `/stop comment:<scopeHash>` — 管理员停止云文档评论任务",
			"- `/timeout [N|off|default]` — 当前 session 的探活分钟数,`/config` 改全局默认",
			"- `/timeout comment:<scopeHash> N` — 管理员设置云文档评论任务探活",
			"- `/ps` — 列出本机所有 bot,标识当前正在回复的那个",
			"- `/exit <id|#>` — 关掉指定 bot(用 `/ps` 看 id/序号)",
			"- `/reconnect` — 强制重连 WebSocket(网络抖动后 bot 没反应时用)",
			fmt.Sprintf("- `/doctor [描述]` — 把日志和描述交给 %s 自助诊断", escapedAgentName),
			"- `/help` — 本帮助",
			"",
			fmt.Sprintf("其他内容直接交给 %s。", escapedAgentName),
		}, "\n")),
		hr(),
		actions([]ButtonSpec{
			{Text: "📊 状态", Value: map[string]any{"cmd": "status"}, Style: "primary"},
			{Text: "🔁 恢复会话", Value: map[string]any{"cmd": "resume"}},
			{Text: "📂 工作目录", Value: map[string]any{"cmd": "ws.list"}},
			{Text: "🆕 新会话", Value: map[string]any{"cmd": "new"}},
		}),
	})
}

func shell(title string, elements []any) JSONCard {
	return JSONCard{
		"config": map[string]any{
			"wide_screen_mode": true,
			"update_multi":     true,
		},
		"header": map[string]any{
			"title": map[string]any{"tag": "plain_text", "content": title},
		},
		"elements": elements,
	}
}

func divMD(content string) map[string]any {
	return map[string]any{"tag": "div", "text": map[string]any{"tag": "lark_md", "content": content}}
}

func actions(buttons []ButtonSpec) map[string]any {
	out := make([]any, 0, len(buttons))
	for _, spec := range buttons {
		style := spec.Style
		if style == "" {
			style = "default"
		}
		out = append(out, map[string]any{
			"tag":   "button",
			"text":  map[string]any{"tag": "plain_text", "content": spec.Text},
			"type":  style,
			"value": spec.Value,
		})
	}
	return map[string]any{"tag": "action", "actions": out}
}

func hr() map[string]any { return map[string]any{"tag": "hr"} }

func escapeMD(s string) string {
	replacer := strings.NewReplacer("\\", "\\\\", "*", "\\*", "_", "\\_", "`", "\\`")
	return replacer.Replace(s)
}

func escapeCode(s string) string { return strings.ReplaceAll(s, "`", "'") }

func orUnset(s string) string {
	if s == "" {
		return "(未设置)"
	}
	return s
}

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8] + "…"
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func joinCode(values []string) string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, "`"+escapeCode(value)+"`")
	}
	return strings.Join(out, ", ")
}

type entry struct {
	key   string
	value string
}

func sortedEntries(in map[string]string) []entry {
	out := make([]entry, 0, len(in))
	for key, value := range in {
		out = append(out, entry{key: key, value: value})
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].key > out[j].key; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}
