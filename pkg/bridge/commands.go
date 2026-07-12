package bridge

import (
	"context"
	"sync"
	"time"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/commands"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/runexecutor"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/access"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/profile"
)

type CommandRequest struct {
	CommandText string
	Command     string
	Args        string
	ScopeID     string
	ChatID      string
	ThreadID    string
	ActorID     string
	SenderID    string
	ChatMode    CommandChatMode
	WorkingDir  string
	Access      AccessDecision
	FormValue   map[string]any
	FromCard    bool
	MessageID   string
	EventID     string
	Mentions    []CommandMention
}

type CommandChatMode string

const (
	CommandChatModeP2P   CommandChatMode = "p2p"
	CommandChatModeGroup CommandChatMode = "group"
	CommandChatModeTopic CommandChatMode = "topic"
)

type CommandResponseKind string

type CommandLarkCLIStatus string

type CommandResponse struct {
	Handled   bool                  `json:"handled"`
	Command   string                `json:"command,omitempty"`
	Kind      CommandResponseKind   `json:"kind"`
	Markdown  string                `json:"markdown,omitempty"`
	NoReply   bool                  `json:"noReply,omitempty"`
	Resume    *CommandResumeView    `json:"resume,omitempty"`
	Status    *CommandStatusView    `json:"status,omitempty"`
	Stop      *CommandStopView      `json:"stop,omitempty"`
	Timeout   *CommandTimeoutView   `json:"timeout,omitempty"`
	PS        *CommandPSView        `json:"ps,omitempty"`
	Session   *CommandSessionView   `json:"session,omitempty"`
	Workspace *CommandWorkspaceView `json:"workspace,omitempty"`
	Help      *CommandHelpView      `json:"help,omitempty"`
	Exit      *CommandExitView      `json:"exit,omitempty"`
	Reconnect *CommandReconnectView `json:"reconnect,omitempty"`
	Doctor    *CommandDoctorView    `json:"doctor,omitempty"`
	Config    *CommandConfigView    `json:"config,omitempty"`
	Account   *CommandAccountView   `json:"account,omitempty"`
	Access    *CommandAccessView    `json:"access,omitempty"`
	Doc       *CommandDocView       `json:"doc,omitempty"`
}

type CommandResumeView struct {
	CWD     string               `json:"cwd,omitempty"`
	Entries []CommandResumeEntry `json:"entries,omitempty"`
	Applied bool                 `json:"applied,omitempty"`
}

type CommandResumeEntry struct {
	Token     string `json:"token"`
	DisplayID string `json:"displayId,omitempty"`
	Preview   string `json:"preview,omitempty"`
	Detail    string `json:"detail,omitempty"`
	UpdatedAt int64  `json:"updatedAt,omitempty"`
	Current   bool   `json:"current,omitempty"`
}

type CommandStatusView struct {
	ProfileName         string                     `json:"profileName"`
	CWD                 string                     `json:"cwd,omitempty"`
	SessionID           string                     `json:"sessionId,omitempty"`
	EmptySessionText    string                     `json:"emptySessionText,omitempty"`
	SessionStale        bool                       `json:"sessionStale,omitempty"`
	AgentName           string                     `json:"agentName"`
	RuntimeAccess       CommandRuntimeAccessView   `json:"runtimeAccess"`
	LarkCLIStatus       CommandLarkCLIStatus       `json:"larkCliStatus"`
	ActiveRun           bool                       `json:"activeRun"`
	ActiveScopes        []string                   `json:"activeScopes"`
	ActiveCommentScopes []string                   `json:"activeCommentScopes"`
	Queue               CommandProcessPoolSnapshot `json:"queue"`
	OwnerState          string                     `json:"ownerState"`
	Scope               string                     `json:"scope"`
	ChatMode            CommandChatMode            `json:"chatMode"`
}

type CommandProcessPoolSnapshot struct {
	Active  int `json:"active"`
	Waiting int `json:"waiting"`
	Cap     int `json:"cap"`
}

type CommandRuntimeAccessView struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type CommandStopView struct {
	Scope       string `json:"scope"`
	Targeted    bool   `json:"targeted"`
	Interrupted bool   `json:"interrupted"`
}

type CommandTimeoutView struct {
	Scope              string `json:"scope"`
	Targeted           bool   `json:"targeted"`
	ValueMinutes       *int   `json:"valueMinutes,omitempty"`
	HasOverride        bool   `json:"hasOverride"`
	EffectiveText      string `json:"effectiveText,omitempty"`
	GlobalMinutes      int    `json:"globalMinutes"`
	FollowGlobal       bool   `json:"followGlobal,omitempty"`
	Cleared            bool   `json:"cleared,omitempty"`
	Invalid            bool   `json:"invalid,omitempty"`
	DisabledForSession bool   `json:"disabledForSession,omitempty"`
}

type CommandPSView struct {
	Processes []CommandProcessEntry `json:"processes"`
	CurrentID string                `json:"currentId,omitempty"`
}

type CommandProcessEntry struct {
	ID        string    `json:"id"`
	PID       int       `json:"pid,omitempty"`
	AppID     string    `json:"appId,omitempty"`
	BotName   string    `json:"botName,omitempty"`
	StartedAt time.Time `json:"startedAt"`
	Current   bool      `json:"current,omitempty"`
}

type CommandSessionView struct {
	Action                     string `json:"action"`
	Scope                      string `json:"scope"`
	CWD                        string `json:"cwd,omitempty"`
	Interrupted                bool   `json:"interrupted"`
	ArchivedCurrent            bool   `json:"archivedCurrent"`
	SessionCleared             bool   `json:"sessionCleared"`
	IdleTimeoutOverrideCleared bool   `json:"idleTimeoutOverrideCleared,omitempty"`
	Unsupported                bool   `json:"unsupported,omitempty"`
	CreatedChatID              string `json:"createdChatId,omitempty"`
	CreatedChatName            string `json:"createdChatName,omitempty"`
	WelcomeSent                bool   `json:"welcomeSent,omitempty"`
	Failure                    string `json:"failure,omitempty"`
}

type CommandWorkspaceView struct {
	Action         string            `json:"action"`
	Scope          string            `json:"scope"`
	Name           string            `json:"name,omitempty"`
	CWD            string            `json:"cwd,omitempty"`
	CurrentCWD     string            `json:"currentCwd,omitempty"`
	Entries        map[string]string `json:"entries,omitempty"`
	Interrupted    bool              `json:"interrupted,omitempty"`
	SessionCleared bool              `json:"sessionCleared,omitempty"`
	Removed        bool              `json:"removed,omitempty"`
	Unsupported    bool              `json:"unsupported,omitempty"`
	Failure        string            `json:"failure,omitempty"`
}

type CommandHelpView struct {
	Commands []CommandHelpCommand `json:"commands"`
}

type CommandHelpCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
	Supported   bool   `json:"supported"`
}

type CommandExitView struct {
	Target       string `json:"target,omitempty"`
	CurrentID    string `json:"currentId,omitempty"`
	Found        bool   `json:"found"`
	Self         bool   `json:"self,omitempty"`
	Terminated   bool   `json:"terminated,omitempty"`
	StillAlive   bool   `json:"stillAlive,omitempty"`
	Unsupported  bool   `json:"unsupported,omitempty"`
	Failure      string `json:"failure,omitempty"`
	ResolvedID   string `json:"resolvedId,omitempty"`
	ResolvedPID  int    `json:"resolvedPid,omitempty"`
	ResolvedRank int    `json:"resolvedRank,omitempty"`
}

type CommandReconnectView struct {
	Wait              bool     `json:"wait"`
	PausedNewRuns     bool     `json:"pausedNewRuns,omitempty"`
	StoppedScopes     []string `json:"stoppedScopes,omitempty"`
	WaitedForRuns     bool     `json:"waitedForRuns,omitempty"`
	Restarted         bool     `json:"restarted,omitempty"`
	Unsupported       bool     `json:"unsupported,omitempty"`
	Failure           string   `json:"failure,omitempty"`
	LifecycleManaged  bool     `json:"lifecycleManaged,omitempty"`
	ReconnectorActive bool     `json:"reconnectorActive,omitempty"`
}

type CommandDoctorView struct {
	Report         string                     `json:"report"`
	ProfileName    string                     `json:"profileName"`
	AgentName      string                     `json:"agentName"`
	AgentKind      AgentKind                  `json:"agentKind"`
	CWD            string                     `json:"cwd,omitempty"`
	WorkspaceCheck string                     `json:"workspaceCheck,omitempty"`
	PolicyCheck    string                     `json:"policyCheck,omitempty"`
	EchoCheck      string                     `json:"echoCheck,omitempty"`
	Queue          CommandProcessPoolSnapshot `json:"queue"`
	Access         AccessDecision             `json:"access"`
	RuntimeAccess  CommandRuntimeAccessView   `json:"runtimeAccess"`
	OwnerState     string                     `json:"ownerState"`
	RunExecutor    bool                       `json:"runExecutor"`
	Redacted       bool                       `json:"redacted"`
}

type CommandConfigView struct {
	Action      string                    `json:"action"`
	ProfileName string                    `json:"profileName"`
	Snapshot    CommandConfigSnapshotView `json:"snapshot"`
	Saved       bool                      `json:"saved,omitempty"`
	Unsupported bool                      `json:"unsupported,omitempty"`
	Failure     string                    `json:"failure,omitempty"`
}

type CommandConfigSnapshotView struct {
	AgentKind             AgentKind                `json:"agentKind"`
	DefaultWorkspace      string                   `json:"defaultWorkspace,omitempty"`
	RuntimeAccess         CommandRuntimeAccessView `json:"runtimeAccess"`
	MessageReply          string                   `json:"messageReply"`
	ShowToolCalls         bool                     `json:"showToolCalls"`
	CotMessages           string                   `json:"cotMessages"`
	MaxConcurrentRuns     int                      `json:"maxConcurrentRuns"`
	RunIdleTimeoutMinutes int                      `json:"runIdleTimeoutMinutes"`
	RequireMentionInGroup bool                     `json:"requireMentionInGroup"`
	LarkCLIIdentity       string                   `json:"larkCliIdentity"`
	AllowedUsersCount     int                      `json:"allowedUsersCount"`
	AllowedChatsCount     int                      `json:"allowedChatsCount"`
	AdminsCount           int                      `json:"adminsCount"`
}

type CommandAccountView struct {
	Action            string `json:"action"`
	AppID             string `json:"appId,omitempty"`
	Tenant            string `json:"tenant,omitempty"`
	BotName           string `json:"botName,omitempty"`
	SecretRedacted    bool   `json:"secretRedacted,omitempty"`
	Saved             bool   `json:"saved,omitempty"`
	Unsupported       bool   `json:"unsupported,omitempty"`
	ValidationSkipped bool   `json:"validationSkipped,omitempty"`
	Failure           string `json:"failure,omitempty"`
}

type CommandAccessView struct {
	Action       string   `json:"action"`
	Kind         string   `json:"kind,omitempty"`
	Added        []string `json:"added,omitempty"`
	Already      []string `json:"already,omitempty"`
	Removed      []string `json:"removed,omitempty"`
	Missing      []string `json:"missing,omitempty"`
	AddedGroups  int      `json:"addedGroups,omitempty"`
	TotalGroups  int      `json:"totalGroups,omitempty"`
	AllowedUsers []string `json:"allowedUsers,omitempty"`
	AllowedChats []string `json:"allowedChats,omitempty"`
	Admins       []string `json:"admins,omitempty"`
}

type CommandDocView struct {
	Unsupported bool `json:"unsupported,omitempty"`
	NoOp        bool `json:"noOp,omitempty"`
}

type CommandMention struct {
	OpenID string `json:"openId"`
	Name   string `json:"name,omitempty"`
	IsBot  bool   `json:"isBot,omitempty"`
}

type CommandKnownChat struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

type CommandAccountValidationResult struct {
	OK      bool
	BotName string
	Reason  string
}

const (
	CommandResponseNone      CommandResponseKind = "none"
	CommandResponseMessage   CommandResponseKind = "message"
	CommandResponseStatus    CommandResponseKind = "status"
	CommandResponseResume    CommandResponseKind = "resume"
	CommandResponseStop      CommandResponseKind = "stop"
	CommandResponseTimeout   CommandResponseKind = "timeout"
	CommandResponsePS        CommandResponseKind = "ps"
	CommandResponseSession   CommandResponseKind = "session"
	CommandResponseWorkspace CommandResponseKind = "workspace"
	CommandResponseHelp      CommandResponseKind = "help"
	CommandResponseExit      CommandResponseKind = "exit"
	CommandResponseReconnect CommandResponseKind = "reconnect"
	CommandResponseDoctor    CommandResponseKind = "doctor"
	CommandResponseConfig    CommandResponseKind = "config"
	CommandResponseAccount   CommandResponseKind = "account"
	CommandResponseAccess    CommandResponseKind = "access"
	CommandResponseDoc       CommandResponseKind = "doc"

	CommandLarkCLIStatusApp         CommandLarkCLIStatus = "app"
	CommandLarkCLIStatusUserReady   CommandLarkCLIStatus = "user-ready"
	CommandLarkCLIStatusUserMissing CommandLarkCLIStatus = "user-missing"
	CommandLarkCLIStatusCheckFailed CommandLarkCLIStatus = "check-failed"
)

type CommandOptions struct {
	ProfileName       string
	RuntimeControls   RuntimeControls
	LarkCLIStatus     CommandLarkCLIStatus
	ProcessID         string
	ProcessIDFunc     func() string
	Processes         CommandProcessLister
	ProcessController CommandProcessController
	Reconnector       CommandReconnector
	LarkCLIIdentity   CommandLarkCLIIdentityPolicyApplier
	ConfigPath        string
	Keystore          *Keystore
	AccountValidator  CommandAccountValidator
	ChatCreator       CommandChatCreator
	KnownChats        []CommandKnownChat
	Workspaces        CommandWorkspaceStore
	GlobalIdleTimeout time.Duration
}

type RuntimeControls struct {
	BotOwnerID        string
	OwnerRefreshState string
	OwnerRefreshedAt  int64
	OwnerRefreshError string
}

type CommandProcessLister interface {
	ListProcesses() []CommandProcessEntry
}

type CommandProcessController interface {
	ExitSelf(ctx context.Context) error
	Terminate(ctx context.Context, entry CommandProcessEntry) (stillAlive bool, err error)
}

type CommandReconnector interface {
	Restart(ctx context.Context, wait bool) error
}

type CommandReconnectorFunc func(context.Context, bool) error

func (f CommandReconnectorFunc) Restart(ctx context.Context, wait bool) error {
	return f(ctx, wait)
}

type CommandLarkCLIIdentityPolicyApplier interface {
	ApplyLarkCLIIdentityPolicy(ctx context.Context, identity string) bool
}

type CommandLarkCLIIdentityPolicyApplierFunc func(context.Context, string) bool

func (f CommandLarkCLIIdentityPolicyApplierFunc) ApplyLarkCLIIdentityPolicy(ctx context.Context, identity string) bool {
	return f(ctx, identity)
}

type CommandAccountValidator interface {
	ValidateAppCredentials(ctx context.Context, appID, appSecret, tenant string) (CommandAccountValidationResult, error)
}

type CommandAccountValidatorFunc func(ctx context.Context, appID, appSecret, tenant string) (CommandAccountValidationResult, error)

func (f CommandAccountValidatorFunc) ValidateAppCredentials(ctx context.Context, appID, appSecret, tenant string) (CommandAccountValidationResult, error) {
	return f(ctx, appID, appSecret, tenant)
}

type CommandWorkspaceStore interface {
	CWDFor(scopeID string) string
	SetCWD(scopeID string, cwd string) error
	ListNamed() map[string]string
	GetNamed(name string) string
	SaveNamed(name string, cwd string) error
	RemoveNamed(name string) (bool, error)
}

type CommandChatCreator interface {
	CreateBoundChat(ctx context.Context, input CommandCreateBoundChatInput) (CommandCreatedChat, error)
}

type CommandChatCreatorFunc func(context.Context, CommandCreateBoundChatInput) (CommandCreatedChat, error)

func (f CommandChatCreatorFunc) CreateBoundChat(ctx context.Context, input CommandCreateBoundChatInput) (CommandCreatedChat, error) {
	return f(ctx, input)
}

type CommandChatMessenger interface {
	SendMessageToChat(ctx context.Context, chatID string, markdown string) error
}

type CommandCreateBoundChatInput struct {
	Name         string
	InviteOpenID string
	Description  string
}

type CommandCreatedChat struct {
	ChatID string `json:"chatId"`
	Name   string `json:"name"`
}

// CommandHandler owns the mutable command facade state for one Client. Use it
// in long-lived SDK hosts that need explicit command handler lifetime instead
// of the backwards-compatible Client.HandleCommand package-level state.
type CommandHandler struct {
	client *Client
	state  *commandServiceState
}

func NewCommandHandler(client *Client) (*CommandHandler, error) {
	if client == nil {
		return nil, ErrNilClient
	}
	return &CommandHandler{client: client, state: newCommandServiceState()}, nil
}

func (h *CommandHandler) HandleCommand(ctx context.Context, req CommandRequest, opts CommandOptions) (CommandResponse, error) {
	if h == nil || h.client == nil {
		return CommandResponse{}, ErrNilClient
	}
	state := h.state
	if state == nil {
		state = newCommandServiceState()
	}
	return handleCommandWithState(ctx, h.client, state, req, opts, h.client.profile)
}

func (c *Client) HandleCommand(ctx context.Context, req CommandRequest, opts CommandOptions) (CommandResponse, error) {
	if c == nil {
		return CommandResponse{}, ErrNilClient
	}
	return c.HandleCommandWithProfile(ctx, req, opts, c.profile)
}

// HandleCommandWithProfile handles one command using the supplied profile
// snapshot without mutating the long-lived Client configuration.
func (c *Client) HandleCommandWithProfile(ctx context.Context, req CommandRequest, opts CommandOptions, profileConfig profile.Config) (CommandResponse, error) {
	if c == nil {
		return CommandResponse{}, ErrNilClient
	}
	state := commandServiceStateForClient(c)
	return handleCommandWithState(ctx, c, state, req, opts, profileConfig)
}

// ReleaseCommandState clears the backwards-compatible command state owned by
// Client.HandleCommand. Long-lived hosts should prefer NewCommandHandler for
// explicit state lifetime, or call this when discarding a Client.
func (c *Client) ReleaseCommandState() {
	if c == nil {
		return
	}
	bridgeCommandServices.Delete(commandServiceKey{client: c})
}

func handleCommandWithState(ctx context.Context, c *Client, state *commandServiceState, req CommandRequest, opts CommandOptions, profileConfig profile.Config) (CommandResponse, error) {
	if req.Access.OK && !req.Access.trusted {
		return CommandResponse{}, ErrUntrustedAccessDecision
	}
	service := commandServiceWithState(c, state, opts, profileConfig)
	response, err := service.Handle(ctx, commands.Request{
		CommandText: req.CommandText,
		Command:     req.Command,
		Args:        req.Args,
		ScopeID:     req.ScopeID,
		ChatID:      req.ChatID,
		ThreadID:    req.ThreadID,
		ActorID:     req.ActorID,
		SenderID:    req.SenderID,
		ChatMode:    commands.ChatMode(req.ChatMode),
		WorkingDir:  req.WorkingDir,
		Access:      toAccessDecision(req.Access),
		FormValue:   req.FormValue,
		FromCard:    req.FromCard,
		MessageID:   req.MessageID,
		EventID:     req.EventID,
		Mentions:    toInternalCommandMentions(req.Mentions),
	})
	return fromInternalCommandResponse(response), err
}

var bridgeCommandServices sync.Map

func commandServiceStateForClient(c *Client) *commandServiceState {
	key := commandServiceKey{client: c}
	value, _ := bridgeCommandServices.LoadOrStore(key, newCommandServiceState())
	return value.(*commandServiceState)
}

func commandServiceWithState(c *Client, state *commandServiceState, opts CommandOptions, profileConfig profile.Config) *commands.Service {
	if state == nil {
		state = newCommandServiceState()
	}
	workspaces := opts.Workspaces
	if workspaces == nil {
		workspaces = state.workspaces
	}
	var processController commands.ProcessController
	if opts.ProcessController != nil {
		processController = processControllerAdapter{delegate: opts.ProcessController}
	}
	var reconnector commands.Reconnector
	if opts.Reconnector != nil {
		reconnector = reconnectorAdapter{delegate: opts.Reconnector}
	}
	var larkCLIIdentity commands.LarkCLIIdentityPolicyApplier
	if opts.LarkCLIIdentity != nil {
		larkCLIIdentity = larkCLIIdentityPolicyApplierAdapter{delegate: opts.LarkCLIIdentity}
	}
	var accountValidator commands.AccountValidator
	if opts.AccountValidator != nil {
		accountValidator = accountValidatorAdapter{delegate: opts.AccountValidator}
	}
	var chatCreator commands.BoundChatCreator
	if opts.ChatCreator != nil {
		chatCreator = chatCreatorAdapter{delegate: opts.ChatCreator}
	}
	processID := opts.ProcessID
	if opts.ProcessIDFunc != nil {
		if got := opts.ProcessIDFunc(); got != "" {
			processID = got
		}
	}
	return commands.New(commands.Options{
		ProfileName:       defaultProfileName(opts.ProfileName),
		ProfileConfig:     profileConfig,
		Capability:        c.cap,
		RuntimeControls:   toInternalRuntimeControls(opts.RuntimeControls),
		Sessions:          c.sessions,
		SessionCatalog:    c.catalog,
		Workspaces:        commandWorkspaceAdapter{delegate: workspaces},
		Executor:          c.executor,
		CodexHistory:      codexHistoryAdapter{client: c, profileConfig: profileConfig},
		Processes:         processListerAdapter{delegate: opts.Processes},
		ProcessController: processController,
		Reconnector:       reconnector,
		LarkCLIIdentity:   larkCLIIdentity,
		ConfigPath:        opts.ConfigPath,
		Keystore:          internalKeystore(opts.Keystore),
		AccountValidator:  accountValidator,
		ChatCreator:       chatCreator,
		KnownChats:        toInternalCommandKnownChats(opts.KnownChats),
		LarkCLIStatus:     commands.LarkCLIStatus(opts.LarkCLIStatus),
		ProcessID:         processID,
		AgentName:         c.agent.DisplayName(),
		GlobalIdleTimeout: opts.GlobalIdleTimeout,
		ResumeStore:       state.resume,
	})
}

type processControllerAdapter struct {
	delegate CommandProcessController
}

func (a processControllerAdapter) ExitSelf(ctx context.Context) error {
	if a.delegate == nil {
		return nil
	}
	return a.delegate.ExitSelf(ctx)
}

func (a processControllerAdapter) Terminate(ctx context.Context, entry commands.ProcessEntry) (bool, error) {
	if a.delegate == nil {
		return true, nil
	}
	return a.delegate.Terminate(ctx, fromInternalCommandProcessEntry(entry))
}

type reconnectorAdapter struct {
	delegate CommandReconnector
}

func (a reconnectorAdapter) Restart(ctx context.Context, wait bool) error {
	if a.delegate == nil {
		return nil
	}
	return a.delegate.Restart(ctx, wait)
}

type larkCLIIdentityPolicyApplierAdapter struct {
	delegate CommandLarkCLIIdentityPolicyApplier
}

func (a larkCLIIdentityPolicyApplierAdapter) ApplyLarkCLIIdentityPolicy(ctx context.Context, identity string) bool {
	if a.delegate == nil {
		return true
	}
	return a.delegate.ApplyLarkCLIIdentityPolicy(ctx, identity)
}

type accountValidatorAdapter struct {
	delegate CommandAccountValidator
}

func (a accountValidatorAdapter) ValidateAppCredentials(ctx context.Context, appID, appSecret, tenant string) (commands.AccountValidationResult, error) {
	if a.delegate == nil {
		return commands.AccountValidationResult{}, nil
	}
	result, err := a.delegate.ValidateAppCredentials(ctx, appID, appSecret, tenant)
	return toInternalCommandAccountValidationResult(result), err
}

type chatCreatorAdapter struct {
	delegate CommandChatCreator
}

func (a chatCreatorAdapter) CreateBoundChat(ctx context.Context, input commands.CreateBoundChatInput) (commands.CreatedChat, error) {
	if a.delegate == nil {
		return commands.CreatedChat{}, nil
	}
	result, err := a.delegate.CreateBoundChat(ctx, fromInternalCreateBoundChatInput(input))
	return toInternalCreatedChat(result), err
}

func (a chatCreatorAdapter) SendMessageToChat(ctx context.Context, chatID string, markdown string) error {
	messenger, ok := a.delegate.(CommandChatMessenger)
	if !ok {
		return nil
	}
	return messenger.SendMessageToChat(ctx, chatID, markdown)
}

type commandServiceKey struct {
	client *Client
}

type commandServiceState struct {
	resume     *commands.ResumeStore
	workspaces *commandMemoryWorkspaceStore
}

func newCommandServiceState() *commandServiceState {
	return &commandServiceState{
		resume:     commands.NewResumeStore(nil),
		workspaces: newCommandMemoryWorkspaceStore(),
	}
}

type codexHistoryAdapter struct {
	client        *Client
	profileConfig profile.Config
}

func (a codexHistoryAdapter) ListCodexThreads(ctx context.Context, query commands.CodexHistoryQuery) ([]commands.CodexThreadHistoryEntry, error) {
	entries, err := a.client.ListCodexThreadsWithProfile(ctx, CodexHistoryOptions{
		CWD:   query.CWD,
		Limit: query.Limit,
	}, a.profileConfig)
	if err != nil {
		return nil, err
	}
	out := make([]commands.CodexThreadHistoryEntry, 0, len(entries))
	for _, entry := range entries {
		name := ""
		if entry.Name != nil {
			name = *entry.Name
		}
		out = append(out, commands.CodexThreadHistoryEntry{
			ThreadID:    entry.ThreadID,
			SessionID:   entry.SessionID,
			Preview:     entry.Preview,
			CWD:         entry.CWD,
			CreatedAtMs: entry.CreatedAtMs,
			UpdatedAtMs: entry.UpdatedAtMs,
			Source:      entry.Source,
			Name:        name,
		})
	}
	return out, nil
}

type processListerAdapter struct {
	delegate CommandProcessLister
}

func (a processListerAdapter) ListProcesses() []commands.ProcessEntry {
	if a.delegate == nil {
		return nil
	}
	entries := a.delegate.ListProcesses()
	out := make([]commands.ProcessEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, toInternalCommandProcessEntry(entry))
	}
	return out
}

type commandWorkspaceAdapter struct {
	delegate CommandWorkspaceStore
}

func (a commandWorkspaceAdapter) CWDFor(scopeID string) string {
	if a.delegate == nil {
		return ""
	}
	return a.delegate.CWDFor(scopeID)
}

func (a commandWorkspaceAdapter) SetCWD(scopeID string, cwd string) error {
	if a.delegate == nil {
		return nil
	}
	return a.delegate.SetCWD(scopeID, cwd)
}

func (a commandWorkspaceAdapter) ListNamed() map[string]string {
	if a.delegate == nil {
		return nil
	}
	return a.delegate.ListNamed()
}

func (a commandWorkspaceAdapter) GetNamed(name string) string {
	if a.delegate == nil {
		return ""
	}
	return a.delegate.GetNamed(name)
}

func (a commandWorkspaceAdapter) SaveNamed(name string, cwd string) error {
	if a.delegate == nil {
		return nil
	}
	return a.delegate.SaveNamed(name, cwd)
}

func (a commandWorkspaceAdapter) RemoveNamed(name string) (bool, error) {
	if a.delegate == nil {
		return false, nil
	}
	return a.delegate.RemoveNamed(name)
}

type commandMemoryWorkspaceStore struct {
	mu    sync.Mutex
	cwds  map[string]string
	named map[string]string
}

func newCommandMemoryWorkspaceStore() *commandMemoryWorkspaceStore {
	return &commandMemoryWorkspaceStore{cwds: map[string]string{}, named: map[string]string{}}
}

func (s *commandMemoryWorkspaceStore) CWDFor(scopeID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cwds[scopeID]
}

func (s *commandMemoryWorkspaceStore) SetCWD(scopeID string, cwd string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cwds[scopeID] = cwd
	return nil
}

func (s *commandMemoryWorkspaceStore) ListNamed() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.named))
	for key, value := range s.named {
		out[key] = value
	}
	return out
}

func (s *commandMemoryWorkspaceStore) GetNamed(name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.named[name]
}

func (s *commandMemoryWorkspaceStore) SaveNamed(name string, cwd string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.named[name] = cwd
	return nil
}

func (s *commandMemoryWorkspaceStore) RemoveNamed(name string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.named[name]; !ok {
		return false, nil
	}
	delete(s.named, name)
	return true, nil
}

func defaultProfileName(name string) string {
	if name != "" {
		return name
	}
	return "codex"
}

func toInternalRuntimeControls(input RuntimeControls) access.RuntimeControls {
	return access.RuntimeControls{
		BotOwnerID:        input.BotOwnerID,
		OwnerRefreshState: access.OwnerRefreshState(input.OwnerRefreshState),
		OwnerRefreshedAt:  input.OwnerRefreshedAt,
		OwnerRefreshError: input.OwnerRefreshError,
	}
}

func fromInternalCommandResponse(response commands.Response) CommandResponse {
	out, _ := convertBridgeJSON[CommandResponse](response)
	return out
}

func toInternalCommandMentions(mentions []CommandMention) []commands.Mention {
	out, _ := convertBridgeJSON[[]commands.Mention](mentions)
	return out
}

func toInternalCommandKnownChats(chats []CommandKnownChat) []commands.KnownChat {
	out, _ := convertBridgeJSON[[]commands.KnownChat](chats)
	return out
}

func toInternalCommandProcessEntry(entry CommandProcessEntry) commands.ProcessEntry {
	out, _ := convertBridgeJSON[commands.ProcessEntry](entry)
	return out
}

func fromInternalCommandProcessEntry(entry commands.ProcessEntry) CommandProcessEntry {
	out, _ := convertBridgeJSON[CommandProcessEntry](entry)
	return out
}

func toInternalCommandAccountValidationResult(result CommandAccountValidationResult) commands.AccountValidationResult {
	out, _ := convertBridgeJSON[commands.AccountValidationResult](result)
	return out
}

func fromInternalCreateBoundChatInput(input commands.CreateBoundChatInput) CommandCreateBoundChatInput {
	out, _ := convertBridgeJSON[CommandCreateBoundChatInput](input)
	return out
}

func toInternalCreatedChat(input CommandCreatedChat) commands.CreatedChat {
	out, _ := convertBridgeJSON[commands.CreatedChat](input)
	return out
}

type commandExecutorView interface {
	Interrupt(context.Context, string) bool
	ActiveScopes() []string
	PoolSnapshot() runexecutor.ProcessPoolSnapshot
}

var _ commandExecutorView = (*runexecutor.Executor)(nil)
