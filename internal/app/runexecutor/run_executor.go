package runexecutor

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/permissions"
	agentport "github.com/zarazhangrui/lark-coding-agent-bridge/internal/ports/agent"
)

const defaultPostDoneExitGrace = 2 * time.Second

type RunPolicy struct {
	Prompt         string
	CWDRealpath    string
	AccessMode     permissions.AccessMode
	Sandbox        permissions.CodexSandboxMode
	PermissionMode permissions.ClaudePermissionMode
	ExpiresAt      time.Time
}

type SubmitRunInput struct {
	ScopeID       string
	Policy        RunPolicy
	SessionID     string
	ThreadID      string
	Model         string
	Images        []string
	StopGraceMs   int
	Nowait        bool
	Observability Observability
}

type Observability struct {
	Profile string
	Agent   string
	Source  string
	Stage   string
}

type Logger interface {
	Info(msg string, fields map[string]any)
	Warn(msg string, fields map[string]any)
}

type Executor struct {
	agent                agentport.AgentAdapter
	pool                 *ProcessPool
	activeRuns           *ActiveRuns
	createRunID          func() string
	now                  func() time.Time
	postDoneExitGrace    time.Duration
	stopAfterDoneTimeout time.Duration
	loggerMu             sync.RWMutex
	logger               Logger
}

type Options struct {
	Agent                agentport.AgentAdapter
	Pool                 *ProcessPool
	ActiveRuns           *ActiveRuns
	CreateRunID          func() string
	Now                  func() time.Time
	PostDoneExitGrace    time.Duration
	StopAfterDoneTimeout time.Duration
	Logger               Logger
}

func New(opts Options) *Executor {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	postDoneExitGrace := opts.PostDoneExitGrace
	if postDoneExitGrace <= 0 {
		postDoneExitGrace = defaultPostDoneExitGrace
	}
	stopAfterDoneTimeout := opts.StopAfterDoneTimeout
	if stopAfterDoneTimeout <= 0 {
		stopAfterDoneTimeout = defaultPostDoneExitGrace
	}
	createRunID := opts.CreateRunID
	if createRunID == nil {
		createRunID = func() string { return time.Now().UTC().Format("20060102150405.000000000") }
	}
	pool := opts.Pool
	if pool == nil {
		pool = NewProcessPool(nil)
	}
	activeRuns := opts.ActiveRuns
	if activeRuns == nil {
		activeRuns = NewActiveRuns()
	}
	return &Executor{
		agent:                opts.Agent,
		pool:                 pool,
		activeRuns:           activeRuns,
		createRunID:          createRunID,
		now:                  now,
		postDoneExitGrace:    postDoneExitGrace,
		stopAfterDoneTimeout: stopAfterDoneTimeout,
		logger:               opts.Logger,
	}
}

func (e *Executor) SetLogger(logger Logger) {
	if e == nil {
		return
	}
	e.loggerMu.Lock()
	defer e.loggerMu.Unlock()
	e.logger = logger
}

func (e *Executor) loggerSnapshot() Logger {
	e.loggerMu.RLock()
	defer e.loggerMu.RUnlock()
	return e.logger
}

func (e *Executor) PauseNewRuns(reason string) func() {
	return e.activeRuns.PauseNewRuns(reason)
}

func (e *Executor) Interrupt(ctx context.Context, scopeID string) bool {
	return e.activeRuns.Interrupt(ctx, scopeID)
}

func (e *Executor) StopAll(ctx context.Context) error {
	return e.activeRuns.StopAll(ctx)
}

func (e *Executor) WaitForAll(ctx context.Context) error {
	return e.activeRuns.WaitForAll(ctx)
}

func (e *Executor) PoolSnapshot() ProcessPoolSnapshot {
	return e.pool.Snapshot()
}

func (e *Executor) ActiveScopes() []string {
	scopes := e.activeRuns.Scopes()
	sort.Strings(scopes)
	return scopes
}

func (e *Executor) Submit(ctx context.Context, input SubmitRunInput) (*RunExecution, error) {
	startedAt := e.now()
	if e.agent == nil {
		return nil, spawnFailed(SpawnFailedAgentSpawn, "agent is required", nil)
	}
	if !input.Policy.ExpiresAt.IsZero() && !input.Policy.ExpiresAt.After(e.now()) {
		return nil, reject(RunRejectedPolicyExpired, "run policy expired before spawn")
	}
	if e.activeRuns.NewRunsPaused() {
		return nil, reject(RunRejectedReconnectInProgress, pauseReason(e.activeRuns))
	}

	releaseScope := e.activeRuns.Reserve(input.ScopeID)
	if releaseScope == nil {
		return nil, reject(RunRejectedAlreadyActive, "another run is already active for this scope")
	}

	releasePool, err := e.acquirePool(ctx, input.Nowait)
	if err != nil {
		releaseScope()
		return nil, err
	}

	releaseBoth := func() {
		releasePool()
		releaseScope()
	}
	if e.activeRuns.NewRunsPaused() {
		releaseBoth()
		return nil, reject(RunRejectedReconnectInProgress, pauseReason(e.activeRuns))
	}

	runID := e.createRunID()
	runOptions := agentport.AgentRunOptions{
		RunID:          runID,
		Prompt:         input.Policy.Prompt,
		CWD:            input.Policy.CWDRealpath,
		SessionID:      input.SessionID,
		ThreadID:       input.ThreadID,
		Model:          input.Model,
		Images:         input.Images,
		Sandbox:        input.Policy.Sandbox,
		PermissionMode: input.Policy.PermissionMode,
		StopGraceMs:    input.StopGraceMs,
	}

	if err := e.agent.PrepareRun(ctx, runOptions); err != nil {
		releaseBoth()
		return nil, spawnFailed(SpawnFailedAgentPrepare, "agent prepare failed", err)
	}
	if err := ctx.Err(); err != nil {
		releaseBoth()
		return nil, err
	}
	if e.activeRuns.NewRunsPaused() {
		releaseBoth()
		return nil, reject(RunRejectedReconnectInProgress, pauseReason(e.activeRuns))
	}

	run, err := e.agent.Run(ctx, runOptions)
	if err != nil {
		releaseBoth()
		return nil, spawnFailed(SpawnFailedAgentSpawn, "agent spawn failed", err)
	}
	logger := e.loggerSnapshot()
	dimensions := e.runDimensions(input, runID)
	e.logRunStarted(logger, dimensions, startedAt, input.Policy)
	if err := ctx.Err(); err != nil {
		releaseBoth()
		_ = run.Stop(context.Background())
		return nil, err
	}

	handle, err := e.activeRuns.Register(input.ScopeID, run)
	if err != nil {
		releaseBoth()
		_ = run.Stop(context.Background())
		return nil, err
	}

	execution := &RunExecution{
		RunID:    runID,
		ScopeID:  input.ScopeID,
		Run:      run,
		handle:   handle,
		stopDone: make(chan struct{}),
	}
	var cleanupOnce sync.Once
	execution.cleanup = func(waitForExit bool) {
		cleanupOnce.Do(func() {
			e.activeRuns.Unregister(input.ScopeID, run)
			releasePool()
			if waitForExit {
				e.waitOrStop(run)
			}
		})
	}
	execution.fanout = newEventFanout(e.observeRunEvents(run.Events(), logger, dimensions, startedAt), func() {
		execution.cleanup(!handle.Interrupted())
	})
	return execution, nil
}

func (e *Executor) runDimensions(input SubmitRunInput, runID string) map[string]any {
	agentID := ""
	if e.agent != nil {
		agentID = e.agent.ID()
	}
	dimensions := map[string]any{
		"runId":   runID,
		"scope":   input.ScopeID,
		"agent":   firstNonEmptyRunExecutor(input.Observability.Agent, agentID),
		"profile": firstNonEmptyRunExecutor(input.Observability.Profile, "unknown"),
		"source":  firstNonEmptyRunExecutor(input.Observability.Source, "unknown"),
		"stage":   firstNonEmptyRunExecutor(input.Observability.Stage, "submit"),
	}
	return dimensions
}

func (e *Executor) logRunStarted(logger Logger, dimensions map[string]any, startedAt time.Time, policy RunPolicy) {
	if logger == nil {
		return
	}
	fields := cloneRunFields(dimensions)
	fields["queueWaitMs"] = maxDurationMilliseconds(e.now().Sub(startedAt))
	fields["accessMode"] = string(policy.AccessMode)
	fields["sandbox"] = string(policy.Sandbox)
	fields["permissionMode"] = string(policy.PermissionMode)
	logger.Info("run.started", fields)
}

func (e *Executor) observeRunEvents(source <-chan agentport.AgentEvent, logger Logger, dimensions map[string]any, startedAt time.Time) <-chan agentport.AgentEvent {
	if logger == nil {
		return source
	}
	out := make(chan agentport.AgentEvent, 16)
	go func() {
		defer close(out)
		for event := range source {
			if event.Type == agentport.EventDone {
				fields := cloneRunFields(dimensions)
				fields["result"] = string(event.TerminationReason)
				fields["durationMs"] = maxDurationMilliseconds(e.now().Sub(startedAt))
				logger.Info("run.completed", fields)
			}
			if event.Type == agentport.EventError {
				fields := cloneRunFields(dimensions)
				fields["result"] = string(event.TerminationReason)
				fields["durationMs"] = maxDurationMilliseconds(e.now().Sub(startedAt))
				if event.Message != nil {
					fields["error"] = *event.Message
				}
				logger.Warn("run.failed", fields)
			}
			out <- event
			if isTerminalEvent(event) {
				return
			}
		}
	}()
	return out
}

func cloneRunFields(input map[string]any) map[string]any {
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func firstNonEmptyRunExecutor(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func maxDurationMilliseconds(duration time.Duration) int64 {
	if duration < 0 {
		return 0
	}
	return duration.Milliseconds()
}

func (e *Executor) acquirePool(ctx context.Context, nowait bool) (func(), error) {
	if nowait {
		release := e.pool.TryAcquire()
		if release == nil {
			return nil, reject(RunRejectedPoolFull, "process pool is full")
		}
		return release, nil
	}
	release, err := e.pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	return release, nil
}

func (e *Executor) waitOrStop(run agentport.AgentRun) {
	waitCtx, cancel := context.WithTimeout(context.Background(), e.postDoneExitGrace)
	exited, err := run.WaitForExit(waitCtx)
	cancel()
	if err == nil && exited {
		return
	}
	stopCtx, stopCancel := context.WithTimeout(context.Background(), e.stopAfterDoneTimeout)
	_ = run.Stop(stopCtx)
	stopCancel()
}

func pauseReason(activeRuns *ActiveRuns) string {
	if reason := activeRuns.NewRunsPauseReason(); reason != "" {
		return reason
	}
	return "new runs are temporarily paused"
}

type RunExecution struct {
	RunID   string
	ScopeID string
	Run     agentport.AgentRun

	handle  *RunHandle
	fanout  *eventFanout
	cleanup func(waitForExit bool)

	stopOnce sync.Once
	stopDone chan struct{}
	stopErr  error
}

func (e *RunExecution) Subscribe(ctx context.Context) <-chan agentport.AgentEvent {
	return e.fanout.Subscribe(ctx)
}

func (e *RunExecution) Stop(ctx context.Context) error {
	e.stopOnce.Do(func() {
		defer close(e.stopDone)
		e.handle.MarkInterrupted()
		if err := e.Run.Stop(ctx); err != nil {
			e.stopErr = err
			return
		}
		waitCtx, cancel := context.WithTimeout(ctx, defaultPostDoneExitGrace)
		_, _ = e.Run.WaitForExit(waitCtx)
		cancel()
		e.cleanup(false)
	})
	select {
	case <-e.stopDone:
		return e.stopErr
	case <-ctx.Done():
		return ctx.Err()
	}
}
