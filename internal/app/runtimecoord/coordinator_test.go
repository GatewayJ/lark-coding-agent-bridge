package runtimecoord

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestAcquireProfileLockExcludesSameProfile(t *testing.T) {
	root := t.TempDir()
	first := newTestCoordinator(t, root, "codex")
	second := newTestCoordinator(t, root, "codex")

	lock, err := first.AcquireProfileLock()
	if err != nil {
		t.Fatalf("AcquireProfileLock() error = %v", err)
	}
	defer lock.Release()

	_, err = second.AcquireProfileLock()
	var conflict *RuntimeLockConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("AcquireProfileLock() error = %v, want RuntimeLockConflictError", err)
	}
	if conflict.Kind != LockProfile || conflict.Meta == nil || conflict.Meta.Profile != "codex" {
		t.Fatalf("conflict = %#v", conflict)
	}
}

func TestStartExcludesSameApp(t *testing.T) {
	root := t.TempDir()
	firstAdapter := &fakeAdapter{}
	first, err := New(Options{
		RootDir:   root,
		Profile:   "codex-a",
		AgentKind: AgentCodex,
		Version:   "test",
		Adapter:   firstAdapter,
	})
	if err != nil {
		t.Fatalf("New(first) error = %v", err)
	}
	if err := first.Start(context.Background(), StartOptions{
		AppID:      "cli_same",
		Tenant:     TenantFeishu,
		AgentKind:  AgentCodex,
		ConfigPath: filepath.Join(root, "config.json"),
	}); err != nil {
		t.Fatalf("first Start() error = %v", err)
	}
	defer first.Shutdown(context.Background())

	second, err := New(Options{
		RootDir:   root,
		Profile:   "codex-b",
		AgentKind: AgentCodex,
		Version:   "test",
		Adapter:   &fakeAdapter{},
	})
	if err != nil {
		t.Fatalf("New(second) error = %v", err)
	}
	err = second.Start(context.Background(), StartOptions{
		AppID:      "cli_same",
		Tenant:     TenantFeishu,
		AgentKind:  AgentCodex,
		ConfigPath: filepath.Join(root, "config.json"),
	})
	var conflict *RuntimeLockConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("second Start() error = %v, want RuntimeLockConflictError", err)
	}
	if conflict.Kind != LockApp || conflict.Meta == nil || conflict.Meta.AppID != "cli_same" {
		t.Fatalf("app conflict = %#v", conflict)
	}
}

func TestStartSeesLegacyRootRegistrySameAppConflict(t *testing.T) {
	root := t.TempDir()
	legacy := newTestCoordinator(t, root, "codex-old")
	profileLock, err := legacy.AcquireProfileLock()
	if err != nil {
		t.Fatalf("legacy AcquireProfileLock() error = %v", err)
	}
	defer profileLock.Release()
	appLock, err := legacy.AcquireAppLock("cli_legacy")
	if err != nil {
		t.Fatalf("legacy AcquireAppLock() error = %v", err)
	}
	defer appLock.Release()

	legacyEntry := ProcessEntry{
		ID:          "legacy",
		PID:         legacy.pid,
		AppID:       "cli_legacy",
		Tenant:      TenantFeishu,
		ProfileName: "codex-old",
		AgentKind:   AgentCodex,
		ConfigPath:  filepath.Join(root, "legacy.json"),
		StartedAt:   time.Now().UTC().Format(time.RFC3339Nano),
		Version:     "js",
	}
	if err := writeRegistryFile(filepath.Join(root, "processes.json"), []ProcessEntry{legacyEntry}); err != nil {
		t.Fatalf("write legacy registry: %v", err)
	}

	next, err := New(Options{
		RootDir:   root,
		Profile:   "codex-new",
		AgentKind: AgentCodex,
		Version:   "test",
		Adapter:   &fakeAdapter{},
		PID:       9002,
	})
	if err != nil {
		t.Fatalf("New(next) error = %v", err)
	}
	err = next.Start(context.Background(), StartOptions{
		AppID:      "cli_legacy",
		Tenant:     TenantFeishu,
		AgentKind:  AgentCodex,
		ConfigPath: filepath.Join(root, "new.json"),
	})
	var conflict *RuntimeLockConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("Start() error = %v, want legacy app lock conflict", err)
	}
	if conflict.Kind != LockApp || conflict.Meta == nil || conflict.Meta.AppID != "cli_legacy" {
		t.Fatalf("legacy conflict = %#v", conflict)
	}
}

func TestReadAndPruneProcessesRemovesStaleEntries(t *testing.T) {
	root := t.TempDir()
	coord := newTestCoordinator(t, root, "codex")
	profileLock, err := coord.AcquireProfileLock()
	if err != nil {
		t.Fatalf("AcquireProfileLock() error = %v", err)
	}
	appLock, err := coord.AcquireAppLock("cli_stale")
	if err != nil {
		t.Fatalf("AcquireAppLock() error = %v", err)
	}
	entry, err := coord.RegisterProcess(StartOptions{
		AppID:      "cli_stale",
		Tenant:     TenantFeishu,
		AgentKind:  AgentCodex,
		ConfigPath: filepath.Join(root, "config.json"),
		Version:    "test",
	})
	if err != nil {
		t.Fatalf("RegisterProcess() error = %v", err)
	}
	if entry.ID == "" {
		t.Fatalf("registered entry has empty id")
	}

	if err := appLock.Release(); err != nil {
		t.Fatalf("app lock release error = %v", err)
	}
	if err := profileLock.Release(); err != nil {
		t.Fatalf("profile lock release error = %v", err)
	}

	entries, err := coord.ReadAndPruneProcesses()
	if err != nil {
		t.Fatalf("ReadAndPruneProcesses() error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries after prune = %#v, want empty", entries)
	}
}

func TestShutdownUnregistersProcess(t *testing.T) {
	root := t.TempDir()
	coord, err := New(Options{
		RootDir:   root,
		Profile:   "codex",
		AgentKind: AgentCodex,
		Version:   "test",
		Adapter:   &fakeAdapter{botName: "Codex"},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := coord.Start(context.Background(), StartOptions{
		AppID:      "cli_shutdown",
		Tenant:     TenantFeishu,
		AgentKind:  AgentCodex,
		ConfigPath: filepath.Join(root, "config.json"),
	}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	status, err := coord.Status(context.Background())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if !status.Started || len(status.Processes) != 1 || status.Entry == nil {
		t.Fatalf("status after start = %#v", status)
	}

	if err := coord.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	status, err = coord.Status(context.Background())
	if err != nil {
		t.Fatalf("Status() after shutdown error = %v", err)
	}
	if status.Started || len(status.Processes) != 0 {
		t.Fatalf("status after shutdown = %#v, want stopped and empty registry", status)
	}
}

func TestShutdownFailureKeepsRuntimeStateRetryable(t *testing.T) {
	root := t.TempDir()
	shutdownErr := errors.New("shutdown failed")
	handle := &fakeHandle{status: AdapterStatus{Connected: true, BotName: "Codex"}, shutdownErr: shutdownErr}
	coord, err := New(Options{
		RootDir:   root,
		Profile:   "codex",
		AgentKind: AgentCodex,
		Version:   "test",
		Adapter:   &fakeAdapter{handle: handle},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := coord.Start(context.Background(), StartOptions{
		AppID:      "cli_shutdown_retry",
		Tenant:     TenantFeishu,
		AgentKind:  AgentCodex,
		ConfigPath: filepath.Join(root, "config.json"),
	}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := coord.Shutdown(context.Background()); !errors.Is(err, shutdownErr) {
		t.Fatalf("Shutdown error = %v, want %v", err, shutdownErr)
	}
	status, err := coord.Status(context.Background())
	if err != nil {
		t.Fatalf("Status() after failed shutdown error = %v", err)
	}
	if !status.Started || len(status.Processes) != 1 || status.Entry == nil {
		t.Fatalf("status after failed shutdown = %#v, want still started and registered", status)
	}

	handle.shutdownErr = nil
	if err := coord.Shutdown(context.Background()); err != nil {
		t.Fatalf("retry Shutdown() error = %v", err)
	}
	status, err = coord.Status(context.Background())
	if err != nil {
		t.Fatalf("Status() after retry shutdown error = %v", err)
	}
	if status.Started || len(status.Processes) != 0 {
		t.Fatalf("status after retry shutdown = %#v, want stopped and empty registry", status)
	}
}

func TestReconnectCallsHookAndUpdatesRegistry(t *testing.T) {
	root := t.TempDir()
	handle := &fakeHandle{status: AdapterStatus{Connected: true, BotName: "Codex"}}
	coord, err := New(Options{
		RootDir:   root,
		Profile:   "codex",
		AgentKind: AgentCodex,
		Version:   "test",
		Adapter:   &fakeAdapter{handle: handle},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := coord.Start(context.Background(), StartOptions{
		AppID:      "cli_old",
		Tenant:     TenantFeishu,
		AgentKind:  AgentCodex,
		ConfigPath: filepath.Join(root, "old.json"),
	}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer coord.Shutdown(context.Background())

	if err := coord.Reconnect(context.Background(), ReconnectOptions{
		AppID:      "cli_new",
		Tenant:     TenantLark,
		ConfigPath: filepath.Join(root, "new.json"),
	}); err != nil {
		t.Fatalf("Reconnect() error = %v", err)
	}
	if handle.reconnects != 1 {
		t.Fatalf("reconnects = %d, want 1", handle.reconnects)
	}
	if handle.lastReconnect.PreviousAppID != "cli_old" || handle.lastReconnect.AppID != "cli_new" {
		t.Fatalf("last reconnect = %#v", handle.lastReconnect)
	}
	status, err := coord.Status(context.Background())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.Entry == nil || status.Entry.AppID != "cli_new" || status.Entry.Tenant != TenantLark {
		t.Fatalf("entry after reconnect = %#v", status.Entry)
	}
	if len(status.Processes) != 1 || status.Processes[0].AppID != "cli_new" {
		t.Fatalf("registry after reconnect = %#v", status.Processes)
	}
}

func newTestCoordinator(t *testing.T, root, profile string) *Coordinator {
	t.Helper()
	coord, err := New(Options{
		RootDir:    root,
		Profile:    profile,
		AgentKind:  AgentCodex,
		Version:    "test",
		PID:        9001,
		LockStale:  time.Minute,
		LockUpdate: time.Hour,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return coord
}

type fakeAdapter struct {
	handle  *fakeHandle
	botName string
	starts  int
}

func (a *fakeAdapter) Start(_ context.Context, req StartRequest) (RuntimeHandle, error) {
	a.starts++
	if a.handle != nil {
		return a.handle, nil
	}
	return &fakeHandle{
		status: AdapterStatus{
			Connected: true,
			BotName:   firstNonEmpty(a.botName, req.AppID),
		},
	}, nil
}

type fakeHandle struct {
	status        AdapterStatus
	shutdowns     int
	shutdownErr   error
	reconnects    int
	lastReconnect ReconnectRequest
}

func (h *fakeHandle) Shutdown(context.Context) error {
	h.shutdowns++
	if h.shutdownErr != nil {
		return h.shutdownErr
	}
	h.status.Connected = false
	return nil
}

func (h *fakeHandle) Status(context.Context) (AdapterStatus, error) {
	return h.status, nil
}

func (h *fakeHandle) Reconnect(_ context.Context, req ReconnectRequest) error {
	h.reconnects++
	h.lastReconnect = req
	h.status.Connected = true
	h.status.BotName = req.AppID
	return nil
}
