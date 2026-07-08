package bridge

import (
	"context"
	"errors"
	"testing"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/runtimecoord"
)

func TestNilRuntimeReturnsRuntimeError(t *testing.T) {
	var runtime *Runtime
	if err := runtime.Start(context.Background(), RuntimeStartOptions{}); !errors.Is(err, ErrNilRuntime) {
		t.Fatalf("Start error = %v, want ErrNilRuntime", err)
	}
	if err := runtime.Shutdown(context.Background()); !errors.Is(err, ErrNilRuntime) {
		t.Fatalf("Shutdown error = %v, want ErrNilRuntime", err)
	}
	if _, err := runtime.Status(context.Background()); !errors.Is(err, ErrNilRuntime) {
		t.Fatalf("Status error = %v, want ErrNilRuntime", err)
	}
	if err := runtime.Reconnect(context.Background(), RuntimeReconnectOptions{}); !errors.Is(err, ErrNilRuntime) {
		t.Fatalf("Reconnect error = %v, want ErrNilRuntime", err)
	}
}

func TestRuntimeAdapterNilHandleReturnsError(t *testing.T) {
	adapter := RuntimeAdapterFunc(func(context.Context, RuntimeStartRequest) (RuntimeHandle, error) {
		return nil, nil
	})
	runtime, err := NewRuntime(RuntimeOptions{
		RootDir:   t.TempDir(),
		AgentKind: RuntimeAgentCodex,
		Adapter:   adapter,
	})
	if err != nil {
		t.Fatalf("NewRuntime returned error: %v", err)
	}
	if err := runtime.Start(context.Background(), RuntimeStartOptions{AppID: "cli_bridge_test"}); !errors.Is(err, ErrNilRuntimeHandle) {
		t.Fatalf("Start error = %v, want ErrNilRuntimeHandle", err)
	}
}

func TestRuntimeRejectsInvalidAgentKindAndTenant(t *testing.T) {
	if _, err := NewRuntime(RuntimeOptions{
		RootDir:   t.TempDir(),
		AgentKind: RuntimeAgentKind("typo"),
		Adapter: RuntimeAdapterFunc(func(context.Context, RuntimeStartRequest) (RuntimeHandle, error) {
			return &retryableRuntimeHandle{}, nil
		}),
	}); !errors.Is(err, ErrRuntimeAgentKindInvalid) {
		t.Fatalf("NewRuntime error = %v, want ErrRuntimeAgentKindInvalid", err)
	}

	runtime, err := NewRuntime(RuntimeOptions{
		RootDir:   t.TempDir(),
		AgentKind: RuntimeAgentCodex,
		Adapter: RuntimeAdapterFunc(func(context.Context, RuntimeStartRequest) (RuntimeHandle, error) {
			return &retryableRuntimeHandle{}, nil
		}),
	})
	if err != nil {
		t.Fatalf("NewRuntime returned error: %v", err)
	}
	if err := runtime.Start(context.Background(), RuntimeStartOptions{
		AppID:  "cli_bridge_test",
		Tenant: RuntimeTenant("larks"),
	}); !errors.Is(err, ErrRuntimeTenantInvalid) {
		t.Fatalf("Start error = %v, want ErrRuntimeTenantInvalid", err)
	}
}

func TestRuntimeStartReturnsPublicLockConflict(t *testing.T) {
	root := t.TempDir()
	adapter := RuntimeAdapterFunc(func(context.Context, RuntimeStartRequest) (RuntimeHandle, error) {
		return &retryableRuntimeHandle{status: RuntimeAdapterStatus{Connected: true, BotName: "Bridge Bot"}}, nil
	})
	first, err := NewRuntime(RuntimeOptions{
		RootDir:   root,
		Profile:   "codex",
		AgentKind: RuntimeAgentCodex,
		Adapter:   adapter,
	})
	if err != nil {
		t.Fatalf("first NewRuntime returned error: %v", err)
	}
	if err := first.Start(context.Background(), RuntimeStartOptions{AppID: "cli_bridge_test"}); err != nil {
		t.Fatalf("first Start returned error: %v", err)
	}
	t.Cleanup(func() {
		_ = first.Shutdown(context.Background())
	})

	second, err := NewRuntime(RuntimeOptions{
		RootDir:   root,
		Profile:   "codex",
		AgentKind: RuntimeAgentCodex,
		Adapter:   adapter,
	})
	if err != nil {
		t.Fatalf("second NewRuntime returned error: %v", err)
	}
	err = second.Start(context.Background(), RuntimeStartOptions{AppID: "cli_bridge_test"})
	var conflict *RuntimeLockConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("second Start error = %v, want RuntimeLockConflictError", err)
	}
	if conflict.Kind == "" || conflict.Target == "" || conflict.Meta == nil || conflict.Meta.Profile != "codex" {
		t.Fatalf("conflict = %#v", conflict)
	}
}

func TestRuntimeStartMapsAlreadyStartedSentinel(t *testing.T) {
	runtime, err := NewRuntime(RuntimeOptions{
		RootDir:   t.TempDir(),
		AgentKind: RuntimeAgentCodex,
		Adapter: RuntimeAdapterFunc(func(context.Context, RuntimeStartRequest) (RuntimeHandle, error) {
			return &retryableRuntimeHandle{}, nil
		}),
	})
	if err != nil {
		t.Fatalf("NewRuntime returned error: %v", err)
	}
	if err := runtime.Start(context.Background(), RuntimeStartOptions{AppID: "cli_bridge_test"}); err != nil {
		t.Fatalf("first Start returned error: %v", err)
	}
	t.Cleanup(func() {
		_ = runtime.Shutdown(context.Background())
	})
	if err := runtime.Start(context.Background(), RuntimeStartOptions{AppID: "cli_bridge_test"}); !errors.Is(err, ErrRuntimeAlreadyStarted) {
		t.Fatalf("second Start error = %v, want ErrRuntimeAlreadyStarted", err)
	}
}

func TestRuntimeErrorMapperReturnsPublicSameAppConflict(t *testing.T) {
	err := toPublicRuntimeError(&runtimecoord.SameAppConflictError{
		AppID: "cli_bridge_test",
		Others: []runtimecoord.ProcessEntry{{
			ID:          "proc-1",
			PID:         123,
			AppID:       "cli_bridge_test",
			ProfileName: "codex",
			AgentKind:   runtimecoord.AgentCodex,
		}},
	})
	var conflict *RuntimeSameAppConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("mapped error = %v, want RuntimeSameAppConflictError", err)
	}
	if conflict.AppID != "cli_bridge_test" || len(conflict.Others) != 1 || conflict.Others[0].ID != "proc-1" {
		t.Fatalf("conflict = %#v", conflict)
	}
}
