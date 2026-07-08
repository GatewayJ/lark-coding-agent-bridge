package bridge

import (
	"fmt"
	"time"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/adapters/agent/claudecli"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/runexecutor"
	appsession "github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/session"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/capability"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/permissions"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/profile"
	agentport "github.com/zarazhangrui/lark-coding-agent-bridge/internal/ports/agent"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/presentation/prompt"
)

const AgentClaude AgentKind = "claude"

type ClaudePermissionMode string

const (
	ClaudePermissionDefault           ClaudePermissionMode = "default"
	ClaudePermissionAcceptEdits       ClaudePermissionMode = "acceptEdits"
	ClaudePermissionBypassPermissions ClaudePermissionMode = "bypassPermissions"
	ClaudePermissionPlan              ClaudePermissionMode = "plan"
)

type ClaudeClientOptions struct {
	Binary             string
	DefaultWorkingDir  string
	SessionStorePath   string
	SessionCatalogPath string
	Logger             Logger
	MaxProcesses       int
	DefaultAccess      AccessMode
	MaxAccess          AccessMode
	AllowedUsers       []string
	AllowedChats       []string
	Admins             []string
	RequireMention     *bool
	PermissionMode     ClaudePermissionMode
	StopGrace          time.Duration
	LarkChannelEnv     map[string]string
	AdditionalEnv      map[string]string
	BotOpenID          string
	BotName            string
}

func NewClaudeClient(options ClaudeClientOptions) (*Client, error) {
	if options.Binary == "" {
		options.Binary = "claude"
	}
	defaultAccess, err := toInternalAccessMode(accessOrDefault(options.DefaultAccess, AccessFull))
	if err != nil {
		return nil, err
	}
	maxAccess, err := toInternalAccessMode(accessOrDefault(options.MaxAccess, AccessFull))
	if err != nil {
		return nil, err
	}
	if err := permissions.AssertAccessPair(defaultAccess, maxAccess, permissions.PermissionSourcePermissions); err != nil {
		return nil, err
	}

	cfg := profile.DefaultConfig(profile.AgentClaude)
	cfg.Workspaces.Default = options.DefaultWorkingDir
	cfg.Permissions = permissions.PermissionConfig{
		DefaultAccess: defaultAccess,
		MaxAccess:     maxAccess,
	}
	applyAccessOptions(&cfg, accessOptions{
		AllowedUsers:   options.AllowedUsers,
		AllowedChats:   options.AllowedChats,
		Admins:         options.Admins,
		RequireMention: options.RequireMention,
	})
	if options.PermissionMode != "" {
		permissionMode := permissions.ClaudePermissionMode(options.PermissionMode)
		if !permissions.IsClaudePermissionMode(permissionMode) {
			return nil, fmt.Errorf("invalid claude permission mode %q", options.PermissionMode)
		}
		cfg.Permissions.Claude = &permissions.ClaudePermissionConfig{PermissionMode: permissionMode}
	}

	adapter := claudecli.New(claudecli.Options{
		Binary:         options.Binary,
		StopGrace:      options.StopGrace,
		LarkChannelEnv: options.LarkChannelEnv,
		AdditionalEnv:  options.AdditionalEnv,
	})
	if options.BotOpenID != "" {
		adapter.SetBotIdentity(agentport.AgentBotIdentity{
			OpenID: options.BotOpenID,
			Name:   options.BotName,
		})
	}

	maxProcesses := options.MaxProcesses
	if maxProcesses <= 0 {
		maxProcesses = 1
	}
	client := &Client{
		agent:    adapter,
		sessions: appsession.NewStore(options.SessionStorePath),
		catalog:  appsession.NewCatalog(options.SessionCatalogPath),
		profile:  cfg,
		cap:      capability.Claude(maxAccess, prompt.BuildBridgeSystemPrompt(nil)),
		logger:   options.Logger,
	}
	client.executor = runexecutor.New(runexecutor.Options{
		Agent:  adapter,
		Pool:   runexecutor.NewProcessPool(func() int { return maxProcesses }),
		Logger: options.Logger,
	})
	return client, nil
}
