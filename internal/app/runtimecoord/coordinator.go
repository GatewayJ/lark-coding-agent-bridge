package runtimecoord

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/compat/apppaths"
)

type Options struct {
	RootDir    string
	Profile    string
	AgentKind  AgentKind
	Version    string
	PID        int
	Now        func() time.Time
	Adapter    RuntimeAdapter
	LockStale  time.Duration
	LockUpdate time.Duration
}

type Coordinator struct {
	paths      apppaths.Paths
	agentKind  AgentKind
	version    string
	pid        int
	now        func() time.Time
	adapter    RuntimeAdapter
	lockStale  time.Duration
	lockUpdate time.Duration

	mu          sync.Mutex
	started     bool
	stopping    bool
	entry       *ProcessEntry
	handle      RuntimeHandle
	profileLock *AcquiredLock
	appLock     *AcquiredLock
	startedAt   time.Time
}

type SameAppConflictError struct {
	AppID   string
	Others  []ProcessEntry
	Message string
}

func (e *SameAppConflictError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("runtime app already has active bridge processes: %s", e.AppID)
}

func New(opts Options) (*Coordinator, error) {
	paths, err := apppaths.Resolve(apppaths.Options{RootDir: opts.RootDir, Profile: opts.Profile})
	if err != nil {
		return nil, err
	}
	agentKind := normalizeAgentKind(opts.AgentKind)
	pid := opts.PID
	if pid == 0 {
		pid = os.Getpid()
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	lockStale := opts.LockStale
	if lockStale <= 0 {
		lockStale = defaultLockStale
	}
	lockUpdate := opts.LockUpdate
	if lockUpdate <= 0 {
		lockUpdate = defaultLockUpdate
	}
	return &Coordinator{
		paths:      paths,
		agentKind:  agentKind,
		version:    firstNonEmpty(opts.Version, "go-sdk"),
		pid:        pid,
		now:        now,
		adapter:    opts.Adapter,
		lockStale:  lockStale,
		lockUpdate: lockUpdate,
	}, nil
}

func (c *Coordinator) Start(ctx context.Context, opts StartOptions) error {
	opts.AgentKind = normalizeAgentKind(opts.AgentKind)
	if opts.AgentKind == "" {
		opts.AgentKind = c.agentKind
	}
	if opts.AgentKind == "" {
		return ErrAgentKindRequired
	}
	opts.Tenant = normalizeTenant(opts.Tenant)
	if opts.AppID == "" {
		return errors.New("appID is required")
	}
	if opts.ConfigPath == "" {
		opts.ConfigPath = c.paths.ConfigFile
	}

	c.mu.Lock()
	if c.started || c.stopping {
		c.mu.Unlock()
		return ErrAlreadyStarted
	}
	c.agentKind = opts.AgentKind
	c.mu.Unlock()
	if c.adapter == nil {
		return errors.New("runtime adapter is required")
	}

	profileLock, err := c.AcquireProfileLock()
	if err != nil {
		return err
	}
	defer func() {
		if profileLock != nil {
			_ = profileLock.Release()
		}
	}()
	appLock, err := c.AcquireAppLock(opts.AppID)
	if err != nil {
		return err
	}
	defer func() {
		if appLock != nil {
			_ = appLock.Release()
		}
	}()

	conflicts, err := c.sameAppLiveOthers(opts.AppID)
	if err != nil {
		return err
	}
	if len(conflicts) > 0 {
		return &SameAppConflictError{AppID: opts.AppID, Others: conflicts}
	}

	entry, err := c.RegisterProcess(opts)
	if err != nil {
		return err
	}
	registered := true
	defer func() {
		if registered {
			_ = c.unregisterProcess(entry.ID)
		}
	}()

	handle, err := c.adapter.Start(ctx, StartRequest{
		AppID:      opts.AppID,
		Tenant:     opts.Tenant,
		Profile:    c.paths.Profile,
		AgentKind:  opts.AgentKind,
		ConfigPath: opts.ConfigPath,
	})
	if err != nil {
		return err
	}
	status, _ := handle.Status(ctx)
	if status.BotName != "" {
		entry.BotName = status.BotName
		_ = c.updateProcess(entry.ID, ProcessEntry{BotName: status.BotName})
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.started {
		_ = handle.Shutdown(ctx)
		return ErrAlreadyStarted
	}
	c.started = true
	c.entry = &entry
	c.handle = handle
	c.profileLock = profileLock
	c.appLock = appLock
	c.startedAt = c.now()
	profileLock = nil
	appLock = nil
	registered = false
	return nil
}

func (c *Coordinator) Shutdown(ctx context.Context) error {
	c.mu.Lock()
	if !c.started {
		c.mu.Unlock()
		return nil
	}
	if c.stopping {
		c.mu.Unlock()
		return ErrShuttingDown
	}
	handle := c.handle
	entry := c.entry
	appLock := c.appLock
	profileLock := c.profileLock
	c.stopping = true
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.stopping = false
		c.mu.Unlock()
	}()

	var errs []error
	if handle != nil {
		if err := handle.Shutdown(ctx); err != nil {
			return err
		}
	}
	if entry != nil {
		if err := c.unregisterProcess(entry.ID); err != nil {
			errs = append(errs, err)
		}
	}
	if appLock != nil {
		if err := appLock.Release(); err != nil {
			errs = append(errs, err)
		}
	}
	if profileLock != nil {
		if err := profileLock.Release(); err != nil {
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	c.mu.Lock()
	if c.handle == handle {
		c.started = false
		c.handle = nil
		c.entry = nil
		c.appLock = nil
		c.profileLock = nil
		c.startedAt = time.Time{}
	}
	c.mu.Unlock()
	return nil
}

func (c *Coordinator) Status(ctx context.Context) (RuntimeStatus, error) {
	processes, err := c.ReadAndPruneProcesses()
	if err != nil {
		return RuntimeStatus{}, err
	}
	c.mu.Lock()
	started := c.started
	entry := cloneEntry(c.entry)
	handle := c.handle
	startedAt := c.startedAt
	c.mu.Unlock()
	var adapter AdapterStatus
	if handle != nil {
		adapter, err = handle.Status(ctx)
		if err != nil {
			return RuntimeStatus{}, err
		}
	}
	status := RuntimeStatus{
		Started:   started,
		Entry:     entry,
		Processes: processes,
		Adapter:   adapter,
		Profile:   c.paths.Profile,
		AgentKind: c.agentKind,
		StartedAt: startedAt,
	}
	if entry != nil {
		status.AppID = entry.AppID
	}
	return status, nil
}

func (c *Coordinator) Reconnect(ctx context.Context, opts ReconnectOptions) error {
	c.mu.Lock()
	if c.stopping || !c.started || c.handle == nil || c.entry == nil {
		c.mu.Unlock()
		return ErrNotStarted
	}
	handle := c.handle
	entry := *c.entry
	oldAppLock := c.appLock
	c.mu.Unlock()

	reconnecter, ok := handle.(Reconnecter)
	if !ok {
		return errors.New("runtime handle does not support reconnect")
	}
	opts.Tenant = normalizeTenant(opts.Tenant)
	if opts.AppID == "" {
		opts.AppID = entry.AppID
	}
	if opts.ConfigPath == "" {
		opts.ConfigPath = entry.ConfigPath
	}

	var nextAppLock *AcquiredLock
	var err error
	appChanged := opts.AppID != entry.AppID
	if appChanged {
		nextAppLock, err = c.AcquireAppLock(opts.AppID)
		if err != nil {
			return err
		}
		defer func() {
			if nextAppLock != nil {
				_ = nextAppLock.Release()
			}
		}()
	}

	if err := reconnecter.Reconnect(ctx, ReconnectRequest{
		PreviousAppID: entry.AppID,
		AppID:         opts.AppID,
		Tenant:        opts.Tenant,
		ConfigPath:    opts.ConfigPath,
	}); err != nil {
		return err
	}

	patch := ProcessEntry{AppID: opts.AppID, Tenant: opts.Tenant, ConfigPath: opts.ConfigPath}
	if status, err := handle.Status(ctx); err == nil && status.BotName != "" {
		patch.BotName = status.BotName
	}
	if err := c.updateProcess(entry.ID, patch); err != nil {
		return err
	}

	c.mu.Lock()
	if c.entry != nil && c.entry.ID == entry.ID {
		c.entry.AppID = opts.AppID
		c.entry.Tenant = opts.Tenant
		c.entry.ConfigPath = opts.ConfigPath
		if patch.BotName != "" {
			c.entry.BotName = patch.BotName
		}
		if nextAppLock != nil {
			c.appLock = nextAppLock
			nextAppLock = nil
		}
	}
	c.mu.Unlock()
	if appChanged && oldAppLock != nil {
		_ = oldAppLock.Release()
	}
	return nil
}

func (c *Coordinator) pathsForProfile(profile string) (apppaths.Paths, error) {
	return apppaths.Resolve(apppaths.Options{RootDir: c.paths.RootDir, Profile: profile})
}

func cloneEntry(entry *ProcessEntry) *ProcessEntry {
	if entry == nil {
		return nil
	}
	copy := *entry
	return &copy
}
