package bridge

import (
	"context"
	"errors"
	"time"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/adapters/agent/codexcli"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/runexecutor"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/runflow"
	appsession "github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/session"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/access"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/capability"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/permissions"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/profile"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/runpolicy"
	agentport "github.com/zarazhangrui/lark-coding-agent-bridge/internal/ports/agent"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/presentation/prompt"
)

var ErrNilClient = errors.New("bridge client is nil")
var ErrUntrustedAccessDecision = errors.New("bridge allow access decisions must come from Client.EvaluateAccess")

type CodexClientOptions struct {
	Binary             string
	ProfileStateDir    string
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
	CodexHome          string
	InheritCodexHome   *bool
	IgnoreUserConfig   bool
	IgnoreRules        *bool
	LarkChannelEnv     map[string]string
	AdditionalEnv      map[string]string
	BotOpenID          string
	BotName            string
}

type Client struct {
	agent           agentport.AgentAdapter
	executor        *runexecutor.Executor
	sessions        *appsession.Store
	catalog         *appsession.Catalog
	profile         profile.Config
	cap             capability.Capability
	profileStateDir string
	logger          Logger
	profileName     string
}

type AgentAvailability struct {
	OK         bool                         `json:"ok"`
	Version    string                       `json:"version,omitempty"`
	Error      string                       `json:"error,omitempty"`
	Diagnostic *AgentAvailabilityDiagnostic `json:"diagnostic,omitempty"`
}

type AgentAvailabilityDiagnostic struct {
	Code          string   `json:"code,omitempty"`
	Command       string   `json:"command,omitempty"`
	BinaryPath    string   `json:"binaryPath,omitempty"`
	Args          []string `json:"args,omitempty"`
	ExitCode      *int     `json:"exitCode,omitempty"`
	TimeoutMs     int      `json:"timeoutMs,omitempty"`
	StdoutExcerpt string   `json:"stdoutExcerpt,omitempty"`
	StderrExcerpt string   `json:"stderrExcerpt,omitempty"`
	Message       string   `json:"message,omitempty"`
}

func NewCodexClient(options CodexClientOptions) (*Client, error) {
	if options.Binary == "" {
		options.Binary = "codex"
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

	cfg := profile.DefaultConfig(profile.AgentCodex)
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
	cfg.Codex = &profile.CodexConfig{
		BinaryPath:       options.Binary,
		CodexHome:        options.CodexHome,
		InheritCodexHome: options.InheritCodexHome == nil || *options.InheritCodexHome,
		IgnoreUserConfig: options.IgnoreUserConfig,
	}
	adapter := codexcli.New(codexcli.Options{
		Binary:           options.Binary,
		ProfileStateDir:  options.ProfileStateDir,
		CodexHome:        options.CodexHome,
		InheritCodexHome: options.InheritCodexHome,
		IgnoreUserConfig: options.IgnoreUserConfig,
		IgnoreRules:      options.IgnoreRules,
		LarkChannelEnv:   options.LarkChannelEnv,
		AdditionalEnv:    options.AdditionalEnv,
		Logger:           options.Logger,
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
		agent:           adapter,
		sessions:        appsession.NewStore(options.SessionStorePath),
		catalog:         appsession.NewCatalog(options.SessionCatalogPath),
		profile:         cfg,
		cap:             capability.Codex(maxAccess, prompt.BuildBridgeSystemPrompt(nil)),
		profileStateDir: options.ProfileStateDir,
		logger:          options.Logger,
	}
	client.executor = runexecutor.New(runexecutor.Options{
		Agent:  adapter,
		Pool:   runexecutor.NewProcessPool(func() int { return maxProcesses }),
		Logger: options.Logger,
	})
	return client, nil
}

func (c *Client) CheckAvailability(ctx context.Context) (AgentAvailability, error) {
	if c == nil {
		return AgentAvailability{}, ErrNilClient
	}
	if checker, ok := c.agent.(agentport.AvailabilityChecker); ok {
		availability, err := checker.CheckAvailability(ctx)
		return fromInternalAgentAvailability(availability), err
	}
	ok, err := c.agent.IsAvailable(ctx)
	return AgentAvailability{OK: ok}, err
}

func (c *Client) LoadState() error {
	if c == nil {
		return ErrNilClient
	}
	if err := c.sessions.Load(); err != nil {
		return err
	}
	if err := c.catalog.Load(); err != nil {
		return err
	}
	return nil
}

func (c *Client) FlushState() error {
	if c == nil {
		return ErrNilClient
	}
	if err := c.sessions.Flush(); err != nil {
		return err
	}
	if err := c.catalog.Flush(); err != nil {
		return err
	}
	return nil
}

type RunInput struct {
	ScopeID     string
	Scope       Scope
	Prompt      string
	WorkingDir  string
	Access      AccessDecision
	Attachments []Attachment
	Model       string
	StopGrace   time.Duration
	Nowait      bool
}

type Run struct {
	metadata RunMetadata
	exec     *runexecutor.RunExecution
	client   *Client
	policy   runpolicy.Allow
}

func (c *Client) Run(ctx context.Context, input RunInput) (*Run, error) {
	if c == nil {
		return nil, ErrNilClient
	}
	if input.ScopeID == "" {
		return nil, errors.New("scopeId is required")
	}
	if input.Access.OK && !input.Access.trusted {
		return nil, ErrUntrustedAccessDecision
	}
	store := workspaceStore{scopeID: input.ScopeID, cwd: input.WorkingDir}
	result, err := runflow.Start(ctx, runflow.StartInput{
		ScopeID:        input.ScopeID,
		Scope:          toRunPolicyScope(input.Scope),
		Prompt:         input.Prompt,
		Attachments:    toRunPolicyAttachments(input.Attachments),
		Access:         toAccessDecision(input.Access),
		Capability:     c.cap,
		ProfileConfig:  c.profile,
		Sessions:       c.sessions,
		SessionCatalog: c.catalog,
		Workspaces:     store,
		Executor:       c.executor,
		StopGraceMs:    int(input.StopGrace / time.Millisecond),
		Model:          input.Model,
		Nowait:         input.Nowait,
		Observability: &runflow.Observability{
			Profile: c.profileName,
			Agent:   string(c.cap.AgentID),
			Source:  string(input.Scope.Source),
			Stage:   "submit",
		},
	})
	if err != nil {
		return nil, err
	}
	if !result.OK {
		return nil, RejectedError{
			Code:        result.RejectReason.Code,
			UserVisible: result.RejectReason.UserVisible,
		}
	}
	run := &Run{
		metadata: RunMetadata{
			RunID:             result.Execution.RunID,
			ScopeID:           result.Execution.ScopeID,
			CWDRealpath:       result.CWDRealpath,
			ResumeFrom:        result.ResumeFrom,
			PolicyFingerprint: result.Policy.PolicyFingerprint,
			ExpiresAt:         result.Policy.ExpiresAt,
		},
		exec:   result.Execution,
		client: c,
		policy: result.Policy,
	}
	go run.recordSessionEvents()
	return run, nil
}

func (c *Client) EvaluateAccess(input AccessInput) AccessDecision {
	if c == nil {
		return trustedAccessDecision(access.Decision{OK: false, Reason: access.ReasonDeniedUser})
	}
	controls := toInternalRuntimeControls(input.RuntimeControls)
	actorID := input.Scope.ActorID
	if input.AdminCommand {
		return trustedAccessDecision(access.CanRunAdminCommand(c.profile, controls, actorID))
	}
	if input.ChatMode == LarkChatModeP2P || (input.ChatMode == "" && input.Scope.ChatID == "") {
		return trustedAccessDecision(access.CanUseDM(c.profile, controls, actorID))
	}
	return trustedAccessDecision(access.CanUseGroup(c.profile, controls, input.Scope.ChatID, actorID))
}

func (c *Client) applyLarkRuntimeContext(identity LarkBotIdentity, env map[string]string) {
	if c == nil || c.agent == nil {
		return
	}
	if identity.OpenID != "" {
		if setter, ok := c.agent.(agentport.BotIdentitySetter); ok {
			setter.SetBotIdentity(agentport.AgentBotIdentity{
				OpenID: identity.OpenID,
				Name:   identity.Name,
			})
		}
	}
	if len(env) > 0 {
		if merger, ok := c.agent.(agentport.EnvMerger); ok {
			merger.MergeEnv(env)
		}
	}
}

func (c *Client) setLogger(logger Logger) {
	if c == nil {
		return
	}
	c.logger = logger
	if c.executor != nil {
		c.executor.SetLogger(logger)
	}
	if adapter, ok := c.agent.(*codexcli.Adapter); ok {
		adapter.SetLogger(logger)
	}
}

func (c *Client) setProfileName(profileName string) {
	if c == nil {
		return
	}
	c.profileName = profileName
}

func (r *Run) Metadata() RunMetadata {
	if r == nil {
		return RunMetadata{}
	}
	return r.metadata
}

func (r *Run) Events(ctx context.Context) <-chan Event {
	out := make(chan Event, 32)
	go func() {
		defer close(out)
		if r == nil || r.exec == nil {
			return
		}
		for event := range r.exec.Subscribe(ctx) {
			outEvent := fromAgentEvent(event)
			outEvent.At = time.Now()
			outEvent.RunID = bridgeStringPtr(r.metadata.RunID)
			outEvent.ScopeID = bridgeStringPtr(r.metadata.ScopeID)
			select {
			case out <- outEvent:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

func (r *Run) Stop(ctx context.Context) error {
	if r == nil || r.exec == nil {
		return nil
	}
	return r.exec.Stop(ctx)
}

func bridgeStringPtr(value string) *string {
	return &value
}

func (r *Run) recordSessionEvents() {
	if r == nil || r.exec == nil || r.client == nil {
		return
	}
	for event := range r.exec.Subscribe(context.Background()) {
		_ = runflow.RecordSessionEvent(runflow.RecordSessionEventInput{
			ScopeID:        r.metadata.ScopeID,
			Sessions:       r.client.sessions,
			SessionCatalog: r.client.catalog,
			Capability:     r.client.cap,
			Policy:         r.policy,
			Event:          event,
		})
	}
}

type RejectedError struct {
	Code        string
	UserVisible string
}

func (e RejectedError) Error() string {
	if e.UserVisible != "" {
		return e.UserVisible
	}
	return e.Code
}

func IsRejected(err error) bool {
	var rejected RejectedError
	return errors.As(err, &rejected)
}

type workspaceStore struct {
	scopeID string
	cwd     string
}

func (s workspaceStore) CWDFor(scopeID string) string {
	if s.scopeID == scopeID {
		return s.cwd
	}
	return ""
}

func accessOrDefault(value AccessMode, fallback AccessMode) AccessMode {
	if value == "" {
		return fallback
	}
	return value
}

func fromInternalAgentAvailability(input agentport.AgentAvailability) AgentAvailability {
	out, _ := convertBridgeJSON[AgentAvailability](input)
	return out
}
