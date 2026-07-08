package service

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/runtimecoord"
)

func TestDefinitionsRunForegroundCommandWithProfileAndEnv(t *testing.T) {
	inputs := DefinitionInputs{
		Executable: "/usr/local/bin/lark-channel-bridge",
		EnvPath:    "/usr/local/bin:/usr/bin",
		Profile:    "codex",
		Home:       "/tmp/lark-home",
		StdoutPath: "/tmp/lark-home/profiles/codex/logs/daemon/daemon-stdout.log",
		StderrPath: "/tmp/lark-home/profiles/codex/logs/daemon/daemon-stderr.log",
	}
	plist, err := BuildLaunchdPlist(inputs)
	if err != nil {
		t.Fatalf("BuildLaunchdPlist returned error: %v", err)
	}
	for _, want := range []string{"/usr/local/bin/lark-channel-bridge", "<string>run</string>", "<string>--profile</string>", "<string>codex</string>", "LARK_CHANNEL_HOME", "/tmp/lark-home"} {
		if !strings.Contains(plist, want) {
			t.Fatalf("launchd plist missing %q:\n%s", want, plist)
		}
	}

	unit, err := BuildSystemdUnit(inputs)
	if err != nil {
		t.Fatalf("BuildSystemdUnit returned error: %v", err)
	}
	for _, want := range []string{`ExecStart="/usr/local/bin/lark-channel-bridge" run --profile "codex"`, `Environment="PATH=/usr/local/bin:/usr/bin"`, `Environment="LARK_CHANNEL_HOME=/tmp/lark-home"`} {
		if !strings.Contains(unit, want) {
			t.Fatalf("systemd unit missing %q:\n%s", want, unit)
		}
	}

	cmd, err := BuildWindowsLauncherCmd(inputs)
	if err != nil {
		t.Fatalf("BuildWindowsLauncherCmd returned error: %v", err)
	}
	for _, want := range []string{`set "LARK_CHANNEL_HOME=/tmp/lark-home"`, `set "PATH=/usr/local/bin:/usr/bin"`, `"/usr/local/bin/lark-channel-bridge" run --profile "codex"`} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("windows launcher missing %q:\n%s", want, cmd)
		}
	}
}

func TestWindowsLauncherEscapesBatchMetacharacters(t *testing.T) {
	cmd, err := BuildWindowsLauncherCmd(DefinitionInputs{
		Executable: `C:\Program Files\Lark & Codex^\bridge"bin.exe`,
		EnvPath:    `C:\Tools;%PATH%;C:\A&B`,
		Profile:    `codex & feishu ^ "quoted" !bang!`,
		Home:       `C:\Users\me\lark%home%^&`,
		StdoutPath: `C:\Logs\out&%.log`,
		StderrPath: `C:\Logs\err^".log`,
	})
	if err != nil {
		t.Fatalf("BuildWindowsLauncherCmd returned error: %v", err)
	}
	for _, want := range []string{
		"setlocal DisableDelayedExpansion",
		`set "LARK_CHANNEL_HOME=C:\Users\me\lark%%home%%^^^&"`,
		`set "PATH=C:\Tools;%%PATH%%;C:\A^&B"`,
		`"C:\Program Files\Lark ^& Codex^^\bridge^"bin.exe" run --profile "codex ^& feishu ^^ ^"quoted^" !bang!"`,
		`>> "C:\Logs\out^&%%.log" 2>> "C:\Logs\err^^^".log"`,
	} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("windows launcher missing escaped fragment %q:\n%s", want, cmd)
		}
	}
}

func TestControllerStartInstallsStopsExistingAndWaitsForFreshRegistryEntry(t *testing.T) {
	ctx := context.Background()
	adapter := &fakeAdapter{running: true, installed: true}
	preflightCalled := false
	entries := []runtimecoord.ProcessEntry{
		{PID: 100, AppID: "cli_app", ProfileName: "codex", BotName: "Old Bot"},
	}
	controller := Controller{
		Adapter:     adapter,
		RootDir:     t.TempDir(),
		Profile:     "codex",
		AppID:       "cli_app",
		AgentKind:   runtimecoord.AgentCodex,
		WaitTimeout: 50 * time.Millisecond,
		Preflight: func(context.Context) error {
			preflightCalled = true
			return nil
		},
		ProcessLister: ProcessListerFunc(func(context.Context) ([]runtimecoord.ProcessEntry, error) {
			if adapter.started {
				return append(entries, runtimecoord.ProcessEntry{ID: "new", PID: 200, AppID: "cli_app", ProfileName: "codex", AgentKind: runtimecoord.AgentCodex, BotName: "Codex Bot"}), nil
			}
			return entries, nil
		}),
	}
	result, err := controller.Start(ctx)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if !preflightCalled {
		t.Fatalf("preflight was not called")
	}
	if !result.Connected || result.Process == nil || result.Process.PID != 200 {
		t.Fatalf("result = %#v, want connected fresh process", result)
	}
	wantOps := []string{"is-running", "describe", "install", "is-running", "stop", "wait-stopped", "start", "describe", "is-running"}
	if !reflect.DeepEqual(adapter.ops, wantOps) {
		t.Fatalf("adapter ops = %#v, want %#v", adapter.ops, wantOps)
	}
}

func TestControllerStartDoesNotStopExistingServiceWhenPreflightFails(t *testing.T) {
	ctx := context.Background()
	adapter := &fakeAdapter{running: true, installed: true, statusText: "pid = 123\n"}
	controller := Controller{
		Adapter:   adapter,
		RootDir:   t.TempDir(),
		Profile:   "codex",
		AppID:     "cli_app",
		AgentKind: runtimecoord.AgentCodex,
		Preflight: func(context.Context) error {
			return errors.New("preflight failed")
		},
	}
	_, err := controller.Start(ctx)
	if err == nil || !strings.Contains(err.Error(), "preflight failed") {
		t.Fatalf("Start error = %v, want preflight failure", err)
	}
	if !adapter.running || adapter.started || !adapter.installed {
		t.Fatalf("adapter = %#v, old service should still be running and not restarted", adapter)
	}
	wantOps := []string{"is-running", "describe"}
	if !reflect.DeepEqual(adapter.ops, wantOps) {
		t.Fatalf("adapter ops = %#v, want %#v", adapter.ops, wantOps)
	}
}

func TestControllerStartRequiresAgentKindForRuntimeLocks(t *testing.T) {
	controller := Controller{
		Adapter: &fakeAdapter{},
		RootDir: t.TempDir(),
		Profile: "codex",
		AppID:   "cli_app",
	}
	_, err := controller.Start(context.Background())
	if !errors.Is(err, runtimecoord.ErrAgentKindRequired) {
		t.Fatalf("Start error = %v, want ErrAgentKindRequired", err)
	}
}

func TestControllerStartAllowsLocksHeldByCurrentServicePID(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	holder, err := runtimecoord.New(runtimecoord.Options{RootDir: root, Profile: "codex", AgentKind: runtimecoord.AgentCodex, PID: 123})
	if err != nil {
		t.Fatalf("runtimecoord.New returned error: %v", err)
	}
	profileLock, err := holder.AcquireProfileLock()
	if err != nil {
		t.Fatalf("AcquireProfileLock returned error: %v", err)
	}
	defer profileLock.Release()
	appLock, err := holder.AcquireAppLock("cli_app")
	if err != nil {
		t.Fatalf("AcquireAppLock returned error: %v", err)
	}
	defer appLock.Release()

	adapter := &fakeAdapter{running: true, installed: true, statusText: "pid = 123\n"}
	preflightCalled := false
	controller := Controller{
		Adapter:     adapter,
		RootDir:     root,
		Profile:     "codex",
		AppID:       "cli_app",
		AgentKind:   runtimecoord.AgentCodex,
		WaitTimeout: time.Millisecond,
		Preflight: func(context.Context) error {
			preflightCalled = true
			return nil
		},
	}
	if _, err := controller.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if !preflightCalled || !adapter.started {
		t.Fatalf("preflightCalled=%v adapter=%#v, want preflight and start", preflightCalled, adapter)
	}
}

func TestControllerStartRejectsForegroundRuntimeLockBeforeInstall(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	holder, err := runtimecoord.New(runtimecoord.Options{RootDir: root, Profile: "codex", AgentKind: runtimecoord.AgentCodex, PID: 4242})
	if err != nil {
		t.Fatalf("runtimecoord.New returned error: %v", err)
	}
	lock, err := holder.AcquireProfileLock()
	if err != nil {
		t.Fatalf("AcquireProfileLock returned error: %v", err)
	}
	defer lock.Release()

	adapter := &fakeAdapter{}
	controller := Controller{
		Adapter:   adapter,
		RootDir:   root,
		Profile:   "codex",
		AppID:     "cli_app",
		AgentKind: runtimecoord.AgentCodex,
	}
	_, err = controller.Start(ctx)
	var conflict *runtimecoord.RuntimeLockConflictError
	if !errors.As(err, &conflict) || conflict.Kind != runtimecoord.LockProfile {
		t.Fatalf("Start error = %v, want profile lock conflict", err)
	}
	if adapter.started || adapter.installed {
		t.Fatalf("adapter was mutated despite lock conflict: %#v", adapter)
	}
}

func TestControllerStartRetriesRuntimeLockWhenHandlerClearsHolder(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	holder, err := runtimecoord.New(runtimecoord.Options{RootDir: root, Profile: "codex", AgentKind: runtimecoord.AgentCodex, PID: 4242})
	if err != nil {
		t.Fatalf("runtimecoord.New returned error: %v", err)
	}
	lock, err := holder.AcquireProfileLock()
	if err != nil {
		t.Fatalf("AcquireProfileLock returned error: %v", err)
	}
	adapter := &fakeAdapter{}
	handlerCalled := false
	controller := Controller{
		Adapter:     adapter,
		RootDir:     root,
		Profile:     "codex",
		AppID:       "cli_app",
		AgentKind:   runtimecoord.AgentCodex,
		WaitTimeout: time.Millisecond,
		LockHandler: RuntimeLockHandlerFunc(func(ctx context.Context, meta runtimecoord.RuntimeLockMeta) (bool, error) {
			handlerCalled = true
			if meta.PID != 4242 {
				t.Fatalf("lock holder pid = %d, want 4242", meta.PID)
			}
			err := lock.Release()
			lock = nil
			return true, err
		}),
	}
	t.Cleanup(func() {
		if lock != nil {
			_ = lock.Release()
		}
	})

	_, err = controller.Start(ctx)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if !handlerCalled {
		t.Fatalf("lock handler was not called")
	}
	if !adapter.installed || !adapter.started {
		t.Fatalf("adapter = %#v, want installed and started", adapter)
	}
}

func TestControllerStatusAndUnregisterUseAdapterState(t *testing.T) {
	ctx := context.Background()
	adapter := &fakeAdapter{running: true, installed: true, statusText: "pid = 123\nlast exit code = 0\n"}
	controller := Controller{
		Adapter: adapter,
		RootDir: t.TempDir(),
		Profile: "codex",
		AppID:   "cli_app",
		ProcessLister: ProcessListerFunc(func(context.Context) ([]runtimecoord.ProcessEntry, error) {
			return []runtimecoord.ProcessEntry{{ID: "p", PID: 123, AppID: "cli_app", ProfileName: "codex", BotName: "Codex Bot"}}, nil
		}),
	}
	status := controller.Status(ctx)
	if !status.Installed || !status.Running || status.PID != "123" || status.Process == nil {
		t.Fatalf("status = %#v", status)
	}
	result, err := controller.Unregister(ctx)
	if err != nil {
		t.Fatalf("Unregister returned error: %v", err)
	}
	if !result.Removed || adapter.installed || adapter.running {
		t.Fatalf("unregister result = %#v adapter = %#v", result, adapter)
	}
}

type fakeAdapter struct {
	installed  bool
	running    bool
	started    bool
	statusText string
	ops        []string
}

func (a *fakeAdapter) PlatformName() string { return "fake" }
func (a *fakeAdapter) FileExists() bool     { return a.installed }
func (a *fakeAdapter) IsRunning(context.Context) bool {
	a.ops = append(a.ops, "is-running")
	return a.running
}
func (a *fakeAdapter) ServicePath() string { return "/tmp/fake.service" }
func (a *fakeAdapter) Install(context.Context) error {
	a.ops = append(a.ops, "install")
	a.installed = true
	return nil
}
func (a *fakeAdapter) Start(context.Context) Result {
	a.ops = append(a.ops, "start")
	a.running = true
	a.started = true
	return Result{OK: true}
}
func (a *fakeAdapter) Stop(context.Context) Result {
	a.ops = append(a.ops, "stop")
	a.running = false
	return Result{OK: true}
}
func (a *fakeAdapter) StopAndDisableAutostart(context.Context) Result {
	a.ops = append(a.ops, "stop-disable")
	a.running = false
	return Result{OK: true}
}
func (a *fakeAdapter) Restart(context.Context) Result {
	a.ops = append(a.ops, "restart")
	a.running = true
	a.started = true
	return Result{OK: true}
}
func (a *fakeAdapter) WaitUntilStopped(context.Context, time.Duration) (bool, error) {
	a.ops = append(a.ops, "wait-stopped")
	return !a.running, nil
}
func (a *fakeAdapter) DeleteFile(context.Context) error {
	a.ops = append(a.ops, "delete")
	a.installed = false
	return nil
}
func (a *fakeAdapter) DescribeStatus(context.Context) string {
	a.ops = append(a.ops, "describe")
	return a.statusText
}
func (a *fakeAdapter) ParseStatus(text string) ParsedStatus {
	return ParsedStatus{PID: firstSubmatch(text, `pid\s*=\s*(\d+)`), LastExit: firstSubmatch(text, `last exit code\s*=\s*(\d+)`)}
}
