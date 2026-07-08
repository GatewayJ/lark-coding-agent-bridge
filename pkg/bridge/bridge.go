package bridge

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	appmedia "github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/media"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/compat/apppaths"
)

var (
	ErrNilBridge                  = errors.New("bridge is nil")
	ErrBridgeAlreadyStarted       = errors.New("bridge already started")
	ErrBridgeStartUnsupported     = errors.New("bridge start requires an injected agent client, lark transport, lark adapter, or runtime adapter")
	ErrBridgeStartMissingAppID    = errors.New("bridge start with lark/runtime adapter requires appID")
	ErrBridgeAmbiguousAgentClient = errors.New("bridge options must provide only one of Client, CodexClient, or ClaudeClient")
	ErrBridgeAmbiguousLarkRuntime = errors.New("bridge options must provide only one of LarkTransport, LarkAdapter, or RuntimeAdapter")
	ErrBridgeLarkIntakeRequired   = errors.New("bridge options with LarkTransport require LarkIntake; use NewLarkAdapter for send-only/custom wiring")
	ErrBridgeCommentSurface       = errors.New("bridge managed lark comments require a comment surface")
	ErrBridgeReconnectUnsupported = errors.New("bridge reconnect requires a runtime adapter")
	ErrBridgeShutdownInProgress   = errors.New("bridge shutdown already in progress")
)

type Bridge struct {
	mu       sync.Mutex
	starting bool
	stopping bool
	started  bool
	mode     BridgeMode

	home      string
	profile   string
	version   string
	startedAt time.Time

	logger    Logger
	telemetry TelemetryAdapter

	client  *Client
	lark    *LarkAdapter
	runtime *Runtime

	appID       string
	tenant      RuntimeTenant
	agentKind   RuntimeAgentKind
	configPath  string
	modeOnStart BridgeMode
}

func New(opts Options) (*Bridge, error) {
	paths, err := apppaths.Resolve(apppaths.Options{RootDir: opts.Home, Profile: opts.Profile})
	if err != nil {
		return nil, err
	}
	client, err := buildBridgeClient(paths, opts)
	if err != nil {
		return nil, err
	}
	runtimeAdapter, larkAdapter, mode, err := buildBridgeRuntimeAdapter(opts, client, paths)
	if err != nil {
		return nil, err
	}

	version := opts.Version
	if version == "" {
		version = "go-sdk"
	}
	agentKind := opts.AgentKind
	if agentKind == "" {
		agentKind = inferRuntimeAgentKind(opts, client)
	}

	var runtime *Runtime
	if runtimeAdapter != nil {
		runtime, err = NewRuntime(RuntimeOptions{
			RootDir:   paths.RootDir,
			Profile:   paths.Profile,
			AgentKind: agentKind,
			Version:   version,
			Adapter:   runtimeAdapter,
		})
		if err != nil {
			return nil, err
		}
	}

	return &Bridge{
		home:        paths.RootDir,
		profile:     paths.Profile,
		version:     version,
		logger:      opts.Logger,
		telemetry:   opts.Telemetry,
		client:      client,
		lark:        larkAdapter,
		runtime:     runtime,
		appID:       opts.AppID,
		tenant:      opts.Tenant,
		agentKind:   agentKind,
		configPath:  firstNonEmptyBridge(opts.ConfigPath, paths.ConfigFile),
		modeOnStart: mode,
	}, nil
}

func (b *Bridge) Start(ctx context.Context) error {
	if b == nil {
		return ErrNilBridge
	}
	b.mu.Lock()
	if b.started || b.starting || b.stopping {
		b.mu.Unlock()
		return ErrBridgeAlreadyStarted
	}
	client := b.client
	runtime := b.runtime
	lark := b.lark
	appID := b.appID
	tenant := b.tenant
	agentKind := b.agentKind
	configPath := b.configPath
	mode := b.modeOnStart
	b.starting = true
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		b.starting = false
		b.mu.Unlock()
	}()

	if client == nil && runtime == nil {
		err := ErrBridgeStartUnsupported
		b.recordError(ctx, err, nil)
		return err
	}
	if runtime != nil && appID == "" {
		err := ErrBridgeStartMissingAppID
		b.recordError(ctx, err, nil)
		return err
	}
	if client != nil {
		if err := client.LoadState(); err != nil {
			b.recordError(ctx, err, map[string]any{"phase": "load_state"})
			return err
		}
	}
	if runtime != nil {
		if err := runtime.Start(ctx, RuntimeStartOptions{
			AppID:      appID,
			Tenant:     tenant,
			AgentKind:  agentKind,
			ConfigPath: configPath,
			Version:    b.version,
		}); err != nil {
			b.recordError(ctx, err, map[string]any{"phase": "runtime_start"})
			return err
		}
		if client != nil && lark != nil {
			client.applyLarkRuntimeContext(lark.BotIdentity(), lark.ProjectionResult().LarkChannelEnv)
		}
	} else {
		mode = BridgeModeAgent
	}

	now := time.Now()
	b.mu.Lock()
	b.started = true
	b.mode = mode
	b.startedAt = now
	b.mu.Unlock()
	b.logInfo("bridge started", map[string]any{"mode": string(mode), "profile": b.profile})
	b.emit(ctx, "bridge.started", map[string]any{"mode": string(mode), "profile": b.profile})
	return nil
}

func (b *Bridge) Shutdown(ctx context.Context) error {
	if b == nil {
		return ErrNilBridge
	}
	b.mu.Lock()
	started := b.started
	if b.stopping {
		b.mu.Unlock()
		return ErrBridgeShutdownInProgress
	}
	client := b.client
	runtime := b.runtime
	b.stopping = true
	b.mu.Unlock()
	defer func() {
		if client != nil {
			client.ReleaseCommandState()
		}
	}()
	defer func() {
		b.mu.Lock()
		b.stopping = false
		b.mu.Unlock()
	}()
	if !started {
		return nil
	}

	var errs []error
	if runtime != nil {
		if err := runtime.Shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if client != nil {
		if err := client.StopAll(ctx); err != nil {
			errs = append(errs, err)
		}
		if err := client.FlushState(); err != nil {
			errs = append(errs, err)
		}
	}
	if b.telemetry != nil {
		if err := safeTelemetryFlush(b.telemetry, ctx); err != nil {
			b.logInfo("bridge telemetry flush failed", map[string]any{"error": err.Error()})
		}
		if err := safeTelemetryClose(b.telemetry, ctx); err != nil {
			b.logInfo("bridge telemetry close failed", map[string]any{"error": err.Error()})
		}
	}
	err := errors.Join(errs...)
	if err != nil {
		b.recordError(ctx, err, map[string]any{"phase": "shutdown"})
		return err
	}
	b.mu.Lock()
	b.started = false
	b.mode = ""
	b.startedAt = time.Time{}
	b.mu.Unlock()
	b.logInfo("bridge stopped", map[string]any{"profile": b.profile})
	return nil
}

func (b *Bridge) Reconnect(ctx context.Context, options RuntimeReconnectOptions) error {
	if b == nil {
		return ErrNilBridge
	}
	b.mu.Lock()
	runtime := b.runtime
	stopping := b.stopping
	b.mu.Unlock()
	if stopping {
		return ErrBridgeShutdownInProgress
	}
	if runtime == nil {
		return ErrBridgeReconnectUnsupported
	}
	return runtime.Reconnect(ctx, options)
}

func (b *Bridge) Status(ctx context.Context) (Status, error) {
	if b == nil {
		return Status{}, ErrNilBridge
	}
	b.mu.Lock()
	started := b.started
	mode := b.mode
	startedAt := b.startedAt
	client := b.client
	runtime := b.runtime
	lark := b.lark
	bridgeStatus := BridgeStatus{
		Started:               started,
		Mode:                  mode,
		Home:                  b.home,
		Profile:               b.profile,
		Version:               b.version,
		StartedAt:             startedAt,
		AgentClientConfigured: client != nil,
		RuntimeConfigured:     runtime != nil,
	}
	b.mu.Unlock()

	var status Status
	if client != nil {
		status = client.Status()
	}
	status.Bridge = bridgeStatus
	if runtime != nil {
		runtimeStatus, err := runtime.Status(ctx)
		if err != nil {
			return Status{}, err
		}
		status.Runtime = &runtimeStatus
	}
	status.Lark = larkStatus(lark)
	return status, nil
}

func buildBridgeClient(paths apppaths.Paths, opts Options) (*Client, error) {
	count := 0
	if opts.Client != nil {
		count++
	}
	if opts.CodexClient != nil {
		count++
	}
	if opts.ClaudeClient != nil {
		count++
	}
	if count > 1 {
		return nil, ErrBridgeAmbiguousAgentClient
	}
	if opts.Client != nil {
		opts.Client.setLogger(opts.Logger)
		opts.Client.setProfileName(paths.Profile)
		return opts.Client, nil
	}
	if opts.CodexClient != nil {
		copied := *opts.CodexClient
		if copied.Logger == nil {
			copied.Logger = opts.Logger
		}
		copied.ProfileStateDir = firstNonEmptyBridge(copied.ProfileStateDir, paths.ProfileDir)
		copied.DefaultWorkingDir = firstNonEmptyBridge(copied.DefaultWorkingDir, paths.DefaultWorkspaceDir)
		copied.SessionStorePath = firstNonEmptyBridge(copied.SessionStorePath, paths.SessionsFile)
		copied.SessionCatalogPath = firstNonEmptyBridge(copied.SessionCatalogPath, paths.SessionsFile+".catalog.json")
		client, err := NewCodexClient(copied)
		if client != nil {
			client.setProfileName(paths.Profile)
		}
		return client, err
	}
	if opts.ClaudeClient != nil {
		copied := *opts.ClaudeClient
		if copied.Logger == nil {
			copied.Logger = opts.Logger
		}
		copied.DefaultWorkingDir = firstNonEmptyBridge(copied.DefaultWorkingDir, paths.DefaultWorkspaceDir)
		copied.SessionStorePath = firstNonEmptyBridge(copied.SessionStorePath, paths.SessionsFile)
		copied.SessionCatalogPath = firstNonEmptyBridge(copied.SessionCatalogPath, paths.SessionsFile+".catalog.json")
		client, err := NewClaudeClient(copied)
		if client != nil {
			client.setProfileName(paths.Profile)
		}
		return client, err
	}
	return nil, nil
}

func buildBridgeRuntimeAdapter(opts Options, client *Client, paths apppaths.Paths) (RuntimeAdapter, *LarkAdapter, BridgeMode, error) {
	count := 0
	if opts.RuntimeAdapter != nil {
		count++
	}
	if opts.LarkAdapter != nil {
		count++
	}
	if opts.LarkTransport != nil {
		count++
	}
	if count > 1 {
		return nil, nil, "", ErrBridgeAmbiguousLarkRuntime
	}
	if opts.RuntimeAdapter != nil {
		return opts.RuntimeAdapter, nil, BridgeModeRuntime, nil
	}
	if opts.LarkAdapter != nil {
		return larkRuntimeAdapter{adapter: opts.LarkAdapter}, opts.LarkAdapter, BridgeModeLark, nil
	}
	if opts.LarkTransport != nil {
		intake := opts.LarkIntake
		cardActions := opts.LarkCardActions
		if intake == nil && client != nil {
			var comments *CommentHandler
			surface := opts.LarkComments
			if surface == nil {
				if oapiTransport, ok := opts.LarkTransport.(*OAPILarkTransport); ok {
					var err error
					surface, err = NewOAPICommentSurface(oapiTransport)
					if err != nil {
						return nil, nil, "", err
					}
				}
			}
			if surface != nil {
				commentOptions := opts.LarkCommentOptions
				if commentOptions.ManagedDefaultWorkspace == "" {
					commentOptions.ManagedDefaultWorkspace = paths.DefaultWorkspaceDir
				}
				handler, err := NewCommentHandler(client, surface, commentOptions)
				if err != nil {
					return nil, nil, "", err
				}
				comments = handler
			}
			managedOptions := opts.LarkManaged
			workspaces := managedOptions.CommandOptions.Workspaces
			if workspaces == nil {
				workspaces = newCommandMemoryWorkspaceStore()
			}
			intake = newManagedLarkIntake(managedLarkIntakeOptions{
				Client:     client,
				Transport:  opts.LarkTransport,
				Comments:   comments,
				Media:      managedMediaCache(opts.LarkTransport, paths.MediaDir),
				AppID:      opts.AppID,
				Managed:    managedOptions,
				Workspaces: workspaces,
				OnInfo: func(ctx context.Context, msg string, fields map[string]any) {
					if opts.Logger != nil {
						opts.Logger.Info(msg, fields)
					}
				},
				OnError: func(ctx context.Context, err error, fields map[string]any) {
					if opts.Logger != nil {
						opts.Logger.Error(err.Error(), fields)
					}
					safeTelemetryRecordError(opts.Telemetry, ctx, err, fields)
				},
			})
			if cardActions == nil {
				if dispatcher, ok := intake.(LarkCardActionDispatcher); ok {
					cardActions = dispatcher
				}
			}
		}
		if intake == nil {
			return nil, nil, "", ErrBridgeLarkIntakeRequired
		}
		adapter, err := NewLarkAdapter(LarkAdapterOptions{
			Transport:                  opts.LarkTransport,
			Intake:                     intake,
			CardActions:                cardActions,
			ForwardCardPromptsToIntake: opts.ForwardCardPromptsToIntake,
			SelfLoopPolicy:             DefaultLarkSelfLoopPolicy(""),
			ProfileProjection:          opts.LarkProfileProjection,
		})
		if err != nil {
			return nil, nil, "", err
		}
		return larkRuntimeAdapter{adapter: adapter}, adapter, BridgeModeLark, nil
	}
	return nil, nil, "", nil
}

func managedMediaCache(transport LarkTransport, mediaDir string) *appmedia.Cache {
	if mediaDir == "" {
		return nil
	}
	downloader := wrapInternalMediaDownloaderFromTransport(transport)
	if downloader == nil {
		return nil
	}
	return appmedia.NewCache(downloader, mediaDir)
}

type larkRuntimeAdapter struct {
	adapter *LarkAdapter
}

func (a larkRuntimeAdapter) Start(ctx context.Context, _ RuntimeStartRequest) (RuntimeHandle, error) {
	if a.adapter == nil {
		return nil, ErrNilLarkTransport
	}
	if err := a.adapter.Start(ctx); err != nil {
		return nil, err
	}
	return larkRuntimeHandle{adapter: a.adapter}, nil
}

type larkRuntimeHandle struct {
	adapter *LarkAdapter
}

func (h larkRuntimeHandle) Shutdown(ctx context.Context) error {
	if h.adapter == nil {
		return nil
	}
	return h.adapter.Disconnect(ctx)
}

func (h larkRuntimeHandle) Status(context.Context) (RuntimeAdapterStatus, error) {
	if h.adapter == nil {
		return RuntimeAdapterStatus{}, nil
	}
	identity := h.adapter.BotIdentity()
	return RuntimeAdapterStatus{
		Connected: h.adapter.Started(),
		BotName:   identity.Name,
		Details: map[string]string{
			"botOpenId": identity.OpenID,
			"botUserId": identity.UserID,
		},
	}, nil
}

func larkStatus(adapter *LarkAdapter) LarkStatus {
	if adapter == nil {
		return LarkStatus{}
	}
	identity := adapter.BotIdentity()
	projection := adapter.ProjectionResult()
	return LarkStatus{
		Configured:              true,
		Started:                 adapter.Started(),
		BotOpenID:               identity.OpenID,
		BotUserID:               identity.UserID,
		BotUnionID:              identity.UnionID,
		BotName:                 identity.Name,
		LarkCliSourceConfigFile: projection.LarkCliSourceConfigFile,
		IdentityPolicyApplied:   projection.IdentityPolicyApplied,
	}
}

func (b *Bridge) logInfo(msg string, fields map[string]any) {
	if b.logger != nil {
		b.logger.Info(msg, fields)
	}
}

func (b *Bridge) recordError(ctx context.Context, err error, fields map[string]any) {
	if b.logger != nil {
		b.logger.Error(err.Error(), fields)
	}
	if b.telemetry != nil {
		safeTelemetryRecordError(b.telemetry, ctx, err, fields)
	}
}

func (b *Bridge) emit(ctx context.Context, name string, fields map[string]any) {
	if b.telemetry != nil {
		safeTelemetryEmit(b.telemetry, ctx, name, fields)
	}
}

func firstNonEmptyBridge(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func inferRuntimeAgentKind(opts Options, client *Client) RuntimeAgentKind {
	if opts.CodexClient != nil {
		return RuntimeAgentCodex
	}
	if opts.ClaudeClient != nil {
		return RuntimeAgentClaude
	}
	if client != nil && client.cap.AgentID == "codex" {
		return RuntimeAgentCodex
	}
	if client != nil {
		return RuntimeAgentClaude
	}
	return ""
}

func (s BridgeStatus) String() string {
	if s.Mode == "" {
		return fmt.Sprintf("profile=%s started=%t", s.Profile, s.Started)
	}
	return fmt.Sprintf("profile=%s mode=%s started=%t", s.Profile, s.Mode, s.Started)
}
