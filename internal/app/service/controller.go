package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/runtimecoord"
)

const defaultWaitTimeout = 30 * time.Second

var ErrServiceConnectTimeout = errors.New("service started but did not connect before timeout")

type Controller struct {
	Adapter          Adapter
	RootDir          string
	Profile          string
	AppID            string
	AgentKind        runtimecoord.AgentKind
	Preflight        func(context.Context) error
	WaitTimeout      time.Duration
	RequireConnected bool
	ProcessLister    ProcessLister
	LockHandler      RuntimeLockHandler
}

type ProcessLister interface {
	ListProcesses(ctx context.Context) ([]runtimecoord.ProcessEntry, error)
}

type ProcessListerFunc func(context.Context) ([]runtimecoord.ProcessEntry, error)

func (f ProcessListerFunc) ListProcesses(ctx context.Context) ([]runtimecoord.ProcessEntry, error) {
	return f(ctx)
}

type RuntimeLockHandler interface {
	HandleRuntimeLockConflict(ctx context.Context, meta runtimecoord.RuntimeLockMeta) (retry bool, err error)
}

type RuntimeLockHandlerFunc func(context.Context, runtimecoord.RuntimeLockMeta) (bool, error)

func (f RuntimeLockHandlerFunc) HandleRuntimeLockConflict(ctx context.Context, meta runtimecoord.RuntimeLockMeta) (bool, error) {
	return f(ctx, meta)
}

type StartResult struct {
	Profile    string
	AppID      string
	Connected  bool
	Process    *runtimecoord.ProcessEntry
	Service    StatusResult
	StartError string
}

type StatusResult struct {
	Profile      string
	Platform     string
	ServicePath  string
	Installed    bool
	Running      bool
	PID          string
	LastExit     string
	StdoutPath   string
	StderrPath   string
	Process      *runtimecoord.ProcessEntry
	ProcessError string
}

type StopResult struct {
	Profile string
	Stopped bool
	Message string
}

type UnregisterResult struct {
	Profile string
	Removed bool
}

func (c Controller) Start(ctx context.Context) (StartResult, error) {
	if err := c.validate(); err != nil {
		return StartResult{}, err
	}
	if c.AgentKind != runtimecoord.AgentClaude && c.AgentKind != runtimecoord.AgentCodex {
		return StartResult{}, runtimecoord.ErrAgentKindRequired
	}
	if err := c.ensureRuntimeLocksAvailable(ctx); err != nil {
		return StartResult{}, err
	}
	if c.Preflight != nil {
		if err := c.Preflight(ctx); err != nil {
			return StartResult{}, err
		}
	}
	if err := c.Adapter.Install(ctx); err != nil {
		return StartResult{}, err
	}
	if c.Adapter.IsRunning(ctx) {
		stop := c.Adapter.Stop(ctx)
		if !stop.OK {
			return StartResult{}, fmt.Errorf("stop existing service failed: %s", formatCommandError(stop))
		}
		ok, err := c.Adapter.WaitUntilStopped(ctx, 5*time.Second)
		if err != nil {
			return StartResult{}, err
		}
		if !ok {
			return StartResult{}, fmt.Errorf("existing service did not stop")
		}
	}
	before, _ := c.currentPIDs(ctx)
	start := c.Adapter.Start(ctx)
	if !start.OK {
		return StartResult{}, fmt.Errorf("start service failed: %s", formatCommandError(start))
	}
	entry, connectErr := c.waitForServiceConnect(ctx, before)
	status := c.Status(ctx)
	if connectErr != nil && status.ProcessError == "" {
		status.ProcessError = connectErr.Error()
	}
	result := StartResult{
		Profile:   c.Profile,
		AppID:     c.AppID,
		Connected: entry != nil,
		Process:   entry,
		Service:   status,
	}
	if c.RequireConnected && connectErr != nil {
		return result, connectErr
	}
	if c.RequireConnected && entry == nil {
		return result, ErrServiceConnectTimeout
	}
	return result, nil
}

func (c Controller) Stop(ctx context.Context) (StopResult, error) {
	if err := c.validate(); err != nil {
		return StopResult{}, err
	}
	if !c.Adapter.FileExists() {
		return StopResult{Profile: c.Profile, Message: "not-installed"}, nil
	}
	if !c.Adapter.IsRunning(ctx) {
		return StopResult{Profile: c.Profile, Message: "not-running"}, nil
	}
	result := c.Adapter.StopAndDisableAutostart(ctx)
	if !result.OK {
		return StopResult{}, fmt.Errorf("stop service failed: %s", formatCommandError(result))
	}
	return StopResult{Profile: c.Profile, Stopped: true}, nil
}

func (c Controller) Restart(ctx context.Context) (StartResult, error) {
	if err := c.validate(); err != nil {
		return StartResult{}, err
	}
	if !c.Adapter.FileExists() {
		return StartResult{}, fmt.Errorf("service is not installed")
	}
	before, _ := c.currentPIDs(ctx)
	var result Result
	if c.Adapter.IsRunning(ctx) {
		result = c.Adapter.Restart(ctx)
	} else {
		result = c.Adapter.Start(ctx)
	}
	if !result.OK {
		return StartResult{}, fmt.Errorf("restart service failed: %s", formatCommandError(result))
	}
	entry, connectErr := c.waitForServiceConnect(ctx, before)
	status := c.Status(ctx)
	if connectErr != nil && status.ProcessError == "" {
		status.ProcessError = connectErr.Error()
	}
	startResult := StartResult{Profile: c.Profile, AppID: c.AppID, Connected: entry != nil, Process: entry, Service: status}
	if c.RequireConnected && connectErr != nil {
		return startResult, connectErr
	}
	if c.RequireConnected && entry == nil {
		return startResult, ErrServiceConnectTimeout
	}
	return startResult, nil
}

func (c Controller) Status(ctx context.Context) StatusResult {
	installed := c.Adapter.FileExists()
	running := false
	parsed := ParsedStatus{}
	if installed {
		statusText := c.Adapter.DescribeStatus(ctx)
		parsed = c.Adapter.ParseStatus(statusText)
		running = c.Adapter.IsRunning(ctx)
	}
	stdout, _ := DaemonStdoutPath(c.RootDir, c.Profile)
	stderr, _ := DaemonStderrPath(c.RootDir, c.Profile)
	process, processErr := c.currentConnectedProcess(ctx)
	return StatusResult{
		Profile:      c.Profile,
		Platform:     c.Adapter.PlatformName(),
		ServicePath:  c.Adapter.ServicePath(),
		Installed:    installed,
		Running:      running,
		PID:          parsed.PID,
		LastExit:     parsed.LastExit,
		StdoutPath:   stdout,
		StderrPath:   stderr,
		Process:      process,
		ProcessError: errorString(processErr),
	}
}

func (c Controller) Unregister(ctx context.Context) (UnregisterResult, error) {
	if err := c.validate(); err != nil {
		return UnregisterResult{}, err
	}
	if !c.Adapter.FileExists() {
		return UnregisterResult{Profile: c.Profile}, nil
	}
	if c.Adapter.IsRunning(ctx) {
		result := c.Adapter.StopAndDisableAutostart(ctx)
		if !result.OK {
			return UnregisterResult{}, fmt.Errorf("stop service failed: %s", formatCommandError(result))
		}
	}
	if err := c.Adapter.DeleteFile(ctx); err != nil {
		return UnregisterResult{}, err
	}
	return UnregisterResult{Profile: c.Profile, Removed: true}, nil
}

func (c Controller) validate() error {
	if c.Adapter == nil {
		return fmt.Errorf("service adapter is required")
	}
	if c.Profile == "" {
		return fmt.Errorf("profile is required")
	}
	if c.RootDir == "" {
		return fmt.Errorf("root directory is required")
	}
	return nil
}

func (c Controller) ensureRuntimeLocksAvailable(ctx context.Context) error {
	servicePID, hasServicePID := c.currentServicePID(ctx)
	retries := 0
	for {
		err := c.tryRuntimeLocks(servicePID, hasServicePID)
		if err == nil {
			return nil
		}
		var conflict *runtimecoord.RuntimeLockConflictError
		if !errors.As(err, &conflict) || conflict.Meta == nil || c.LockHandler == nil {
			return err
		}
		retry, handleErr := c.LockHandler.HandleRuntimeLockConflict(ctx, *conflict.Meta)
		if handleErr != nil {
			return handleErr
		}
		if !retry {
			return err
		}
		retries++
		if retries >= 3 {
			return err
		}
	}
}

func (c Controller) tryRuntimeLocks(servicePID int, hasServicePID bool) error {
	coord, err := runtimecoord.New(runtimecoord.Options{
		RootDir:   c.RootDir,
		Profile:   c.Profile,
		AgentKind: c.AgentKind,
	})
	if err != nil {
		return err
	}
	profileLock, err := coord.AcquireProfileLock()
	if err != nil {
		if !lockHeldByServicePID(err, servicePID, hasServicePID) {
			return err
		}
		profileLock = nil
	}
	if profileLock != nil {
		defer profileLock.Release()
	}
	if c.AppID == "" {
		return nil
	}
	appLock, err := coord.AcquireAppLock(c.AppID)
	if err != nil {
		if lockHeldByServicePID(err, servicePID, hasServicePID) {
			return nil
		}
		return err
	}
	return appLock.Release()
}

func (c Controller) currentServicePID(ctx context.Context) (int, bool) {
	if c.Adapter == nil || !c.Adapter.IsRunning(ctx) {
		return 0, false
	}
	parsed := c.Adapter.ParseStatus(c.Adapter.DescribeStatus(ctx))
	pid, err := strconv.Atoi(strings.TrimSpace(parsed.PID))
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

func lockHeldByServicePID(err error, servicePID int, hasServicePID bool) bool {
	if !hasServicePID {
		return false
	}
	var conflict *runtimecoord.RuntimeLockConflictError
	return errors.As(err, &conflict) && conflict.Meta != nil && conflict.Meta.PID == servicePID
}

func (c Controller) waitForServiceConnect(ctx context.Context, before map[int]struct{}) (*runtimecoord.ProcessEntry, error) {
	timeout := c.WaitTimeout
	if timeout <= 0 {
		timeout = defaultWaitTimeout
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		entry, err := c.freshConnectedProcess(ctx, before)
		if err != nil {
			return nil, err
		}
		if entry != nil {
			return entry, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, nil
		case <-ticker.C:
		}
	}
}

func (c Controller) freshConnectedProcess(ctx context.Context, before map[int]struct{}) (*runtimecoord.ProcessEntry, error) {
	entries, err := c.listProcesses(ctx)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.ProfileName != c.Profile || entry.AppID != c.AppID || entry.BotName == "" {
			continue
		}
		if _, ok := before[entry.PID]; ok {
			continue
		}
		copy := entry
		return &copy, nil
	}
	return nil, nil
}

func (c Controller) currentConnectedProcess(ctx context.Context) (*runtimecoord.ProcessEntry, error) {
	entries, err := c.listProcesses(ctx)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.ProfileName == c.Profile && (c.AppID == "" || entry.AppID == c.AppID) && entry.BotName != "" {
			copy := entry
			return &copy, nil
		}
	}
	return nil, nil
}

func (c Controller) currentPIDs(ctx context.Context) (map[int]struct{}, error) {
	out := map[int]struct{}{}
	entries, err := c.listProcesses(ctx)
	if err != nil {
		return out, err
	}
	for _, entry := range entries {
		if entry.ProfileName == c.Profile && (c.AppID == "" || entry.AppID == c.AppID) {
			out[entry.PID] = struct{}{}
		}
	}
	return out, nil
}

func (c Controller) listProcesses(ctx context.Context) ([]runtimecoord.ProcessEntry, error) {
	if c.ProcessLister == nil {
		coord, err := runtimecoord.New(runtimecoord.Options{RootDir: c.RootDir, Profile: c.Profile})
		if err != nil {
			return nil, err
		}
		entries, err := coord.ReadAndPruneProcesses()
		if err != nil {
			return nil, err
		}
		return entries, nil
	}
	entries, err := c.ProcessLister.ListProcesses(ctx)
	if err != nil {
		return nil, err
	}
	return entries, nil
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func formatCommandError(result Result) string {
	if result.Stderr != "" {
		return result.Stderr
	}
	if result.Stdout != "" {
		return result.Stdout
	}
	return "unknown error"
}
