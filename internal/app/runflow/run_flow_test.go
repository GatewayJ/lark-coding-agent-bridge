package runflow

import (
	"context"
	"testing"
	"time"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/runexecutor"
	appsession "github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/session"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/access"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/capability"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/profile"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/runpolicy"
	agentport "github.com/zarazhangrui/lark-coding-agent-bridge/internal/ports/agent"
)

func TestStartRecordsAndResumesCodexThread(t *testing.T) {
	executor := &fakeExecutor{}
	sessions := appsession.NewStore("")
	catalog := appsession.NewCatalog("")
	cfg := profile.DefaultConfig(profile.AgentCodex)
	cfg.Workspaces.Default = t.TempDir()
	cap := capability.Codex(cfg.Permissions.MaxAccess, "")

	first, err := Start(context.Background(), StartInput{
		ScopeID:        "scope-1",
		Scope:          testScope(),
		Prompt:         "hello",
		Access:         access.Decision{OK: true, Reason: access.ReasonAllowedUser},
		Capability:     cap,
		ProfileConfig:  cfg,
		Sessions:       sessions,
		SessionCatalog: catalog,
		Executor:       executor,
		Now:            time.UnixMilli(1000),
		Attachments: []runpolicy.AgentAttachment{
			{Kind: "image", Requiredness: runpolicy.AttachmentOptional, Decision: runpolicy.AttachmentAccepted, Path: "/cache/a.png"},
			{Kind: "image", Requiredness: runpolicy.AttachmentOptional, Decision: runpolicy.AttachmentRejected, Path: "/cache/rejected.png"},
			{Kind: "file", Requiredness: runpolicy.AttachmentOptional, Decision: runpolicy.AttachmentAccepted, Path: "/cache/file.txt"},
		},
	})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if !first.OK {
		t.Fatalf("Start rejected first run: %#v", first.RejectReason)
	}
	if got := executor.last.Images; len(got) != 1 || got[0] != "/cache/a.png" {
		t.Fatalf("Codex images = %#v, want only accepted image", got)
	}
	threadID := "thread-1"
	if err := RecordSessionEvent(RecordSessionEventInput{
		ScopeID:        "scope-1",
		Sessions:       sessions,
		SessionCatalog: catalog,
		Capability:     cap,
		Policy:         first.Policy,
		Event:          agentport.AgentEvent{Type: agentport.EventSystem, ThreadID: &threadID},
		Now:            time.UnixMilli(2000),
	}); err != nil {
		t.Fatalf("RecordSessionEvent returned error: %v", err)
	}

	second, err := Start(context.Background(), StartInput{
		ScopeID:        "scope-1",
		Scope:          testScope(),
		Prompt:         "hello again",
		Access:         access.Decision{OK: true, Reason: access.ReasonAllowedUser},
		Capability:     cap,
		ProfileConfig:  cfg,
		Sessions:       sessions,
		SessionCatalog: catalog,
		Executor:       executor,
		Now:            time.UnixMilli(3000),
	})
	if err != nil {
		t.Fatalf("second Start returned error: %v", err)
	}
	if !second.OK || second.ResumeFrom != "thread-1" || executor.last.ThreadID != "thread-1" {
		t.Fatalf("second result = %#v, submit = %#v; want codex thread resume", second, executor.last)
	}
}

func TestStartFallsBackToClaudeStoreAndClearsStaleCWD(t *testing.T) {
	executor := &fakeExecutor{}
	sessions := appsession.NewStore("")
	cfg := profile.DefaultConfig(profile.AgentClaude)
	cfg.Workspaces.Default = t.TempDir()
	cap := capability.Claude(cfg.Permissions.MaxAccess, "")
	sessions.Set("scope-1", "stale-session", "/other")

	result, err := Start(context.Background(), StartInput{
		ScopeID:       "scope-1",
		Scope:         testScope(),
		Prompt:        "hello",
		Access:        access.Decision{OK: true, Reason: access.ReasonAllowedUser},
		Capability:    cap,
		ProfileConfig: cfg,
		Sessions:      sessions,
		Executor:      executor,
		Now:           time.UnixMilli(1000),
	})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if !result.OK {
		t.Fatalf("Start rejected run: %#v", result.RejectReason)
	}
	if executor.last.SessionID != "" || result.ResumeFrom != "" {
		t.Fatalf("stale session was resumed: result=%#v submit=%#v", result, executor.last)
	}
	if _, ok := sessions.GetRaw("scope-1"); ok {
		t.Fatalf("stale session was not cleared")
	}

	sessionID := "session-1"
	cwd := result.CWDRealpath
	if err := RecordSessionEvent(RecordSessionEventInput{
		ScopeID:    "scope-1",
		Sessions:   sessions,
		Capability: cap,
		Policy:     result.Policy,
		Event:      agentport.AgentEvent{Type: agentport.EventSystem, SessionID: &sessionID, CWD: &cwd},
	}); err != nil {
		t.Fatalf("RecordSessionEvent returned error: %v", err)
	}
	second, err := Start(context.Background(), StartInput{
		ScopeID:       "scope-1",
		Scope:         testScope(),
		Prompt:        "hello again",
		Access:        access.Decision{OK: true, Reason: access.ReasonAllowedUser},
		Capability:    cap,
		ProfileConfig: cfg,
		Sessions:      sessions,
		Executor:      executor,
		Now:           time.UnixMilli(2000),
	})
	if err != nil {
		t.Fatalf("second Start returned error: %v", err)
	}
	if !second.OK || second.ResumeFrom != "session-1" || executor.last.SessionID != "session-1" {
		t.Fatalf("second result = %#v, submit = %#v; want claude session resume", second, executor.last)
	}
}

func TestStartMapsRejectionsToUserVisibleReasons(t *testing.T) {
	cfg := profile.DefaultConfig(profile.AgentClaude)
	cfg.Workspaces.Default = t.TempDir()
	executor := &fakeExecutor{}

	result, err := Start(context.Background(), StartInput{
		ScopeID:       "scope-1",
		Scope:         testScope(),
		Prompt:        "hello",
		Access:        access.Decision{OK: false, Reason: access.ReasonDeniedUser},
		Capability:    capability.Claude(cfg.Permissions.MaxAccess, ""),
		ProfileConfig: cfg,
		Executor:      executor,
	})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if result.OK || result.RejectReason.Code != "access-denied" {
		t.Fatalf("result = %#v, want access-denied", result)
	}

	executor.err = &runexecutor.RunRejected{Code: runexecutor.RunRejectedAlreadyActive, Message: "active"}
	result, err = Start(context.Background(), StartInput{
		ScopeID:       "scope-1",
		Scope:         testScope(),
		Prompt:        "hello",
		Access:        access.Decision{OK: true, Reason: access.ReasonAllowedUser},
		Capability:    capability.Claude(cfg.Permissions.MaxAccess, ""),
		ProfileConfig: cfg,
		Executor:      executor,
	})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if result.OK || result.RejectReason.Code != string(runexecutor.RunRejectedAlreadyActive) || result.RejectReason.UserVisible == "" {
		t.Fatalf("result = %#v, want run-already-active user rejection", result)
	}
}

type fakeExecutor struct {
	last runexecutor.SubmitRunInput
	err  error
}

func (f *fakeExecutor) Submit(_ context.Context, input runexecutor.SubmitRunInput) (*runexecutor.RunExecution, error) {
	f.last = input
	return nil, f.err
}

func testScope() runpolicy.ScopeContext {
	return runpolicy.ScopeContext{
		Source:  runpolicy.SourceIM,
		ChatID:  "oc_chat",
		ActorID: "ou_user",
	}
}
