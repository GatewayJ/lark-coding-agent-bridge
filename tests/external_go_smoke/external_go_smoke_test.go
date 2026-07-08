package external_go_smoke

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestExternalConsumerCanImportBridgeSDK(t *testing.T) {
	repoRoot := repositoryRoot(t)
	tmp := t.TempDir()
	runGo(t, tmp, "mod", "init", "smoke.example")
	runGo(t, tmp, "mod", "edit", "-replace", "github.com/zarazhangrui/lark-coding-agent-bridge="+repoRoot)
	runGo(t, tmp, "get", "github.com/zarazhangrui/lark-coding-agent-bridge/pkg/bridge")
	writeSmokeTest(t, filepath.Join(tmp, "bridge_smoke_test.go"))
	runGo(t, tmp, "test", "./...")
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func writeSmokeTest(t *testing.T, path string) {
	t.Helper()
	content := `package smoke

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	bridge "github.com/zarazhangrui/lark-coding-agent-bridge/pkg/bridge"
)

func TestBridgePublicAPICompiles(t *testing.T) {
	surface, err := bridge.NewOAPICommentSurface(nil)
	if surface != nil || !errors.Is(err, bridge.ErrNilLarkTransport) {
		t.Fatalf("NewOAPICommentSurface(nil) surface=%#v err=%v", surface, err)
	}
	var oapiErr *bridge.LarkOAPIError
	if !errors.As(&bridge.LarkOAPIError{Operation: "op", Code: 1, Message: "msg"}, &oapiErr) {
		t.Fatalf("LarkOAPIError does not support errors.As")
	}
	restoreTelemetry := bridge.SetDefaultTelemetry(fakeTelemetry{})
	defer restoreTelemetry()
	restoreFuncTelemetry := bridge.SetDefaultTelemetry(bridge.TelemetryAdapterFunc(func(context.Context, bridge.TelemetryEvent) {}))
	restoreFuncTelemetry()
	restoreNoopTelemetry := bridge.SetDefaultTelemetry(bridge.NoopTelemetryAdapter{})
	restoreNoopTelemetry()
	if events := bridge.RequiredObservabilityEvents(); len(events) == 0 || events[0] != "run.started" {
		t.Fatalf("RequiredObservabilityEvents = %#v", events)
	}
	var factory bridge.AdapterFactory = func(meta bridge.AdapterMeta) bridge.TelemetryAdapter {
		if meta.Version == "" {
			t.Fatalf("AdapterMeta version is empty")
		}
		return fakeTelemetry{}
	}
	_ = factory(bridge.AdapterMeta{Version: "smoke", AppID: "cli_smoke", Tenant: "feishu", Hostname: "host"})
	var _ bridge.RuntimeAdapter = bridge.RuntimeAdapterFunc(func(context.Context, bridge.RuntimeStartRequest) (bridge.RuntimeHandle, error) {
		return nil, nil
	})
	bridge.ReportEvent(context.Background(), "smoke.event", map[string]any{"phase": "external"})
	bridge.ReportMetric(context.Background(), "smoke.metric", 1, map[string]string{"profile": "codex"})
	bridge.ReportError(context.Background(), errors.New("smoke error"), map[string]any{"profile": "codex"})
	jsState := bridge.InitialState()
	jsState = bridge.Reduce(jsState, bridge.Event{Type: bridge.EventText, Delta: strPtr("hello")})
	if bridge.RenderText(jsState) == "" {
		t.Fatalf("RenderText returned empty content")
	}
	if bridge.RenderCard(jsState, bridge.CardRenderOptions{})["schema"] != "2.0" {
		t.Fatalf("RenderCard returned unexpected schema")
	}
	if bridge.FinalizeIfRunning(jsState).Terminal != bridge.TerminalDone {
		t.Fatalf("FinalizeIfRunning did not finish")
	}
	if bridge.MarkInterrupted(jsState).Terminal != bridge.TerminalInterrupted {
		t.Fatalf("MarkInterrupted did not interrupt")
	}
	if bridge.MarkIdleTimeout(jsState, 5).IdleTimeoutMinutes != 5 {
		t.Fatalf("MarkIdleTimeout did not preserve minutes")
	}
	configProfile := bridge.ConfigProfile{
		SchemaVersion: 2,
		AgentKind: bridge.ConfigAgentCodex,
		Accounts: bridge.LarkCLIAccountsConfig{App: bridge.LarkCLIAppCredentials{
			ID: "cli_smoke",
			Secret: "secret",
			Tenant: bridge.LarkCLITenantFeishu,
		}},
		Preferences: map[string]any{},
		Workspaces: bridge.ConfigWorkspaces{Default: t.TempDir()},
		LarkCli: bridge.ConfigLarkCli{IdentityPreset: bridge.ConfigLarkCliIdentityBotOnly},
	}
	_ = bridge.ConfigRoot{
		SchemaVersion: 2,
		ActiveProfile: "codex",
		Preferences: map[string]any{},
		Profiles: map[string]bridge.ConfigProfile{"codex": configProfile},
	}
	larkEnv := bridge.BuildLarkChannelEnv(bridge.LarkCLIEnvContext{
		Profile: "codex",
		RootDir: t.TempDir(),
		LarkCliConfigDir: t.TempDir(),
	})
	if larkEnv["LARK_CHANNEL"] != "1" || larkEnv["LARK_CHANNEL_PROFILE"] != "codex" {
		t.Fatalf("BuildLarkChannelEnv = %#v", larkEnv)
	}
	projection := bridge.BuildLarkCLISourceProjection(bridge.LarkCLIAppConfig{
		Accounts: bridge.LarkCLIAccountsConfig{App: bridge.LarkCLIAppCredentials{
			ID: "cli_smoke",
			Secret: bridge.LarkCLISecretRef{Source: bridge.SecretSourceInline, ID: "app-cli_smoke"},
			Tenant: bridge.LarkCLITenantFeishu,
		}},
	}, bridge.LarkCLIProjectionPaths{})
	if projection.Accounts.App.ID != "cli_smoke" {
		t.Fatalf("BuildLarkCLISourceProjection = %#v", projection)
	}
	secretRoot := t.TempDir()
	secret, err := bridge.ResolveAppSecret(context.Background(), bridge.SecretAppConfig{
		Accounts: bridge.SecretAccountsConfig{App: bridge.SecretAppCredentials{
			ID: "cli_smoke",
			Secret: bridge.PlainSecret("plain-secret"),
			Tenant: "feishu",
		}},
	}, bridge.SecretResolverOptions{Paths: bridge.KeystorePaths{
		SecretsFile: filepath.Join(secretRoot, "secrets.json"),
		KeystoreSaltFile: filepath.Join(secretRoot, "salt"),
		SecretsGetterScript: filepath.Join(secretRoot, "secrets-getter.sh"),
	}})
	if err != nil || secret != "plain-secret" {
		t.Fatalf("ResolveAppSecret secret=%q err=%v", secret, err)
	}
	workspaceStore, err := bridge.NewFileWorkspaceStore(filepath.Join(t.TempDir(), "workspaces.json"))
	if err != nil {
		t.Fatalf("NewFileWorkspaceStore returned error: %v", err)
	}
	workspaceStore.SetCWD("chat-smoke", "/repo")
	workspaceStore.SaveNamed("main", "/repo")
	if workspaceStore.CWDFor("chat-smoke") != "/repo" || workspaceStore.GetNamed("main") != "/repo" {
		t.Fatalf("FileWorkspaceStore did not preserve values")
	}
	if cwd := workspaceStore.ListCWDs()["chat-smoke"]; cwd != "/repo" {
		t.Fatalf("ListCWDs chat-smoke = %q", cwd)
	}
	if removed, err := workspaceStore.RemoveCWD("chat-smoke"); err != nil || !removed {
		t.Fatalf("RemoveCWD removed=%v err=%v", removed, err)
	}
	if workspaceStore.CWDFor("chat-smoke") != "" {
		t.Fatalf("RemoveCWD did not clear chat-smoke")
	}
	card := bridge.RenderRunCard(bridge.NewRunCardState(bridge.RunCardStateInput{
		RunID: "run-smoke",
		Scope: "oc_smoke",
		Status: bridge.RunCardQueued,
	}), bridge.CardRenderOptions{})
	if card.Status != bridge.RunCardQueued {
		t.Fatalf("RenderRunCard status=%q", card.Status)
	}
	transport, err := bridge.NewOAPILarkTransport(bridge.OAPILarkTransportOptions{
		AppID: "cli_smoke",
		AppSecret: "secret",
		DisableWebSocket: true,
	})
	if err != nil || transport == nil {
		t.Fatalf("NewOAPILarkTransport transport=%#v err=%v", transport, err)
	}
	fakeTransport := bridge.NewFakeLarkTransport(bridge.LarkBotIdentity{OpenID: "ou_smoke", Name: "Smoke Bot"})
	instance, err := bridge.New(bridge.Options{
		Home: t.TempDir(),
		Profile: "codex",
		AppID: "cli_smoke",
		AgentKind: bridge.RuntimeAgentCodex,
		LarkTransport: fakeTransport,
		LarkIntake: bridge.LarkIntakeSinkFunc(func(context.Context, bridge.LarkNormalizedEvent) error {
			return nil
		}),
	})
	if err != nil {
		t.Fatalf("bridge.New returned error: %v", err)
	}
	if err := instance.Start(context.Background()); err != nil {
		t.Fatalf("Bridge.Start returned error: %v", err)
	}
	if err := instance.Shutdown(context.Background()); err != nil {
		t.Fatalf("Bridge.Shutdown returned error: %v", err)
	}
	client, err := bridge.NewCodexClient(bridge.CodexClientOptions{DefaultWorkingDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewCodexClient returned error: %v", err)
	}
	commandHandler, err := bridge.NewCommandHandler(client)
	if err != nil {
		t.Fatalf("NewCommandHandler returned error: %v", err)
	}
	if _, err := commandHandler.HandleCommand(context.Background(), bridge.CommandRequest{
		CommandText: "/help",
		ScopeID: "scope-smoke",
		ChatID: "chat-smoke",
		ActorID: "ou_smoke",
		SenderID: "ou_smoke",
		ChatMode: bridge.CommandChatModeP2P,
	}, bridge.CommandOptions{}); err != nil {
		t.Fatalf("CommandHandler.HandleCommand returned error: %v", err)
	}
	_, err = client.HandleCommand(context.Background(), bridge.CommandRequest{
		CommandText: "/resume",
		ScopeID: "scope-smoke",
		ChatID: "chat-smoke",
		ActorID: "ou_smoke",
		SenderID: "ou_smoke",
		ChatMode: bridge.CommandChatModeP2P,
		Access: bridge.AccessDecision{OK: true, Reason: bridge.AccessOwner},
	}, bridge.CommandOptions{})
	if !errors.Is(err, bridge.ErrUntrustedAccessDecision) {
		t.Fatalf("HandleCommand err=%v, want ErrUntrustedAccessDecision", err)
	}
	adapter := &fakeServiceAdapter{}
	controller := bridge.NewServiceController(bridge.ServiceControllerOptions{
		Adapter: adapter,
		RootDir: t.TempDir(),
		Profile: "codex",
		AppID: "cli_smoke",
		AgentKind: bridge.RuntimeAgentCodex,
		WaitTimeout: time.Millisecond,
		ProcessLister: bridge.ServiceProcessListerFunc(func(context.Context) ([]bridge.ServiceProcessEntry, error) {
			if !adapter.started {
				return nil, nil
			}
			return []bridge.ServiceProcessEntry{{
				ID: "proc",
				PID: 123,
				AppID: "cli_smoke",
				Tenant: "feishu",
				ProfileName: "codex",
				AgentKind: "codex",
				BotName: "Smoke Bot",
			}}, nil
		}),
		LockHandler: bridge.ServiceRuntimeLockHandlerFunc(func(context.Context, bridge.ServiceRuntimeLockMeta) (bool, error) {
			return false, nil
		}),
	})
	serviceResult, err := bridge.StartService(context.Background(), controller)
	if err != nil || serviceResult.Process == nil || serviceResult.Process.BotName != "Smoke Bot" {
		t.Fatalf("StartService result=%#v err=%v", serviceResult, err)
	}
	if _, err := bridge.BuildLaunchdPlist(bridge.ServiceDefinitionInputs{Executable: "/bin/echo", Profile: "codex", Home: t.TempDir(), StdoutPath: "/tmp/out", StderrPath: "/tmp/err"}); err != nil {
		t.Fatalf("BuildLaunchdPlist: %v", err)
	}
	if _, err := bridge.BuildSystemdUnit(bridge.ServiceDefinitionInputs{Executable: "/bin/echo", Profile: "codex", Home: t.TempDir(), StdoutPath: "/tmp/out", StderrPath: "/tmp/err"}); err != nil {
		t.Fatalf("BuildSystemdUnit: %v", err)
	}
	if _, err := bridge.BuildWindowsLauncherCmd(bridge.ServiceDefinitionInputs{Executable: "bridge.exe", Profile: "codex", Home: t.TempDir(), StdoutPath: "out.log", StderrPath: "err.log"}); err != nil {
		t.Fatalf("BuildWindowsLauncherCmd: %v", err)
	}
	_, _ = bridge.NewPlatformServiceAdapter(bridge.ServiceAdapterOptions{Profile: "codex", RootDir: t.TempDir(), Executable: "/bin/echo"})
	profileRoot := t.TempDir()
	t.Setenv("SMOKE_PROFILE_SECRET", "secret")
	if _, err := bridge.BootstrapProfileConfig(bridge.BootstrapProfileOptions{
		RootDir: profileRoot,
		Profile: "codex",
		AgentKind: bridge.ConfigAgentCodex,
		AppID: "cli_smoke_profile",
		AppSecret: bridge.SecretReference(bridge.SecretRef{Source: bridge.SecretSourceEnv, ID: "SMOKE_PROFILE_SECRET"}),
		Tenant: bridge.LarkCLITenantFeishu,
		DefaultWorkspace: filepath.Join(profileRoot, "workspaces", "codex"),
	}); err != nil {
		t.Fatalf("BootstrapProfileConfig returned error: %v", err)
	}
	profileAdapter := &fakeServiceAdapter{}
	profileResult, err := bridge.StartProfileService(context.Background(), bridge.ProfileServiceOptions{
		Home: profileRoot,
		Profile: "codex",
		Adapter: profileAdapter,
		SkipCheckLarkCLI: true,
		WaitTimeout: time.Millisecond,
		ProcessLister: bridge.ServiceProcessListerFunc(func(context.Context) ([]bridge.ServiceProcessEntry, error) {
			if !profileAdapter.started {
				return nil, nil
			}
			return []bridge.ServiceProcessEntry{{
				ID: "profile-proc",
				PID: 456,
				AppID: "cli_smoke_profile",
				Tenant: "feishu",
				ProfileName: "codex",
				AgentKind: "codex",
				BotName: "Profile Smoke Bot",
			}}, nil
		}),
	})
	if err != nil || profileResult.Process == nil || profileResult.Process.ID != "profile-proc" {
		t.Fatalf("StartProfileService result=%#v err=%v", profileResult, err)
	}
	profileBridge, profileInfo, err := bridge.NewProfileBridge(context.Background(), bridge.ProfileBridgeOptions{
		Home: profileRoot,
		Profile: "codex",
		AppSecret: "override-secret",
		SecretsGetterCommand: "/bin/echo",
		SkipCheckLarkCLI: true,
		SkipAgentAvailability: true,
		DisableDefaultLogger: true,
		DisableDefaultTelemetry: true,
		LarkTransport: bridge.NewFakeLarkTransport(bridge.LarkBotIdentity{OpenID: "ou_profile_bot", Name: "Profile Bot"}),
	})
	if err != nil || profileInfo.AppID != "cli_smoke_profile" {
		t.Fatalf("NewProfileBridge info=%#v err=%v", profileInfo, err)
	}
	if err := profileBridge.Start(context.Background()); err != nil {
		t.Fatalf("Profile Bridge.Start returned error: %v", err)
	}
	if err := profileBridge.Shutdown(context.Background()); err != nil {
		t.Fatalf("Profile Bridge.Shutdown returned error: %v", err)
	}
}

type fakeServiceAdapter struct {
	installed bool
	running bool
	started bool
}

func (a *fakeServiceAdapter) PlatformName() string { return "fake" }
func (a *fakeServiceAdapter) FileExists() bool { return a.installed }
func (a *fakeServiceAdapter) IsRunning(context.Context) bool { return a.running }
func (a *fakeServiceAdapter) ServicePath() string { return "/tmp/fake.service" }
func (a *fakeServiceAdapter) Install(context.Context) error {
	a.installed = true
	return nil
}
func (a *fakeServiceAdapter) Start(context.Context) bridge.ServiceResult {
	a.running = true
	a.started = true
	return bridge.ServiceResult{OK: true}
}
func (a *fakeServiceAdapter) Stop(context.Context) bridge.ServiceResult {
	a.running = false
	return bridge.ServiceResult{OK: true}
}
func (a *fakeServiceAdapter) StopAndDisableAutostart(context.Context) bridge.ServiceResult {
	a.running = false
	return bridge.ServiceResult{OK: true}
}
func (a *fakeServiceAdapter) Restart(context.Context) bridge.ServiceResult {
	a.running = true
	a.started = true
	return bridge.ServiceResult{OK: true}
}
func (a *fakeServiceAdapter) WaitUntilStopped(context.Context, time.Duration) (bool, error) {
	return !a.running, nil
}
func (a *fakeServiceAdapter) DeleteFile(context.Context) error {
	a.installed = false
	return nil
}
func (a *fakeServiceAdapter) DescribeStatus(context.Context) string { return "pid = 123\n" }
func (a *fakeServiceAdapter) ParseStatus(string) bridge.ServiceParsedStatus {
	return bridge.ServiceParsedStatus{PID: "123"}
}

type fakeTelemetry struct{}

func (fakeTelemetry) Emit(context.Context, bridge.TelemetryEvent) {}
func (fakeTelemetry) RecordError(context.Context, error, map[string]any) {}
func (fakeTelemetry) RecordMetric(context.Context, string, float64, map[string]string) {}
func (fakeTelemetry) Flush(context.Context) error { return nil }
func (fakeTelemetry) Close(context.Context) error { return nil }

func strPtr(value string) *string { return &value }
	`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write smoke test: %v", err)
	}
}

func runGo(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go %v failed: %v\n%s", args, err, output)
	}
}
