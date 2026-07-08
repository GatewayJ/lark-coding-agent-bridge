package commands

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/configstore"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/larkcli"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/runexecutor"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/secretstore"
	appsession "github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/session"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/workspace"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/access"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/capability"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/permissions"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/profile"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/runpolicy"
)

const (
	resumeCandidateTTL = 10 * time.Minute
	resumeAppliedReply = "已完成，请继续发送下一条消息。"
)

type ChatMode string

const (
	ChatModeP2P   ChatMode = "p2p"
	ChatModeGroup ChatMode = "group"
	ChatModeTopic ChatMode = "topic"
)

type ResponseKind string

const (
	ResponseNone      ResponseKind = "none"
	ResponseMessage   ResponseKind = "message"
	ResponseStatus    ResponseKind = "status"
	ResponseResume    ResponseKind = "resume"
	ResponseStop      ResponseKind = "stop"
	ResponseTimeout   ResponseKind = "timeout"
	ResponsePS        ResponseKind = "ps"
	ResponseSession   ResponseKind = "session"
	ResponseWorkspace ResponseKind = "workspace"
	ResponseHelp      ResponseKind = "help"
	ResponseExit      ResponseKind = "exit"
	ResponseReconnect ResponseKind = "reconnect"
	ResponseDoctor    ResponseKind = "doctor"
	ResponseConfig    ResponseKind = "config"
	ResponseAccount   ResponseKind = "account"
	ResponseAccess    ResponseKind = "access"
	ResponseDoc       ResponseKind = "doc"
)

type Executor interface {
	Interrupt(ctx context.Context, scopeID string) bool
	ActiveScopes() []string
	PoolSnapshot() runexecutor.ProcessPoolSnapshot
}

type runLifecycleController interface {
	PauseNewRuns(reason string) func()
	StopAll(ctx context.Context) error
	WaitForAll(ctx context.Context) error
}

type doctorSubmitter interface {
	Submit(ctx context.Context, input runexecutor.SubmitRunInput) (*runexecutor.RunExecution, error)
}

type WorkspaceStore interface {
	CWDFor(scopeID string) string
	SetCWD(scopeID string, cwd string) error
	ListNamed() map[string]string
	GetNamed(name string) string
	SaveNamed(name string, cwd string) error
	RemoveNamed(name string) (bool, error)
}

type CodexHistoryProvider interface {
	ListCodexThreads(ctx context.Context, query CodexHistoryQuery) ([]CodexThreadHistoryEntry, error)
}

type CodexHistoryProviderFunc func(ctx context.Context, query CodexHistoryQuery) ([]CodexThreadHistoryEntry, error)

func (f CodexHistoryProviderFunc) ListCodexThreads(ctx context.Context, query CodexHistoryQuery) ([]CodexThreadHistoryEntry, error) {
	return f(ctx, query)
}

type ClaudeHistoryProvider interface {
	ListClaudeSessions(ctx context.Context, cwd string, limit int) ([]ClaudeSessionSummary, error)
}

type ClaudeHistoryProviderFunc func(ctx context.Context, cwd string, limit int) ([]ClaudeSessionSummary, error)

func (f ClaudeHistoryProviderFunc) ListClaudeSessions(ctx context.Context, cwd string, limit int) ([]ClaudeSessionSummary, error) {
	return f(ctx, cwd, limit)
}

type ProcessLister interface {
	ListProcesses() []ProcessEntry
}

type ProcessListerFunc func() []ProcessEntry

func (f ProcessListerFunc) ListProcesses() []ProcessEntry {
	return f()
}

type ProcessController interface {
	ExitSelf(ctx context.Context) error
	Terminate(ctx context.Context, entry ProcessEntry) (stillAlive bool, err error)
}

type ProcessControllerFunc struct {
	ExitSelfFunc  func(context.Context) error
	TerminateFunc func(context.Context, ProcessEntry) (bool, error)
}

func (f ProcessControllerFunc) ExitSelf(ctx context.Context) error {
	if f.ExitSelfFunc == nil {
		return nil
	}
	return f.ExitSelfFunc(ctx)
}

func (f ProcessControllerFunc) Terminate(ctx context.Context, entry ProcessEntry) (bool, error) {
	if f.TerminateFunc == nil {
		return true, errors.New("process termination is not configured")
	}
	return f.TerminateFunc(ctx, entry)
}

type Reconnector interface {
	Restart(ctx context.Context, wait bool) error
}

type ReconnectorFunc func(context.Context, bool) error

func (f ReconnectorFunc) Restart(ctx context.Context, wait bool) error {
	return f(ctx, wait)
}

type LarkCLIIdentityPolicyApplier interface {
	ApplyLarkCLIIdentityPolicy(ctx context.Context, identity string) bool
}

type LarkCLIIdentityPolicyApplierFunc func(context.Context, string) bool

func (f LarkCLIIdentityPolicyApplierFunc) ApplyLarkCLIIdentityPolicy(ctx context.Context, identity string) bool {
	return f(ctx, identity)
}

type AccountValidationResult struct {
	OK      bool
	BotName string
	Reason  string
}

type AccountValidator interface {
	ValidateAppCredentials(ctx context.Context, appID, appSecret, tenant string) (AccountValidationResult, error)
}

type AccountValidatorFunc func(ctx context.Context, appID, appSecret, tenant string) (AccountValidationResult, error)

func (f AccountValidatorFunc) ValidateAppCredentials(ctx context.Context, appID, appSecret, tenant string) (AccountValidationResult, error) {
	return f(ctx, appID, appSecret, tenant)
}

type BoundChatCreator interface {
	CreateBoundChat(ctx context.Context, input CreateBoundChatInput) (CreatedChat, error)
}

type BoundChatCreatorFunc func(context.Context, CreateBoundChatInput) (CreatedChat, error)

func (f BoundChatCreatorFunc) CreateBoundChat(ctx context.Context, input CreateBoundChatInput) (CreatedChat, error) {
	return f(ctx, input)
}

type CreateBoundChatInput struct {
	Name         string
	InviteOpenID string
	Description  string
}

type CreatedChat struct {
	ChatID string `json:"chatId"`
	Name   string `json:"name"`
}

type Mention struct {
	OpenID string `json:"openId"`
	Name   string `json:"name,omitempty"`
	IsBot  bool   `json:"isBot,omitempty"`
}

type KnownChat struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

type LarkCLIStatus string

const (
	LarkCLIStatusApp         LarkCLIStatus = "app"
	LarkCLIStatusUserReady   LarkCLIStatus = "user-ready"
	LarkCLIStatusUserMissing LarkCLIStatus = "user-missing"
	LarkCLIStatusCheckFailed LarkCLIStatus = "check-failed"
)

type LarkCLIConfig struct {
	TargetConfigFile string
	AppID            string
	Tenant           string
	IdentityPreset   larkcli.IdentityPreset
}

type Options struct {
	ProfileName       string
	ProfileConfig     profile.Config
	Capability        capability.Capability
	RuntimeControls   access.RuntimeControls
	Sessions          *appsession.Store
	SessionCatalog    *appsession.Catalog
	Workspaces        WorkspaceStore
	Executor          Executor
	CodexHistory      CodexHistoryProvider
	ClaudeHistory     ClaudeHistoryProvider
	Processes         ProcessLister
	ProcessController ProcessController
	Reconnector       Reconnector
	ConfigPath        string
	ConfigLoadOptions configstore.LoadOptions
	Keystore          *secretstore.Keystore
	AccountValidator  AccountValidator
	ChatCreator       BoundChatCreator
	KnownChats        []KnownChat
	ResumeStore       *ResumeStore
	LarkCLI           LarkCLIConfig
	LarkCLIStatus     LarkCLIStatus
	ProcessID         string
	AgentName         string
	GlobalIdleTimeout time.Duration
	LarkCLIIdentity   LarkCLIIdentityPolicyApplier
	Now               func() time.Time
}

type Service struct {
	opts Options
}

func New(opts Options) *Service {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.ResumeStore == nil {
		opts.ResumeStore = NewResumeStore(opts.Now)
	}
	if opts.Capability.AgentID == "" {
		if opts.ProfileConfig.AgentKind == profile.AgentCodex {
			opts.Capability = capability.Codex(opts.ProfileConfig.Permissions.MaxAccess, "")
		} else {
			opts.Capability = capability.Claude(opts.ProfileConfig.Permissions.MaxAccess, "")
		}
	}
	if opts.AgentName == "" {
		opts.AgentName = string(opts.Capability.AgentID)
	}
	return &Service{opts: opts}
}

type Request struct {
	CommandText string
	Command     string
	Args        string
	ScopeID     string
	ChatID      string
	ThreadID    string
	ActorID     string
	SenderID    string
	ChatMode    ChatMode
	WorkingDir  string
	Access      access.Decision
	FormValue   map[string]any
	FromCard    bool
	MessageID   string
	EventID     string
	Mentions    []Mention
}

type Response struct {
	Handled   bool           `json:"handled"`
	Command   string         `json:"command,omitempty"`
	Kind      ResponseKind   `json:"kind"`
	Markdown  string         `json:"markdown,omitempty"`
	NoReply   bool           `json:"noReply,omitempty"`
	Resume    *ResumeView    `json:"resume,omitempty"`
	Status    *StatusView    `json:"status,omitempty"`
	Stop      *StopView      `json:"stop,omitempty"`
	Timeout   *TimeoutView   `json:"timeout,omitempty"`
	PS        *PSView        `json:"ps,omitempty"`
	Session   *SessionView   `json:"session,omitempty"`
	Workspace *WorkspaceView `json:"workspace,omitempty"`
	Help      *HelpView      `json:"help,omitempty"`
	Exit      *ExitView      `json:"exit,omitempty"`
	Reconnect *ReconnectView `json:"reconnect,omitempty"`
	Doctor    *DoctorView    `json:"doctor,omitempty"`
	Config    *ConfigView    `json:"config,omitempty"`
	Account   *AccountView   `json:"account,omitempty"`
	Access    *AccessView    `json:"access,omitempty"`
	Doc       *DocView       `json:"doc,omitempty"`
}

type ResumeView struct {
	CWD     string        `json:"cwd,omitempty"`
	Entries []ResumeEntry `json:"entries,omitempty"`
	Applied bool          `json:"applied,omitempty"`
}

type ResumeEntry struct {
	Token     string `json:"token"`
	DisplayID string `json:"displayId,omitempty"`
	Preview   string `json:"preview,omitempty"`
	Detail    string `json:"detail,omitempty"`
	UpdatedAt int64  `json:"updatedAt,omitempty"`
	Current   bool   `json:"current,omitempty"`
}

type StatusView struct {
	ProfileName         string                          `json:"profileName"`
	CWD                 string                          `json:"cwd,omitempty"`
	SessionID           string                          `json:"sessionId,omitempty"`
	EmptySessionText    string                          `json:"emptySessionText,omitempty"`
	SessionStale        bool                            `json:"sessionStale,omitempty"`
	AgentName           string                          `json:"agentName"`
	RuntimeAccess       RuntimeAccessView               `json:"runtimeAccess"`
	LarkCLIStatus       LarkCLIStatus                   `json:"larkCliStatus"`
	ActiveRun           bool                            `json:"activeRun"`
	ActiveScopes        []string                        `json:"activeScopes"`
	ActiveCommentScopes []string                        `json:"activeCommentScopes"`
	Queue               runexecutor.ProcessPoolSnapshot `json:"queue"`
	OwnerState          string                          `json:"ownerState"`
	Scope               string                          `json:"scope"`
	ChatMode            ChatMode                        `json:"chatMode"`
}

type RuntimeAccessView struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type StopView struct {
	Scope       string `json:"scope"`
	Targeted    bool   `json:"targeted"`
	Interrupted bool   `json:"interrupted"`
}

type TimeoutView struct {
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

type PSView struct {
	Processes []ProcessEntry `json:"processes"`
	CurrentID string         `json:"currentId,omitempty"`
}

type ProcessEntry struct {
	ID        string    `json:"id"`
	PID       int       `json:"pid,omitempty"`
	AppID     string    `json:"appId,omitempty"`
	BotName   string    `json:"botName,omitempty"`
	StartedAt time.Time `json:"startedAt"`
	Current   bool      `json:"current,omitempty"`
}

type SessionView struct {
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

type WorkspaceView struct {
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

type HelpView struct {
	Commands []HelpCommand `json:"commands"`
}

type HelpCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
	Supported   bool   `json:"supported"`
}

type ExitView struct {
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

type ReconnectView struct {
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

type DoctorView struct {
	Report         string                          `json:"report"`
	ProfileName    string                          `json:"profileName"`
	AgentName      string                          `json:"agentName"`
	AgentKind      profile.AgentKind               `json:"agentKind"`
	CWD            string                          `json:"cwd,omitempty"`
	WorkspaceCheck string                          `json:"workspaceCheck,omitempty"`
	PolicyCheck    string                          `json:"policyCheck,omitempty"`
	EchoCheck      string                          `json:"echoCheck,omitempty"`
	Queue          runexecutor.ProcessPoolSnapshot `json:"queue"`
	Access         access.Decision                 `json:"access"`
	RuntimeAccess  RuntimeAccessView               `json:"runtimeAccess"`
	OwnerState     string                          `json:"ownerState"`
	RunExecutor    bool                            `json:"runExecutor"`
	Redacted       bool                            `json:"redacted"`
}

type ConfigView struct {
	Action      string             `json:"action"`
	ProfileName string             `json:"profileName"`
	Snapshot    ConfigSnapshotView `json:"snapshot"`
	Saved       bool               `json:"saved,omitempty"`
	Unsupported bool               `json:"unsupported,omitempty"`
	Failure     string             `json:"failure,omitempty"`
}

type ConfigSnapshotView struct {
	AgentKind             profile.AgentKind `json:"agentKind"`
	DefaultWorkspace      string            `json:"defaultWorkspace,omitempty"`
	RuntimeAccess         RuntimeAccessView `json:"runtimeAccess"`
	MessageReply          string            `json:"messageReply"`
	ShowToolCalls         bool              `json:"showToolCalls"`
	CotMessages           string            `json:"cotMessages"`
	MaxConcurrentRuns     int               `json:"maxConcurrentRuns"`
	RunIdleTimeoutMinutes int               `json:"runIdleTimeoutMinutes"`
	RequireMentionInGroup bool              `json:"requireMentionInGroup"`
	LarkCLIIdentity       string            `json:"larkCliIdentity"`
	AllowedUsersCount     int               `json:"allowedUsersCount"`
	AllowedChatsCount     int               `json:"allowedChatsCount"`
	AdminsCount           int               `json:"adminsCount"`
}

type AccountView struct {
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

type AccessView struct {
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

type DocView struct {
	Unsupported bool `json:"unsupported,omitempty"`
	NoOp        bool `json:"noOp,omitempty"`
}

type CodexHistoryQuery struct {
	CWD   string
	Limit int
}

type CodexThreadHistoryEntry struct {
	ThreadID    string `json:"threadId"`
	SessionID   string `json:"sessionId,omitempty"`
	Preview     string `json:"preview"`
	CWD         string `json:"cwd"`
	CreatedAtMs int64  `json:"createdAtMs"`
	UpdatedAtMs int64  `json:"updatedAtMs"`
	Source      string `json:"source"`
	Name        string `json:"name,omitempty"`
}

type ClaudeSessionSummary struct {
	SessionID string `json:"sessionId"`
	Preview   string `json:"preview,omitempty"`
	MTime     int64  `json:"mtime,omitempty"`
	LineCount int    `json:"lineCount,omitempty"`
}

func (s *Service) Handle(ctx context.Context, req Request) (Response, error) {
	cmd, args := parseCommand(req)
	if cmd == "" {
		return Response{Handled: false}, nil
	}
	if req.ChatMode == "" {
		req.ChatMode = ChatModeP2P
	}
	if req.ScopeID == "" {
		req.ScopeID = req.ChatID
	}
	if req.SenderID == "" {
		req.SenderID = req.ActorID
	}
	req.Command = cmd
	req.Args = args

	if isAdminCommand(cmd) && !s.canAdmin(req) {
		return message(cmd, "❌ 此命令仅管理员可用。"), nil
	}

	switch cmd {
	case "/new", "/reset":
		return s.handleNew(ctx, req)
	case "/cd":
		return s.handleCD(ctx, req)
	case "/ws":
		return s.handleWS(ctx, req)
	case "/resume":
		return s.handleResume(ctx, req)
	case "/status":
		return s.handleStatus(ctx, req)
	case "/stop":
		return s.handleStop(ctx, req)
	case "/timeout":
		return s.handleTimeout(ctx, req)
	case "/ps":
		return s.handlePS(req), nil
	case "/help":
		return s.handleHelp(req), nil
	case "/exit":
		return s.handleExit(ctx, req), nil
	case "/reconnect":
		return s.handleReconnect(ctx, req), nil
	case "/doctor":
		return s.handleDoctor(ctx, req)
	case "/config":
		return s.handleConfig(ctx, req)
	case "/account":
		return s.handleAccount(ctx, req)
	case "/invite":
		return s.handleInvite(ctx, req)
	case "/remove":
		return s.handleRemove(ctx, req)
	case "/doc":
		return s.handleDoc(req), nil
	default:
		return Response{Handled: false}, nil
	}
}

func parseCommand(req Request) (string, string) {
	if strings.TrimSpace(req.CommandText) != "" {
		trimmed := strings.TrimSpace(req.CommandText)
		if !strings.HasPrefix(trimmed, "/") {
			return "", ""
		}
		parts := strings.Fields(trimmed)
		if len(parts) == 0 {
			return "", ""
		}
		return parts[0], strings.Join(parts[1:], " ")
	}
	cmd := strings.TrimSpace(req.Command)
	if cmd == "" {
		return "", ""
	}
	if !strings.HasPrefix(cmd, "/") {
		cmd = "/" + cmd
	}
	return cmd, strings.TrimSpace(req.Args)
}

func isAdminCommand(cmd string) bool {
	switch cmd {
	case "/account", "/config", "/ps", "/exit", "/reconnect", "/doctor", "/cd", "/ws", "/invite", "/remove":
		return true
	default:
		return false
	}
}

func (s *Service) handleNew(ctx context.Context, req Request) (Response, error) {
	trimmed := strings.TrimSpace(req.Args)
	view := &SessionView{Action: strings.TrimPrefix(req.Command, "/"), Scope: req.ScopeID}
	if trimmed == "chat" || strings.HasPrefix(trimmed, "chat ") {
		rawName := ""
		if trimmed != "chat" {
			rawName = strings.TrimSpace(strings.TrimPrefix(trimmed, "chat"))
		}
		return s.handleNewChat(ctx, req, view, rawName)
	}

	view.Interrupted = s.interrupt(ctx, req.ScopeID)
	view.ArchivedCurrent = s.archiveCurrentSession(req)
	if s.opts.Sessions != nil {
		s.opts.Sessions.Clear(req.ScopeID)
		view.SessionCleared = true
		view.IdleTimeoutOverrideCleared = s.opts.Sessions.ClearIdleTimeoutOverride(req.ScopeID)
	}
	text := "已开始新会话。"
	if view.Interrupted {
		text = "已中断当前任务并开始新会话。"
	}
	return Response{
		Handled:  true,
		Command:  req.Command,
		Kind:     ResponseSession,
		Markdown: text,
		Session:  view,
	}, nil
}

func (s *Service) handleNewChat(ctx context.Context, req Request, view *SessionView, rawName string) (Response, error) {
	if view == nil {
		view = &SessionView{Action: strings.TrimPrefix(req.Command, "/"), Scope: req.ScopeID}
	}
	if s.opts.ChatCreator == nil {
		view.Unsupported = true
		view.Failure = "chat creator is not configured"
		return Response{
			Handled:  true,
			Command:  req.Command,
			Kind:     ResponseSession,
			Markdown: "`/new chat` 当前未配置建群能力。",
			Session:  view,
		}, nil
	}

	sourceCWD := s.effectiveCWD(req)
	name := strings.TrimSpace(rawName)
	if name == "" {
		name = defaultChatName(s.opts.AgentName, s.opts.Now())
	}
	created, err := s.opts.ChatCreator.CreateBoundChat(ctx, CreateBoundChatInput{
		Name:         name,
		InviteOpenID: req.SenderID,
	})
	if err != nil {
		view.Failure = err.Error()
		return Response{
			Handled:  true,
			Command:  req.Command,
			Kind:     ResponseSession,
			Markdown: fmt.Sprintf("❌ 创建群失败：%s\n\n确认 bot 已开启 `im:chat` 权限。", err.Error()),
			Session:  view,
		}, nil
	}
	view.CreatedChatID = created.ChatID
	view.CreatedChatName = firstNonEmpty(created.Name, name)
	cwdInherited := false
	if sourceCWD != "" && created.ChatID != "" && s.opts.Workspaces != nil {
		if err := s.opts.Workspaces.SetCWD(created.ChatID, sourceCWD); err != nil {
			view.Failure = err.Error()
		} else {
			view.CWD = sourceCWD
			cwdInherited = true
		}
	}

	welcome := "🎉 群已建好。\n\n@我 + 任意消息开始对话。"
	if cwdInherited {
		welcome = fmt.Sprintf("🎉 群已建好，cwd 继承自原群：`%s`\n\n@我 + 任意消息开始对话。", sourceCWD)
	}
	if created.ChatID != "" && s.opts.ChatCreator != nil {
		if sender, ok := s.opts.ChatCreator.(interface {
			SendMessageToChat(context.Context, string, string) error
		}); ok {
			if err := sender.SendMessageToChat(ctx, created.ChatID, welcome); err == nil {
				view.WelcomeSent = true
			}
		}
	}

	if view.Failure != "" {
		return Response{
			Handled:  true,
			Command:  req.Command,
			Kind:     ResponseSession,
			Markdown: fmt.Sprintf("⚠️ 已创建群 **%s**，但保存 cwd 继承失败：%s\n\n请在新群里重新发送 `/cd %s`。", view.CreatedChatName, view.Failure, sourceCWD),
			Session:  view,
		}, nil
	}

	return Response{
		Handled:  true,
		Command:  req.Command,
		Kind:     ResponseSession,
		Markdown: fmt.Sprintf("✓ 已创建群 **%s**，去新群里继续。", view.CreatedChatName),
		Session:  view,
	}, nil
}

func (s *Service) handleCD(ctx context.Context, req Request) (Response, error) {
	input := strings.TrimSpace(req.Args)
	if input == "" {
		return workspaceMessage(req.Command, "cd", "用法：`/cd <绝对路径>` 或 `/cd ~/xxx`", &WorkspaceView{Action: "cd", Scope: req.ScopeID}), nil
	}
	if !isAbsoluteOrTilde(input) {
		return workspaceMessage(req.Command, "cd", "请使用绝对路径，或 `~/xxx` 表示 home 下的子路径。", &WorkspaceView{Action: "cd", Scope: req.ScopeID}), nil
	}
	if s.opts.Workspaces == nil {
		return workspaceMessage(req.Command, "cd", "当前命令未配置 workspace store。", &WorkspaceView{Action: "cd", Scope: req.ScopeID}), nil
	}

	resolved := workspace.ResolveWorkingDirectory(expandTilde(input))
	if !resolved.OK {
		return workspaceMessage(req.Command, "cd", resolved.UserVisible, &WorkspaceView{Action: "cd", Scope: req.ScopeID}), nil
	}

	view := &WorkspaceView{Action: "cd", Scope: req.ScopeID, CWD: resolved.CWDRealpath}
	if err := s.opts.Workspaces.SetCWD(req.ScopeID, resolved.CWDRealpath); err != nil {
		return workspacePersistenceFailure(req.Command, "cd", view, err), nil
	}
	view.Interrupted = s.interrupt(ctx, req.ScopeID)
	s.archiveCurrentSession(req)
	if s.opts.Sessions != nil {
		s.opts.Sessions.Clear(req.ScopeID)
		view.SessionCleared = true
	}
	return workspaceMessage(req.Command, "cd", fmt.Sprintf("✓ 已切换 cwd 到 `%s`\n（session 已重置）", resolved.CWDRealpath), view), nil
}

func (s *Service) handleWS(ctx context.Context, req Request) (Response, error) {
	parts := strings.Fields(strings.TrimSpace(req.Args))
	sub := ""
	if len(parts) > 0 {
		sub = parts[0]
	}
	name := ""
	if len(parts) > 1 {
		name = strings.Join(parts[1:], " ")
	}
	switch sub {
	case "", "list":
		return s.handleWSList(req), nil
	case "save":
		return s.handleWSSave(req, strings.TrimSpace(name)), nil
	case "use":
		return s.handleWSUse(ctx, req, strings.TrimSpace(name)), nil
	case "remove", "rm":
		return s.handleWSRemove(req, strings.TrimSpace(name)), nil
	default:
		return workspaceMessage(req.Command, "usage", "用法：`/ws [list|save <name>|use <name>|remove <name>]`", &WorkspaceView{Action: "usage", Scope: req.ScopeID}), nil
	}
}

func (s *Service) handleWSList(req Request) Response {
	view := &WorkspaceView{
		Action:     "list",
		Scope:      req.ScopeID,
		CurrentCWD: s.effectiveCWD(req),
		Entries:    s.listScopedWorkspaces(req),
	}
	rows := []string{fmt.Sprintf("当前 cwd：`%s`", emptyAs(view.CurrentCWD, "(未设置)"))}
	if len(view.Entries) == 0 {
		rows = append(rows, "暂无命名工作目录。")
	} else {
		names := make([]string, 0, len(view.Entries))
		for name := range view.Entries {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			marker := ""
			if view.Entries[name] == view.CurrentCWD {
				marker = "  ← 当前"
			}
			rows = append(rows, fmt.Sprintf("**%s** → `%s`%s", name, view.Entries[name], marker))
		}
	}
	return workspaceMessage(req.Command, "list", strings.Join(rows, "\n"), view)
}

func (s *Service) handleWSSave(req Request, name string) Response {
	view := &WorkspaceView{Action: "save", Scope: req.ScopeID, Name: name}
	if name == "" {
		return workspaceMessage(req.Command, "save", "用法：`/ws save <name>`", view)
	}
	if s.opts.Workspaces == nil {
		return workspaceMessage(req.Command, "save", "当前命令未配置 workspace store。", view)
	}
	cwd := s.effectiveCWD(req)
	if strings.TrimSpace(cwd) == "" {
		return workspaceMessage(req.Command, "save", "当前 chat 未设置 cwd，先用 `/cd` 设置再保存。", view)
	}
	view.CWD = cwd
	if err := s.opts.Workspaces.SaveNamed(s.scopedWorkspaceName(req, name), cwd); err != nil {
		return workspacePersistenceFailure(req.Command, "save", view, err)
	}
	return workspaceMessage(req.Command, "save", fmt.Sprintf("✓ 工作目录别名已保存：`%s` → %s", name, cwd), view)
}

func (s *Service) handleWSUse(ctx context.Context, req Request, name string) Response {
	view := &WorkspaceView{Action: "use", Scope: req.ScopeID, Name: name}
	if name == "" {
		return workspaceMessage(req.Command, "use", "用法：`/ws use <name>`", view)
	}
	if s.opts.Workspaces == nil {
		return workspaceMessage(req.Command, "use", "当前命令未配置 workspace store。", view)
	}
	cwd := s.workspaceAlias(req, name)
	if cwd == "" {
		return workspaceMessage(req.Command, "use", fmt.Sprintf("未找到工作目录别名：`%s`", name), view)
	}
	resolved := workspace.ResolveWorkingDirectory(cwd)
	if !resolved.OK {
		return workspaceMessage(req.Command, "use", resolved.UserVisible, view)
	}
	view.CWD = resolved.CWDRealpath
	if err := s.opts.Workspaces.SetCWD(req.ScopeID, resolved.CWDRealpath); err != nil {
		return workspacePersistenceFailure(req.Command, "use", view, err)
	}
	view.Interrupted = s.interrupt(ctx, req.ScopeID)
	s.archiveCurrentSession(req)
	if s.opts.Sessions != nil {
		s.opts.Sessions.Clear(req.ScopeID)
		view.SessionCleared = true
	}
	return workspaceMessage(req.Command, "use", fmt.Sprintf("✓ 已切换到 `%s` (%s)\n（session 已重置）", name, resolved.CWDRealpath), view)
}

func (s *Service) handleWSRemove(req Request, name string) Response {
	view := &WorkspaceView{Action: "remove", Scope: req.ScopeID, Name: name}
	if name == "" {
		return workspaceMessage(req.Command, "remove", "用法：`/ws remove <name>`", view)
	}
	if s.opts.Workspaces == nil {
		return workspaceMessage(req.Command, "remove", "当前命令未配置 workspace store。", view)
	}
	removed, err := s.removeWorkspaceAlias(req, name)
	if err != nil {
		return workspacePersistenceFailure(req.Command, "remove", view, err)
	}
	view.Removed = removed
	if !view.Removed {
		return workspaceMessage(req.Command, "remove", fmt.Sprintf("未找到工作目录别名：`%s`", name), view)
	}
	return workspaceMessage(req.Command, "remove", fmt.Sprintf("✓ 已删除工作目录别名：`%s`", name), view)
}

func (s *Service) handleResume(ctx context.Context, req Request) (Response, error) {
	parts := strings.Fields(req.Args)
	if len(parts) >= 2 && parts[0] == "use" {
		return s.applyResume(ctx, req, strings.Join(parts[1:], " "))
	}

	limit := 5
	if len(parts) > 0 {
		if parsed, err := strconv.Atoi(parts[0]); err == nil && parsed > 0 && parsed <= 20 {
			limit = parsed
		}
	}

	identity, cwd, ok, userVisible, err := s.catalogIdentity(req)
	if err != nil {
		return Response{}, err
	}
	if !ok {
		return message("/resume", userVisible), nil
	}
	if req.ChatMode != ChatModeP2P {
		return message("/resume", "群聊中不展示历史会话详情。请私聊 bot 使用 `/resume` 查看和选择历史会话。"), nil
	}

	if s.opts.Capability.AgentID == capability.IDCodex {
		return s.listCodexResume(ctx, req, identity, cwd, limit)
	}
	return s.listClaudeResume(ctx, req, identity, cwd, limit)
}

func (s *Service) listCodexResume(ctx context.Context, req Request, identity appsession.CatalogIdentity, cwd string, limit int) (Response, error) {
	var current appsession.CatalogEntry
	hasCurrent := false
	if s.opts.SessionCatalog != nil {
		current, hasCurrent = s.opts.SessionCatalog.ActiveFor(identity)
	}
	if s.opts.CodexHistory != nil {
		history, err := s.opts.CodexHistory.ListCodexThreads(ctx, CodexHistoryQuery{CWD: cwd, Limit: limit})
		if err != nil {
			history = nil
		}
		if len(history) > 0 {
			entries := make([]ResumeEntry, 0, len(history))
			for _, thread := range history {
				token := s.opts.ResumeStore.Issue(identity, resumeTarget{threadID: thread.ThreadID})
				preview := thread.Name
				if preview == "" {
					preview = thread.Preview
				}
				entries = append(entries, ResumeEntry{
					Token:     token,
					Preview:   preview,
					Detail:    "Codex · " + thread.Source,
					UpdatedAt: thread.UpdatedAtMs,
					Current:   hasCurrent && thread.ThreadID == current.ThreadID,
				})
			}
			return Response{
				Handled: true,
				Command: "/resume",
				Kind:    ResponseResume,
				Resume:  &ResumeView{CWD: cwd, Entries: entries},
			}, nil
		}
	}
	if hasCurrent && current.ThreadID != "" {
		token := s.opts.ResumeStore.Issue(identity, resumeTarget{threadID: current.ThreadID})
		return Response{
			Handled:  true,
			Command:  "/resume",
			Kind:     ResponseMessage,
			Markdown: fmt.Sprintf("当前 Codex thread 可恢复。\n使用 `/resume use %s` 恢复（10 分钟内有效）。", token),
			Resume:   &ResumeView{CWD: cwd, Entries: []ResumeEntry{{Token: token, Current: true}}},
		}, nil
	}
	return Response{
		Handled:  true,
		Command:  "/resume",
		Kind:     ResponseResume,
		Markdown: "此 cwd 下没有历史会话。",
		Resume:   &ResumeView{CWD: cwd},
	}, nil
}

func (s *Service) listClaudeResume(ctx context.Context, req Request, identity appsession.CatalogIdentity, cwd string, limit int) (Response, error) {
	var sessions []ClaudeSessionSummary
	if s.opts.ClaudeHistory != nil {
		list, err := s.opts.ClaudeHistory.ListClaudeSessions(ctx, cwd, limit)
		if err != nil {
			return Response{}, err
		}
		sessions = list
	}
	current, _ := s.currentSession(req.ScopeID)
	entries := make([]ResumeEntry, 0, len(sessions))
	for _, session := range sessions {
		token := s.opts.ResumeStore.Issue(identity, resumeTarget{sessionID: session.SessionID})
		entries = append(entries, ResumeEntry{
			Token:     token,
			DisplayID: session.SessionID,
			Preview:   session.Preview,
			UpdatedAt: session.MTime,
			Current:   session.SessionID == current,
		})
	}
	return Response{
		Handled: true,
		Command: "/resume",
		Kind:    ResponseResume,
		Resume:  &ResumeView{CWD: cwd, Entries: entries},
	}, nil
}

func (s *Service) applyResume(ctx context.Context, req Request, token string) (Response, error) {
	identity, cwd, ok, userVisible, err := s.catalogIdentity(req)
	if err != nil {
		return Response{}, err
	}
	if !ok {
		if s.opts.Capability.AgentID == capability.IDCodex {
			return message("/resume", "当前上下文没有可恢复的 Codex thread，请先在当前工作区完成一次运行。"), nil
		}
		return message("/resume", userVisible), nil
	}

	if resolved, matched := s.opts.ResumeStore.Consume(token, identity); matched {
		s.interrupt(ctx, req.ScopeID)
		if identity.AgentID == capability.IDCodex {
			if s.opts.SessionCatalog != nil {
				_, err := s.opts.SessionCatalog.UpsertActive(appsession.UpsertCatalogInput{
					CatalogIdentity: identity,
					ThreadID:        resolved.threadID,
				})
				if err != nil {
					return Response{}, err
				}
			}
		} else {
			if s.opts.SessionCatalog != nil {
				_, err := s.opts.SessionCatalog.UpsertActive(appsession.UpsertCatalogInput{
					CatalogIdentity: identity,
					SessionID:       resolved.sessionID,
				})
				if err != nil {
					return Response{}, err
				}
			}
			if s.opts.Sessions != nil {
				s.opts.Sessions.Set(req.ScopeID, resolved.sessionID, cwd)
			}
		}
		return Response{
			Handled:  true,
			Command:  "/resume",
			Kind:     ResponseResume,
			Markdown: resumeAppliedReply,
			Resume:   &ResumeView{CWD: cwd, Applied: true},
		}, nil
	}

	if identity.AgentID == capability.IDCodex {
		return message("/resume", "当前上下文不可恢复这个会话，请先用 `/resume` 重新生成恢复候选。"), nil
	}

	if s.opts.SessionCatalog != nil {
		entry, active := s.opts.SessionCatalog.ActiveFor(identity)
		if !active || entry.SessionID != token {
			return message("/resume", "当前上下文不可恢复这个会话，请重新选择当前工作区和权限策略下的会话。"), nil
		}
		s.interrupt(ctx, req.ScopeID)
		if s.opts.Sessions != nil {
			s.opts.Sessions.Set(req.ScopeID, token, cwd)
		}
		return Response{
			Handled:  true,
			Command:  "/resume",
			Kind:     ResponseResume,
			Markdown: resumeAppliedReply,
			Resume:   &ResumeView{CWD: cwd, Applied: true},
		}, nil
	}

	s.interrupt(ctx, req.ScopeID)
	if s.opts.Sessions != nil {
		s.opts.Sessions.Set(req.ScopeID, token, cwd)
	}
	return Response{
		Handled:  true,
		Command:  "/resume",
		Kind:     ResponseResume,
		Markdown: resumeAppliedReply,
		Resume:   &ResumeView{CWD: cwd, Applied: true},
	}, nil
}

func (s *Service) handleStatus(ctx context.Context, req Request) (Response, error) {
	cwd := s.effectiveCWD(req)
	isCodex := s.opts.Capability.AgentID == capability.IDCodex
	sessionID := ""
	emptySession := ""
	sessionStale := false
	if isCodex {
		emptySession = "(未建立)"
		if identity, _, ok, _, err := s.catalogIdentity(req); err != nil {
			return Response{}, err
		} else if ok && s.opts.SessionCatalog != nil {
			if entry, active := s.opts.SessionCatalog.ActiveFor(identity); active {
				sessionID = entry.ThreadID
			}
		}
	} else if s.opts.Sessions != nil {
		if sess, ok := s.opts.Sessions.GetRaw(req.ScopeID); ok {
			sessionID = sess.SessionID
			sessionStale = cwd != "" && sess.CWD != "" && sess.CWD != cwd
		}
	}

	activeScopes, commentScopes := splitScopes(s.activeScopes())
	status := &StatusView{
		ProfileName:         s.opts.ProfileName,
		CWD:                 cwd,
		SessionID:           sessionID,
		EmptySessionText:    emptySession,
		SessionStale:        sessionStale,
		AgentName:           s.opts.AgentName,
		RuntimeAccess:       runtimeAccessStatus(s.opts.ProfileConfig),
		LarkCLIStatus:       s.larkCLIStatus(ctx, req),
		ActiveRun:           contains(activeScopes, req.ScopeID) || contains(commentScopes, req.ScopeID),
		ActiveScopes:        activeScopes,
		ActiveCommentScopes: commentScopes,
		Queue:               s.poolSnapshot(),
		OwnerState:          s.ownerState(),
		Scope:               req.ScopeID,
		ChatMode:            req.ChatMode,
	}
	return Response{
		Handled:  true,
		Command:  "/status",
		Kind:     ResponseStatus,
		Markdown: statusMarkdown(status),
		Status:   status,
	}, nil
}

func (s *Service) handleStop(ctx context.Context, req Request) (Response, error) {
	targetScope := strings.TrimSpace(req.Args)
	if targetScope != "" && !s.canAdmin(req) {
		return message("/stop", "❌ 指定 scope 停止任务仅管理员可用。"), nil
	}
	scope := targetScope
	if scope == "" {
		scope = req.ScopeID
	}
	interrupted := s.interrupt(ctx, scope)
	view := &StopView{Scope: scope, Targeted: targetScope != "", Interrupted: interrupted}
	if targetScope == "" {
		return Response{
			Handled: true,
			Command: "/stop",
			Kind:    ResponseStop,
			NoReply: true,
			Stop:    view,
		}, nil
	}
	text := fmt.Sprintf("未找到正在运行的任务：`%s`。", scope)
	if interrupted {
		text = fmt.Sprintf("已请求停止 `%s`。", scope)
	}
	return Response{
		Handled:  true,
		Command:  "/stop",
		Kind:     ResponseStop,
		Markdown: text,
		Stop:     view,
	}, nil
}

func (s *Service) handleTimeout(_ context.Context, req Request) (Response, error) {
	parsed := parseTimeoutTarget(strings.ToLower(strings.TrimSpace(req.Args)), req.ScopeID)
	if parsed.targeted && !s.canAdmin(req) {
		return message("/timeout", "❌ 指定 scope 设置 timeout 仅管理员可用。"), nil
	}
	globalMinutes := 0
	if s.opts.GlobalIdleTimeout > 0 {
		globalMinutes = int((s.opts.GlobalIdleTimeout + 30*time.Second) / time.Minute)
	}
	formatGlobal := func() string {
		if globalMinutes > 0 {
			return fmt.Sprintf("%d 分钟", globalMinutes)
		}
		return "未启用"
	}
	scopeLabel := ""
	if parsed.targeted {
		scopeLabel = " (" + parsed.scope + ")"
	}
	view := &TimeoutView{Scope: parsed.scope, Targeted: parsed.targeted, GlobalMinutes: globalMinutes}

	if parsed.value == "" {
		if s.opts.Sessions != nil {
			if minutes, ok := s.opts.Sessions.GetIdleTimeoutMinutes(parsed.scope); ok {
				view.HasOverride = true
				view.ValueMinutes = &minutes
				if minutes > 0 {
					view.EffectiveText = fmt.Sprintf("%d 分钟", minutes)
				} else {
					view.EffectiveText = "已关闭（当前 session）"
					view.DisabledForSession = true
				}
				return timeoutResponse(fmt.Sprintf("⏱ 当前 session%s 探活:%s\n全局默认:%s%s", scopeLabel, view.EffectiveText, formatGlobal(), timeoutUsage()), view), nil
			}
		}
		view.FollowGlobal = true
		return timeoutResponse(fmt.Sprintf("⏱ 当前 session%s 探活:跟随全局(%s)%s", scopeLabel, formatGlobal(), timeoutUsage()), view), nil
	}

	if parsed.value == "default" {
		cleared := false
		if s.opts.Sessions != nil {
			cleared = s.opts.Sessions.ClearIdleTimeoutOverride(parsed.scope)
		}
		view.Cleared = cleared
		if cleared {
			return timeoutResponse(fmt.Sprintf("✅ 已清除 session 覆盖,回退到全局(%s)。", formatGlobal()), view), nil
		}
		return timeoutResponse(fmt.Sprintf("当前 session 本来就没设过覆盖,跟随全局(%s)。", formatGlobal()), view), nil
	}

	if parsed.value == "off" || parsed.value == "0" {
		minutes := 0
		if s.opts.Sessions != nil {
			s.opts.Sessions.SetIdleTimeoutMinutes(parsed.scope, minutes)
		}
		view.HasOverride = true
		view.ValueMinutes = &minutes
		view.DisabledForSession = true
		return timeoutResponse("✅ 已关闭当前 session 的探活。", view), nil
	}

	minutes, err := strconv.Atoi(parsed.value)
	if err != nil || minutes < 1 || minutes > 120 {
		view.Invalid = true
		return timeoutResponse("❌ 用法:`/timeout <1-120>` / `/timeout off` / `/timeout default`", view), nil
	}
	if s.opts.Sessions != nil {
		s.opts.Sessions.SetIdleTimeoutMinutes(parsed.scope, minutes)
	}
	view.HasOverride = true
	view.ValueMinutes = &minutes
	return timeoutResponse(fmt.Sprintf("✅ 当前 session 探活已设为 %d 分钟。", minutes), view), nil
}

func (s *Service) handlePS(req Request) Response {
	var processes []ProcessEntry
	if s.opts.Processes != nil {
		processes = s.opts.Processes.ListProcesses()
	}
	for i := range processes {
		processes[i].Current = processes[i].ID != "" && processes[i].ID == s.opts.ProcessID
	}
	sort.Slice(processes, func(i, j int) bool {
		return processes[i].StartedAt.Before(processes[j].StartedAt)
	})
	if len(processes) == 0 {
		return Response{
			Handled:  true,
			Command:  "/ps",
			Kind:     ResponsePS,
			Markdown: "当前没有 bot 在运行(理论上不可能,你正在跟其中之一对话…)",
			PS:       &PSView{CurrentID: s.opts.ProcessID},
		}
	}
	rows := []string{"| # | ID | Bot | 启动 |", "|---|---|---|---|"}
	now := s.opts.Now()
	for i, proc := range processes {
		me := ""
		if proc.Current {
			me = " ← 当前正在回复"
		}
		bot := "`" + proc.AppID + "`"
		if proc.BotName != "" {
			bot = proc.BotName + " (`" + proc.AppID + "`)"
		}
		rows = append(rows, fmt.Sprintf("| %d | `%s`%s | %s | %s |", i+1, proc.ID, me, bot, formatAgo(now.Sub(proc.StartedAt))))
	}
	body := fmt.Sprintf("🧭 **当前有 %d 个 bot 在运行**\n\n%s\n\n用 `/exit <id|#>` 关掉某一个;`/exit %s` 关掉正在回复你的这个 bot。", len(processes), strings.Join(rows, "\n"), s.opts.ProcessID)
	return Response{
		Handled:  true,
		Command:  req.Command,
		Kind:     ResponsePS,
		Markdown: body,
		PS:       &PSView{Processes: processes, CurrentID: s.opts.ProcessID},
	}
}

func (s *Service) handleExit(ctx context.Context, req Request) Response {
	target := strings.TrimSpace(req.Args)
	view := &ExitView{Target: target, CurrentID: s.opts.ProcessID}
	if target == "" {
		view.Unsupported = false
		return exitResponse("用法:`/exit <id|#>` —— `id` 是 `/ps` 显示的短 id,`#` 是序号。\n"+
			fmt.Sprintf("当前正在回复你的是 `%s`。", s.opts.ProcessID), view)
	}
	entry, rank, ok := s.resolveProcessTarget(target)
	if !ok {
		return exitResponse(fmt.Sprintf("❌ 没找到匹配的 bot:`%s`。发 `/ps` 看可选目标。", target), view)
	}
	view.Found = true
	view.ResolvedID = entry.ID
	view.ResolvedPID = entry.PID
	view.ResolvedRank = rank
	view.Self = entry.ID != "" && entry.ID == s.opts.ProcessID
	if s.opts.ProcessController == nil {
		view.Unsupported = true
		return exitResponse(fmt.Sprintf("Go SDK 已解析 bot `%s`,但未配置 ProcessController,未实际关闭。", entry.ID), view)
	}
	if view.Self {
		if err := s.opts.ProcessController.ExitSelf(ctx); err != nil {
			view.Failure = err.Error()
			return exitResponse(fmt.Sprintf("❌ 关闭当前 bot `%s` 失败:%s", entry.ID, err.Error()), view)
		}
		view.Terminated = true
		return exitResponse(fmt.Sprintf("👋 即将关闭当前 bot `%s`,再见。", entry.ID), view)
	}
	stillAlive, err := s.opts.ProcessController.Terminate(ctx, entry)
	if err != nil {
		view.Failure = err.Error()
		return exitResponse(fmt.Sprintf("❌ 关掉 bot `%s` 失败:%s", entry.ID, err.Error()), view)
	}
	view.StillAlive = stillAlive
	view.Terminated = !stillAlive
	if stillAlive {
		return exitResponse(fmt.Sprintf("📨 已请求关闭 `%s`,但还在收尾。再发 `/ps` 复查一下。", entry.ID), view)
	}
	return exitResponse(fmt.Sprintf("✓ 已关闭 bot `%s`。", entry.ID), view)
}

func (s *Service) handleReconnect(ctx context.Context, req Request) Response {
	wait := contains(strings.Fields(req.Args), "--wait")
	view := &ReconnectView{Wait: wait, ReconnectorActive: s.opts.Reconnector != nil}
	lifecycle, hasLifecycle := s.opts.Executor.(runLifecycleController)
	view.LifecycleManaged = hasLifecycle
	var resume func()
	if hasLifecycle {
		resume = lifecycle.PauseNewRuns("reconnect-in-progress")
		view.PausedNewRuns = true
		defer resume()
		if wait {
			if err := lifecycle.WaitForAll(ctx); err != nil {
				view.Failure = err.Error()
				return reconnectResponse(fmt.Sprintf("❌ 重连失败:%s", err.Error()), view)
			}
			view.WaitedForRuns = true
		} else {
			if err := lifecycle.StopAll(ctx); err != nil {
				view.Failure = err.Error()
				return reconnectResponse(fmt.Sprintf("❌ 重连失败:%s", err.Error()), view)
			}
			view.StoppedScopes = s.activeScopes()
		}
	} else if !wait {
		scopes := s.activeScopes()
		for _, scope := range scopes {
			if s.interrupt(ctx, scope) {
				view.StoppedScopes = append(view.StoppedScopes, scope)
			}
		}
	}
	ack := "⏳ 正在停止当前运行并重连…"
	if wait {
		ack = "⏳ 将在当前运行结束后重连…"
	}
	if s.opts.Reconnector == nil {
		view.Unsupported = true
		return reconnectResponse(ack+"\n\nGo SDK 未配置 reconnect 回调,未实际重连。", view)
	}
	if err := s.opts.Reconnector.Restart(ctx, wait); err != nil {
		view.Failure = err.Error()
		return reconnectResponse(fmt.Sprintf("❌ 重连失败:%s", err.Error()), view)
	}
	view.Restarted = true
	return reconnectResponse(ack, view)
}

const doctorEchoPrompt = "Bridge doctor agent echo check. Do not inspect files, do not use history, and reply exactly: OK"

func (s *Service) handleDoctor(ctx context.Context, req Request) (Response, error) {
	requestedCWD := s.effectiveCWD(req)
	if strings.TrimSpace(requestedCWD) == "" {
		view := s.buildDoctorView(req, "", "未设置工作目录。先用 `/cd <path>` 或 `/ws use <name>` 选择工作目录后再运行 agent echo check。", "", "skipped")
		return doctorResponse(view), nil
	}
	resolved := workspace.ResolveWorkingDirectory(requestedCWD)
	if !resolved.OK {
		view := s.buildDoctorView(req, requestedCWD, resolved.UserVisible+" 工作目录不可用时只执行 self-check,不启动 agent。", "", "skipped")
		return doctorResponse(view), nil
	}
	policy, err := runpolicy.Evaluate(runpolicy.Input{
		Scope: runpolicy.ScopeContext{
			Source:   runpolicy.SourceIM,
			ChatID:   req.ChatID,
			ThreadID: req.ThreadID,
			ActorID:  req.SenderID,
		},
		Prompt:        doctorEchoPrompt,
		RequestedCWD:  requestedCWD,
		CWDRealpath:   resolved.CWDRealpath,
		Access:        access.CanRunAdminCommand(s.opts.ProfileConfig, s.opts.RuntimeControls, req.SenderID),
		Capability:    s.opts.Capability,
		ProfileConfig: s.opts.ProfileConfig,
		Now:           s.opts.Now(),
		TTL:           time.Minute,
	})
	if err != nil {
		return Response{}, err
	}
	if !policy.OK {
		view := s.buildDoctorView(req, resolved.CWDRealpath, "ok ("+resolved.CWDRealpath+")", "", policy.RejectReason.UserVisible)
		return doctorResponse(view), nil
	}
	runtimeAccess := runtimeAccessStatus(s.opts.ProfileConfig)
	policyLine := "ok " + runtimeAccess.Label + "=" + runtimeAccess.Value
	if submitter, ok := s.opts.Executor.(doctorSubmitter); ok {
		echo := s.runDoctorEcho(ctx, submitter, req, policy.Allow)
		view := s.buildDoctorView(req, resolved.CWDRealpath, "ok ("+resolved.CWDRealpath+")", policyLine, echo)
		return doctorResponse(view), nil
	}
	view := s.buildDoctorView(req, resolved.CWDRealpath, "ok ("+resolved.CWDRealpath+")", policyLine, "run executor unavailable")
	return doctorResponse(view), nil
}

func (s *Service) runDoctorEcho(ctx context.Context, submitter doctorSubmitter, req Request, allow runpolicy.Allow) string {
	execution, err := submitter.Submit(ctx, runexecutor.SubmitRunInput{
		ScopeID: req.ScopeID + ":doctor",
		Policy: runexecutor.RunPolicy{
			Prompt:         doctorEchoPrompt,
			CWDRealpath:    allow.CWDRealpath,
			AccessMode:     allow.AccessMode,
			Sandbox:        allow.Sandbox,
			PermissionMode: allow.PermissionMode,
			ExpiresAt:      allow.ExpiresAt,
		},
		Nowait: true,
		Observability: runexecutor.Observability{
			Profile: s.opts.ProfileName,
			Agent:   string(s.opts.Capability.AgentID),
			Source:  "command",
			Stage:   "agent-probe",
		},
	})
	if err != nil {
		var rejected *runexecutor.RunRejected
		if errors.As(err, &rejected) && rejected.Code == runexecutor.RunRejectedPoolFull {
			return "pool-full"
		}
		return "failed"
	}
	echo := ""
	for event := range execution.Subscribe(ctx) {
		switch string(event.Type) {
		case "text":
			if event.Delta != nil {
				echo += *event.Delta
			}
		case "done":
			trimmed := strings.TrimSpace(echo)
			if trimmed == "" {
				return "empty"
			}
			if len(trimmed) > 80 {
				return trimmed[:80] + "..."
			}
			return trimmed
		case "error":
			if event.Message != nil && *event.Message != "" {
				return *event.Message
			}
			return "error"
		}
	}
	trimmed := strings.TrimSpace(echo)
	if trimmed == "" {
		return "empty"
	}
	return trimmed
}

func (s *Service) handleConfig(ctx context.Context, req Request) (Response, error) {
	sub := firstField(req.Args)
	switch sub {
	case "":
		view, err := s.configSnapshotView(ctx, "form")
		if err != nil {
			return Response{}, err
		}
		return configResponse("当前配置快照已生成。", view), nil
	case "submit":
		view, err := s.submitConfig(ctx, req)
		if err != nil {
			return Response{}, err
		}
		if view.Unsupported {
			return configResponse("Go SDK /config submit 需要 ConfigPath 才能写入配置。", view), nil
		}
		if view.Failure != "" {
			return configResponse("❌ 保存失败:"+view.Failure, view), nil
		}
		return configResponse("✅ 偏好已保存。", view), nil
	case "cancel":
		view, err := s.configSnapshotView(ctx, "cancel")
		if err != nil {
			return Response{}, err
		}
		return Response{Handled: true, Command: "/config", Kind: ResponseConfig, NoReply: true, Config: view}, nil
	default:
		return message("/config", "用法:`/config`"), nil
	}
}

func (s *Service) submitConfig(ctx context.Context, req Request) (*ConfigView, error) {
	view, err := s.configSnapshotView(ctx, "submit")
	if err != nil {
		return nil, err
	}
	if s.opts.ConfigPath == "" {
		view.Unsupported = true
		return view, nil
	}
	currentPrefs := map[string]any{
		"messageReply":          view.Snapshot.MessageReply,
		"showToolCalls":         view.Snapshot.ShowToolCalls,
		"cotMessages":           view.Snapshot.CotMessages,
		"maxConcurrentRuns":     view.Snapshot.MaxConcurrentRuns,
		"runIdleTimeoutMinutes": view.Snapshot.RunIdleTimeoutMinutes,
	}
	messageReply := parseMessageReply(formString(req.FormValue, "message_reply", "messageReply"), getMessageReply(currentPrefs))
	showToolCalls := parseShowToolCalls(formString(req.FormValue, "show_tool_calls", "showToolCalls"), getShowToolCalls(currentPrefs))
	cotMessages := parseCotMessages(formString(req.FormValue, "cot_messages", "cotMessages"), getCotMessages(currentPrefs))
	maxConcurrentRuns := parseClampedInt(formString(req.FormValue, "max_concurrent_runs", "maxConcurrentRuns"), getMaxConcurrentRuns(currentPrefs), 1, 50, false)
	runIdleTimeoutMinutes := parseClampedInt(formString(req.FormValue, "run_idle_timeout_minutes", "runIdleTimeoutMinutes"), getRunIdleTimeoutMinutes(currentPrefs), 0, 120, true)
	requireMention := parseRequireMention(formString(req.FormValue, "require_mention_in_group", "requireMentionInGroup"), view.Snapshot.RequireMentionInGroup)
	previousLarkIdentity := configstore.LarkCliIdentityPreset(view.Snapshot.LarkCLIIdentity)
	larkIdentity := parseLarkCLIIdentity(formString(req.FormValue, "lark_cli_identity", "larkCliIdentity"), previousLarkIdentity)
	identityChanged := larkIdentity != previousLarkIdentity
	identityApplied := false
	if identityChanged && s.opts.LarkCLIIdentity != nil {
		if !s.opts.LarkCLIIdentity.ApplyLarkCLIIdentityPolicy(ctx, string(larkIdentity)) {
			view.Failure = "lark-cli identity policy apply failed"
			return view, nil
		}
		identityApplied = true
	}
	if err := s.mutateRootProfile(func(root *configstore.RootConfig, prof *configstore.ProfileConfig) error {
		prefs := copyAnyMap(prof.Preferences)
		prefs["messageReply"] = messageReply
		prefs["messageReplyMigrated"] = true
		prefs["showToolCalls"] = showToolCalls
		prefs["cotMessages"] = cotMessages
		prefs["maxConcurrentRuns"] = maxConcurrentRuns
		prefs["runIdleTimeoutMinutes"] = runIdleTimeoutMinutes
		prof.Preferences = prefs
		prof.Access.RequireMentionInGroup = requireMention
		prof.LarkCli = configstore.LarkCliConfig{
			IdentityPreset: larkIdentity,
			LocalUserImport: &configstore.LarkCliLocalUserImport{
				Status:      configstore.LarkCliUserImportNotNeeded,
				AttemptedAt: s.opts.Now().UTC().Format(time.RFC3339),
				Reason:      larkCLIManualReason(larkIdentity),
			},
		}
		_ = root
		return nil
	}); err != nil {
		if identityApplied {
			_ = s.opts.LarkCLIIdentity.ApplyLarkCLIIdentityPolicy(ctx, string(previousLarkIdentity))
		}
		view.Failure = err.Error()
		return view, nil
	}
	view, err = s.configSnapshotView(ctx, "submit")
	if err != nil {
		return nil, err
	}
	view.Saved = true
	return view, nil
}

func (s *Service) handleAccount(ctx context.Context, req Request) (Response, error) {
	sub := firstField(req.Args)
	switch sub {
	case "":
		view, err := s.accountCurrentView(ctx)
		if err != nil {
			return Response{}, err
		}
		return accountResponse(fmt.Sprintf("当前 App:`%s` tenant:`%s` secret:已隐藏", emptyAs(view.AppID, "(unknown)"), emptyAs(view.Tenant, "(unknown)")), view), nil
	case "change":
		view, err := s.accountCurrentView(ctx)
		if err != nil {
			return Response{}, err
		}
		view.Action = "form"
		return accountResponse("请提交新的 App ID / App Secret。", view), nil
	case "submit":
		view, err := s.submitAccount(ctx, req)
		if err != nil {
			return Response{}, err
		}
		if view.Unsupported {
			return accountResponse("Go SDK /account submit 需要 ConfigPath 和 Keystore 才能保存凭据。", view), nil
		}
		if view.Failure != "" {
			return accountResponse("❌ "+view.Failure, view), nil
		}
		return accountResponse(fmt.Sprintf("✅ App 凭据已保存:`%s`。", view.AppID), view), nil
	case "cancel":
		return Response{Handled: true, Command: "/account", Kind: ResponseAccount, NoReply: true, Account: &AccountView{Action: "cancel"}}, nil
	default:
		return message("/account", "用法：`/account` 或 `/account change`"), nil
	}
}

func (s *Service) submitAccount(ctx context.Context, req Request) (*AccountView, error) {
	appID := strings.TrimSpace(formString(req.FormValue, "app_id", "appId"))
	appSecret := strings.TrimSpace(formString(req.FormValue, "app_secret", "appSecret"))
	tenant := strings.TrimSpace(formString(req.FormValue, "tenant"))
	if tenant != "lark" {
		tenant = "feishu"
	}
	view := &AccountView{Action: "submit", AppID: appID, Tenant: tenant, SecretRedacted: true}
	if appID == "" || appSecret == "" {
		view.Failure = "App ID 或 App Secret 为空"
		return view, nil
	}
	if s.opts.ConfigPath == "" || s.opts.Keystore == nil {
		view.Unsupported = true
		return view, nil
	}
	if s.opts.AccountValidator != nil {
		result, err := s.opts.AccountValidator.ValidateAppCredentials(ctx, appID, appSecret, tenant)
		if err != nil {
			view.Failure = err.Error()
			return view, nil
		}
		if !result.OK {
			view.Failure = emptyAs(result.Reason, "App 凭据校验失败")
			return view, nil
		}
		view.BotName = result.BotName
	} else {
		view.ValidationSkipped = true
	}
	secretID := secretstore.SecretKeyForApp(appID)
	if err := s.opts.Keystore.SetSecret(secretID, appSecret); err != nil {
		view.Failure = "保存凭据失败：" + err.Error()
		return view, nil
	}
	if err := s.mutateRootProfile(func(root *configstore.RootConfig, prof *configstore.ProfileConfig) error {
		prof.Accounts.App.ID = appID
		prof.Accounts.App.Tenant = larkcli.TenantBrand(tenant)
		prof.Accounts.App.Secret = larkcli.SecretRef{Source: "exec", Provider: "bridge", ID: secretID}
		if root.Secrets == nil {
			root.Secrets = &larkcli.SecretsConfig{}
		}
		if root.Secrets.Providers == nil {
			root.Secrets.Providers = map[string]larkcli.ProviderConfig{}
		}
		root.Secrets.Providers["bridge"] = larkcli.ProviderConfig{
			"source":  "exec",
			"command": s.opts.Keystore.Paths().SecretsGetterScript,
			"args":    []string{},
		}
		return nil
	}); err != nil {
		view.Failure = "保存配置失败：" + err.Error()
		return view, nil
	}
	view.Saved = true
	return view, nil
}

func (s *Service) handleInvite(ctx context.Context, req Request) (Response, error) {
	_ = ctx
	tokens := lowerFields(req.Args)
	view := &AccessView{Action: "invite"}
	if contains(tokens, "all") && contains(tokens, "group") {
		added := 0
		accessAfter, err := s.saveAccess(func(current profile.Access) profile.Access {
			for _, chat := range s.opts.KnownChats {
				if chat.ID == "" {
					continue
				}
				if !contains(current.AllowedChats, chat.ID) {
					current.AllowedChats = append(current.AllowedChats, chat.ID)
					added++
				}
			}
			return current
		})
		if err != nil {
			return Response{}, err
		}
		view.Kind = "all group"
		view.AddedGroups = added
		view.TotalGroups = len(accessAfter.AllowedChats)
		fillAccessView(view, accessAfter)
		if len(s.opts.KnownChats) == 0 {
			return accessResponse("当前 bot 还不在任何群里，没有可加入的群。", view), nil
		}
		return accessResponse(fmt.Sprintf("✅ 已把 bot 所在的 %d 个群加入响应群名单（共 %d 个）。", added, len(accessAfter.AllowedChats)), view), nil
	}
	kind := accessKind(tokens)
	view.Kind = kind
	if kind == "" {
		return accessResponse("用法：\n• `/invite user @某人` — 加入允许私聊\n• `/invite admin @某人` — 加入管理员\n• `/invite group` — 把当前群加入响应群名单\n• `/invite all group` — 把 bot 所在的所有群一键加入", view), nil
	}
	if kind == "group" {
		if req.ChatMode == ChatModeP2P {
			return accessResponse("❌ `/invite group` 只能在群里发，在私聊里没有 chat_id 可以加。", view), nil
		}
		already := false
		accessAfter, err := s.saveAccess(func(current profile.Access) profile.Access {
			already = contains(current.AllowedChats, req.ChatID)
			if !already {
				current.AllowedChats = append(current.AllowedChats, req.ChatID)
				view.Added = []string{req.ChatID}
			} else {
				view.Already = []string{req.ChatID}
			}
			return current
		})
		if err != nil {
			return Response{}, err
		}
		fillAccessView(view, accessAfter)
		if already {
			return accessResponse("✅ 当前群已在白名单里，无需重复添加。", view), nil
		}
		return accessResponse(fmt.Sprintf("✅ 已把当前群（`%s`）加入响应群名单。", req.ChatID), view), nil
	}
	targets := mentionTargets(req)
	if len(targets) == 0 {
		return accessResponse(fmt.Sprintf("❌ 没检测到 @ 的用户。请像这样发：`/invite %s @某人`（注意 @ 用户不是 @ bot）。", kind), view), nil
	}
	accessAfter, err := s.saveAccess(func(current profile.Access) profile.Access {
		for _, target := range targets {
			list := &current.AllowedUsers
			if kind == "admin" {
				list = &current.Admins
			}
			if contains(*list, target.OpenID) {
				view.Already = append(view.Already, mentionLabel(target))
			} else {
				*list = append(*list, target.OpenID)
				view.Added = append(view.Added, mentionLabel(target))
			}
		}
		return current
	})
	if err != nil {
		return Response{}, err
	}
	fillAccessView(view, accessAfter)
	label := "用户白名单"
	if kind == "admin" {
		label = "管理员"
	}
	parts := []string{}
	if len(view.Added) > 0 {
		parts = append(parts, fmt.Sprintf("✅ 已把 %s 加入%s。", strings.Join(view.Added, "、"), label))
	}
	if len(view.Already) > 0 {
		parts = append(parts, fmt.Sprintf("_%s 已经在%s里，跳过。_", strings.Join(view.Already, "、"), label))
	}
	return accessResponse(strings.Join(parts, "\n"), view), nil
}

func (s *Service) handleRemove(ctx context.Context, req Request) (Response, error) {
	_ = ctx
	tokens := lowerFields(req.Args)
	kind := accessKind(tokens)
	view := &AccessView{Action: "remove", Kind: kind}
	if kind == "" {
		return accessResponse("用法：\n• `/remove user @某人` — 移出用户白名单\n• `/remove admin @某人` — 移出管理员\n• `/remove group` — 把当前群移出响应群名单", view), nil
	}
	if kind == "group" {
		if req.ChatMode == ChatModeP2P {
			return accessResponse("`/remove group` 请在要移除的群里发，私聊里没有可移除的群。", view), nil
		}
		missing := false
		accessAfter, err := s.saveAccess(func(current profile.Access) profile.Access {
			next, removed := removeString(current.AllowedChats, req.ChatID)
			current.AllowedChats = next
			missing = !removed
			if removed {
				view.Removed = []string{req.ChatID}
			} else {
				view.Missing = []string{req.ChatID}
			}
			return current
		})
		if err != nil {
			return Response{}, err
		}
		fillAccessView(view, accessAfter)
		if missing {
			return accessResponse("✅ 当前群本来就不在响应名单里，无需移除。", view), nil
		}
		return accessResponse("✅ 已把当前群移出响应群名单。", view), nil
	}
	targets := mentionTargets(req)
	if len(targets) == 0 {
		return accessResponse(fmt.Sprintf("请 @ 上要移除的人，例如：`/remove %s @某人`。", kind), view), nil
	}
	accessAfter, err := s.saveAccess(func(current profile.Access) profile.Access {
		for _, target := range targets {
			list := &current.AllowedUsers
			if kind == "admin" {
				list = &current.Admins
			}
			next, removed := removeString(*list, target.OpenID)
			*list = next
			if removed {
				view.Removed = append(view.Removed, mentionLabel(target))
			} else {
				view.Missing = append(view.Missing, mentionLabel(target))
			}
		}
		return current
	})
	if err != nil {
		return Response{}, err
	}
	fillAccessView(view, accessAfter)
	label := "用户白名单"
	if kind == "admin" {
		label = "管理员"
	}
	parts := []string{}
	if len(view.Removed) > 0 {
		parts = append(parts, fmt.Sprintf("✅ 已把 %s 移出%s。", strings.Join(view.Removed, "、"), label))
	}
	if len(view.Missing) > 0 {
		parts = append(parts, fmt.Sprintf("%s 本来就不在%s里，无需移除。", strings.Join(view.Missing, "、"), label))
	}
	return accessResponse(strings.Join(parts, "\n"), view), nil
}

func (s *Service) handleDoc(req Request) Response {
	return Response{
		Handled:  true,
		Command:  req.Command,
		Kind:     ResponseDoc,
		Markdown: "云文档评论现在不需要绑定工作区；在支持的文档评论里 @bot 即可触发回复。",
		Doc:      &DocView{NoOp: true},
	}
}

func (s *Service) resolveProcessTarget(target string) (ProcessEntry, int, bool) {
	if s.opts.Processes == nil {
		return ProcessEntry{}, 0, false
	}
	processes := s.opts.Processes.ListProcesses()
	sort.Slice(processes, func(i, j int) bool {
		return processes[i].StartedAt.Before(processes[j].StartedAt)
	})
	indexText := strings.TrimPrefix(target, "#")
	if index, err := strconv.Atoi(indexText); err == nil && index > 0 && index <= len(processes) {
		return processes[index-1], index, true
	}
	var match ProcessEntry
	matches := 0
	rank := 0
	for i, entry := range processes {
		if entry.ID == target || (target != "" && strings.HasPrefix(entry.ID, target)) {
			match = entry
			rank = i + 1
			matches++
		}
	}
	return match, rank, matches == 1
}

func (s *Service) buildDoctorView(req Request, cwd string, workspaceCheck string, policyCheck string, echoCheck string) *DoctorView {
	accessDecision := s.accessDecision(req)
	view := &DoctorView{
		ProfileName:    s.opts.ProfileName,
		AgentName:      s.opts.AgentName,
		AgentKind:      s.opts.ProfileConfig.AgentKind,
		CWD:            cwd,
		WorkspaceCheck: workspaceCheck,
		PolicyCheck:    policyCheck,
		EchoCheck:      echoCheck,
		Queue:          s.poolSnapshot(),
		Access:         accessDecision,
		RuntimeAccess:  runtimeAccessStatus(s.opts.ProfileConfig),
		OwnerState:     s.ownerState(),
		RunExecutor:    s.opts.Executor != nil,
		Redacted:       true,
	}
	view.Report = s.doctorReport(view)
	return view
}

func (s *Service) doctorReport(view *DoctorView) string {
	queue := "unknown"
	if view.Queue.Cap > 0 || view.Queue.Active > 0 || view.Queue.Waiting > 0 {
		queue = fmt.Sprintf("%d/%d active, %d waiting", view.Queue.Active, view.Queue.Cap, view.Queue.Waiting)
	}
	defaultWorkspace := "missing"
	if s.opts.ProfileConfig.Workspaces.Default != "" {
		defaultWorkspace = "set"
	}
	workspaceLine := view.CWD
	if workspaceLine == "" {
		workspaceLine = "(未设置)"
	}
	lines := []string{
		"self-check: ok",
		"profile: " + view.ProfileName,
		fmt.Sprintf("agent: %s (%s)", view.AgentName, view.AgentKind),
		"workspace: " + workspaceLine,
		"workspace default: " + defaultWorkspace,
		view.RuntimeAccess.Label + ": " + view.RuntimeAccess.Value,
		fmt.Sprintf("access: %s (%s)", okDenied(view.Access.OK), view.Access.Reason),
		"owner API: " + view.OwnerState,
		"queue: " + queue,
		"run executor: " + okUnavailable(view.RunExecutor),
	}
	if view.WorkspaceCheck != "" {
		lines = append(lines, "workspace check: "+view.WorkspaceCheck)
	}
	if view.PolicyCheck != "" {
		lines = append(lines, "policy check: "+view.PolicyCheck)
	}
	if view.EchoCheck != "" {
		lines = append(lines, "agent echo check: "+view.EchoCheck)
	}
	return strings.Join(lines, "\n")
}

func (s *Service) configSnapshotView(ctx context.Context, action string) (*ConfigView, error) {
	view := &ConfigView{
		Action:      action,
		ProfileName: s.opts.ProfileName,
		Snapshot:    configSnapshotFromDomain(s.opts.ProfileConfig),
	}
	if s.opts.ConfigPath == "" {
		return view, nil
	}
	snapshot, err := configstore.Load(s.opts.ConfigPath, s.configLoadOptions())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return view, nil
		}
		return nil, err
	}
	_ = ctx
	s.applyConfigSnapshot(snapshot.Profile)
	view.ProfileName = snapshot.ProfileName
	view.Snapshot = configSnapshotFromStore(snapshot.Profile)
	return view, nil
}

func (s *Service) accountCurrentView(ctx context.Context) (*AccountView, error) {
	view := &AccountView{Action: "current", SecretRedacted: true}
	if s.opts.ConfigPath == "" {
		view.Unsupported = true
		return view, nil
	}
	snapshot, err := configstore.Load(s.opts.ConfigPath, s.configLoadOptions())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			view.Unsupported = true
			return view, nil
		}
		return nil, err
	}
	_ = ctx
	s.applyConfigSnapshot(snapshot.Profile)
	view.AppID = snapshot.Profile.Accounts.App.ID
	view.Tenant = string(snapshot.Profile.Accounts.App.Tenant)
	return view, nil
}

var configPathLocks sync.Map

func (s *Service) mutateRootProfile(mutate func(root *configstore.RootConfig, prof *configstore.ProfileConfig) error) error {
	if s.opts.ConfigPath == "" {
		return errors.New("config path is required")
	}
	value, _ := configPathLocks.LoadOrStore(s.opts.ConfigPath, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()

	snapshot, err := configstore.Load(s.opts.ConfigPath, s.configLoadOptions())
	if err != nil {
		return err
	}
	root := snapshot.Root
	prof, ok := root.Profiles[snapshot.ProfileName]
	if !ok {
		return fmt.Errorf("profile not found: %s", snapshot.ProfileName)
	}
	if err := mutate(&root, &prof); err != nil {
		return err
	}
	root.Profiles[snapshot.ProfileName] = prof
	if err := writeRootConfig(s.opts.ConfigPath, root); err != nil {
		return err
	}
	refreshed, err := configstore.Load(s.opts.ConfigPath, s.configLoadOptions())
	if err != nil {
		return err
	}
	s.applyConfigSnapshot(refreshed.Profile)
	return nil
}

func writeRootConfig(path string, root configstore.RootConfig) error {
	return configstore.SaveRoot(path, root)
}

func (s *Service) saveAccess(mutate func(profile.Access) profile.Access) (profile.Access, error) {
	if s.opts.ConfigPath == "" {
		next := mutate(s.opts.ProfileConfig.Access)
		s.opts.ProfileConfig.Access = next
		return next, nil
	}
	var out profile.Access
	err := s.mutateRootProfile(func(_ *configstore.RootConfig, prof *configstore.ProfileConfig) error {
		current := accessFromStore(prof.Access)
		next := mutate(current)
		prof.Access = accessToStore(next)
		out = next
		return nil
	})
	if err != nil {
		return profile.Access{}, err
	}
	if out.AllowedUsers == nil && out.AllowedChats == nil && out.Admins == nil {
		out = s.opts.ProfileConfig.Access
	}
	return out, nil
}

func (s *Service) configLoadOptions() configstore.LoadOptions {
	opts := s.opts.ConfigLoadOptions
	if opts.Profile == "" {
		opts.Profile = s.opts.ProfileName
	}
	if opts.AgentKind == "" {
		if s.opts.ProfileConfig.AgentKind == profile.AgentCodex {
			opts.AgentKind = configstore.AgentCodex
		} else {
			opts.AgentKind = configstore.AgentClaude
		}
	}
	if opts.Codex == nil && s.opts.ProfileConfig.Codex != nil {
		opts.Codex = &configstore.CodexConfig{
			BinaryPath:       s.opts.ProfileConfig.Codex.BinaryPath,
			Realpath:         s.opts.ProfileConfig.Codex.Realpath,
			Version:          s.opts.ProfileConfig.Codex.Version,
			SHA256:           s.opts.ProfileConfig.Codex.SHA256,
			Owner:            s.opts.ProfileConfig.Codex.Owner,
			Mode:             s.opts.ProfileConfig.Codex.Mode,
			CodexHome:        s.opts.ProfileConfig.Codex.CodexHome,
			InheritCodexHome: s.opts.ProfileConfig.Codex.InheritCodexHome,
			IgnoreUserConfig: s.opts.ProfileConfig.Codex.IgnoreUserConfig,
			IgnoreRules:      s.opts.ProfileConfig.Codex.IgnoreRules,
		}
	}
	return opts
}

func (s *Service) applyConfigSnapshot(prof configstore.ProfileConfig) {
	s.opts.ProfileConfig = profile.Config{
		SchemaVersion:    prof.SchemaVersion,
		AgentKind:        profile.AgentKind(prof.AgentKind),
		Access:           accessFromStore(prof.Access),
		Workspaces:       profile.Workspaces{Default: prof.Workspaces.Default},
		Permissions:      prof.Permissions,
		PermissionSource: prof.PermissionSource,
		Codex:            codexFromStore(prof.Codex),
		Attachments: profile.AttachmentConfig{
			MaxCount:      prof.Attachments.MaxCount,
			MaxBytes:      prof.Attachments.MaxBytes,
			MaxFileBytes:  prof.Attachments.MaxFileBytes,
			ImageMaxBytes: prof.Attachments.ImageMaxBytes,
			CacheTTLMS:    prof.Attachments.CacheTTLMS,
			CacheMaxBytes: prof.Attachments.CacheMaxBytes,
		},
		LarkCli: profile.LarkCliConfig{IdentityPreset: profile.LarkCliIdentityPreset(prof.LarkCli.IdentityPreset)},
	}
}

func configSnapshotFromDomain(cfg profile.Config) ConfigSnapshotView {
	return ConfigSnapshotView{
		AgentKind:             cfg.AgentKind,
		DefaultWorkspace:      cfg.Workspaces.Default,
		RuntimeAccess:         runtimeAccessStatus(cfg),
		MessageReply:          "markdown",
		ShowToolCalls:         true,
		CotMessages:           "off",
		MaxConcurrentRuns:     10,
		RunIdleTimeoutMinutes: 0,
		RequireMentionInGroup: cfg.Access.RequireMentionInGroup,
		LarkCLIIdentity:       string(cfg.LarkCli.IdentityPreset),
		AllowedUsersCount:     len(cfg.Access.AllowedUsers),
		AllowedChatsCount:     len(cfg.Access.AllowedChats),
		AdminsCount:           len(cfg.Access.Admins),
	}
}

func configSnapshotFromStore(prof configstore.ProfileConfig) ConfigSnapshotView {
	domain := profile.Config{
		AgentKind:   profile.AgentKind(prof.AgentKind),
		Access:      accessFromStore(prof.Access),
		Workspaces:  profile.Workspaces{Default: prof.Workspaces.Default},
		Permissions: prof.Permissions,
		LarkCli:     profile.LarkCliConfig{IdentityPreset: profile.LarkCliIdentityPreset(prof.LarkCli.IdentityPreset)},
	}
	return ConfigSnapshotView{
		AgentKind:             domain.AgentKind,
		DefaultWorkspace:      prof.Workspaces.Default,
		RuntimeAccess:         runtimeAccessStatus(domain),
		MessageReply:          getMessageReply(prof.Preferences),
		ShowToolCalls:         getShowToolCalls(prof.Preferences),
		CotMessages:           getCotMessages(prof.Preferences),
		MaxConcurrentRuns:     getMaxConcurrentRuns(prof.Preferences),
		RunIdleTimeoutMinutes: getRunIdleTimeoutMinutes(prof.Preferences),
		RequireMentionInGroup: prof.Access.RequireMentionInGroup,
		LarkCLIIdentity:       string(prof.LarkCli.IdentityPreset),
		AllowedUsersCount:     len(prof.Access.AllowedUsers),
		AllowedChatsCount:     len(prof.Access.AllowedChats),
		AdminsCount:           len(prof.Access.Admins),
	}
}

func accessFromStore(input configstore.ProfileAccess) profile.Access {
	return profile.Access{
		AllowedUsers:          append([]string(nil), input.AllowedUsers...),
		AllowedChats:          append([]string(nil), input.AllowedChats...),
		Admins:                append([]string(nil), input.Admins...),
		RequireMentionInGroup: input.RequireMentionInGroup,
	}
}

func accessToStore(input profile.Access) configstore.ProfileAccess {
	return configstore.ProfileAccess{
		AllowedUsers:          append([]string(nil), input.AllowedUsers...),
		AllowedChats:          append([]string(nil), input.AllowedChats...),
		Admins:                append([]string(nil), input.Admins...),
		RequireMentionInGroup: input.RequireMentionInGroup,
	}
}

func codexFromStore(input *configstore.CodexConfig) *profile.CodexConfig {
	if input == nil {
		return nil
	}
	return &profile.CodexConfig{
		BinaryPath:       input.BinaryPath,
		Realpath:         input.Realpath,
		Version:          input.Version,
		SHA256:           input.SHA256,
		Owner:            input.Owner,
		Mode:             input.Mode,
		CodexHome:        input.CodexHome,
		InheritCodexHome: input.InheritCodexHome,
		IgnoreUserConfig: input.IgnoreUserConfig,
		IgnoreRules:      input.IgnoreRules,
	}
}

func (s *Service) catalogIdentity(req Request) (appsession.CatalogIdentity, string, bool, string, error) {
	cwd := s.effectiveCWD(req)
	if strings.TrimSpace(cwd) == "" {
		return appsession.CatalogIdentity{}, "", false, "请先使用 /cd <path> 选择工作目录，再查看或恢复会话。", nil
	}
	resolved := workspace.ResolveWorkingDirectory(cwd)
	if !resolved.OK {
		return appsession.CatalogIdentity{}, "", false, resolved.UserVisible, nil
	}
	policy, err := runpolicy.Evaluate(runpolicy.Input{
		Scope: runpolicy.ScopeContext{
			Source:   runpolicy.SourceIM,
			ChatID:   req.ChatID,
			ThreadID: req.ThreadID,
			ActorID:  req.ActorID,
		},
		Prompt:        "",
		RequestedCWD:  cwd,
		CWDRealpath:   resolved.CWDRealpath,
		Access:        s.accessDecision(req),
		Capability:    s.opts.Capability,
		ProfileConfig: s.opts.ProfileConfig,
		Now:           s.opts.Now(),
	})
	if err != nil {
		return appsession.CatalogIdentity{}, "", false, "", err
	}
	if !policy.OK {
		return appsession.CatalogIdentity{}, "", false, policy.RejectReason.UserVisible, nil
	}
	return appsession.CatalogIdentity{
		ScopeID:           req.ScopeID,
		AgentID:           s.opts.Capability.AgentID,
		CWDRealpath:       policy.Allow.CWDRealpath,
		PolicyFingerprint: policy.Allow.PolicyFingerprint,
	}, policy.Allow.CWDRealpath, true, "", nil
}

func (s *Service) effectiveCWD(req Request) string {
	if s.opts.Workspaces != nil {
		if cwd := s.opts.Workspaces.CWDFor(req.ScopeID); cwd != "" {
			return cwd
		}
	}
	if req.WorkingDir != "" {
		return req.WorkingDir
	}
	return s.opts.ProfileConfig.Workspaces.Default
}

func (s *Service) accessDecision(req Request) access.Decision {
	if req.Access.OK || req.Access.Reason != "" {
		return req.Access
	}
	if req.ChatMode == ChatModeGroup || req.ChatMode == ChatModeTopic {
		return access.CanUseGroup(s.opts.ProfileConfig, s.opts.RuntimeControls, req.ChatID, req.SenderID)
	}
	return access.CanUseDM(s.opts.ProfileConfig, s.opts.RuntimeControls, req.SenderID)
}

func (s *Service) canAdmin(req Request) bool {
	return access.CanRunAdminCommand(s.opts.ProfileConfig, s.opts.RuntimeControls, req.SenderID).OK
}

func (s *Service) interrupt(ctx context.Context, scopeID string) bool {
	if s.opts.Executor == nil {
		return false
	}
	return s.opts.Executor.Interrupt(ctx, scopeID)
}

func (s *Service) activeScopes() []string {
	if s.opts.Executor == nil {
		return nil
	}
	return s.opts.Executor.ActiveScopes()
}

func (s *Service) poolSnapshot() runexecutor.ProcessPoolSnapshot {
	if s.opts.Executor == nil {
		return runexecutor.ProcessPoolSnapshot{}
	}
	return s.opts.Executor.PoolSnapshot()
}

func (s *Service) currentSession(scopeID string) (string, bool) {
	if scopeID == "" || s.opts.Sessions == nil {
		return "", false
	}
	entry, ok := s.opts.Sessions.GetRaw(scopeID)
	return entry.SessionID, ok
}

func (s *Service) ownerState() string {
	state := s.opts.RuntimeControls.OwnerRefreshState
	if state == "" {
		state = access.OwnerRefreshUnknown
	}
	owner := "missing"
	if s.opts.RuntimeControls.BotOwnerID != "" {
		owner = "present"
	}
	refreshed := ""
	if s.opts.RuntimeControls.OwnerRefreshedAt > 0 {
		refreshed = " refreshed=" + time.UnixMilli(s.opts.RuntimeControls.OwnerRefreshedAt).UTC().Format(time.RFC3339)
	}
	return fmt.Sprintf("%s owner=%s%s", state, owner, refreshed)
}

func (s *Service) larkCLIStatus(_ context.Context, req Request) LarkCLIStatus {
	if s.opts.LarkCLIStatus != "" {
		return s.opts.LarkCLIStatus
	}
	status, err := DetectLarkCLIStatus(s.opts.LarkCLI, s.canAdmin(req))
	if err != nil {
		return LarkCLIStatusCheckFailed
	}
	return status
}

func DetectLarkCLIStatus(cfg LarkCLIConfig, isAdmin bool) (LarkCLIStatus, error) {
	if cfg.TargetConfigFile != "" && cfg.AppID != "" {
		raw, err := os.ReadFile(cfg.TargetConfigFile)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return LarkCLIStatusCheckFailed, err
			}
		} else if larkCliConfigHasReadyUser(raw, cfg.AppID, cfg.Tenant) {
			return LarkCLIStatusUserReady, nil
		}
	}
	if cfg.IdentityPreset == larkcli.IdentityUserDefault && isAdmin {
		return LarkCLIStatusUserMissing, nil
	}
	return LarkCLIStatusApp, nil
}

func larkCliConfigHasReadyUser(raw []byte, appID string, tenant string) bool {
	var parsed struct {
		Apps []struct {
			AppID      string `json:"appId"`
			Brand      string `json:"brand"`
			DefaultAs  string `json:"defaultAs"`
			StrictMode string `json:"strictMode"`
			Users      any    `json:"users"`
		} `json:"apps"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return false
	}
	for _, app := range parsed.Apps {
		if app.AppID == appID && app.Brand == tenant && app.DefaultAs == "auto" && app.StrictMode == "off" && larkcli.HasStructuredLarkCliUserAuth(app.Users) {
			return true
		}
	}
	return false
}

type timeoutTarget struct {
	scope    string
	value    string
	targeted bool
}

func parseTimeoutTarget(input string, currentScope string) timeoutTarget {
	parts := strings.Fields(input)
	first := ""
	if len(parts) > 0 {
		first = parts[0]
	}
	if strings.HasPrefix(first, "comment:") {
		return timeoutTarget{
			scope:    first,
			value:    strings.Join(parts[1:], " "),
			targeted: true,
		}
	}
	return timeoutTarget{scope: currentScope, value: input}
}

func timeoutUsage() string {
	return "\n\n用法:\n- `/timeout 15` 当前 session 设 15 分钟\n- `/timeout off` 当前 session 关闭探活\n- `/timeout default` 清除 session 覆盖,回退全局\n- `/timeout comment:<scopeHash> 15` 管理员设置 comment scope\n\n_注:`/new` 会清掉当前 session 的覆盖,回到全局_"
}

func timeoutResponse(markdown string, view *TimeoutView) Response {
	return Response{
		Handled:  true,
		Command:  "/timeout",
		Kind:     ResponseTimeout,
		Markdown: markdown,
		Timeout:  view,
	}
}

func message(command string, markdown string) Response {
	return Response{
		Handled:  true,
		Command:  command,
		Kind:     ResponseMessage,
		Markdown: markdown,
	}
}

func workspaceMessage(command string, _ string, markdown string, view *WorkspaceView) Response {
	return Response{
		Handled:   true,
		Command:   command,
		Kind:      ResponseWorkspace,
		Markdown:  markdown,
		Workspace: view,
	}
}

func workspacePersistenceFailure(command string, action string, view *WorkspaceView, err error) Response {
	if view == nil {
		view = &WorkspaceView{Action: action}
	}
	view.Failure = err.Error()
	return workspaceMessage(command, action, fmt.Sprintf("❌ 保存工作目录失败：%v", err), view)
}

func exitResponse(markdown string, view *ExitView) Response {
	return Response{Handled: true, Command: "/exit", Kind: ResponseExit, Markdown: markdown, Exit: view}
}

func reconnectResponse(markdown string, view *ReconnectView) Response {
	return Response{Handled: true, Command: "/reconnect", Kind: ResponseReconnect, Markdown: markdown, Reconnect: view}
}

func doctorResponse(view *DoctorView) Response {
	return Response{Handled: true, Command: "/doctor", Kind: ResponseDoctor, Markdown: view.Report, Doctor: view}
}

func configResponse(markdown string, view *ConfigView) Response {
	return Response{Handled: true, Command: "/config", Kind: ResponseConfig, Markdown: markdown, Config: view}
}

func accountResponse(markdown string, view *AccountView) Response {
	return Response{Handled: true, Command: "/account", Kind: ResponseAccount, Markdown: markdown, Account: view}
}

func accessResponse(markdown string, view *AccessView) Response {
	return Response{Handled: true, Command: "/" + view.Action, Kind: ResponseAccess, Markdown: markdown, Access: view}
}

func firstField(input string) string {
	fields := strings.Fields(strings.TrimSpace(input))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func lowerFields(input string) []string {
	fields := strings.Fields(strings.TrimSpace(input))
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		out = append(out, strings.ToLower(field))
	}
	return out
}

func formString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := values[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			return typed
		case fmt.Stringer:
			return typed.String()
		case int:
			return strconv.Itoa(typed)
		case int64:
			return strconv.FormatInt(typed, 10)
		case float64:
			return strconv.FormatFloat(typed, 'f', -1, 64)
		case bool:
			if typed {
				return "true"
			}
			return "false"
		default:
			if value != nil {
				return fmt.Sprint(value)
			}
		}
	}
	return ""
}

func copyAnyMap(input map[string]any) map[string]any {
	out := make(map[string]any, len(input)+4)
	for key, value := range input {
		out[key] = value
	}
	return out
}

func parseMessageReply(raw string, current string) string {
	switch strings.TrimSpace(raw) {
	case "markdown", "text", "card":
		return strings.TrimSpace(raw)
	default:
		return current
	}
}

func getMessageReply(prefs map[string]any) string {
	raw, _ := prefs["messageReply"].(string)
	if raw == "text" && prefs["messageReplyMigrated"] != true {
		return "markdown"
	}
	if raw == "card" || raw == "markdown" || raw == "text" {
		return raw
	}
	return "markdown"
}

func parseShowToolCalls(raw string, current bool) bool {
	switch strings.TrimSpace(raw) {
	case "hide", "false", "no", "0":
		return false
	case "show", "true", "yes", "1":
		return true
	default:
		return current
	}
}

func getShowToolCalls(prefs map[string]any) bool {
	value, ok := prefs["showToolCalls"].(bool)
	if ok {
		return value
	}
	return true
}

func parseCotMessages(raw string, current string) string {
	switch strings.TrimSpace(raw) {
	case "brief", "simple":
		return "brief"
	case "detailed", "on":
		return "detailed"
	case "off":
		return "off"
	default:
		return current
	}
}

func getCotMessages(prefs map[string]any) string {
	raw, _ := prefs["cotMessages"].(string)
	switch raw {
	case "brief", "simple":
		return "brief"
	case "detailed", "on":
		return "detailed"
	default:
		return "off"
	}
}

func parseClampedInt(raw string, current int, min int, max int, allowZero bool) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return current
	}
	parsed, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return current
	}
	if parsed == 0 && allowZero {
		return 0
	}
	if parsed < float64(min) {
		return current
	}
	value := int(parsed)
	if value > max {
		return max
	}
	return value
}

func getMaxConcurrentRuns(prefs map[string]any) int {
	raw, ok := numberPreference(prefs["maxConcurrentRuns"])
	if !ok || raw < 1 {
		return 10
	}
	if raw > 50 {
		return 50
	}
	return int(raw)
}

func getRunIdleTimeoutMinutes(prefs map[string]any) int {
	raw, ok := numberPreference(prefs["runIdleTimeoutMinutes"])
	if !ok || raw <= 0 {
		return 0
	}
	if raw > 120 {
		return 120
	}
	return int(raw)
}

func numberPreference(value any) (float64, bool) {
	switch typed := value.(type) {
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case float64:
		return typed, true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func parseRequireMention(raw string, current bool) bool {
	switch strings.TrimSpace(raw) {
	case "yes", "true", "1":
		return true
	case "no", "false", "0":
		return false
	default:
		return current
	}
}

func parseLarkCLIIdentity(raw string, current configstore.LarkCliIdentityPreset) configstore.LarkCliIdentityPreset {
	switch strings.TrimSpace(raw) {
	case string(configstore.LarkCliIdentityUserDefault):
		return configstore.LarkCliIdentityUserDefault
	case string(configstore.LarkCliIdentityBotOnly):
		return configstore.LarkCliIdentityBotOnly
	default:
		return current
	}
}

func larkCLIManualReason(identity configstore.LarkCliIdentityPreset) string {
	if identity == configstore.LarkCliIdentityUserDefault {
		return "manual-user-default"
	}
	return "manual-bot-only"
}

func accessKind(tokens []string) string {
	for _, token := range tokens {
		if token == "user" || token == "admin" || token == "group" {
			return token
		}
	}
	return ""
}

func mentionTargets(req Request) []Mention {
	out := []Mention{}
	for _, mention := range req.Mentions {
		if mention.IsBot || mention.OpenID == "" {
			continue
		}
		out = append(out, mention)
	}
	return out
}

func mentionLabel(mention Mention) string {
	if mention.Name != "" {
		return mention.Name
	}
	return mention.OpenID
}

func fillAccessView(view *AccessView, access profile.Access) {
	view.AllowedUsers = append([]string(nil), access.AllowedUsers...)
	view.AllowedChats = append([]string(nil), access.AllowedChats...)
	view.Admins = append([]string(nil), access.Admins...)
}

func removeString(items []string, value string) ([]string, bool) {
	out := make([]string, 0, len(items))
	removed := false
	for _, item := range items {
		if item == value {
			removed = true
			continue
		}
		out = append(out, item)
	}
	return out, removed
}

func okDenied(ok bool) string {
	if ok {
		return "ok"
	}
	return "denied"
}

func okUnavailable(ok bool) string {
	if ok {
		return "available"
	}
	return "unavailable"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func defaultChatName(agentName string, now time.Time) string {
	if strings.TrimSpace(agentName) == "" {
		agentName = "Agent"
	}
	if now.IsZero() {
		now = time.Now()
	}
	return fmt.Sprintf("%s · %d-%d %02d:%02d", agentName, int(now.Month()), now.Day(), now.Hour(), now.Minute())
}

func (s *Service) handleHelp(req Request) Response {
	view := &HelpView{Commands: supportedHelpCommands()}
	lines := []string{"**命令列表**", ""}
	for _, command := range view.Commands {
		if !command.Supported {
			continue
		}
		lines = append(lines, fmt.Sprintf("- `%s` — %s", command.Command, command.Description))
	}
	lines = append(lines, "", fmt.Sprintf("其他内容直接交给 %s。", emptyAs(s.opts.AgentName, "Agent")))
	return Response{
		Handled:  true,
		Command:  req.Command,
		Kind:     ResponseHelp,
		Markdown: strings.Join(lines, "\n"),
		Help:     view,
	}
}

func supportedHelpCommands() []HelpCommand {
	return []HelpCommand{
		{Command: "/new", Description: "清空当前 scope 的会话", Supported: true},
		{Command: "/new chat [name]", Description: "新建群和新会话并邀请当前发送者", Supported: true},
		{Command: "/reset", Description: "清空当前 scope 的会话", Supported: true},
		{Command: "/resume [N]", Description: "列出并恢复历史会话（最多 N 条）", Supported: true},
		{Command: "/cd <path>", Description: "切换工作目录（会重置 session）", Supported: true},
		{Command: "/ws list|save <name>|use <name>|remove <name>", Description: "管理当前 scope 的命名工作目录", Supported: true},
		{Command: "/status", Description: "查看当前状态", Supported: true},
		{Command: "/stop", Description: "结束当前正在跑的任务", Supported: true},
		{Command: "/stop comment:<scopeHash>", Description: "管理员停止云文档评论任务", Supported: true},
		{Command: "/timeout [N|off|default]", Description: "设置或清除当前 session 的探活分钟数", Supported: true},
		{Command: "/timeout comment:<scopeHash> N", Description: "管理员设置云文档评论任务探活", Supported: true},
		{Command: "/ps", Description: "列出本机 bridge 进程", Supported: true},
		{Command: "/exit <id|#>", Description: "关闭指定 bridge 进程", Supported: true},
		{Command: "/reconnect [--wait]", Description: "重连当前 bridge", Supported: true},
		{Command: "/doctor", Description: "运行自检并输出诊断结构", Supported: true},
		{Command: "/config", Description: "查看或提交 profile 偏好配置", Supported: true},
		{Command: "/account", Description: "查看或更新 App 凭据", Supported: true},
		{Command: "/invite user|admin|group", Description: "维护 profile 访问名单", Supported: true},
		{Command: "/remove user|admin|group", Description: "从 profile 访问名单移除", Supported: true},
		{Command: "/doc", Description: "云文档 workspace 绑定兼容 no-op", Supported: true},
		{Command: "/help", Description: "本帮助", Supported: true},
	}
}

func (s *Service) archiveCurrentSession(req Request) bool {
	if s.opts.SessionCatalog == nil {
		return false
	}
	identity, _, ok, _, err := s.catalogIdentity(req)
	if err != nil || !ok {
		return false
	}
	return s.opts.SessionCatalog.ArchiveActive(appsession.ArchiveCatalogInput{
		CatalogIdentity: identity,
		Now:             s.opts.Now(),
	})
}

func expandTilde(path string) string {
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}

func isAbsoluteOrTilde(path string) bool {
	return filepath.IsAbs(path) || path == "~" || strings.HasPrefix(path, "~/")
}

const workspaceNameSeparator = "\x1f"

func (s *Service) scopedWorkspaceName(req Request, name string) string {
	owner := s.opts.RuntimeControls.BotOwnerID
	if owner == "" {
		owner = "owner-unknown"
	}
	return strings.Join([]string{s.opts.ProfileName, owner, req.ScopeID, name}, workspaceNameSeparator)
}

func (s *Service) workspaceAlias(req Request, name string) string {
	if s.opts.Workspaces == nil {
		return ""
	}
	for _, key := range []string{s.scopedWorkspaceName(req, name), name} {
		if cwd := s.opts.Workspaces.GetNamed(key); cwd != "" {
			return cwd
		}
	}
	return ""
}

func (s *Service) removeWorkspaceAlias(req Request, name string) (bool, error) {
	if s.opts.Workspaces == nil {
		return false, nil
	}
	removed, err := s.opts.Workspaces.RemoveNamed(s.scopedWorkspaceName(req, name))
	if err != nil || removed {
		return removed, err
	}
	return s.opts.Workspaces.RemoveNamed(name)
}

func (s *Service) listScopedWorkspaces(req Request) map[string]string {
	out := map[string]string{}
	if s.opts.Workspaces == nil {
		return out
	}
	prefix := s.scopedWorkspaceName(req, "")
	for key, cwd := range s.opts.Workspaces.ListNamed() {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		display := strings.TrimPrefix(key, prefix)
		if display != "" {
			out[display] = cwd
		}
	}
	for key, cwd := range s.opts.Workspaces.ListNamed() {
		if key != "" && !strings.Contains(key, workspaceNameSeparator) {
			if _, exists := out[key]; !exists {
				out[key] = cwd
			}
		}
	}
	return out
}

func emptyAs(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func runtimeAccessStatus(cfg profile.Config) RuntimeAccessView {
	if cfg.AgentKind == profile.AgentClaude {
		return RuntimeAccessView{
			Label: "permission",
			Value: string(permissions.AccessToClaudePermissionMode(cfg.Permissions.DefaultAccess, &cfg.Permissions)),
		}
	}
	return RuntimeAccessView{
		Label: "sandbox",
		Value: string(mustSandbox(cfg.Permissions.DefaultAccess)) + "/" + string(mustSandbox(cfg.Permissions.MaxAccess)),
	}
}

func mustSandbox(mode permissions.AccessMode) permissions.CodexSandboxMode {
	sandbox, err := permissions.AccessToCodexSandbox(mode)
	if err != nil {
		return ""
	}
	return sandbox
}

func splitScopes(scopes []string) ([]string, []string) {
	active := make([]string, 0, len(scopes))
	comments := make([]string, 0)
	for _, scope := range scopes {
		if strings.HasPrefix(scope, "comment:") {
			comments = append(comments, scope)
		} else {
			active = append(active, scope)
		}
	}
	sort.Strings(active)
	sort.Strings(comments)
	return active, comments
}

func statusMarkdown(status *StatusView) string {
	session := status.SessionID
	if session == "" {
		session = status.EmptySessionText
	}
	if session == "" {
		session = "(无)"
	}
	return fmt.Sprintf("**profile** %s\n**cwd** %s\n**session** %s\n**agent** %s\n**%s** %s\n**lark-cli** %s\n**active scopes** %s\n**comment scopes** %s\n**owner API** %s",
		status.ProfileName,
		status.CWD,
		session,
		status.AgentName,
		status.RuntimeAccess.Label,
		status.RuntimeAccess.Value,
		status.LarkCLIStatus,
		strings.Join(status.ActiveScopes, ","),
		strings.Join(status.ActiveCommentScopes, ","),
		status.OwnerState,
	)
}

func formatAgo(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	minutes := int(d.Minutes())
	if minutes < 1 {
		return "刚刚"
	}
	if minutes < 60 {
		return fmt.Sprintf("%d 分钟前", minutes)
	}
	hours := minutes / 60
	if hours < 24 {
		return fmt.Sprintf("%d 小时前", hours)
	}
	return fmt.Sprintf("%d 天前", hours/24)
}

func contains(items []string, needle string) bool {
	for _, item := range items {
		if item == needle {
			return true
		}
	}
	return false
}

type resumeTarget struct {
	sessionID string
	threadID  string
}

type resumeCandidate struct {
	identity appsession.CatalogIdentity
	target   resumeTarget
	expires  time.Time
}

type ResumeStore struct {
	mu         sync.Mutex
	candidates map[string]resumeCandidate
	now        func() time.Time
}

func NewResumeStore(now func() time.Time) *ResumeStore {
	if now == nil {
		now = time.Now
	}
	return &ResumeStore{
		candidates: map[string]resumeCandidate{},
		now:        now,
	}
}

func (s *ResumeStore) Issue(identity appsession.CatalogIdentity, target resumeTarget) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	token := randomToken()
	for {
		if _, exists := s.candidates[token]; !exists {
			break
		}
		token = randomToken()
	}
	s.candidates[token] = resumeCandidate{
		identity: identity,
		target:   target,
		expires:  s.now().Add(resumeCandidateTTL),
	}
	return token
}

func (s *ResumeStore) Consume(token string, identity appsession.CatalogIdentity) (resumeTarget, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	candidate, ok := s.candidates[token]
	if !ok {
		return resumeTarget{}, false
	}
	delete(s.candidates, token)
	if candidate.identity != identity {
		return resumeTarget{}, false
	}
	if identity.AgentID == capability.IDCodex && candidate.target.threadID == "" {
		return resumeTarget{}, false
	}
	if identity.AgentID == capability.IDClaude && candidate.target.sessionID == "" {
		return resumeTarget{}, false
	}
	return candidate.target, true
}

func (s *ResumeStore) pruneLocked() {
	now := s.now()
	for token, candidate := range s.candidates {
		if !candidate.expires.After(now) {
			delete(s.candidates, token)
		}
	}
}

func randomToken() string {
	var buf [6]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(buf[:])
}
