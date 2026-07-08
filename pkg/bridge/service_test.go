package bridge

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestServiceControllerFacadeUsesPublicProcessTypes(t *testing.T) {
	ctx := context.Background()
	adapter := &testServiceAdapter{}
	controller := NewServiceController(ServiceControllerOptions{
		Adapter:     adapter,
		RootDir:     t.TempDir(),
		Profile:     "codex",
		AppID:       "cli_app",
		AgentKind:   RuntimeAgentCodex,
		WaitTimeout: 10 * time.Millisecond,
		ProcessLister: ServiceProcessListerFunc(func(context.Context) ([]ServiceProcessEntry, error) {
			if adapter.started {
				return []ServiceProcessEntry{{
					ID:          "proc",
					PID:         123,
					AppID:       "cli_app",
					Tenant:      "feishu",
					ProfileName: "codex",
					AgentKind:   "codex",
					BotName:     "Codex Bot",
				}}, nil
			}
			return nil, nil
		}),
	})

	result, err := StartService(ctx, controller)
	if err != nil {
		t.Fatalf("StartService returned error: %v", err)
	}
	if !result.Connected || result.Process == nil || result.Process.AgentKind != "codex" {
		t.Fatalf("result = %#v, want connected public process entry", result)
	}
	status := controller.Status(ctx)
	if status.Process == nil || status.Process.BotName != "Codex Bot" {
		t.Fatalf("status = %#v, want public process entry", status)
	}
}

func TestServiceControllerRequireConnectedReturnsPublicTimeout(t *testing.T) {
	ctx := context.Background()
	adapter := &testServiceAdapter{}
	controller := NewServiceController(ServiceControllerOptions{
		Adapter:          adapter,
		RootDir:          t.TempDir(),
		Profile:          "codex",
		AppID:            "cli_app",
		AgentKind:        RuntimeAgentCodex,
		WaitTimeout:      time.Millisecond,
		RequireConnected: true,
		ProcessLister: ServiceProcessListerFunc(func(context.Context) ([]ServiceProcessEntry, error) {
			return nil, nil
		}),
	})

	result, err := StartService(ctx, controller)
	if !errors.Is(err, ErrServiceConnectTimeout) {
		t.Fatalf("StartService err = %v, want ErrServiceConnectTimeout", err)
	}
	if result.Connected || result.Process != nil || !result.Service.Running {
		t.Fatalf("result = %#v, want running service without connected bridge process", result)
	}
}

func TestServiceControllerRequireConnectedReturnsProcessListerError(t *testing.T) {
	ctx := context.Background()
	adapter := &testServiceAdapter{}
	listErr := errors.New("registry unavailable")
	controller := NewServiceController(ServiceControllerOptions{
		Adapter:          adapter,
		RootDir:          t.TempDir(),
		Profile:          "codex",
		AppID:            "cli_app",
		AgentKind:        RuntimeAgentCodex,
		WaitTimeout:      time.Millisecond,
		RequireConnected: true,
		ProcessLister: ServiceProcessListerFunc(func(context.Context) ([]ServiceProcessEntry, error) {
			return nil, listErr
		}),
	})

	result, err := StartService(ctx, controller)
	if !errors.Is(err, listErr) {
		t.Fatalf("StartService err = %v, want %v", err, listErr)
	}
	if result.Service.ProcessError != listErr.Error() {
		t.Fatalf("service process error = %q, want %q", result.Service.ProcessError, listErr.Error())
	}
}

type testServiceAdapter struct {
	installed bool
	running   bool
	started   bool
}

func (a *testServiceAdapter) PlatformName() string { return "test" }
func (a *testServiceAdapter) FileExists() bool     { return a.installed }
func (a *testServiceAdapter) IsRunning(context.Context) bool {
	return a.running
}
func (a *testServiceAdapter) ServicePath() string { return "/tmp/test.service" }
func (a *testServiceAdapter) Install(context.Context) error {
	a.installed = true
	return nil
}
func (a *testServiceAdapter) Start(context.Context) ServiceResult {
	a.running = true
	a.started = true
	return ServiceResult{OK: true}
}
func (a *testServiceAdapter) Stop(context.Context) ServiceResult {
	a.running = false
	return ServiceResult{OK: true}
}
func (a *testServiceAdapter) StopAndDisableAutostart(context.Context) ServiceResult {
	a.running = false
	return ServiceResult{OK: true}
}
func (a *testServiceAdapter) Restart(context.Context) ServiceResult {
	a.running = true
	a.started = true
	return ServiceResult{OK: true}
}
func (a *testServiceAdapter) WaitUntilStopped(context.Context, time.Duration) (bool, error) {
	return !a.running, nil
}
func (a *testServiceAdapter) DeleteFile(context.Context) error {
	a.installed = false
	return nil
}
func (a *testServiceAdapter) DescribeStatus(context.Context) string { return "pid = 123\n" }
func (a *testServiceAdapter) ParseStatus(string) ServiceParsedStatus {
	return ServiceParsedStatus{PID: "123"}
}
