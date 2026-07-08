package cardkit

import (
	"fmt"
	"strings"
)

type TenantBrand string

const (
	TenantFeishu TenantBrand = "feishu"
	TenantLark   TenantBrand = "lark"
)

type MessageReplyMode string

const (
	MessageReplyCard     MessageReplyMode = "card"
	MessageReplyMarkdown MessageReplyMode = "markdown"
	MessageReplyText     MessageReplyMode = "text"
)

type CotMessagesMode string

const (
	CotMessagesOff      CotMessagesMode = "off"
	CotMessagesBrief    CotMessagesMode = "brief"
	CotMessagesDetailed CotMessagesMode = "detailed"
)

type LarkCLIIdentityPreset string

const (
	LarkCLIIdentityBotOnly     LarkCLIIdentityPreset = "bot-only"
	LarkCLIIdentityUserDefault LarkCLIIdentityPreset = "user-default"
)

type CurrentInfo struct {
	AppID   string
	BotName string
	Tenant  TenantBrand
}

type AccountFormOptions struct {
	InitialTenant TenantBrand
	PrefillAppID  string
	ErrorMessage  string
}

type KnownChat struct {
	ID   string
	Name string
}

type ConfigFormOptions struct {
	MessageReply          MessageReplyMode
	ShowToolCalls         bool
	CotMessages           CotMessagesMode
	MaxConcurrentRuns     int
	RunIdleTimeoutMinutes int
	RequireMentionInGroup bool
	LarkCLIIdentity       LarkCLIIdentityPreset
	AllowedUsers          []string
	AllowedChats          []string
	Admins                []string
	KnownChats            []KnownChat
}

func AccountCurrentCard(info CurrentInfo) JSONCard {
	return card2("当前应用", []any{
		markdown(strings.Join([]string{
			"📋 **当前应用**",
			"",
			fmt.Sprintf("**App ID**: `%s`", maskAppID(info.AppID)),
			fmt.Sprintf("**Bot 名**: %s", botNameOrUnknown(info.BotName)),
			fmt.Sprintf("**Tenant**: %s", info.Tenant),
		}, "\n")),
		hr(),
		callbackButton("更换凭据", "primary", map[string]any{"cmd": "account.change"}, "", false),
	})
}

func AccountFormCard(opts AccountFormOptions) JSONCard {
	initialTenant := opts.InitialTenant
	if initialTenant == "" {
		initialTenant = TenantFeishu
	}
	bodyElements := []any{}
	if opts.ErrorMessage != "" {
		bodyElements = append(bodyElements, markdown(fmt.Sprintf("❌ **校验失败**：%s", opts.ErrorMessage)))
	}

	appIDInput := map[string]any{
		"tag":         "input",
		"name":        "app_id",
		"label":       plainText("App ID"),
		"placeholder": plainText("cli_xxxxxxxxxxxx"),
		"required":    true,
	}
	if opts.PrefillAppID != "" {
		appIDInput["default_value"] = opts.PrefillAppID
	}

	bodyElements = append(bodyElements, map[string]any{
		"tag":  "form",
		"name": "account_form",
		"elements": []any{
			appIDInput,
			map[string]any{
				"tag":         "input",
				"name":        "app_secret",
				"label":       plainText("App Secret"),
				"placeholder": plainText("32 位字符串"),
				"required":    true,
			},
			markdown("**Tenant**"),
			map[string]any{
				"tag":            "select_static",
				"name":           "tenant",
				"initial_option": string(initialTenant),
				"options": []any{
					option("Feishu (国内)", "feishu"),
					option("Lark (海外)", "lark"),
				},
			},
			columnSet([]any{
				buttonColumn(callbackButton("提交", "primary", map[string]any{"cmd": "account.submit"}, "submit_btn", true)),
				buttonColumn(callbackButton("取消", "", map[string]any{"cmd": "account.cancel"}, "cancel_btn", false)),
			}),
		},
	})

	return card2("更换凭据", bodyElements)
}

func AccountValidatingCard() JSONCard {
	return card2("正在校验...", []any{markdown("⏳ **正在校验凭据...**")})
}

func AccountSuccessCard(info CurrentInfo) JSONCard {
	lines := []string{
		"✅ **凭据已保存**",
		"",
		fmt.Sprintf("**App ID**: `%s`", maskAppID(info.AppID)),
	}
	if info.BotName != "" {
		lines = append(lines, fmt.Sprintf("**Bot 名**: %s", info.BotName))
	}
	lines = append(lines,
		fmt.Sprintf("**Tenant**: %s", info.Tenant),
		"",
		"正在用新凭据重连 WebSocket...",
		"⚠️ 如果新 bot 不在此群，后续消息将由新 bot 接管，老 bot 不会再回复。",
	)
	return card2("已保存", []any{markdown(strings.Join(lines, "\n"))})
}

func AccountFailureCard(reason string) JSONCard {
	return card2("校验失败", []any{
		markdown(fmt.Sprintf("❌ **校验失败**\n\n`%s`\n\n请检查 App ID 和 Secret 是否正确，重发 `/account change` 重试。", reason)),
	})
}

func AccountCancelledCard() JSONCard {
	return card2("已取消", []any{markdown("已取消，未做任何修改。")})
}

func ConfigFormCard(opts ConfigFormOptions) JSONCard {
	accessElements := []any{
		markdown("_控制谁能通过私聊和群聊使用 bot。**留空 = 不响应聊天消息**。云文档评论按文档权限生效。_"),
		hr(),
		markdown(fmt.Sprintf("**允许私聊的用户**（共 %d 人）\n%s\n\n_加 / 删：_ `/invite user @某人`  `/remove user @某人`", len(opts.AllowedUsers), atMentionLine(opts.AllowedUsers))),
		hr(),
		markdown(fmt.Sprintf("**允许响应的群**（共 %d 个）\n%s\n\n_一键加全部 bot 所在的群：_ `/invite all group`\n_加 / 删（在目标群里发）：_ `/invite group`  `/remove group`", len(opts.AllowedChats), chatList(opts.AllowedChats, opts.KnownChats))),
		hr(),
		markdown(fmt.Sprintf("**管理员**（共 %d 人）\n%s\n\n_可以跑敏感命令：`/account` `/config` `/exit` `/reconnect` `/doctor` `/cd` `/ws` `/invite` `/remove`。管理员也自动获得私聊权限，并可在未白名单群里管理访问控制。_\n\n_加 / 删：_ `/invite admin @某人`  `/remove admin @某人`", len(opts.Admins), atMentionLine(opts.Admins))),
	}

	messageReply := opts.MessageReply
	if messageReply == MessageReplyCard {
		messageReply = MessageReplyMarkdown
	}

	return card2("偏好设置", []any{
		markdown("⚙️ **偏好设置**\n\n调整 bot 的行为偏好。改完点提交后写入当前 profile 配置；消息和访问控制设置立即生效。"),
		hr(),
		map[string]any{
			"tag":  "form",
			"name": "config_form",
			"elements": []any{
				markdown("**消息回复方式**\n_纯文本:agent 跑完一次性发出,不流式,体感最轻_\n_消息卡片:轻量流式 markdown 卡片,飞书原生打字机动画_"),
				selectStatic("message_reply", string(messageReply), []any{
					option("纯文本", "text"),
					option("消息卡片(默认)", "markdown"),
				}),
				markdown("\n**工具调用显示**\n_显示:可以看到 bot 跑了什么命令、读了哪些文件等过程_\n_隐藏:只看 agent 最终的文字答复,跳过所有工具块_"),
				selectStatic("show_tool_calls", showHide(opts.ShowToolCalls), []any{
					option("显示(默认)", "show"),
					option("隐藏", "hide"),
				}),
				markdown("\n**COT 过程消息**\n_关闭:只发送最终回复_\n_简略:展示 agent 过程文本和工具摘要_\n_详细:额外展示工具参数和输出摘要_"),
				selectStatic("cot_messages", string(opts.CotMessages), []any{
					option("关闭", "off"),
					option("简略", "brief"),
					option("详细", "detailed"),
				}),
				markdown("\n**并发上限**\n_全局同时运行的 agent 进程数(主要影响话题群多话题并行场景)_\n_默认 10,范围 1-50。超出的请求会 FIFO 排队_"),
				textInput("max_concurrent_runs", fmt.Sprint(opts.MaxConcurrentRuns), "10"),
				markdown("\n**run 探活(分钟)**\n_agent 长时间没输出时自动 kill,防止假死_\n_0 = 关闭(默认),范围 1-120。可被 `/timeout` 在单个 scope 覆盖_"),
				textInput("run_idle_timeout_minutes", fmt.Sprint(opts.RunIdleTimeoutMinutes), "0"),
				markdown("\n**群里需要 @ bot**\n_是(默认):群和话题群里,不 @ bot 的消息不会触发回复,bot 不接群里聊天_\n_否:任何消息都会发给 agent(0.1.21 及更早版本的行为)_\n_私聊永远不需要 @;`@全员` 永远不响应_"),
				selectStatic("require_mention_in_group", yesNoCN(opts.RequireMentionInGroup), []any{
					option("是(默认)", "yes"),
					option("否", "no"),
				}),
				markdown("\n**lark-cli 身份策略**\n_只允许应用身份:使用 bot/app 能力,不访问个人资源_\n_允许用户身份:保留应用身份,并允许已授权用户访问个人日历、邮箱、云盘等资源_"),
				selectStatic("lark_cli_identity", string(opts.LarkCLIIdentity), []any{
					option("只允许应用身份", "bot-only"),
					option("允许用户身份", "user-default"),
				}),
				hr(),
				collapsedAccessPanel("🔒 **访问控制**（点击展开）", accessElements),
				columnSet([]any{
					buttonColumn(callbackButton("提交", "primary", map[string]any{"cmd": "config.submit"}, "submit_btn", true)),
					buttonColumn(callbackButton("取消", "", map[string]any{"cmd": "config.cancel"}, "cancel_btn", false)),
				}),
			},
		},
	})
}

func ConfigSavedCard(opts ConfigFormOptions) JSONCard {
	replyLabel := "纯文本"
	if opts.MessageReply == MessageReplyCard {
		replyLabel = "交互卡片"
	} else if opts.MessageReply == MessageReplyMarkdown {
		replyLabel = "消息卡片"
	}
	identityLabel := "只允许应用身份"
	if opts.LarkCLIIdentity == LarkCLIIdentityUserDefault {
		identityLabel = "允许用户身份"
	}
	content := "✅ **偏好已保存**\n\n" +
		fmt.Sprintf("**消息回复方式**:%s\n", replyLabel) +
		fmt.Sprintf("**工具调用显示**:`%s`\n", showHide(opts.ShowToolCalls)) +
		fmt.Sprintf("**COT 过程消息**:`%s`\n", cotMessagesLabel(opts.CotMessages)) +
		fmt.Sprintf("**并发上限**:`%d`\n", opts.MaxConcurrentRuns) +
		fmt.Sprintf("**run 探活**:`%s`\n", runIdleTimeoutLabel(opts.RunIdleTimeoutMinutes)) +
		fmt.Sprintf("**群里需要 @ bot**:`%s`\n\n", yesNoChinese(opts.RequireMentionInGroup)) +
		fmt.Sprintf("**lark-cli 身份策略**:`%s`\n\n", identityLabel) +
		"🔒 **访问控制**\n" +
		fmt.Sprintf("**允许私聊的用户**:%s\n", summarizeList(opts.AllowedUsers)) +
		fmt.Sprintf("**允许响应的群**:%s\n", summarizeList(opts.AllowedChats)) +
		fmt.Sprintf("**管理员**:%s\n\n", summarizeList(opts.Admins)) +
		"下条消息开始生效。"
	return card2("偏好已保存", []any{markdown(content)})
}

func GroupMsgScopeGrantCard(url string, expireMins int) JSONCard {
	content := "⚠️ **「群里不需要 @ bot」还差一个权限**\n\n" +
		"你已开启「不 @ bot 也回复」，但当前应用没有 **获取群组中所有消息**（`im:message.group_msg`）权限。" +
		"没有它，飞书不会把群里非 @ 的消息推给 bot，所以这个设置暂时不生效。\n\n" +
		fmt.Sprintf("**点下面的链接补授权**（约 %d 分钟内有效）：\n", expireMins) +
		fmt.Sprintf("[🔗 点此一键授权](%s)\n\n", url) +
		"_扫码/点击后会进入确认页，新权限已预填好，确认即可。授权成功后，群里新消息开始自动生效，无需重启。_\n" +
		fmt.Sprintf("_若链接打不开，可复制：_\n`%s`\n\n", url) +
		"_授权后若群里仍收不到非 @ 消息，发 `/reconnect` 重连一次即可。_"
	return card2("需要补授权", []any{markdown(content)})
}

func GroupMsgScopeGrantedCard() JSONCard {
	return card2("授权成功", []any{
		markdown("✅ **授权成功**\n\n`im:message.group_msg` 权限已生效，群里非 @ bot 的消息从现在开始会触发回复。\n\n_若仍未生效，发 `/reconnect` 重连一次。_"),
	})
}

func ConfigCancelledCard() JSONCard {
	return card2("已取消", []any{markdown("已取消,未做任何修改。")})
}

func ConfigFailedCard(reason string) JSONCard {
	return card2("保存失败", []any{markdown(fmt.Sprintf("保存失败：%s", reason))})
}

func card2(summary string, elements []any) JSONCard {
	return JSONCard{
		"schema": "2.0",
		"config": map[string]any{
			"summary": map[string]any{"content": summary},
		},
		"body": map[string]any{
			"elements": elements,
		},
	}
}

func maskAppID(id string) string {
	if len(id) < 12 {
		return id
	}
	prefixLen := 13
	if len(id) < prefixLen {
		prefixLen = len(id)
	}
	return id[:prefixLen] + "****" + id[len(id)-2:]
}

func botNameOrUnknown(name string) string {
	if name == "" {
		return "(未知)"
	}
	return name
}

func plainText(content string) map[string]any {
	return map[string]any{"tag": "plain_text", "content": content}
}

func option(text string, value string) map[string]any {
	return map[string]any{"text": plainText(text), "value": value}
}

func selectStatic(name string, initial string, options []any) map[string]any {
	return map[string]any{
		"tag":            "select_static",
		"name":           name,
		"initial_option": initial,
		"options":        options,
	}
}

func textInput(name string, value string, placeholder string) map[string]any {
	return map[string]any{
		"tag":           "input",
		"name":          name,
		"default_value": value,
		"placeholder":   plainText(placeholder),
		"input_type":    "text",
	}
}

func callbackButton(text string, style string, value map[string]any, name string, submit bool) map[string]any {
	button := map[string]any{
		"tag": "button",
		"text": map[string]any{
			"tag":     "plain_text",
			"content": text,
		},
		"behaviors": []any{
			map[string]any{
				"type":  "callback",
				"value": value,
			},
		},
	}
	if style != "" {
		button["type"] = style
	}
	if name != "" {
		button["name"] = name
	}
	if submit {
		button["form_action_type"] = "submit"
	}
	return button
}

func columnSet(columns []any) map[string]any {
	return map[string]any{
		"tag":                "column_set",
		"flex_mode":          "flow",
		"horizontal_spacing": "small",
		"columns":            columns,
	}
}

func buttonColumn(button map[string]any) map[string]any {
	return map[string]any{
		"tag":      "column",
		"width":    "auto",
		"elements": []any{button},
	}
}

func collapsedAccessPanel(title string, elements []any) map[string]any {
	return map[string]any{
		"tag":      "collapsible_panel",
		"expanded": false,
		"header":   panelHeader(title),
		"border": map[string]any{
			"color":         "blue",
			"corner_radius": "5px",
		},
		"vertical_spacing": "8px",
		"padding":          "8px 8px 8px 8px",
		"elements":         elements,
	}
}

func atMentionLine(openIDs []string) string {
	if len(openIDs) == 0 {
		return "_（暂无）_"
	}
	out := make([]string, 0, len(openIDs))
	for _, id := range openIDs {
		out = append(out, fmt.Sprintf("<at id=\"%s\"></at>", id))
	}
	return strings.Join(out, "  ")
}

func chatList(chatIDs []string, knownChats []KnownChat) string {
	if len(chatIDs) == 0 {
		return "_（暂无）_"
	}
	nameMap := make(map[string]string, len(knownChats))
	for _, chat := range knownChats {
		nameMap[chat.ID] = chat.Name
	}
	out := make([]string, 0, len(chatIDs))
	for _, id := range chatIDs {
		name, ok := nameMap[id]
		if !ok {
			name = "(未知群)"
		}
		suffix := id
		if len(id) > 6 {
			suffix = id[len(id)-6:]
		}
		out = append(out, fmt.Sprintf("- **%s**（...%s）", name, suffix))
	}
	return strings.Join(out, "\n")
}

func showHide(show bool) string {
	if show {
		return "show"
	}
	return "hide"
}

func yesNoCN(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func yesNoChinese(value bool) string {
	if value {
		return "是"
	}
	return "否"
}

func summarizeList(values []string) string {
	if len(values) == 0 {
		return "_(空)_"
	}
	return fmt.Sprintf("%d 项", len(values))
}

func cotMessagesLabel(value CotMessagesMode) string {
	if value == CotMessagesBrief {
		return "简略"
	}
	if value == CotMessagesDetailed {
		return "详细"
	}
	return "关闭"
}

func runIdleTimeoutLabel(minutes int) string {
	if minutes > 0 {
		return fmt.Sprintf("%d 分钟", minutes)
	}
	return "关闭"
}
