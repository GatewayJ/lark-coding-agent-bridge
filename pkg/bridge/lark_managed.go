package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	appcardkit "github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/cardkit"
	appcardrender "github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/cardrender"
	appconfigstore "github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/configstore"
	appcot "github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/cotpresenter"
	appimpresenter "github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/impresenter"
	appintake "github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/intake"
	appmedia "github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/media"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/access"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/profile"
	agentport "github.com/zarazhangrui/lark-coding-agent-bridge/internal/ports/agent"
)

const defaultManagedMessageQuietPeriod = 600 * time.Millisecond
const defaultManagedCardActionSettle = time.Second
const defaultManagedAccountReconnectDelay = 1500 * time.Millisecond
const defaultManagedCloseTimeout = 5 * time.Second
const defaultManagedInfoRefreshInterval = 30 * time.Minute
const defaultManagedReactionCleanupGrace = time.Second
const managedAccountReconnectDedupTTL = 5 * time.Minute
const managedRuntimeConfigFailureMessage = "服务配置读取失败，暂不处理这条消息。请联系管理员检查 bridge 配置。"

type managedLarkIntake struct {
	client    *Client
	transport LarkTransport
	comments  *CommentHandler
	media     *appmedia.Cache
	appID     string

	queue          *appintake.Queue
	commandMu      sync.RWMutex
	commandOptions CommandOptions
	quoteResolver  LarkQuoteResolver
	callbackAuth   *CallbackAuth
	callbackTTL    time.Duration
	cardSettle     time.Duration
	accountRestart time.Duration
	closeTimeout   time.Duration
	scopeChecker   LarkScopeChecker
	scopeGrant     LarkScopeGrantRequester
	reactioner     LarkMessageReactioner
	infoSource     LarkRuntimeInfoSource
	infoInterval   time.Duration
	infoOnce       sync.Once
	presentationMu sync.RWMutex
	replyMode      LarkReplyMode
	showToolCalls  bool
	cotMessages    appcot.Mode
	workspaces     CommandWorkspaceStore
	cotClient      appcot.Client
	onInfo         func(ctx context.Context, msg string, fields map[string]any)
	onError        func(ctx context.Context, err error, fields map[string]any)

	activeMu   sync.Mutex
	activeRuns map[string]managedActiveRun

	lifecycleCtx    context.Context
	lifecycleCancel context.CancelFunc
	lifecycleMu     sync.Mutex
	lifecycleClosed bool
	lifecycleWG     sync.WaitGroup

	accountReconnectMu      sync.Mutex
	accountReconnectPending bool
	accountReconnectSeen    map[string]time.Time
}

func newManagedLarkIntake(options managedLarkIntakeOptions) *managedLarkIntake {
	quietPeriod := options.Managed.MessageQuietPeriod
	if quietPeriod <= 0 {
		quietPeriod = defaultManagedMessageQuietPeriod
	}
	quoteResolver := options.Managed.QuoteResolver
	if quoteResolver == nil {
		if resolver, ok := options.Transport.(LarkQuoteResolver); ok {
			quoteResolver = resolver
		}
	}
	scopeChecker := options.Managed.ScopeChecker
	if scopeChecker == nil {
		if checker, ok := options.Transport.(LarkScopeChecker); ok {
			scopeChecker = checker
		}
	}
	scopeGrant := options.Managed.ScopeGrant
	if scopeGrant == nil {
		if grant, ok := options.Transport.(LarkScopeGrantRequester); ok {
			scopeGrant = grant
		}
	}
	reactioner := options.Managed.Reactioner
	if reactioner == nil {
		if got, ok := options.Transport.(LarkMessageReactioner); ok {
			reactioner = got
		}
	}
	cotClient := options.Managed.COTClient
	if cotClient == nil {
		if got, ok := options.Transport.(interface{ internalCOTClient() appcot.Client }); ok {
			cotClient = nil
			if internal := got.internalCOTClient(); internal != nil {
				cotClient = publicCOTClientAdapter{inner: internal}
			}
		} else if got, ok := options.Transport.(LarkCOTClient); ok {
			cotClient = got
		}
	}
	infoSource := options.Managed.RuntimeInfo
	if infoSource == nil {
		if source, ok := options.Transport.(LarkRuntimeInfoSource); ok {
			infoSource = source
		}
	}
	commandOptions := options.Managed.CommandOptions
	initialOwnerOpenID := strings.TrimSpace(options.Managed.InitialOwnerOpenID)
	if initialOwnerOpenID != "" && strings.TrimSpace(commandOptions.RuntimeControls.BotOwnerID) == "" {
		commandOptions.RuntimeControls.BotOwnerID = initialOwnerOpenID
		commandOptions.RuntimeControls.OwnerRefreshState = "ok"
		commandOptions.RuntimeControls.OwnerRefreshedAt = time.Now().UnixMilli()
		commandOptions.RuntimeControls.OwnerRefreshError = ""
	}
	if commandOptions.ChatCreator == nil {
		if creator, ok := options.Transport.(CommandChatCreator); ok {
			commandOptions.ChatCreator = creator
		}
	}
	lifecycleCtx, lifecycleCancel := context.WithCancel(context.Background())
	intake := &managedLarkIntake{
		client:               options.Client,
		transport:            options.Transport,
		comments:             options.Comments,
		media:                options.Media,
		appID:                options.AppID,
		commandOptions:       commandOptions,
		quoteResolver:        quoteResolver,
		callbackAuth:         options.Managed.CallbackAuth,
		callbackTTL:          managedCallbackTTL(options.Managed.CallbackTTL),
		cardSettle:           managedCardActionSettle(options.Managed.CardActionSettle),
		accountRestart:       managedAccountReconnectDelay(options.Managed.AccountReconnect),
		closeTimeout:         managedCloseTimeout(options.Managed.CloseTimeout),
		scopeChecker:         scopeChecker,
		scopeGrant:           scopeGrant,
		reactioner:           reactioner,
		infoSource:           infoSource,
		infoInterval:         managedInfoRefreshInterval(options.Managed.InfoRefreshInterval),
		replyMode:            normalizeLarkReplyMode(options.Managed.MessageReplyMode),
		showToolCalls:        managedShowToolCalls(options.Managed.ShowToolCalls),
		cotMessages:          managedCotMessagesMode(options.Managed.CotMessages),
		workspaces:           options.Workspaces,
		cotClient:            wrapInternalLarkCOTClient(cotClient),
		onInfo:               options.OnInfo,
		onError:              options.OnError,
		activeRuns:           make(map[string]managedActiveRun),
		lifecycleCtx:         lifecycleCtx,
		lifecycleCancel:      lifecycleCancel,
		accountReconnectSeen: make(map[string]time.Time),
	}
	if intake.commandOptions.Workspaces == nil {
		intake.commandOptions.Workspaces = options.Workspaces
	}
	intake.queue = appintake.NewQueue(appintake.QueueOptions{
		QuietPeriod:  quietPeriod,
		FlushTimeout: options.Managed.FlushTimeout,
		Handler:      intake.handleBatch,
	})
	return intake
}

type managedLarkIntakeOptions struct {
	Client     *Client
	Transport  LarkTransport
	Comments   *CommentHandler
	Media      *appmedia.Cache
	AppID      string
	Managed    LarkManagedOptions
	Workspaces CommandWorkspaceStore
	OnInfo     func(ctx context.Context, msg string, fields map[string]any)
	OnError    func(ctx context.Context, err error, fields map[string]any)
}

type managedActiveRun struct {
	runID             string
	policyFingerprint string
}

func (i *managedLarkIntake) HandleLarkEvent(ctx context.Context, event LarkNormalizedEvent) error {
	i.ensureRuntimeInfo(ctx)
	internal := toInternalLarkNormalizedEvent(event)
	i.recordInfo(ctx, "lark.event.received", managedEventFields(internal))
	switch internal.Kind {
	case appintake.EventComment:
		return i.handleComment(ctx, internal)
	case appintake.EventMessage:
		return i.handleMessage(ctx, internal)
	case appintake.EventReconnect, appintake.EventKeepalive, appintake.EventDisconnect:
		return nil
	default:
		return nil
	}
}

func (i *managedLarkIntake) Start(ctx context.Context) error {
	if i == nil || i.infoSource == nil || i.appID == "" {
		return nil
	}
	i.refreshRuntimeOwner(ctx)
	i.ensureRuntimeInfo(ctx)
	return nil
}

func (i *managedLarkIntake) Close() {
	if i == nil {
		return
	}
	i.lifecycleMu.Lock()
	if !i.lifecycleClosed {
		i.lifecycleClosed = true
		if i.lifecycleCancel != nil {
			i.lifecycleCancel()
		}
	}
	i.lifecycleMu.Unlock()
	if i.queue != nil {
		i.queue.Close()
	}
	done := make(chan struct{})
	go func() {
		i.lifecycleWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(i.closeTimeout):
		i.recordError(context.Background(), errors.New("managed lark intake close timed out"), map[string]any{"phase": "managed_close"})
	}
}

func (i *managedLarkIntake) startManagedTask(fn func(context.Context)) bool {
	if i == nil || fn == nil {
		return false
	}
	i.lifecycleMu.Lock()
	defer i.lifecycleMu.Unlock()
	if i.lifecycleClosed {
		return false
	}
	ctx := i.lifecycleCtx
	i.lifecycleWG.Add(1)
	go func() {
		defer i.lifecycleWG.Done()
		fn(ctx)
	}()
	return true
}

func (i *managedLarkIntake) currentCommandOptions() CommandOptions {
	i.commandMu.RLock()
	defer i.commandMu.RUnlock()
	return i.commandOptions
}

func (i *managedLarkIntake) updateCommandOptions(update func(*CommandOptions)) {
	if update == nil {
		return
	}
	i.commandMu.Lock()
	defer i.commandMu.Unlock()
	update(&i.commandOptions)
}

type managedRuntimeConfig struct {
	profile       profile.Config
	replyMode     appimpresenter.ReplyMode
	showToolCalls bool
	cotMessages   appcot.Mode
	idleTimeout   time.Duration
}

func (i *managedLarkIntake) presentationOptions(runtime managedRuntimeConfig, scopeID string) (appimpresenter.ReplyMode, bool, time.Duration, appcot.Mode) {
	timeout := runtime.idleTimeout
	if i.client != nil && i.client.sessions != nil {
		if minutes, ok := i.client.sessions.GetIdleTimeoutMinutes(scopeID); ok {
			if minutes > 0 {
				timeout = time.Duration(minutes) * time.Minute
			} else {
				timeout = 0
			}
		}
	}
	return runtime.replyMode, runtime.showToolCalls, timeout, runtime.cotMessages
}

func (i *managedLarkIntake) legacyRuntimeConfig() managedRuntimeConfig {
	if i == nil || i.client == nil {
		return managedRuntimeConfig{replyMode: appimpresenter.ReplyMarkdown, showToolCalls: true}
	}
	i.presentationMu.RLock()
	replyMode := i.replyMode
	showToolCalls := i.showToolCalls
	cotMessages := i.cotMessages
	i.presentationMu.RUnlock()
	return managedRuntimeConfig{
		profile:       i.client.profile,
		replyMode:     appimpresenter.ReplyMode(replyMode),
		showToolCalls: showToolCalls,
		cotMessages:   cotMessages,
		idleTimeout:   i.currentCommandOptions().GlobalIdleTimeout,
	}
}

func (i *managedLarkIntake) loadRuntimeConfig() (managedRuntimeConfig, error) {
	if i == nil || i.client == nil {
		return managedRuntimeConfig{}, ErrNilClient
	}
	commandOptions := i.currentCommandOptions()
	if commandOptions.ConfigPath == "" {
		return i.legacyRuntimeConfig(), nil
	}
	loadOptions := appconfigstore.LoadOptions{Profile: commandOptions.ProfileName}
	if i.client.profile.AgentKind == profile.AgentClaude {
		loadOptions.AgentKind = appconfigstore.AgentClaude
	} else {
		loadOptions.AgentKind = appconfigstore.AgentCodex
	}
	snapshot, err := appconfigstore.Load(commandOptions.ConfigPath, loadOptions)
	if err != nil {
		return managedRuntimeConfig{}, err
	}
	return managedRuntimeConfigFromStore(snapshot.Profile), nil
}

func managedRuntimeConfigFromStore(prof appconfigstore.ProfileConfig) managedRuntimeConfig {
	return managedRuntimeConfig{
		profile:       managedProfileConfigFromStore(prof),
		replyMode:     appimpresenter.ReplyMode(normalizeLarkReplyMode(LarkReplyMode(managedPreferenceMessageReply(prof.Preferences)))),
		showToolCalls: managedPreferenceShowToolCalls(prof.Preferences),
		cotMessages:   managedCotMessagesSnapshot(managedPreferenceCotMessages(prof.Preferences)),
		idleTimeout:   time.Duration(managedPreferenceRunIdleTimeoutMinutes(prof.Preferences)) * time.Minute,
	}
}

func (i *managedLarkIntake) notifyRuntimeConfigFailure(ctx context.Context, msg appintake.MessageInput, scope appintake.Scope, err error) {
	i.recordError(ctx, err, map[string]any{"phase": "managed_profile_reload", "scope": scope.Key})
	if msg.ChatType != appintake.ChatTypeP2P && !msg.MentionedBot {
		return
	}
	if sendErr := i.sendMarkdown(ctx, msg.ChatID, managedRuntimeConfigFailureMessage, managedReplyOptions(msg, scope)); sendErr != nil {
		i.recordError(ctx, sendErr, map[string]any{"phase": "managed_profile_reload_reply", "scope": scope.Key})
	}
}

func managedProfileConfigFromStore(prof appconfigstore.ProfileConfig) profile.Config {
	return profile.Config{
		SchemaVersion:    prof.SchemaVersion,
		AgentKind:        profile.AgentKind(prof.AgentKind),
		Access:           managedAccessFromStore(prof.Access),
		Workspaces:       profile.Workspaces{Default: prof.Workspaces.Default},
		Permissions:      prof.Permissions,
		PermissionSource: prof.PermissionSource,
		Codex:            managedCodexFromStore(prof.Codex),
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

func managedAccessFromStore(input appconfigstore.ProfileAccess) profile.Access {
	return profile.Access{
		AllowedUsers:          append([]string(nil), input.AllowedUsers...),
		AllowedChats:          append([]string(nil), input.AllowedChats...),
		Admins:                append([]string(nil), input.Admins...),
		RequireMentionInGroup: input.RequireMentionInGroup,
	}
}

func managedCodexFromStore(input *appconfigstore.CodexConfig) *profile.CodexConfig {
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

func (i *managedLarkIntake) ensureRuntimeInfo(ctx context.Context) {
	if i == nil || i.infoSource == nil || i.appID == "" {
		return
	}
	i.infoOnce.Do(func() {
		i.startManagedTask(func(ctx context.Context) {
			i.refreshRuntimeInfo(ctx)
			ticker := time.NewTicker(i.infoInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					i.refreshRuntimeInfo(ctx)
				}
			}
		})
	})
}

func (i *managedLarkIntake) refreshRuntimeInfo(ctx context.Context) {
	i.refreshRuntimeOwner(ctx)
	i.refreshRuntimeKnownChats(ctx)
}

func (i *managedLarkIntake) refreshRuntimeOwner(ctx context.Context) {
	if i == nil || i.infoSource == nil || i.appID == "" {
		return
	}
	ownerID, err := i.infoSource.FetchLarkOwner(ctx, i.appID)
	ownerID = strings.TrimSpace(ownerID)
	if err == nil && ownerID == "" {
		err = errors.New("application owner missing from API response")
	}
	now := time.Now().UnixMilli()
	if err != nil {
		i.updateCommandOptions(func(options *CommandOptions) {
			options.RuntimeControls.OwnerRefreshState = "failed"
			options.RuntimeControls.OwnerRefreshedAt = now
			options.RuntimeControls.OwnerRefreshError = err.Error()
		})
		i.recordError(ctx, err, map[string]any{"phase": "managed_owner_refresh", "appId": i.appID})
	} else {
		i.updateCommandOptions(func(options *CommandOptions) {
			options.RuntimeControls.BotOwnerID = ownerID
			options.RuntimeControls.OwnerRefreshState = "ok"
			options.RuntimeControls.OwnerRefreshedAt = now
			options.RuntimeControls.OwnerRefreshError = ""
		})
		i.recordInfo(ctx, "access.owner_refresh_succeeded", map[string]any{
			"phase":   "managed_owner_refresh",
			"appId":   i.appID,
			"ownerId": ownerID,
		})
	}
}

func (i *managedLarkIntake) refreshRuntimeKnownChats(ctx context.Context) {
	if i == nil || i.infoSource == nil || i.appID == "" {
		return
	}
	chats, err := i.infoSource.ListLarkKnownChats(ctx, 5)
	if err != nil {
		i.recordError(ctx, err, map[string]any{"phase": "managed_known_chats_refresh", "appId": i.appID})
		return
	}
	if len(chats) > 0 {
		i.updateCommandOptions(func(options *CommandOptions) {
			options.KnownChats = managedCommandKnownChats(chats)
		})
	}
}

func (i *managedLarkIntake) refreshRuntimeKnownChatsIfEmpty(ctx context.Context) {
	if i == nil || i.infoSource == nil || i.appID == "" {
		return
	}
	if len(i.currentCommandOptions().KnownChats) > 0 {
		return
	}
	i.refreshRuntimeKnownChats(ctx)
}

func (i *managedLarkIntake) handleComment(ctx context.Context, event appintake.NormalizedEvent) error {
	if i.comments == nil {
		return nil
	}
	if event.Comment == nil {
		return ErrMissingLarkEventPayload
	}
	_, err := i.comments.Handle(ctx, fromInternalLarkCommentInput(*event.Comment))
	if err != nil {
		i.recordError(ctx, err, map[string]any{"phase": "managed_comment", "eventId": event.Comment.EventID})
	}
	return nil
}

func (i *managedLarkIntake) handleMessage(ctx context.Context, event appintake.NormalizedEvent) error {
	if i.client == nil {
		return ErrNilClient
	}
	if event.Message == nil {
		return ErrMissingLarkEventPayload
	}
	msg := *event.Message
	runtimeConfig, err := i.loadRuntimeConfig()
	if err != nil {
		i.notifyRuntimeConfigFailure(ctx, msg, event.Scope, err)
		return nil
	}
	decision := i.messageAccessDecisionWithProfile(msg, runtimeConfig.profile)
	if !decision.OK {
		i.recordInfo(ctx, "lark.message.ignored", managedMessageDecisionFields(msg, decision.Reason))
		if msg.ChatType != appintake.ChatTypeP2P && decision.Reason == AccessDeniedChat && msg.MentionedBot {
			i.sendNonAllowedGroupHint(ctx, msg)
		}
		return nil
	}
	if msg.ChatType != appintake.ChatTypeP2P && runtimeConfig.profile.Access.RequireMentionInGroup && !msg.MentionedBot {
		i.recordInfo(ctx, "lark.message.ignored", managedMessageDecisionFields(msg, "missing-mention"))
		return nil
	}

	i.refreshKnownChatsForCommand(ctx, msg.Content)
	commandOptions := i.currentCommandOptions()
	commandOptions.GlobalIdleTimeout = runtimeConfig.idleTimeout
	response, err := i.client.HandleCommandWithProfile(ctx, CommandRequest{
		CommandText: msg.Content,
		ScopeID:     event.Scope.Key,
		ChatID:      msg.ChatID,
		ThreadID:    msg.ThreadID,
		ActorID:     msg.Sender.OpenID,
		SenderID:    msg.Sender.OpenID,
		ChatMode:    CommandChatMode(event.Scope.ChatMode),
		WorkingDir:  i.cwdFor(event.Scope.Key),
		Access:      decision,
		Mentions:    managedCommandMentions(msg.Mentions),
	}, commandOptions, runtimeConfig.profile)
	if err != nil {
		i.recordError(ctx, err, map[string]any{"phase": "managed_command", "scope": event.Scope.Key})
		return nil
	}
	if response.Handled {
		if i.queue != nil {
			i.queue.Cancel(event.Scope.Key)
		}
		return i.sendCommandResponse(ctx, msg, event.Scope, response)
	}

	if i.queue == nil {
		return i.handleBatch(ctx, appintake.Batch{Scope: event.Scope, Events: []appintake.NormalizedEvent{event}})
	}
	if _, err := i.queue.Push(event); err != nil {
		i.recordError(ctx, err, map[string]any{"phase": "managed_queue", "scope": event.Scope.Key})
	}
	return nil
}

func (i *managedLarkIntake) refreshKnownChatsForCommand(ctx context.Context, content string) {
	if !managedCommandNeedsKnownChats(content) {
		return
	}
	i.refreshRuntimeKnownChatsIfEmpty(ctx)
}

func managedCommandNeedsKnownChats(content string) bool {
	tokens := strings.Fields(strings.TrimSpace(content))
	if len(tokens) == 0 {
		return false
	}
	command := strings.TrimPrefix(strings.ToLower(tokens[0]), "/")
	switch command {
	case "config":
		return true
	case "invite":
		hasAll := false
		hasGroup := false
		for _, token := range tokens[1:] {
			switch strings.ToLower(token) {
			case "all":
				hasAll = true
			case "group":
				hasGroup = true
			}
		}
		return hasAll && hasGroup
	default:
		return false
	}
}

func (i *managedLarkIntake) Dispatch(ctx context.Context, input CardActionDispatchInput) (CardActionDispatchResult, error) {
	if i == nil || i.client == nil {
		return CardActionDispatchResult{}, ErrNilClient
	}
	i.ensureRuntimeInfo(ctx)
	runtimeConfig, err := i.loadRuntimeConfig()
	if err != nil {
		scope := appintake.CardActionScope(toInternalLarkCardActionInput(input))
		message := managedCardActionMessage(input, scope)
		message.MentionedBot = true
		i.notifyRuntimeConfigFailure(ctx, message, scope, err)
		return CardActionDispatchResult{Outcome: CardDispatchRejected, RejectReason: "config-unavailable"}, nil
	}
	decision := i.cardActionAccessDecisionWithProfile(input, runtimeConfig.profile)
	if !decision.OK {
		return CardActionDispatchResult{
			Outcome:      CardDispatchRejected,
			RejectReason: string(decision.Reason),
		}, nil
	}
	if i.shouldDetachCardCommand(input) {
		scope := appintake.CardActionScope(toInternalLarkCardActionInput(input))
		command, args := managedCardActionCommand(input)
		submittedAt := time.Now()
		input = cloneManagedCardActionInput(input)
		if !i.startManagedTask(func(ctx context.Context) {
			i.dispatchDetachedCardCommand(ctx, input, submittedAt, runtimeConfig)
		}) {
			return CardActionDispatchResult{}, context.Canceled
		}
		return CardActionDispatchResult{
			Outcome: CardDispatchCommand,
			Scope:   fromInternalLarkScope(scope),
			Command: command,
			Args:    args,
		}, nil
	}
	return i.dispatchCardActionNow(ctx, input, time.Time{}, runtimeConfig)
}

func cloneManagedCardActionInput(input CardActionDispatchInput) CardActionDispatchInput {
	input.ActionValue = cloneManagedMap(input.ActionValue)
	input.FormValue = cloneManagedMap(input.FormValue)
	return input
}

func cloneManagedMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = cloneManagedValue(value)
	}
	return out
}

func cloneManagedValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneManagedMap(typed)
	case []any:
		out := make([]any, len(typed))
		for idx, item := range typed {
			out[idx] = cloneManagedValue(item)
		}
		return out
	default:
		return typed
	}
}

func (i *managedLarkIntake) dispatchDetachedCardCommand(ctx context.Context, input CardActionDispatchInput, submittedAt time.Time, runtimeConfig managedRuntimeConfig) {
	result, err := i.dispatchCardActionNow(ctx, input, submittedAt, runtimeConfig)
	fields := map[string]any{"phase": "managed_card_action_detached", "messageId": input.MessageID}
	if cmd, _ := managedCardActionCommand(input); cmd != "" {
		fields["command"] = cmd
	}
	if err != nil {
		i.recordError(ctx, err, fields)
		return
	}
	if result.RejectReason != "" {
		fields["rejectReason"] = result.RejectReason
		i.recordError(ctx, errors.New(result.RejectReason), fields)
	}
}

func (i *managedLarkIntake) dispatchCardActionNow(ctx context.Context, input CardActionDispatchInput, submittedAt time.Time, runtimeConfig managedRuntimeConfig) (CardActionDispatchResult, error) {
	commandOptions := i.currentCommandOptions()
	commandOptions.GlobalIdleTimeout = runtimeConfig.idleTimeout
	result, err := i.client.HandleCardAction(ctx, input, CardActionOptions{
		CommandOptions: commandOptions,
		ProfileConfig:  &runtimeConfig.profile,
		CallbackAuth:   i.callbackAuth,
		ActiveRuns:     i,
		Enqueuer: CardPromptEnqueuerFunc(func(ctx context.Context, event LarkNormalizedEvent) error {
			return i.HandleLarkEvent(ctx, event)
		}),
		CarrierThreads: managedCarrierThreadResolver{transport: i.transport},
	})
	if err != nil {
		return result, err
	}
	if result.Outcome == CardDispatchCommand {
		if response, ok := result.CommandResponse.(CommandResponse); ok && response.Handled {
			scope := toInternalLarkScope(result.Scope)
			msg := managedCardActionMessage(input, scope)
			if !submittedAt.IsZero() {
				i.waitCardActionSettle(ctx, submittedAt)
			}
			if sendErr := i.sendCommandResponse(ctx, msg, scope, response); sendErr != nil {
				return result, sendErr
			}
		}
	}
	return result, nil
}

func (i *managedLarkIntake) shouldDetachCardCommand(input CardActionDispatchInput) bool {
	if isManagedSignedCardAction(input.ActionValue) {
		return false
	}
	command, args := managedCardActionCommand(input)
	if command == "config" && strings.HasPrefix(args, "submit") {
		return true
	}
	if command == "account" && strings.HasPrefix(args, "submit") {
		return true
	}
	return false
}

func managedCardActionCommand(input CardActionDispatchInput) (string, string) {
	raw, _ := input.ActionValue["cmd"].(string)
	if raw == "" {
		return "", ""
	}
	parts := strings.Split(raw, ".")
	command := parts[0]
	args := strings.Join(parts[1:], " ")
	return command, args
}

func isManagedSignedCardAction(value map[string]any) bool {
	if value == nil {
		return false
	}
	if _, ok := value[BridgeCardCallbackMarker]; ok {
		return true
	}
	token, _ := value[BridgeCardTokenKey].(string)
	return token != ""
}

func (i *managedLarkIntake) waitCardActionSettle(ctx context.Context, submittedAt time.Time) {
	delay := i.cardSettle - time.Since(submittedAt)
	if delay <= 0 {
		return
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

func (i *managedLarkIntake) handleBatch(ctx context.Context, batch appintake.Batch) error {
	messages := managedBatchMessages(batch.Events)
	if len(messages) == 0 {
		return nil
	}
	first := messages[0]
	last := messages[len(messages)-1]
	runtimeConfig, err := i.loadRuntimeConfig()
	if err != nil {
		i.notifyRuntimeConfigFailure(ctx, first, batch.Scope, err)
		return nil
	}
	decision := i.messageAccessDecisionWithProfile(first, runtimeConfig.profile)
	if !decision.OK {
		if first.ChatType != appintake.ChatTypeP2P && decision.Reason == AccessDeniedChat && first.MentionedBot {
			i.sendNonAllowedGroupHint(ctx, first)
		}
		return nil
	}
	if i.queue != nil {
		i.queue.Block(batch.Scope.Key)
		defer i.queue.Unblock(batch.Scope.Key)
	}
	messages = i.resolveMergedForwardMessages(ctx, batch.Scope, messages)
	attachments, err := i.resolveAttachments(ctx, runtimeConfig.profile, messages)
	if err != nil {
		i.recordError(ctx, err, map[string]any{"phase": "managed_media", "scope": batch.Scope.Key})
		return i.sendMarkdown(ctx, first.ChatID, "暂不支持处理这条消息里的附件。请先发纯文本，或使用支持媒体下载的 Lark transport。", managedReplyOptions(last, batch.Scope))
	}
	quotes := i.resolveQuotedMessages(ctx, batch.Scope, messages)

	run, err := i.client.RunWithProfile(ctx, RunInput{
		ScopeID:     batch.Scope.Key,
		Scope:       managedRunScope(first),
		Prompt:      i.buildMessagePrompt(ctx, messages, attachments, quotes),
		WorkingDir:  i.cwdFor(batch.Scope.Key),
		Access:      decision,
		Attachments: managedRunAttachments(attachments),
	}, runtimeConfig.profile)
	if err != nil {
		if IsRejected(err) {
			return i.sendMarkdown(ctx, first.ChatID, rejectedUserVisible(err), managedReplyOptions(last, batch.Scope))
		}
		i.recordError(ctx, err, map[string]any{"phase": "managed_run", "scope": batch.Scope.Key})
		return nil
	}

	i.registerActiveRun(batch.Scope.Key, run.Metadata())
	defer i.unregisterActiveRun(batch.Scope.Key, run.Metadata().RunID)
	replyMode, showToolCalls, idleTimeout, cotMessages := i.presentationOptions(runtimeConfig, batch.Scope.Key)
	reaction := i.addWorkingReaction(last.MessageID, replyMode, cotMessages)
	defer i.scheduleWorkingReactionCleanup(last.MessageID, reaction)
	_, err = i.presentRun(ctx, managedPresentInput{
		Run:           presenterRun{run: run},
		ChatID:        first.ChatID,
		Options:       toPresenterSendOptions(managedReplyOptions(last, batch.Scope)),
		ReplyMode:     replyMode,
		HideToolCalls: !showToolCalls,
		IdleTimeout:   idleTimeout,
		COTMessages:   cotMessages,
		RunID:         run.Metadata().RunID,
		ScopeID:       batch.Scope.Key,
		OriginMessage: last.MessageID,
		InputPreview:  strings.TrimSpace(last.Content),
		RenderOptions: i.renderOptions(run.Metadata(), first),
	})
	return err
}

func (i *managedLarkIntake) presenterChannel() appimpresenter.Channel {
	base := presenterChannel{transport: i.transport, onInfo: i.recordInfo, onError: i.recordError}
	if _, ok := i.transport.(interface {
		UpdateMessage(context.Context, LarkUpdateMessageRequest) error
	}); ok {
		return presenterStreamingChannel{presenterChannel: base}
	}
	return base
}

type managedPresentInput struct {
	Run           appimpresenter.Run
	ChatID        string
	Options       appimpresenter.SendOptions
	ReplyMode     appimpresenter.ReplyMode
	HideToolCalls bool
	IdleTimeout   time.Duration
	COTMessages   appcot.Mode
	RunID         string
	ScopeID       string
	OriginMessage string
	InputPreview  string
	RenderOptions appcardrender.RenderOptions
}

func (i *managedLarkIntake) presentRun(ctx context.Context, input managedPresentInput) (appcardrender.RunState, error) {
	channel := i.presenterChannel()
	if i == nil || i.cotClient == nil || input.COTMessages == appcot.ModeOff {
		return appimpresenter.Present(ctx, appimpresenter.Input{
			Run:           input.Run,
			Channel:       channel,
			ChatID:        input.ChatID,
			Options:       input.Options,
			ReplyMode:     input.ReplyMode,
			HideToolCalls: input.HideToolCalls,
			IdleTimeout:   input.IdleTimeout,
			RenderOptions: input.RenderOptions,
		})
	}
	publisher := appcot.NewPublisher(appcot.PublisherOptions{
		Client:          i.cotClient,
		ChatID:          input.ChatID,
		OriginMessageID: input.OriginMessage,
		RunID:           input.RunID,
		Scope:           input.ScopeID,
		InputPreview:    input.InputPreview,
	})
	if !publisher.Start(ctx) {
		return appimpresenter.Present(ctx, appimpresenter.Input{
			Run:           input.Run,
			Channel:       channel,
			ChatID:        input.ChatID,
			Options:       input.Options,
			ReplyMode:     input.ReplyMode,
			HideToolCalls: input.HideToolCalls,
			IdleTimeout:   input.IdleTimeout,
			RenderOptions: input.RenderOptions,
		})
	}
	fanoutCtx, cancelFanout := context.WithCancel(ctx)
	defer cancelFanout()
	presenterEvents, cotEvents := splitPresenterEvents(fanoutCtx, input.Run.Events(fanoutCtx))
	cotDone := make(chan struct{})
	go func() {
		defer close(cotDone)
		_ = appcot.ConsumeEvents(fanoutCtx, cotEvents, publisher, input.COTMessages)
	}()
	state, err := appimpresenter.Present(ctx, appimpresenter.Input{
		Run:             presenterEventRun{events: presenterEvents, stopper: input.Run},
		Channel:         channel,
		ChatID:          input.ChatID,
		Options:         input.Options,
		ReplyMode:       input.ReplyMode,
		HideToolCalls:   input.HideToolCalls,
		IdleTimeout:     input.IdleTimeout,
		DeferUntilDone:  true,
		FinalAnswerOnly: true,
		RenderOptions:   input.RenderOptions,
		BeforeFinal: func(ctx context.Context, _ appcardrender.RunState) error {
			select {
			case <-cotDone:
			case <-ctx.Done():
				return ctx.Err()
			}
			if reason := publisher.DegradedReason(); reason != "" {
				return i.sendMarkdown(ctx, input.ChatID, "COT 过程消息更新失败，已停止展示过程；最终答案仍会继续发送。", fromPresenterSendOptions(input.Options))
			}
			return nil
		},
	})
	return state, err
}

func (i *managedLarkIntake) addWorkingReaction(messageID string, replyMode appimpresenter.ReplyMode, cotMessages appcot.Mode) <-chan string {
	if i == nil || i.reactioner == nil || messageID == "" || !managedWorkingReactionEnabled(replyMode, cotMessages) {
		return nil
	}
	out := make(chan string, 1)
	if !i.startManagedTask(func(taskCtx context.Context) {
		defer close(out)
		result, err := i.reactioner.AddMessageReaction(taskCtx, LarkMessageReactionRequest{
			MessageID: messageID,
			EmojiType: "Typing",
		})
		if err != nil || result.ReactionID == "" {
			return
		}
		select {
		case out <- result.ReactionID:
		case <-taskCtx.Done():
		}
	}) {
		close(out)
	}
	return out
}

func managedWorkingReactionEnabled(replyMode appimpresenter.ReplyMode, cotMessages appcot.Mode) bool {
	return cotMessages == appcot.ModeOff && replyMode != appimpresenter.ReplyCard
}

func (i *managedLarkIntake) scheduleWorkingReactionCleanup(messageID string, reaction <-chan string) {
	if i == nil || i.reactioner == nil || messageID == "" || reaction == nil {
		return
	}
	i.startManagedTask(func(ctx context.Context) {
		timer := time.NewTimer(defaultManagedReactionCleanupGrace)
		defer timer.Stop()
		select {
		case reactionID, ok := <-reaction:
			if ok && reactionID != "" {
				_ = i.reactioner.DeleteMessageReaction(ctx, LarkMessageReactionRequest{MessageID: messageID, ReactionID: reactionID})
			}
			return
		case <-timer.C:
		case <-ctx.Done():
			return
		}
		select {
		case reactionID, ok := <-reaction:
			if ok && reactionID != "" {
				_ = i.reactioner.DeleteMessageReaction(ctx, LarkMessageReactionRequest{MessageID: messageID, ReactionID: reactionID})
			}
		case <-ctx.Done():
		}
	})
}

func (i *managedLarkIntake) buildMessagePrompt(ctx context.Context, messages []appintake.MessageInput, attachments []appmedia.NormalizedAttachment, quotes []BridgePromptQuotedMessage) string {
	first := messages[0]
	identity := LarkBotIdentity{}
	if i.transport != nil {
		if got, err := i.transport.BotIdentity(ctx); err == nil {
			identity = got
		}
	}
	return BuildAgentPrompt(BuildAgentPromptInput{
		Context: BridgePromptContext{
			ChatID:     first.ChatID,
			ChatType:   string(first.ChatType),
			SenderID:   first.Sender.OpenID,
			SenderName: first.Sender.Name,
			SenderType: managedPromptSenderType(first.SenderType),
			BotOpenID:  identity.OpenID,
			ThreadID:   first.ThreadID,
			MessageIDs: managedMessageIDs(messages),
			Mentions:   managedPromptMentions(messages, identity.OpenID),
			Source:     BridgePromptSourceIM,
		},
		UserInput:        managedUserInput(messages),
		QuotedMessages:   quotes,
		InteractiveCards: managedPromptInteractiveCards(messages),
		Attachments:      managedPromptAttachments(attachments),
	})
}

func (i *managedLarkIntake) resolveAttachments(ctx context.Context, profileConfig profile.Config, messages []appintake.MessageInput) ([]appmedia.NormalizedAttachment, error) {
	requests := managedResourceRequests(messages)
	if len(requests) == 0 {
		return nil, nil
	}
	if i.media == nil {
		return nil, errors.New("media downloader is required")
	}
	return i.media.Resolve(ctx, requests, appmedia.ResolveOptionsFromProfile(profileConfig.Attachments))
}

func (i *managedLarkIntake) renderOptions(metadata RunMetadata, first appintake.MessageInput) appcardrender.RenderOptions {
	if i.callbackAuth == nil || metadata.RunID == "" || metadata.ScopeID == "" || metadata.PolicyFingerprint == "" || first.Sender.OpenID == "" {
		return appcardrender.RenderOptions{}
	}
	return appcardrender.RenderOptions{
		SignCallback: func(action string) string {
			token, err := i.callbackAuth.Sign(CallbackSignInput{
				RunID:             metadata.RunID,
				Scope:             metadata.ScopeID,
				ChatID:            first.ChatID,
				OperatorOpenID:    first.Sender.OpenID,
				Action:            action,
				PolicyFingerprint: metadata.PolicyFingerprint,
				TTL:               i.callbackTTL,
			})
			if err != nil {
				i.recordError(context.Background(), err, map[string]any{"phase": "managed_callback_sign", "scope": metadata.ScopeID, "action": action})
				return ""
			}
			return token
		},
	}
}

func (i *managedLarkIntake) registerActiveRun(scope string, metadata RunMetadata) {
	if i == nil || scope == "" || metadata.RunID == "" {
		return
	}
	i.activeMu.Lock()
	defer i.activeMu.Unlock()
	if i.activeRuns == nil {
		i.activeRuns = make(map[string]managedActiveRun)
	}
	i.activeRuns[scope] = managedActiveRun{
		runID:             metadata.RunID,
		policyFingerprint: metadata.PolicyFingerprint,
	}
}

func (i *managedLarkIntake) unregisterActiveRun(scope string, runID string) {
	if i == nil || scope == "" {
		return
	}
	i.activeMu.Lock()
	defer i.activeMu.Unlock()
	if current, ok := i.activeRuns[scope]; ok && (runID == "" || current.runID == runID) {
		delete(i.activeRuns, scope)
	}
}

func (i *managedLarkIntake) ActiveRun(_ context.Context, scope string) (CardActiveRun, bool, error) {
	if i == nil {
		return CardActiveRun{}, false, nil
	}
	i.activeMu.Lock()
	defer i.activeMu.Unlock()
	current, ok := i.activeRuns[scope]
	if !ok {
		return CardActiveRun{}, false, nil
	}
	return CardActiveRun{
		RunID:             current.runID,
		PolicyFingerprint: current.policyFingerprint,
	}, true, nil
}

func (i *managedLarkIntake) resolveQuotedMessages(ctx context.Context, scope appintake.Scope, messages []appintake.MessageInput) []BridgePromptQuotedMessage {
	if i.quoteResolver == nil {
		return nil
	}
	batchIDs := make(map[string]struct{}, len(messages))
	for _, msg := range messages {
		if msg.MessageID != "" {
			batchIDs[msg.MessageID] = struct{}{}
		}
	}
	seen := map[string]struct{}{}
	out := []BridgePromptQuotedMessage{}
	for _, msg := range messages {
		targetID := managedReplyQuoteTarget(msg, scope)
		if targetID == "" {
			continue
		}
		if _, ok := batchIDs[targetID]; ok {
			continue
		}
		if _, ok := seen[targetID]; ok {
			continue
		}
		seen[targetID] = struct{}{}
		quote, ok, err := i.quoteResolver.ResolveLarkQuote(ctx, LarkQuoteTarget{
			MessageID:       targetID,
			ChatID:          msg.ChatID,
			ChatType:        LarkChatType(msg.ChatType),
			ResolvedMode:    LarkChatMode(msg.ResolvedMode),
			ThreadID:        msg.ThreadID,
			RootID:          msg.RootID,
			ParentID:        msg.ParentID,
			SourceMessageID: msg.MessageID,
		})
		if err != nil {
			i.recordError(ctx, err, map[string]any{"phase": "managed_quote", "messageId": targetID, "scope": scope.Key})
			continue
		}
		if ok {
			out = append(out, quote)
		}
	}
	return out
}

func (i *managedLarkIntake) resolveMergedForwardMessages(ctx context.Context, scope appintake.Scope, messages []appintake.MessageInput) []appintake.MessageInput {
	if i.quoteResolver == nil {
		return messages
	}
	out := append([]appintake.MessageInput(nil), messages...)
	for idx := range out {
		msg := &out[idx]
		if msg.RawContentType != "merge_forward" || msg.MessageID == "" {
			continue
		}
		forwarded, ok, err := i.quoteResolver.ResolveLarkQuote(ctx, LarkQuoteTarget{
			MessageID:       msg.MessageID,
			ChatID:          msg.ChatID,
			ChatType:        LarkChatType(msg.ChatType),
			ResolvedMode:    LarkChatMode(msg.ResolvedMode),
			ThreadID:        msg.ThreadID,
			RootID:          msg.RootID,
			ParentID:        msg.ParentID,
			SourceMessageID: msg.MessageID,
		})
		if err != nil {
			i.recordError(ctx, err, map[string]any{"phase": "managed_merge_forward", "messageId": msg.MessageID, "scope": scope.Key})
			continue
		}
		if ok && strings.TrimSpace(forwarded.Content) != "" {
			msg.Content = forwarded.Content
		}
	}
	return out
}

func (i *managedLarkIntake) messageAccessDecisionWithProfile(msg appintake.MessageInput, profileConfig profile.Config) AccessDecision {
	commandOptions := i.currentCommandOptions()
	controls := toInternalRuntimeControls(commandOptions.RuntimeControls)
	var decision access.Decision
	if msg.ChatType == appintake.ChatTypeP2P {
		decision = access.CanUseDM(profileConfig, controls, msg.Sender.OpenID)
	} else {
		decision = access.CanUseGroup(profileConfig, controls, msg.ChatID, msg.Sender.OpenID)
	}
	return fromInternalAccessDecision(decision)
}

func (i *managedLarkIntake) cardActionAccessDecisionWithProfile(input CardActionDispatchInput, profileConfig profile.Config) AccessDecision {
	commandOptions := i.currentCommandOptions()
	controls := toInternalRuntimeControls(commandOptions.RuntimeControls)
	if input.ChatType == LarkChatTypeP2P || input.ResolvedMode == LarkChatModeP2P {
		return fromInternalAccessDecision(access.CanUseDM(profileConfig, controls, input.Operator.OpenID))
	}
	return fromInternalAccessDecision(access.CanUseGroup(profileConfig, controls, input.ChatID, input.Operator.OpenID))
}

func managedCardActionMessage(input CardActionDispatchInput, scope appintake.Scope) appintake.MessageInput {
	chatType := appintake.ChatType(input.ChatType)
	if chatType == "" {
		if scope.ChatMode == appintake.ChatModeP2P {
			chatType = appintake.ChatTypeP2P
		} else {
			chatType = appintake.ChatTypeGroup
		}
	}
	return appintake.MessageInput{
		MessageID:      input.MessageID,
		ChatID:         input.ChatID,
		ChatType:       chatType,
		ResolvedMode:   scope.ChatMode,
		ThreadID:       scope.ThreadID,
		Sender:         toInternalLarkActor(input.Operator),
		RawContentType: "card_action",
		CreateTime:     input.CreateTime,
	}
}

func (i *managedLarkIntake) sendCommandResponse(ctx context.Context, msg appintake.MessageInput, scope appintake.Scope, response CommandResponse) error {
	if i.shouldSendAccountRetryForm(msg, response) {
		return i.sendAccountFailureRetryForm(ctx, msg, scope, response.Account)
	}
	var err error
	if card := i.commandResponseCard(ctx, response); card != nil {
		err = i.sendCommandCardResponse(ctx, msg, scope, card)
	} else if !response.NoReply && strings.TrimSpace(response.Markdown) != "" {
		err = i.sendMarkdown(ctx, msg.ChatID, response.Markdown, managedReplyOptions(msg, scope))
	}
	i.scheduleAccountReconnect(msg, response)
	i.scheduleGroupMsgScopeGrant(msg, response)
	if err != nil {
		return err
	}
	return nil
}

func (i *managedLarkIntake) shouldSendAccountRetryForm(msg appintake.MessageInput, response CommandResponse) bool {
	return msg.RawContentType == "card_action" &&
		msg.MessageID != "" &&
		response.Kind == CommandResponseAccount &&
		response.Account != nil &&
		response.Account.Action == "submit" &&
		response.Account.Failure != ""
}

func (i *managedLarkIntake) sendAccountFailureRetryForm(ctx context.Context, msg appintake.MessageInput, scope appintake.Scope, view *CommandAccountView) error {
	if i.transport == nil {
		return ErrNilLarkTransport
	}
	failureCard := appcardkit.AccountFailureCard(view.Failure)
	if err := i.transport.UpdateCard(ctx, LarkUpdateCardRequest{MessageID: msg.MessageID, Card: failureCard}); err != nil {
		i.recordError(ctx, err, map[string]any{"phase": "managed_account_failure_card_update", "messageId": msg.MessageID})
	}
	retryCard := appcardkit.AccountFormCard(appcardkit.AccountFormOptions{
		InitialTenant: appcardkit.TenantBrand(view.Tenant),
		PrefillAppID:  view.AppID,
	})
	_, err := i.transport.SendCard(ctx, LarkSendCardRequest{
		ChatID:  msg.ChatID,
		Card:    retryCard,
		Options: managedReplyOptions(msg, scope),
	})
	return err
}

func (i *managedLarkIntake) scheduleAccountReconnect(msg appintake.MessageInput, response CommandResponse) {
	if i == nil || response.Kind != CommandResponseAccount || response.Account == nil || !response.Account.Saved || response.Account.Failure != "" {
		return
	}
	commandOptions := i.currentCommandOptions()
	reconnector := commandOptions.Reconnector
	if reconnector == nil {
		return
	}
	key := i.accountReconnectKey(msg, response)
	now := time.Now()
	i.accountReconnectMu.Lock()
	for seenKey, seenAt := range i.accountReconnectSeen {
		if now.Sub(seenAt) > managedAccountReconnectDedupTTL {
			delete(i.accountReconnectSeen, seenKey)
		}
	}
	if key != "" {
		if seenAt, ok := i.accountReconnectSeen[key]; ok && now.Sub(seenAt) <= managedAccountReconnectDedupTTL {
			i.accountReconnectMu.Unlock()
			return
		}
		i.accountReconnectSeen[key] = now
	}
	if i.accountReconnectPending {
		i.accountReconnectMu.Unlock()
		return
	}
	i.accountReconnectPending = true
	i.accountReconnectMu.Unlock()

	delay := i.accountRestart
	if !i.startManagedTask(func(ctx context.Context) {
		defer i.clearAccountReconnectPending()
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if err := reconnector.Restart(ctx, false); err != nil {
			i.recordError(ctx, err, map[string]any{"phase": "managed_account_reconnect"})
		}
	}) {
		i.clearAccountReconnectPending()
	}
}

func (i *managedLarkIntake) accountReconnectKey(msg appintake.MessageInput, response CommandResponse) string {
	if response.Account == nil {
		return ""
	}
	appID := response.Account.AppID
	if appID == "" {
		appID = i.appID
	}
	if appID == "" || msg.MessageID == "" {
		return ""
	}
	return appID + "\x00" + msg.MessageID
}

func (i *managedLarkIntake) clearAccountReconnectPending() {
	i.accountReconnectMu.Lock()
	i.accountReconnectPending = false
	i.accountReconnectMu.Unlock()
}

func (i *managedLarkIntake) scheduleGroupMsgScopeGrant(msg appintake.MessageInput, response CommandResponse) {
	if i == nil || i.transport == nil || i.scopeChecker == nil || i.scopeGrant == nil || i.appID == "" || response.Kind != CommandResponseConfig || response.Config == nil || !response.Config.Saved || response.Config.Failure != "" || response.Config.Snapshot.RequireMentionInGroup {
		return
	}
	chatID := msg.ChatID
	if chatID == "" {
		return
	}
	i.startManagedTask(func(ctx context.Context) {
		i.promptGroupMsgScopeIfMissing(ctx, chatID)
	})
}

func (i *managedLarkIntake) promptGroupMsgScopeIfMissing(ctx context.Context, chatID string) {
	has, err := i.scopeChecker.HasLarkScope(ctx, i.appID, LarkGroupMsgScope)
	if err != nil {
		i.recordError(ctx, err, map[string]any{"phase": "managed_group_msg_scope_check", "appId": i.appID})
		return
	}
	if has {
		return
	}
	link, err := i.scopeGrant.RequestLarkScopeGrant(ctx, LarkScopeGrantRequest{
		AppID:        i.appID,
		TenantScopes: []string{LarkGroupMsgScope},
	})
	if err != nil {
		i.recordError(ctx, err, map[string]any{"phase": "managed_group_msg_scope_grant_link", "appId": i.appID})
		return
	}
	if strings.TrimSpace(link.URL) == "" {
		cancelScopeGrantLink(link)
		i.recordError(ctx, errors.New("scope grant link is empty"), map[string]any{"phase": "managed_group_msg_scope_grant_link", "appId": i.appID})
		return
	}
	expireMins := int(link.ExpiresIn.Minutes() + 0.5)
	if expireMins < 1 {
		expireMins = 1
	}
	result, err := i.transport.SendCard(ctx, LarkSendCardRequest{
		ChatID: chatID,
		Card:   appcardkit.GroupMsgScopeGrantCard(link.URL, expireMins),
	})
	if err != nil {
		cancelScopeGrantLink(link)
		i.recordError(ctx, err, map[string]any{"phase": "managed_group_msg_scope_grant_card", "appId": i.appID})
		return
	}
	if link.Wait == nil || result.MessageID == "" {
		if result.MessageID == "" {
			cancelScopeGrantLink(link)
		}
		return
	}
	i.waitGroupMsgScopeGrant(ctx, link, result.MessageID)
}

func cancelScopeGrantLink(link LarkScopeGrantLink) {
	if link.Cancel != nil {
		link.Cancel()
	}
}

func (i *managedLarkIntake) waitGroupMsgScopeGrant(parent context.Context, link LarkScopeGrantLink, messageID string) {
	ctx := parent
	cancel := func() {}
	if link.ExpiresIn > 0 {
		ctx, cancel = context.WithTimeout(parent, link.ExpiresIn+time.Minute)
	} else {
		ctx, cancel = context.WithCancel(parent)
	}
	defer cancel()
	if err := link.Wait(ctx); err != nil {
		i.recordError(ctx, err, map[string]any{"phase": "managed_group_msg_scope_grant_wait", "appId": i.appID})
		return
	}
	if err := i.transport.UpdateCard(ctx, LarkUpdateCardRequest{MessageID: messageID, Card: appcardkit.GroupMsgScopeGrantedCard()}); err != nil {
		i.recordError(ctx, err, map[string]any{"phase": "managed_group_msg_scope_granted_card", "appId": i.appID, "messageId": messageID})
	}
}

func (i *managedLarkIntake) sendCommandCardResponse(ctx context.Context, msg appintake.MessageInput, scope appintake.Scope, card map[string]any) error {
	if i.transport == nil {
		return ErrNilLarkTransport
	}
	if msg.RawContentType == "card_action" && msg.MessageID != "" {
		if err := i.transport.UpdateCard(ctx, LarkUpdateCardRequest{MessageID: msg.MessageID, Card: card}); err == nil {
			return nil
		} else {
			i.recordError(ctx, err, map[string]any{"phase": "managed_command_card_update", "messageId": msg.MessageID})
		}
	}
	_, err := i.transport.SendCard(ctx, LarkSendCardRequest{
		ChatID:  msg.ChatID,
		Card:    card,
		Options: managedReplyOptions(msg, scope),
	})
	return err
}

func (i *managedLarkIntake) commandResponseCard(ctx context.Context, response CommandResponse) map[string]any {
	switch response.Kind {
	case CommandResponseConfig:
		if response.Config == nil {
			return nil
		}
		return i.configResponseCard(ctx, response.Config)
	case CommandResponseAccount:
		if response.Account == nil {
			return nil
		}
		return accountResponseCard(response.Account)
	default:
		return nil
	}
}

func (i *managedLarkIntake) configResponseCard(ctx context.Context, view *CommandConfigView) map[string]any {
	opts, err := i.configCardOptions(ctx, view)
	if err != nil {
		i.recordError(ctx, err, map[string]any{"phase": "managed_config_card"})
		return appcardkit.ConfigFailedCard(managedRuntimeConfigFailureMessage)
	}
	switch {
	case view.Failure != "":
		return appcardkit.ConfigFailedCard(view.Failure)
	case view.Unsupported:
		return appcardkit.ConfigFailedCard("Go SDK /config submit 需要 ConfigPath 才能保存配置。")
	case view.Saved:
		return appcardkit.ConfigSavedCard(opts)
	case view.Action == "cancel":
		return appcardkit.ConfigCancelledCard()
	default:
		return appcardkit.ConfigFormCard(opts)
	}
}

func (i *managedLarkIntake) configCardOptions(ctx context.Context, view *CommandConfigView) (appcardkit.ConfigFormOptions, error) {
	snapshot := view.Snapshot
	i.refreshRuntimeKnownChatsIfEmpty(ctx)
	commandOptions := i.currentCommandOptions()
	opts := appcardkit.ConfigFormOptions{
		MessageReply:          appcardkit.MessageReplyMode(snapshot.MessageReply),
		ShowToolCalls:         snapshot.ShowToolCalls,
		CotMessages:           appcardkit.CotMessagesMode(snapshot.CotMessages),
		MaxConcurrentRuns:     snapshot.MaxConcurrentRuns,
		RunIdleTimeoutMinutes: snapshot.RunIdleTimeoutMinutes,
		RequireMentionInGroup: snapshot.RequireMentionInGroup,
		LarkCLIIdentity:       appcardkit.LarkCLIIdentityPreset(snapshot.LarkCLIIdentity),
		KnownChats:            managedKnownChats(commandOptions.KnownChats),
	}
	if i != nil && i.client != nil {
		runtimeConfig, err := i.loadRuntimeConfig()
		if err != nil {
			return opts, err
		}
		opts.AllowedUsers = append([]string(nil), runtimeConfig.profile.Access.AllowedUsers...)
		opts.AllowedChats = append([]string(nil), runtimeConfig.profile.Access.AllowedChats...)
		opts.Admins = append([]string(nil), runtimeConfig.profile.Access.Admins...)
	}
	return opts, nil
}

func accountResponseCard(view *CommandAccountView) map[string]any {
	info := appcardkit.CurrentInfo{
		AppID:   view.AppID,
		BotName: view.BotName,
		Tenant:  appcardkit.TenantBrand(view.Tenant),
	}
	switch {
	case view.Failure != "":
		return appcardkit.AccountFailureCard(view.Failure)
	case view.Unsupported:
		return appcardkit.AccountFailureCard("Go SDK /account submit 需要 ConfigPath 和 Keystore 才能保存凭据。")
	case view.Saved || view.Action == "submit":
		return appcardkit.AccountSuccessCard(info)
	case view.Action == "form":
		return appcardkit.AccountFormCard(appcardkit.AccountFormOptions{
			InitialTenant: appcardkit.TenantBrand(view.Tenant),
			PrefillAppID:  view.AppID,
		})
	case view.Action == "cancel":
		return appcardkit.AccountCancelledCard()
	default:
		return appcardkit.AccountCurrentCard(info)
	}
}

func managedKnownChats(input []CommandKnownChat) []appcardkit.KnownChat {
	out := make([]appcardkit.KnownChat, 0, len(input))
	for _, chat := range input {
		out = append(out, appcardkit.KnownChat{ID: chat.ID, Name: chat.Name})
	}
	return out
}

func managedCommandKnownChats(input []LarkKnownChatInfo) []CommandKnownChat {
	out := make([]CommandKnownChat, 0, len(input))
	for _, chat := range input {
		out = append(out, CommandKnownChat{ID: chat.ID, Name: chat.Name})
	}
	return out
}

func (i *managedLarkIntake) sendNonAllowedGroupHint(ctx context.Context, msg appintake.MessageInput) {
	text := "当前群尚未加入响应列表，所以 bot 不会处理消息。\nBot owner/管理员可在本群发 /invite group 加入白名单。"
	if err := i.sendText(ctx, msg.ChatID, text, LarkSendOptions{ReplyTo: msg.MessageID}); err != nil {
		i.recordError(ctx, err, map[string]any{"phase": "managed_denied_hint", "chatId": msg.ChatID})
		_ = i.sendText(ctx, msg.ChatID, text, LarkSendOptions{})
	}
}

func (i *managedLarkIntake) sendText(ctx context.Context, chatID string, text string, opts LarkSendOptions) error {
	if i.transport == nil {
		return ErrNilLarkTransport
	}
	_, err := i.transport.SendMessage(ctx, LarkSendMessageRequest{
		ChatID: chatID,
		Content: LarkMessageContent{
			Text: text,
		},
		Options: opts,
	})
	return err
}

func (i *managedLarkIntake) sendMarkdown(ctx context.Context, chatID string, markdown string, opts LarkSendOptions) error {
	if i.transport == nil {
		return ErrNilLarkTransport
	}
	_, err := i.transport.SendMessage(ctx, LarkSendMessageRequest{
		ChatID: chatID,
		Content: LarkMessageContent{
			Markdown: markdown,
		},
		Options: opts,
	})
	return err
}

func (i *managedLarkIntake) cwdFor(scopeID string) string {
	if i.workspaces == nil {
		return ""
	}
	return i.workspaces.CWDFor(scopeID)
}

func (i *managedLarkIntake) recordError(ctx context.Context, err error, fields map[string]any) {
	if i.onError != nil {
		i.onError(ctx, err, fields)
	}
}

func (i *managedLarkIntake) recordInfo(ctx context.Context, msg string, fields map[string]any) {
	if i.onInfo != nil {
		i.onInfo(ctx, msg, fields)
	}
}

func managedEventFields(event appintake.NormalizedEvent) map[string]any {
	fields := map[string]any{
		"phase": "lark_event",
		"kind":  string(event.Kind),
	}
	if event.Scope.Key != "" {
		fields["scope"] = event.Scope.Key
	}
	if event.Scope.Source != "" {
		fields["source"] = string(event.Scope.Source)
	}
	if event.Scope.ChatID != "" {
		fields["chatId"] = event.Scope.ChatID
	}
	if event.Scope.ChatType != "" {
		fields["chatType"] = string(event.Scope.ChatType)
	}
	if event.Scope.ChatMode != "" {
		fields["chatMode"] = string(event.Scope.ChatMode)
	}
	if event.Scope.ThreadID != "" {
		fields["threadId"] = event.Scope.ThreadID
	}
	if event.Scope.ActorID != "" {
		fields["actorId"] = event.Scope.ActorID
	}
	if event.Message != nil {
		fields["messageId"] = event.Message.MessageID
		fields["senderId"] = event.Message.Sender.OpenID
		if event.Message.SenderType != "" {
			fields["senderType"] = string(event.Message.SenderType)
		}
		fields["mentionedBot"] = event.Message.MentionedBot
	}
	if event.Self.Drop {
		fields["selfLoop"] = true
		fields["selfLoopReason"] = string(event.Self.Reason)
	}
	if event.Reconnect != nil {
		fields["reconnectPhase"] = string(event.Reconnect.Phase)
		if event.Reconnect.Error != "" {
			fields["reconnectError"] = event.Reconnect.Error
		}
	}
	if event.Keepalive != nil {
		fields["connectionState"] = string(event.Keepalive.State)
		fields["networkReachable"] = event.Keepalive.NetworkReachable
	}
	if event.Disconnect != nil {
		fields["disconnectReason"] = event.Disconnect.Reason
	}
	return fields
}

func managedMessageDecisionFields(msg appintake.MessageInput, reason any) map[string]any {
	fields := map[string]any{
		"phase":        "lark_event",
		"kind":         string(appintake.EventMessage),
		"messageId":    msg.MessageID,
		"chatId":       msg.ChatID,
		"chatType":     string(msg.ChatType),
		"senderId":     msg.Sender.OpenID,
		"mentionedBot": msg.MentionedBot,
		"reason":       fmt.Sprint(reason),
	}
	if msg.ResolvedMode != "" {
		fields["chatMode"] = string(msg.ResolvedMode)
	}
	if msg.ThreadID != "" {
		fields["threadId"] = msg.ThreadID
	}
	if msg.SenderType != "" {
		fields["senderType"] = string(msg.SenderType)
	}
	return fields
}

func managedBatchMessages(events []appintake.NormalizedEvent) []appintake.MessageInput {
	out := make([]appintake.MessageInput, 0, len(events))
	for _, event := range events {
		if event.Kind == appintake.EventMessage && event.Message != nil {
			out = append(out, *event.Message)
		}
	}
	return out
}

func managedRunScope(msg appintake.MessageInput) Scope {
	return Scope{
		Source:   SourceIM,
		ChatID:   msg.ChatID,
		ThreadID: msg.ThreadID,
		ActorID:  msg.Sender.OpenID,
	}
}

func managedReplyOptions(msg appintake.MessageInput, scope appintake.Scope) LarkSendOptions {
	opts := LarkSendOptions{
		ReplyTo: msg.MessageID,
		Metadata: map[string]any{
			"bridgeReply": true,
			"source":      "lark-channel-bridge",
		},
	}
	if scope.ChatMode == appintake.ChatModeTopic && msg.ThreadID != "" {
		opts.ReplyInThread = true
		opts.ThreadID = msg.ThreadID
	}
	return opts
}

func managedReplyQuoteTarget(msg appintake.MessageInput, scope appintake.Scope) string {
	replyTo := msg.ReplyToMessageID
	if replyTo == "" {
		replyTo = msg.ParentID
	}
	if replyTo == "" {
		return ""
	}
	isTopic := scope.ChatMode == appintake.ChatModeTopic || msg.ResolvedMode == appintake.ChatModeTopic || msg.ThreadID != ""
	if isTopic && msg.ThreadID != "" && msg.RootID != "" && replyTo == msg.RootID {
		return ""
	}
	return replyTo
}

func managedUserInput(messages []appintake.MessageInput) string {
	annotate := len(messages) > 1
	fileKeys := managedMessageFileKeys(messages)
	parts := make([]string, 0, len(messages))
	for _, msg := range messages {
		text := strings.TrimSpace(managedStripAttachmentRefs(msg.Content, fileKeys))
		if text == "" {
			continue
		}
		if annotate {
			text = managedSenderAnnotation(msg) + " " + text
		}
		parts = append(parts, text)
	}
	if len(parts) > 0 {
		return strings.Join(parts, "\n\n")
	}
	for _, msg := range messages {
		if len(msg.Resources) > 0 {
			return "请看下面的附件。"
		}
	}
	return "（对方发来一条没有正文的消息——通常是只 @ 了你的唤醒（ping）。请简短回应。）"
}

func managedSenderAnnotation(msg appintake.MessageInput) string {
	name := msg.Sender.Name
	if name == "" {
		name = msg.Sender.OpenID
	}
	senderType := managedPromptSenderType(msg.SenderType)
	if senderType != "" {
		return "[" + name + " (" + string(senderType) + ")]:"
	}
	return "[" + name + "]:"
}

func managedPromptSenderType(senderType appintake.SenderType) BridgePromptSenderType {
	switch senderType {
	case appintake.SenderTypeUser:
		return BridgePromptSenderUser
	case appintake.SenderTypeBot:
		return BridgePromptSenderBot
	default:
		return ""
	}
}

func managedMessageFileKeys(messages []appintake.MessageInput) []string {
	out := []string{}
	for _, msg := range messages {
		for _, resource := range msg.Resources {
			if resource.ID != "" {
				out = append(out, resource.ID)
			}
		}
	}
	return out
}

func managedStripAttachmentRefs(text string, fileKeys []string) string {
	if text == "" || len(fileKeys) == 0 {
		return text
	}
	out := text
	for _, key := range fileKeys {
		escaped := regexp.QuoteMeta(key)
		out = regexp.MustCompile(`!?\[[^\]]*\]\(`+escaped+`\)`).ReplaceAllString(out, "")
		out = regexp.MustCompile(`(?i)<\s*(?:file|image|img|audio|video|media|folder)\b[^>]*\bkey\s*=\s*["']`+escaped+`["'][^>]*>`).ReplaceAllString(out, "")
	}
	return regexp.MustCompile(`\n{3,}`).ReplaceAllString(out, "\n\n")
}

func managedMessageIDs(messages []appintake.MessageInput) []string {
	out := make([]string, 0, len(messages))
	for _, msg := range messages {
		if msg.MessageID != "" {
			out = append(out, msg.MessageID)
		}
	}
	return out
}

func managedPromptInteractiveCards(messages []appintake.MessageInput) []BridgePromptInteractiveCard {
	out := []BridgePromptInteractiveCard{}
	for _, msg := range messages {
		if msg.RawContentType != "interactive" {
			continue
		}
		content, ok := managedInteractiveCardContent(msg)
		if !ok {
			continue
		}
		out = append(out, BridgePromptInteractiveCard{
			MessageID: msg.MessageID,
			Content:   content,
		})
	}
	return out
}

func managedInteractiveCardContent(msg appintake.MessageInput) (any, bool) {
	for _, value := range []any{
		msg.RawContent,
		msg.Metadata["rawContent"],
		msg.Metadata["raw_content"],
		msg.Metadata["content"],
		managedNestedMetadata(msg.Metadata, "raw", "event", "message", "content"),
		managedNestedMetadata(msg.Metadata, "raw", "message", "content"),
		managedNestedMetadata(msg.Metadata, "raw", "content"),
	} {
		if value == nil {
			continue
		}
		switch typed := value.(type) {
		case string:
			if typed == "" {
				continue
			}
		case []byte:
			if len(typed) == 0 {
				continue
			}
		}
		return managedParseJSONOrRaw(value), true
	}
	return nil, false
}

func managedNestedMetadata(metadata map[string]any, path ...string) any {
	var current any = metadata
	for _, key := range path {
		m, ok := current.(map[string]any)
		if !ok {
			payload, err := json.Marshal(current)
			if err != nil {
				return nil
			}
			if err := json.Unmarshal(payload, &m); err != nil {
				return nil
			}
		}
		current = m[key]
		if current == nil {
			return nil
		}
	}
	return current
}

func managedParseJSONOrRaw(value any) any {
	text, ok := value.(string)
	if !ok {
		return value
	}
	if strings.TrimSpace(text) == "" {
		return text
	}
	var parsed any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		return text
	}
	return parsed
}

func managedPromptMentions(messages []appintake.MessageInput, botOpenID string) []BridgePromptMention {
	seen := map[string]struct{}{}
	out := []BridgePromptMention{}
	for _, msg := range messages {
		for _, mention := range msg.Mentions {
			key := managedMentionDedupeKey(mention)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			promptMention := BridgePromptMention{
				OpenID: mention.OpenID,
				Name:   mention.Name,
			}
			if mention.IsBot != nil {
				isBot := *mention.IsBot
				promptMention.IsBot = &isBot
			} else if botOpenID != "" && mention.OpenID == botOpenID {
				isBot := true
				promptMention.IsBot = &isBot
			}
			out = append(out, promptMention)
		}
	}
	return out
}

func managedMentionDedupeKey(mention appintake.Mention) string {
	if mention.OpenID != "" {
		return mention.OpenID
	}
	return mention.Name + ":" + mention.Key
}

func managedCommandMentions(mentions []appintake.Mention) []CommandMention {
	out := make([]CommandMention, 0, len(mentions))
	for _, mention := range mentions {
		isBot := mention.IsBot != nil && *mention.IsBot
		out = append(out, CommandMention{
			OpenID: mention.OpenID,
			Name:   mention.Name,
			IsBot:  isBot,
		})
	}
	return out
}

func managedResourceRequests(messages []appintake.MessageInput) []appmedia.ResourceRequest {
	var out []appmedia.ResourceRequest
	for _, msg := range messages {
		for _, resource := range msg.Resources {
			if resource.ID == "" {
				continue
			}
			out = append(out, appmedia.ResourceRequest{
				MessageID: msg.MessageID,
				Resource: appmedia.ResourceDescriptor{
					Type:         managedAttachmentKind(resource.Kind),
					FileKey:      resource.ID,
					FileName:     resource.Name,
					Requiredness: appmedia.AttachmentOptional,
				},
			})
		}
	}
	return out
}

func managedAttachmentKind(kind string) appmedia.AttachmentKind {
	switch strings.ToLower(kind) {
	case string(appmedia.AttachmentKindImage), "img":
		return appmedia.AttachmentKindImage
	case string(appmedia.AttachmentKindAudio):
		return appmedia.AttachmentKindAudio
	case string(appmedia.AttachmentKindVideo):
		return appmedia.AttachmentKindVideo
	case string(appmedia.AttachmentKindSticker):
		return appmedia.AttachmentKindSticker
	default:
		return appmedia.AttachmentKindFile
	}
}

func managedRunAttachments(attachments []appmedia.NormalizedAttachment) []Attachment {
	out := make([]Attachment, 0, len(attachments))
	for _, attachment := range attachments {
		policyAttachment := appmedia.ToPolicyAttachment(attachment)
		out = append(out, Attachment{
			Kind:            policyAttachment.Kind,
			Requiredness:    AttachmentRequiredness(policyAttachment.Requiredness),
			Decision:        AttachmentDecision(policyAttachment.Decision),
			RejectionReason: policyAttachment.RejectionReason,
			OriginalName:    policyAttachment.OriginalName,
			Size:            policyAttachment.Size,
			Hash:            policyAttachment.Hash,
			Path:            policyAttachment.Path,
		})
	}
	return out
}

func managedPromptAttachments(attachments []appmedia.NormalizedAttachment) []BridgePromptAttachment {
	out := make([]BridgePromptAttachment, 0, len(attachments))
	for _, attachment := range attachments {
		promptAttachment := appmedia.ToPromptAttachment(attachment)
		out = append(out, BridgePromptAttachment{
			Path:            promptAttachment.Path,
			Kind:            promptAttachment.Kind,
			Hash:            promptAttachment.Hash,
			Size:            promptAttachment.Size,
			MIME:            promptAttachment.MIME,
			SourceMessageID: promptAttachment.SourceMessageID,
			Requiredness:    promptAttachment.Requiredness,
			Decision:        promptAttachment.Decision,
			RejectionReason: promptAttachment.RejectionReason,
		})
	}
	return out
}

func splitPresenterEvents(ctx context.Context, source <-chan agentport.AgentEvent) (<-chan agentport.AgentEvent, <-chan agentport.AgentEvent) {
	first := make(chan agentport.AgentEvent, 32)
	second := make(chan agentport.AgentEvent, 32)
	go func() {
		defer close(first)
		defer close(second)
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-source:
				if !ok {
					return
				}
				if !sendPresenterEvent(ctx, first, event) {
					return
				}
				if !sendPresenterEvent(ctx, second, event) {
					return
				}
			}
		}
	}()
	return first, second
}

func sendPresenterEvent(ctx context.Context, target chan<- agentport.AgentEvent, event agentport.AgentEvent) bool {
	select {
	case target <- event:
		return true
	case <-ctx.Done():
		return false
	}
}

type presenterEventRun struct {
	events  <-chan agentport.AgentEvent
	stopper appimpresenter.Run
}

func (r presenterEventRun) Events(context.Context) <-chan agentport.AgentEvent {
	return r.events
}

func (r presenterEventRun) Stop(ctx context.Context) error {
	stopper, ok := r.stopper.(appimpresenter.RunStopper)
	if !ok {
		return nil
	}
	return stopper.Stop(ctx)
}

type presenterRun struct {
	run *Run
}

func (r presenterRun) Events(ctx context.Context) <-chan agentport.AgentEvent {
	out := make(chan agentport.AgentEvent, 32)
	go func() {
		defer close(out)
		if r.run == nil {
			return
		}
		for event := range r.run.Events(ctx) {
			select {
			case out <- toAgentEvent(event):
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

func (r presenterRun) Stop(ctx context.Context) error {
	if r.run == nil {
		return nil
	}
	return r.run.Stop(ctx)
}

type presenterChannel struct {
	transport LarkTransport
	onInfo    func(ctx context.Context, msg string, fields map[string]any)
	onError   func(ctx context.Context, err error, fields map[string]any)
}

func (c presenterChannel) SendMessage(ctx context.Context, req appimpresenter.SendMessageRequest) (appimpresenter.SendMessageResult, error) {
	if c.transport == nil {
		return appimpresenter.SendMessageResult{}, ErrNilLarkTransport
	}
	opts := fromPresenterSendOptions(req.Options)
	result, err := c.transport.SendMessage(ctx, LarkSendMessageRequest{
		ChatID: req.ChatID,
		Content: LarkMessageContent{
			Text:     req.Content.Text,
			Markdown: req.Content.Markdown,
			Card:     req.Content.Card,
		},
		Options: opts,
	})
	c.recordDelivery(ctx, "lark.reply.sent", presenterDeliveryFields("send_message", presenterMessageContentKind(req.Content), req.ChatID, req.Options, result.MessageID), err)
	return appimpresenter.SendMessageResult{MessageID: result.MessageID}, err
}

func (c presenterChannel) SendCard(ctx context.Context, req appimpresenter.SendCardRequest) (appimpresenter.SendCardResult, error) {
	if c.transport == nil {
		return appimpresenter.SendCardResult{}, ErrNilLarkTransport
	}
	opts := fromPresenterSendOptions(req.Options)
	result, err := c.transport.SendCard(ctx, LarkSendCardRequest{
		ChatID:  req.ChatID,
		Card:    req.Card,
		Options: opts,
	})
	c.recordDelivery(ctx, "lark.reply.sent", presenterDeliveryFields("send_card", "card", req.ChatID, req.Options, result.MessageID), err)
	return appimpresenter.SendCardResult{MessageID: result.MessageID}, err
}

func (c presenterChannel) UpdateCard(ctx context.Context, req appimpresenter.UpdateCardRequest) error {
	if c.transport == nil {
		return ErrNilLarkTransport
	}
	err := c.transport.UpdateCard(ctx, LarkUpdateCardRequest{
		MessageID: req.MessageID,
		Card:      req.Card,
	})
	c.recordDelivery(ctx, "lark.reply.updated", presenterDeliveryFields("update_card", "card", "", appimpresenter.SendOptions{}, req.MessageID), err)
	return err
}

type presenterStreamingChannel struct {
	presenterChannel
}

func (c presenterStreamingChannel) UpdateMessage(ctx context.Context, req appimpresenter.UpdateMessageRequest) error {
	if c.transport == nil {
		return ErrNilLarkTransport
	}
	updater := c.transport.(interface {
		UpdateMessage(context.Context, LarkUpdateMessageRequest) error
	})
	err := updater.UpdateMessage(ctx, LarkUpdateMessageRequest{
		MessageID: req.MessageID,
		Content: LarkMessageContent{
			Text:     req.Content.Text,
			Markdown: req.Content.Markdown,
			Card:     req.Content.Card,
		},
	})
	c.recordDelivery(ctx, "lark.reply.updated", presenterDeliveryFields("update_message", presenterMessageContentKind(req.Content), "", appimpresenter.SendOptions{}, req.MessageID), err)
	return err
}

func (c presenterChannel) recordDelivery(ctx context.Context, msg string, fields map[string]any, err error) {
	if err != nil {
		if c.onError != nil {
			c.onError(ctx, err, fields)
		}
		return
	}
	if c.onInfo != nil {
		c.onInfo(ctx, msg, fields)
	}
}

func presenterDeliveryFields(operation, contentKind, chatID string, opts appimpresenter.SendOptions, messageID string) map[string]any {
	fields := map[string]any{
		"phase":       "lark_reply",
		"operation":   operation,
		"contentKind": contentKind,
	}
	if chatID != "" {
		fields["chatId"] = chatID
	}
	if messageID != "" {
		fields["messageId"] = messageID
	}
	if opts.ReplyTo != "" {
		fields["replyTo"] = opts.ReplyTo
	}
	if opts.ReplyInThread {
		fields["replyInThread"] = true
	}
	if opts.ThreadID != "" {
		fields["threadId"] = opts.ThreadID
	}
	return fields
}

func presenterMessageContentKind(content appimpresenter.MessageContent) string {
	switch {
	case content.Card != nil:
		return "card"
	case content.Markdown != "":
		return "markdown"
	case content.Text != "":
		return "text"
	default:
		return "empty"
	}
}

func toPresenterSendOptions(opts LarkSendOptions) appimpresenter.SendOptions {
	return appimpresenter.SendOptions{
		ReplyTo:       opts.ReplyTo,
		ReplyInThread: opts.ReplyInThread,
		ThreadID:      opts.ThreadID,
		Metadata:      opts.Metadata,
	}
}

func fromPresenterSendOptions(opts appimpresenter.SendOptions) LarkSendOptions {
	return LarkSendOptions{
		ReplyTo:       opts.ReplyTo,
		ReplyInThread: opts.ReplyInThread,
		ThreadID:      opts.ThreadID,
		Metadata:      opts.Metadata,
	}
}

func toAgentEvent(event Event) agentport.AgentEvent {
	return agentport.AgentEvent{
		Type:                  agentport.EventType(event.Type),
		SessionID:             event.SessionID,
		ThreadID:              event.ThreadID,
		CWD:                   event.CWD,
		Model:                 event.Model,
		Delta:                 event.Delta,
		ID:                    event.ID,
		Name:                  event.Name,
		Input:                 event.Input,
		Output:                event.Output,
		IsError:               event.IsError,
		InputTokens:           event.InputTokens,
		OutputTokens:          event.OutputTokens,
		CachedInputTokens:     event.CachedInputTokens,
		ReasoningOutputTokens: event.ReasoningOutputTokens,
		CostUSD:               event.CostUSD,
		Message:               event.Message,
		TerminationReason:     agentport.TerminationReason(event.TerminationReason),
	}
}

func normalizeLarkReplyMode(mode LarkReplyMode) LarkReplyMode {
	switch mode {
	case LarkReplyCard, LarkReplyText, LarkReplyMarkdown:
		return mode
	default:
		return LarkReplyMarkdown
	}
}

func managedCotMessagesMode(mode LarkCotMessagesMode) appcot.Mode {
	return managedCotMessagesString(string(mode))
}

func managedCotMessagesSnapshot(raw string) appcot.Mode {
	return managedCotMessagesString(raw)
}

func managedPreferenceMessageReply(prefs map[string]any) string {
	raw, _ := prefs["messageReply"].(string)
	if raw == "text" && prefs["messageReplyMigrated"] != true {
		return "markdown"
	}
	switch raw {
	case "card", "markdown", "text":
		return raw
	default:
		return "markdown"
	}
}

func managedPreferenceShowToolCalls(prefs map[string]any) bool {
	value, ok := prefs["showToolCalls"].(bool)
	return !ok || value
}

func managedPreferenceCotMessages(prefs map[string]any) string {
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

func managedPreferenceRunIdleTimeoutMinutes(prefs map[string]any) int {
	value, ok := managedPreferenceNumber(prefs["runIdleTimeoutMinutes"])
	if !ok || value <= 0 {
		return 0
	}
	if value > 120 {
		return 120
	}
	return int(value)
}

func managedPreferenceNumber(value any) (float64, bool) {
	switch typed := value.(type) {
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case float64:
		return typed, true
	default:
		return 0, false
	}
}

func managedCotMessagesString(raw string) appcot.Mode {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "brief", "simple":
		return appcot.ModeBrief
	case "detailed", "on":
		return appcot.ModeDetailed
	default:
		return appcot.ModeOff
	}
}

func managedCallbackTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return 24 * time.Hour
	}
	return ttl
}

func managedCardActionSettle(delay time.Duration) time.Duration {
	if delay <= 0 {
		return defaultManagedCardActionSettle
	}
	return delay
}

func managedAccountReconnectDelay(delay time.Duration) time.Duration {
	if delay <= 0 {
		return defaultManagedAccountReconnectDelay
	}
	return delay
}

func managedCloseTimeout(delay time.Duration) time.Duration {
	if delay <= 0 {
		return defaultManagedCloseTimeout
	}
	return delay
}

func managedInfoRefreshInterval(delay time.Duration) time.Duration {
	if delay <= 0 {
		return defaultManagedInfoRefreshInterval
	}
	return delay
}

func managedShowToolCalls(value *bool) bool {
	if value == nil {
		return true
	}
	return *value
}

type managedCarrierThreadResolver struct {
	transport LarkTransport
}

func (r managedCarrierThreadResolver) ResolveCarrierThreadID(ctx context.Context, chatID, messageID string) (string, error) {
	resolver, ok := r.transport.(LarkCarrierThreadResolver)
	if !ok || resolver == nil {
		return "", nil
	}
	return resolver.ResolveCarrierThreadID(ctx, chatID, messageID)
}

func fromInternalAccessDecision(decision access.Decision) AccessDecision {
	return trustedAccessDecision(decision)
}

func trustedAccessDecision(decision access.Decision) AccessDecision {
	return AccessDecision{
		OK:      decision.OK,
		Reason:  AccessReason(decision.Reason),
		trusted: true,
	}
}

func rejectedUserVisible(err error) string {
	var rejected RejectedError
	if errors.As(err, &rejected) && rejected.UserVisible != "" {
		return rejected.UserVisible
	}
	if strings.TrimSpace(err.Error()) != "" {
		return err.Error()
	}
	return "运行被拒绝。"
}
