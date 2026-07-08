package bridge

import "github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/cardkit"

type CardKitJSON map[string]any

type CardKitButtonSpec struct {
	Text  string
	Value map[string]any
	Style string
}

type CardKitStatusInfo struct {
	ProfileName         string
	CWD                 string
	SessionID           string
	EmptySessionText    string
	SessionStale        bool
	AgentName           string
	RuntimeAccess       CardKitLabelValue
	LarkCLIStatus       string
	ActiveRun           bool
	ActiveScopes        []string
	ActiveCommentScopes []string
	Queue               *CardKitQueueInfo
	OwnerState          string
	Scope               string
	ChatMode            string
}

type CardKitLabelValue struct {
	Label string
	Value string
}

type CardKitQueueInfo struct {
	Active  int
	Waiting int
	Cap     int
}

type CardKitResumeEntry struct {
	SessionID string
	DisplayID string
	Preview   string
	RelTime   string
	LineCount int
	Detail    string
	Current   bool
}

type CardKitTenantBrand string

type CardKitMessageReplyMode string

type CardKitCotMessagesMode string

type CardKitLarkCLIIdentityPreset string

type CardKitCurrentInfo struct {
	AppID   string
	BotName string
	Tenant  CardKitTenantBrand
}

type CardKitAccountFormOptions struct {
	InitialTenant CardKitTenantBrand
	PrefillAppID  string
	ErrorMessage  string
}

type CardKitKnownChat struct {
	ID   string
	Name string
}

type CardKitConfigFormOptions struct {
	MessageReply          CardKitMessageReplyMode
	ShowToolCalls         bool
	CotMessages           CardKitCotMessagesMode
	MaxConcurrentRuns     int
	RunIdleTimeoutMinutes int
	RequireMentionInGroup bool
	LarkCLIIdentity       CardKitLarkCLIIdentityPreset
	AllowedUsers          []string
	AllowedChats          []string
	Admins                []string
	KnownChats            []CardKitKnownChat
}

const (
	CardKitTenantFeishu CardKitTenantBrand = "feishu"
	CardKitTenantLark   CardKitTenantBrand = "lark"

	CardKitMessageReplyCard     CardKitMessageReplyMode = "card"
	CardKitMessageReplyMarkdown CardKitMessageReplyMode = "markdown"
	CardKitMessageReplyText     CardKitMessageReplyMode = "text"

	CardKitCotMessagesOff      CardKitCotMessagesMode = "off"
	CardKitCotMessagesBrief    CardKitCotMessagesMode = "brief"
	CardKitCotMessagesDetailed CardKitCotMessagesMode = "detailed"

	CardKitLarkCLIIdentityBotOnly     CardKitLarkCLIIdentityPreset = "bot-only"
	CardKitLarkCLIIdentityUserDefault CardKitLarkCLIIdentityPreset = "user-default"
)

func RenderRunCardKit(state RunCardState, options CardRenderOptions) CardKitJSON {
	return CardKitJSON(cardkit.RenderRunCardKit(toInternalRunCardState(state), toInternalCardRenderOptions(options)))
}

func RenderCardViewCardKit(view CardView) CardKitJSON {
	return CardKitJSON(cardkit.RenderCardView(toInternalCardView(view)))
}

func WorkspacesCardKit(current string, named map[string]string) CardKitJSON {
	return CardKitJSON(cardkit.WorkspacesCard(current, named))
}

func StatusCardKit(info CardKitStatusInfo) CardKitJSON {
	return CardKitJSON(cardkit.StatusCard(toInternalCardKitStatusInfo(info)))
}

func ResumeCardKit(cwd string, entries []CardKitResumeEntry) CardKitJSON {
	return CardKitJSON(cardkit.ResumeCard(cwd, toInternalCardKitResumeEntries(entries)))
}

func HelpCardKit(agentName string) CardKitJSON {
	return CardKitJSON(cardkit.HelpCard(agentName))
}

func AccountCurrentCardKit(info CardKitCurrentInfo) CardKitJSON {
	return CardKitJSON(cardkit.AccountCurrentCard(toInternalCardKitCurrentInfo(info)))
}

func AccountFormCardKit(opts CardKitAccountFormOptions) CardKitJSON {
	return CardKitJSON(cardkit.AccountFormCard(toInternalCardKitAccountFormOptions(opts)))
}

func AccountValidatingCardKit() CardKitJSON {
	return CardKitJSON(cardkit.AccountValidatingCard())
}

func AccountSuccessCardKit(info CardKitCurrentInfo) CardKitJSON {
	return CardKitJSON(cardkit.AccountSuccessCard(toInternalCardKitCurrentInfo(info)))
}

func AccountFailureCardKit(reason string) CardKitJSON {
	return CardKitJSON(cardkit.AccountFailureCard(reason))
}

func AccountCancelledCardKit() CardKitJSON {
	return CardKitJSON(cardkit.AccountCancelledCard())
}

func ConfigFormCardKit(opts CardKitConfigFormOptions) CardKitJSON {
	return CardKitJSON(cardkit.ConfigFormCard(toInternalCardKitConfigFormOptions(opts)))
}

func ConfigSavedCardKit(opts CardKitConfigFormOptions) CardKitJSON {
	return CardKitJSON(cardkit.ConfigSavedCard(toInternalCardKitConfigFormOptions(opts)))
}

func GroupMsgScopeGrantCardKit(url string, expireMins int) CardKitJSON {
	return CardKitJSON(cardkit.GroupMsgScopeGrantCard(url, expireMins))
}

func GroupMsgScopeGrantedCardKit() CardKitJSON {
	return CardKitJSON(cardkit.GroupMsgScopeGrantedCard())
}

func ConfigCancelledCardKit() CardKitJSON {
	return CardKitJSON(cardkit.ConfigCancelledCard())
}

func ConfigFailedCardKit(reason string) CardKitJSON {
	return CardKitJSON(cardkit.ConfigFailedCard(reason))
}

func toInternalCardKitStatusInfo(info CardKitStatusInfo) cardkit.StatusInfo {
	out, _ := convertBridgeJSON[cardkit.StatusInfo](info)
	return out
}

func toInternalCardKitResumeEntries(entries []CardKitResumeEntry) []cardkit.ResumeEntry {
	out, _ := convertBridgeJSON[[]cardkit.ResumeEntry](entries)
	return out
}

func toInternalCardKitCurrentInfo(info CardKitCurrentInfo) cardkit.CurrentInfo {
	out, _ := convertBridgeJSON[cardkit.CurrentInfo](info)
	return out
}

func toInternalCardKitAccountFormOptions(options CardKitAccountFormOptions) cardkit.AccountFormOptions {
	out, _ := convertBridgeJSON[cardkit.AccountFormOptions](options)
	return out
}

func toInternalCardKitConfigFormOptions(options CardKitConfigFormOptions) cardkit.ConfigFormOptions {
	out, _ := convertBridgeJSON[cardkit.ConfigFormOptions](options)
	return out
}
