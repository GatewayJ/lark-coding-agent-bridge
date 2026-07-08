package access

import "github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/profile"

type OwnerRefreshState string

const (
	OwnerRefreshOK      OwnerRefreshState = "ok"
	OwnerRefreshFailed  OwnerRefreshState = "failed"
	OwnerRefreshUnknown OwnerRefreshState = "unknown"
)

type RuntimeControls struct {
	BotOwnerID        string            `json:"botOwnerId,omitempty"`
	OwnerRefreshState OwnerRefreshState `json:"ownerRefreshState"`
	OwnerRefreshedAt  int64             `json:"ownerRefreshedAt,omitempty"`
	OwnerRefreshError string            `json:"ownerRefreshError,omitempty"`
}

type Reason string

const (
	ReasonOwner          Reason = "owner"
	ReasonAllowedUser    Reason = "allowed-user"
	ReasonAllowedAdmin   Reason = "allowed-admin"
	ReasonAllowedChat    Reason = "allowed-chat"
	ReasonCommentMention Reason = "comment-mention"
	ReasonDeniedUser     Reason = "denied-user"
	ReasonDeniedChat     Reason = "denied-chat"
	ReasonDeniedAdmin    Reason = "denied-admin"
)

type Decision struct {
	OK     bool   `json:"ok"`
	Reason Reason `json:"reason"`
}

func IsCreator(controls RuntimeControls, senderID string) bool {
	return controls.OwnerRefreshState != OwnerRefreshUnknown &&
		controls.BotOwnerID != "" &&
		controls.BotOwnerID == senderID
}

func CanUseDM(cfg profile.Config, controls RuntimeControls, senderID string) Decision {
	if IsCreator(controls, senderID) {
		return allow(ReasonOwner)
	}
	if includes(cfg.Access.AllowedUsers, senderID) {
		return allow(ReasonAllowedUser)
	}
	if includes(cfg.Access.Admins, senderID) {
		return allow(ReasonAllowedAdmin)
	}
	return deny(ReasonDeniedUser)
}

func CanUseGroup(cfg profile.Config, controls RuntimeControls, chatID string, senderID string) Decision {
	if IsCreator(controls, senderID) {
		return allow(ReasonOwner)
	}
	if includes(cfg.Access.Admins, senderID) {
		return allow(ReasonAllowedAdmin)
	}
	if includes(cfg.Access.AllowedChats, chatID) {
		return allow(ReasonAllowedChat)
	}
	return deny(ReasonDeniedChat)
}

func CanRunAdminCommand(cfg profile.Config, controls RuntimeControls, senderID string) Decision {
	if IsCreator(controls, senderID) {
		return allow(ReasonOwner)
	}
	if includes(cfg.Access.Admins, senderID) {
		return allow(ReasonAllowedAdmin)
	}
	return deny(ReasonDeniedAdmin)
}

func allow(reason Reason) Decision {
	return Decision{OK: true, Reason: reason}
}

func deny(reason Reason) Decision {
	return Decision{OK: false, Reason: reason}
}

func includes(items []string, needle string) bool {
	for _, item := range items {
		if item == needle {
			return true
		}
	}
	return false
}
