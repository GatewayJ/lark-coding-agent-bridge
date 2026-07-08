package bridge

import "github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/profile"

type accessOptions struct {
	AllowedUsers   []string
	AllowedChats   []string
	Admins         []string
	RequireMention *bool
}

func applyAccessOptions(cfg *profile.Config, opts accessOptions) {
	if cfg == nil {
		return
	}
	if opts.AllowedUsers != nil {
		cfg.Access.AllowedUsers = append([]string(nil), opts.AllowedUsers...)
	}
	if opts.AllowedChats != nil {
		cfg.Access.AllowedChats = append([]string(nil), opts.AllowedChats...)
	}
	if opts.Admins != nil {
		cfg.Access.Admins = append([]string(nil), opts.Admins...)
	}
	if opts.RequireMention != nil {
		cfg.Access.RequireMentionInGroup = *opts.RequireMention
	}
}
