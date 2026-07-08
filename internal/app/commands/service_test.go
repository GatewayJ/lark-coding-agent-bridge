package commands

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/configstore"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/larkcli"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/runexecutor"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/secretstore"
	appsession "github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/session"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/workspace"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/access"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/capability"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/permissions"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/profile"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/runpolicy"
)

func TestResumeKeepsHistoryDetailsOutOfGroupChats(t *testing.T) {
	cfg, cap, cwd := commandTestConfig(t, profile.AgentCodex, permissions.AccessFull)
	executor := &fakeCommandExecutor{}
	service := New(Options{
		ProfileName:     "codex-dev",
		ProfileConfig:   cfg,
		Capability:      cap,
		RuntimeControls: ownerControls(),
		Sessions:        appsession.NewStore(""),
		SessionCatalog:  appsession.NewCatalog(""),
		Executor:        executor,
		CodexHistory: CodexHistoryProviderFunc(func(context.Context, CodexHistoryQuery) ([]CodexThreadHistoryEntry, error) {
			return []CodexThreadHistoryEntry{{ThreadID: "thread-secret", Preview: "secret prompt", UpdatedAtMs: 1000, Source: "exec"}}, nil
		}),
	})

	resp, err := service.Handle(context.Background(), commandRequest("/resume", "scope-1", cwd, ChatModeGroup))
	if err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}
	if !strings.Contains(resp.Markdown, "私聊") || strings.Contains(resp.Markdown, "secret prompt") || strings.Contains(resp.Markdown, "thread-secret") {
		t.Fatalf("group resume response leaked history detail: %#v", resp)
	}
}

func TestResumeNonceRequiresScopeCWDAndPolicyFingerprint(t *testing.T) {
	store := NewResumeStore(func() time.Time { return time.UnixMilli(1000) })
	cfg, cap, cwd := commandTestConfig(t, profile.AgentCodex, permissions.AccessFull)
	catalog := appsession.NewCatalog("")
	base := func(cfg profile.Config, cap capability.Capability) *Service {
		return New(Options{
			ProfileName:     "codex-dev",
			ProfileConfig:   cfg,
			Capability:      cap,
			RuntimeControls: ownerControls(),
			Sessions:        appsession.NewStore(""),
			SessionCatalog:  catalog,
			Executor:        &fakeCommandExecutor{},
			ResumeStore:     store,
			CodexHistory: CodexHistoryProviderFunc(func(context.Context, CodexHistoryQuery) ([]CodexThreadHistoryEntry, error) {
				return []CodexThreadHistoryEntry{{ThreadID: "thread-target", Preview: "target", UpdatedAtMs: 1000, Source: "exec"}}, nil
			}),
		})
	}

	token := issueFirstToken(t, base(cfg, cap), cwd, "scope-1")
	resp, err := base(cfg, cap).Handle(context.Background(), commandRequest("/resume use "+token, "scope-2", cwd, ChatModeP2P))
	if err != nil {
		t.Fatalf("scope mismatch apply returned error: %v", err)
	}
	if !strings.Contains(resp.Markdown, "不可恢复") {
		t.Fatalf("scope mismatch response = %#v, want not recoverable", resp)
	}

	otherCWD := t.TempDir()
	token = issueFirstToken(t, base(cfg, cap), cwd, "scope-1")
	resp, err = base(cfg, cap).Handle(context.Background(), commandRequest("/resume use "+token, "scope-1", otherCWD, ChatModeP2P))
	if err != nil {
		t.Fatalf("cwd mismatch apply returned error: %v", err)
	}
	if !strings.Contains(resp.Markdown, "不可恢复") {
		t.Fatalf("cwd mismatch response = %#v, want not recoverable", resp)
	}

	readOnlyCfg := cfg
	readOnlyCfg.Permissions = permissions.PermissionConfig{
		DefaultAccess: permissions.AccessReadOnly,
		MaxAccess:     permissions.AccessReadOnly,
	}
	readOnlyCap := capability.Codex(readOnlyCfg.Permissions.MaxAccess, "")
	token = issueFirstToken(t, base(cfg, cap), cwd, "scope-1")
	resp, err = base(readOnlyCfg, readOnlyCap).Handle(context.Background(), commandRequest("/resume use "+token, "scope-1", cwd, ChatModeP2P))
	if err != nil {
		t.Fatalf("policy mismatch apply returned error: %v", err)
	}
	if !strings.Contains(resp.Markdown, "不可恢复") {
		t.Fatalf("policy mismatch response = %#v, want not recoverable", resp)
	}

	token = issueFirstToken(t, base(cfg, cap), cwd, "scope-1")
	resp, err = base(cfg, cap).Handle(context.Background(), commandRequest("/resume use "+token, "scope-1", cwd, ChatModeP2P))
	if err != nil {
		t.Fatalf("matching apply returned error: %v", err)
	}
	if !resp.Resume.Applied {
		t.Fatalf("matching apply = %#v, want applied", resp)
	}
	identity := identityForTest(t, cfg, cap, "scope-1", cwd)
	entry, ok := catalog.ActiveFor(identity)
	if !ok || entry.ThreadID != "thread-target" {
		t.Fatalf("catalog active = %#v, %v; want thread-target", entry, ok)
	}
}

func TestResumeFallsBackToCurrentCodexThreadWhenHistoryFails(t *testing.T) {
	cfg, cap, cwd := commandTestConfig(t, profile.AgentCodex, permissions.AccessFull)
	catalog := appsession.NewCatalog("")
	identity := identityForTest(t, cfg, cap, "scope-1", cwd)
	if _, err := catalog.UpsertActive(appsession.UpsertCatalogInput{
		CatalogIdentity: identity,
		ThreadID:        "thread-current",
	}); err != nil {
		t.Fatalf("UpsertActive returned error: %v", err)
	}
	service := New(Options{
		ProfileName:     "codex-dev",
		ProfileConfig:   cfg,
		Capability:      cap,
		RuntimeControls: ownerControls(),
		Sessions:        appsession.NewStore(""),
		SessionCatalog:  catalog,
		Executor:        &fakeCommandExecutor{},
		CodexHistory: CodexHistoryProviderFunc(func(context.Context, CodexHistoryQuery) ([]CodexThreadHistoryEntry, error) {
			return nil, os.ErrPermission
		}),
	})

	resp, err := service.Handle(context.Background(), commandRequest("/resume", "scope-1", cwd, ChatModeP2P))
	if err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}
	if resp.Resume == nil || len(resp.Resume.Entries) != 1 || !resp.Resume.Entries[0].Current || !strings.Contains(resp.Markdown, "当前 Codex thread") {
		t.Fatalf("resume fallback response = %#v", resp)
	}
}

func TestStatusIncludesProfileCWDSessionActiveScopesAndLarkCLI(t *testing.T) {
	cfg, cap, cwd := commandTestConfig(t, profile.AgentCodex, permissions.AccessFull)
	catalog := appsession.NewCatalog("")
	identity := identityForTest(t, cfg, cap, "scope-1", cwd)
	if _, err := catalog.UpsertActive(appsession.UpsertCatalogInput{
		CatalogIdentity: identity,
		ThreadID:        "thread-current",
	}); err != nil {
		t.Fatalf("UpsertActive returned error: %v", err)
	}
	executor := &fakeCommandExecutor{
		scopes: []string{"scope-1", "comment:abc"},
		pool:   runexecutor.ProcessPoolSnapshot{Active: 1, Waiting: 0, Cap: 2},
	}
	service := New(Options{
		ProfileName:     "codex-dev",
		ProfileConfig:   cfg,
		Capability:      cap,
		RuntimeControls: ownerControls(),
		Sessions:        appsession.NewStore(""),
		SessionCatalog:  catalog,
		Executor:        executor,
		LarkCLIStatus:   LarkCLIStatusUserReady,
		AgentName:       "Codex",
	})

	resp, err := service.Handle(context.Background(), commandRequest("/status", "scope-1", cwd, ChatModeP2P))
	if err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}
	status := resp.Status
	if status == nil || status.ProfileName != "codex-dev" || status.CWD != cwd || status.SessionID != "thread-current" || status.LarkCLIStatus != LarkCLIStatusUserReady {
		t.Fatalf("status = %#v", status)
	}
	if !status.ActiveRun || len(status.ActiveScopes) != 1 || status.ActiveScopes[0] != "scope-1" || len(status.ActiveCommentScopes) != 1 || status.ActiveCommentScopes[0] != "comment:abc" {
		t.Fatalf("active scopes not surfaced: %#v", status)
	}
	if !strings.Contains(resp.Markdown, "profile") || !strings.Contains(resp.Markdown, "lark-cli") {
		t.Fatalf("status markdown missing expected labels: %q", resp.Markdown)
	}
}

func TestStopTimeoutAndPSBasicBehavior(t *testing.T) {
	cfg, cap, cwd := commandTestConfig(t, profile.AgentClaude, permissions.AccessWorkspace)
	sessions := appsession.NewStore("")
	executor := &fakeCommandExecutor{scopes: []string{"scope-1", "comment:abc"}}
	now := time.Unix(1000, 0)
	service := New(Options{
		ProfileName:       "claude",
		ProfileConfig:     cfg,
		Capability:        cap,
		RuntimeControls:   ownerControls(),
		Sessions:          sessions,
		Executor:          executor,
		GlobalIdleTimeout: 5 * time.Minute,
		ProcessID:         "proc-1",
		Processes: ProcessListerFunc(func() []ProcessEntry {
			return []ProcessEntry{{ID: "proc-1", AppID: "app-1", BotName: "Bridge", StartedAt: now.Add(-time.Hour)}}
		}),
		Now: func() time.Time { return now },
	})

	stopResp, err := service.Handle(context.Background(), commandRequest("/stop", "scope-1", cwd, ChatModeP2P))
	if err != nil {
		t.Fatalf("/stop returned error: %v", err)
	}
	if !stopResp.NoReply || !executor.interrupted["scope-1"] {
		t.Fatalf("/stop response=%#v interrupted=%#v", stopResp, executor.interrupted)
	}

	targetStop, err := service.Handle(context.Background(), commandRequest("/stop comment:abc", "scope-1", cwd, ChatModeP2P))
	if err != nil {
		t.Fatalf("target /stop returned error: %v", err)
	}
	if !targetStop.Stop.Targeted || !executor.interrupted["comment:abc"] || !strings.Contains(targetStop.Markdown, "已请求停止") {
		t.Fatalf("target /stop response=%#v interrupted=%#v", targetStop, executor.interrupted)
	}

	timeoutResp, err := service.Handle(context.Background(), commandRequest("/timeout 15", "scope-1", cwd, ChatModeP2P))
	if err != nil {
		t.Fatalf("/timeout set returned error: %v", err)
	}
	if got, ok := sessions.GetIdleTimeoutMinutes("scope-1"); !ok || got != 15 || !strings.Contains(timeoutResp.Markdown, "15 分钟") {
		t.Fatalf("timeout set got=%d ok=%v response=%#v", got, ok, timeoutResp)
	}
	offResp, _ := service.Handle(context.Background(), commandRequest("/timeout off", "scope-1", cwd, ChatModeP2P))
	if got, ok := sessions.GetIdleTimeoutMinutes("scope-1"); !ok || got != 0 || !offResp.Timeout.DisabledForSession {
		t.Fatalf("timeout off got=%d ok=%v response=%#v", got, ok, offResp)
	}
	defaultResp, _ := service.Handle(context.Background(), commandRequest("/timeout default", "scope-1", cwd, ChatModeP2P))
	if _, ok := sessions.GetIdleTimeoutMinutes("scope-1"); ok || !defaultResp.Timeout.Cleared {
		t.Fatalf("timeout default response=%#v", defaultResp)
	}
	invalidResp, _ := service.Handle(context.Background(), commandRequest("/timeout 999", "scope-1", cwd, ChatModeP2P))
	if !invalidResp.Timeout.Invalid || !strings.Contains(invalidResp.Markdown, "/timeout <1-120>") {
		t.Fatalf("timeout invalid response=%#v", invalidResp)
	}

	psResp, err := service.Handle(context.Background(), commandRequest("/ps", "scope-1", cwd, ChatModeP2P))
	if err != nil {
		t.Fatalf("/ps returned error: %v", err)
	}
	if psResp.PS == nil || len(psResp.PS.Processes) != 1 || !psResp.PS.Processes[0].Current || !strings.Contains(psResp.Markdown, "当前有 1 个 bot") {
		t.Fatalf("/ps response=%#v", psResp)
	}
}

func TestNewAndResetClearSessionCatalogTimeoutAndInterrupt(t *testing.T) {
	cfg, cap, cwd := commandTestConfig(t, profile.AgentCodex, permissions.AccessFull)
	sessions := appsession.NewStore("")
	sessions.Set("scope-1", "thread-old", cwd)
	sessions.SetIdleTimeoutMinutes("scope-1", 15)
	catalog := appsession.NewCatalog("")
	identity := identityForTest(t, cfg, cap, "scope-1", cwd)
	if _, err := catalog.UpsertActive(appsession.UpsertCatalogInput{
		CatalogIdentity: identity,
		ThreadID:        "thread-old",
	}); err != nil {
		t.Fatalf("UpsertActive returned error: %v", err)
	}
	executor := &fakeCommandExecutor{scopes: []string{"scope-1"}}
	service := New(Options{
		ProfileName:     "codex-dev",
		ProfileConfig:   cfg,
		Capability:      cap,
		RuntimeControls: ownerControls(),
		Sessions:        sessions,
		SessionCatalog:  catalog,
		Executor:        executor,
	})

	resp, err := service.Handle(context.Background(), commandRequest("/new", "scope-1", cwd, ChatModeP2P))
	if err != nil {
		t.Fatalf("/new returned error: %v", err)
	}
	if resp.Session == nil || !resp.Session.Interrupted || !resp.Session.ArchivedCurrent || !resp.Session.SessionCleared {
		t.Fatalf("/new response = %#v", resp)
	}
	if !executor.interrupted["scope-1"] {
		t.Fatalf("/new did not interrupt scope")
	}
	if session, ok := sessions.GetRaw("scope-1"); ok && session.SessionID != "" {
		t.Fatalf("session still active after /new: %#v", session)
	}
	if _, ok := sessions.GetIdleTimeoutMinutes("scope-1"); ok {
		t.Fatalf("idle timeout override still present after /new")
	}
	if _, ok := catalog.ActiveFor(identity); ok {
		t.Fatalf("catalog entry still active after /new")
	}

	sessions.Set("scope-1", "thread-next", cwd)
	reset, err := service.Handle(context.Background(), commandRequest("/reset", "scope-1", cwd, ChatModeP2P))
	if err != nil {
		t.Fatalf("/reset returned error: %v", err)
	}
	if reset.Session == nil || reset.Session.Action != "reset" || !strings.Contains(reset.Markdown, "新会话") {
		t.Fatalf("/reset response = %#v", reset)
	}
}

func TestCDValidatesCWDAndClearsStaleSession(t *testing.T) {
	cfg, cap, cwd := commandTestConfig(t, profile.AgentClaude, permissions.AccessFull)
	target := t.TempDir()
	file := filepath.Join(target, "file.txt")
	if err := os.WriteFile(file, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	sessions := appsession.NewStore("")
	sessions.Set("scope-1", "session-old", cwd)
	workspaces := newFakeWorkspaceStore()
	executor := &fakeCommandExecutor{scopes: []string{"scope-1"}}
	service := New(Options{
		ProfileName:     "claude-dev",
		ProfileConfig:   cfg,
		Capability:      cap,
		RuntimeControls: ownerControls(),
		Sessions:        sessions,
		Workspaces:      workspaces,
		Executor:        executor,
	})

	relative, err := service.Handle(context.Background(), commandRequest("/cd relative", "scope-1", cwd, ChatModeP2P))
	if err != nil {
		t.Fatalf("relative /cd returned error: %v", err)
	}
	if !strings.Contains(relative.Markdown, "绝对路径") {
		t.Fatalf("relative /cd response = %#v", relative)
	}
	notDir, err := service.Handle(context.Background(), commandRequest("/cd "+file, "scope-1", cwd, ChatModeP2P))
	if err != nil {
		t.Fatalf("file /cd returned error: %v", err)
	}
	if !strings.Contains(notDir.Markdown, "路径不是目录") {
		t.Fatalf("file /cd response = %#v", notDir)
	}

	resp, err := service.Handle(context.Background(), commandRequest("/cd "+target, "scope-1", cwd, ChatModeP2P))
	if err != nil {
		t.Fatalf("/cd returned error: %v", err)
	}
	real := workspace.ResolveWorkingDirectory(target).CWDRealpath
	if resp.Workspace == nil || resp.Workspace.CWD != real || !resp.Workspace.Interrupted || !resp.Workspace.SessionCleared {
		t.Fatalf("/cd response = %#v", resp)
	}
	if got := workspaces.CWDFor("scope-1"); got != real {
		t.Fatalf("workspace cwd = %q, want %q", got, real)
	}
	if session, ok := sessions.GetRaw("scope-1"); ok && session.SessionID != "" {
		t.Fatalf("stale session still active after /cd: %#v", session)
	}
}

func TestWorkspaceSaveUseRemoveAndList(t *testing.T) {
	cfg, cap, _ := commandTestConfig(t, profile.AgentClaude, permissions.AccessFull)
	main := t.TempDir()
	other := t.TempDir()
	workspaces := newFakeWorkspaceStore()
	workspaces.SetCWD("scope-1", main)
	sessions := appsession.NewStore("")
	sessions.Set("scope-1", "session-old", other)
	service := New(Options{
		ProfileName:     "claude-dev",
		ProfileConfig:   cfg,
		Capability:      cap,
		RuntimeControls: ownerControls(),
		Sessions:        sessions,
		Workspaces:      workspaces,
		Executor:        &fakeCommandExecutor{scopes: []string{"scope-1"}},
	})

	save, err := service.Handle(context.Background(), Request{
		CommandText: "/ws save main",
		ScopeID:     "scope-1",
		ChatID:      "chat-1",
		ActorID:     "ou-user",
		SenderID:    "ou-user",
		ChatMode:    ChatModeP2P,
	})
	if err != nil {
		t.Fatalf("/ws save returned error: %v", err)
	}
	if save.Workspace == nil || save.Workspace.Name != "main" || !strings.Contains(save.Markdown, "工作目录别名已保存") {
		t.Fatalf("/ws save response = %#v", save)
	}

	list, err := service.Handle(context.Background(), Request{
		CommandText: "/ws list",
		ScopeID:     "scope-1",
		ChatID:      "chat-1",
		ActorID:     "ou-user",
		SenderID:    "ou-user",
		ChatMode:    ChatModeP2P,
	})
	if err != nil {
		t.Fatalf("/ws list returned error: %v", err)
	}
	if list.Workspace == nil || list.Workspace.Entries["main"] == "" || !strings.Contains(list.Markdown, "main") {
		t.Fatalf("/ws list response = %#v", list)
	}

	workspaces.SetCWD("scope-1", other)
	use, err := service.Handle(context.Background(), Request{
		CommandText: "/ws use main",
		ScopeID:     "scope-1",
		ChatID:      "chat-1",
		ActorID:     "ou-user",
		SenderID:    "ou-user",
		ChatMode:    ChatModeP2P,
	})
	if err != nil {
		t.Fatalf("/ws use returned error: %v", err)
	}
	if use.Workspace == nil || use.Workspace.CWD == "" || !use.Workspace.SessionCleared || workspaces.CWDFor("scope-1") != use.Workspace.CWD {
		t.Fatalf("/ws use response = %#v cwd=%q", use, workspaces.CWDFor("scope-1"))
	}
	if session, ok := sessions.GetRaw("scope-1"); ok && session.SessionID != "" {
		t.Fatalf("session still active after /ws use: %#v", session)
	}

	remove, err := service.Handle(context.Background(), Request{
		CommandText: "/ws remove main",
		ScopeID:     "scope-1",
		ChatID:      "chat-1",
		ActorID:     "ou-user",
		SenderID:    "ou-user",
		ChatMode:    ChatModeP2P,
	})
	if err != nil {
		t.Fatalf("/ws remove returned error: %v", err)
	}
	if remove.Workspace == nil || !remove.Workspace.Removed || !strings.Contains(remove.Markdown, "已删除") {
		t.Fatalf("/ws remove response = %#v", remove)
	}
	missing, err := service.Handle(context.Background(), Request{
		CommandText: "/ws use main",
		ScopeID:     "scope-1",
		ChatID:      "chat-1",
		ActorID:     "ou-user",
		SenderID:    "ou-user",
		ChatMode:    ChatModeP2P,
	})
	if err != nil {
		t.Fatalf("missing /ws use returned error: %v", err)
	}
	if !strings.Contains(missing.Markdown, "未找到") {
		t.Fatalf("missing /ws use response = %#v", missing)
	}
}

func TestWorkspacePersistFailuresReturnFailureMessages(t *testing.T) {
	cfg, cap, cwd := commandTestConfig(t, profile.AgentClaude, permissions.AccessFull)
	target := t.TempDir()
	persistErr := errors.New("persist failed")
	workspaces := newFakeWorkspaceStore()
	sessions := appsession.NewStore("")
	sessions.Set("scope-1", "session-old", cwd)
	executor := &fakeCommandExecutor{scopes: []string{"scope-1"}}
	service := New(Options{
		ProfileName:     "claude-dev",
		ProfileConfig:   cfg,
		Capability:      cap,
		RuntimeControls: ownerControls(),
		Sessions:        sessions,
		Workspaces:      workspaces,
		Executor:        executor,
	})

	workspaces.setErr = persistErr
	cd, err := service.Handle(context.Background(), commandRequest("/cd "+target, "scope-1", cwd, ChatModeP2P))
	if err != nil {
		t.Fatalf("/cd returned error: %v", err)
	}
	if cd.Workspace == nil || cd.Workspace.Failure != persistErr.Error() || !strings.Contains(cd.Markdown, "保存工作目录失败") {
		t.Fatalf("failed /cd response = %#v", cd)
	}
	if got := workspaces.CWDFor("scope-1"); got != "" {
		t.Fatalf("workspace cwd after failed /cd = %q, want empty", got)
	}
	if session, ok := sessions.GetRaw("scope-1"); !ok || session.SessionID != "session-old" {
		t.Fatalf("session after failed /cd = %#v, ok=%v", session, ok)
	}
	if executor.interrupted["scope-1"] {
		t.Fatalf("executor interrupted on failed /cd")
	}

	workspaces.setErr = nil
	workspaces.saveErr = persistErr
	save, err := service.Handle(context.Background(), commandRequest("/ws save main", "scope-1", cwd, ChatModeP2P))
	if err != nil {
		t.Fatalf("/ws save returned error: %v", err)
	}
	if save.Workspace == nil || save.Workspace.Failure != persistErr.Error() || !strings.Contains(save.Markdown, "保存工作目录失败") {
		t.Fatalf("failed /ws save response = %#v", save)
	}
	if got := workspaces.GetNamed(service.scopedWorkspaceName(commandRequest("", "scope-1", cwd, ChatModeP2P), "main")); got != "" {
		t.Fatalf("saved alias after failed /ws save = %q, want empty", got)
	}

	req := commandRequest("", "scope-1", cwd, ChatModeP2P)
	scopedName := service.scopedWorkspaceName(req, "main")
	workspaces.saveErr = nil
	workspaces.named[scopedName] = target
	workspaces.cwds["scope-1"] = cwd
	workspaces.setErr = persistErr
	sessions.Set("scope-1", "session-old", cwd)
	executor.interrupted = nil
	use, err := service.Handle(context.Background(), commandRequest("/ws use main", "scope-1", cwd, ChatModeP2P))
	if err != nil {
		t.Fatalf("/ws use returned error: %v", err)
	}
	if use.Workspace == nil || use.Workspace.Failure != persistErr.Error() || !strings.Contains(use.Markdown, "保存工作目录失败") {
		t.Fatalf("failed /ws use response = %#v", use)
	}
	if got := workspaces.CWDFor("scope-1"); got != cwd {
		t.Fatalf("workspace cwd after failed /ws use = %q, want %q", got, cwd)
	}
	if session, ok := sessions.GetRaw("scope-1"); !ok || session.SessionID != "session-old" {
		t.Fatalf("session after failed /ws use = %#v, ok=%v", session, ok)
	}
	if executor.interrupted["scope-1"] {
		t.Fatalf("executor interrupted on failed /ws use")
	}

	workspaces.setErr = nil
	workspaces.removeErr = persistErr
	remove, err := service.Handle(context.Background(), commandRequest("/ws remove main", "scope-1", cwd, ChatModeP2P))
	if err != nil {
		t.Fatalf("/ws remove returned error: %v", err)
	}
	if remove.Workspace == nil || remove.Workspace.Failure != persistErr.Error() || remove.Workspace.Removed || !strings.Contains(remove.Markdown, "保存工作目录失败") {
		t.Fatalf("failed /ws remove response = %#v", remove)
	}
	if got := workspaces.GetNamed(scopedName); got != target {
		t.Fatalf("alias after failed /ws remove = %q, want %q", got, target)
	}
}

func TestHelpOnlyMarksSupportedCommandsAvailable(t *testing.T) {
	cfg, cap, cwd := commandTestConfig(t, profile.AgentCodex, permissions.AccessFull)
	service := New(Options{
		ProfileName:     "codex-dev",
		ProfileConfig:   cfg,
		Capability:      cap,
		RuntimeControls: ownerControls(),
		Sessions:        appsession.NewStore(""),
	})

	resp, err := service.Handle(context.Background(), commandRequest("/help", "scope-1", cwd, ChatModeP2P))
	if err != nil {
		t.Fatalf("/help returned error: %v", err)
	}
	if resp.Help == nil || !strings.Contains(resp.Markdown, "/new") || !strings.Contains(resp.Markdown, "/ws list") || !strings.Contains(resp.Markdown, "/help") {
		t.Fatalf("/help response = %#v", resp)
	}
	for _, supported := range []string{"/account", "/config", "/invite", "/remove", "/exit", "/reconnect", "/doctor", "/doc"} {
		if !strings.Contains(resp.Markdown, supported) {
			t.Fatalf("/help markdown missing supported command %s: %q", supported, resp.Markdown)
		}
	}
	for _, command := range resp.Help.Commands {
		if !command.Supported {
			t.Fatalf("help contains unsupported command without explicit filtering: %#v", command)
		}
	}
}

func TestNewChatCreatesBoundChatAndInheritsWorkspace(t *testing.T) {
	cfg, cap, cwd := commandTestConfig(t, profile.AgentCodex, permissions.AccessFull)
	workspaces := newFakeWorkspaceStore()
	workspaces.cwds["scope-1"] = cwd
	creator := &fakeBoundChatCreator{chatID: "oc_created", name: "Pairing Room"}
	service := New(Options{
		ProfileName:     "codex-dev",
		ProfileConfig:   cfg,
		Capability:      cap,
		RuntimeControls: ownerControls(),
		Sessions:        appsession.NewStore(""),
		Workspaces:      workspaces,
		ChatCreator:     creator,
	})
	req := commandRequest("/new chat Pairing Room", "scope-1", "", ChatModeP2P)
	req.SenderID = "ou-sender"

	resp, err := service.Handle(context.Background(), req)
	if err != nil {
		t.Fatalf("/new chat returned error: %v", err)
	}
	if resp.Session == nil || resp.Session.CreatedChatID != "oc_created" || resp.Session.CreatedChatName != "Pairing Room" || !resp.Session.WelcomeSent {
		t.Fatalf("/new chat response = %#v", resp)
	}
	if len(creator.created) != 1 || creator.created[0].Name != "Pairing Room" || creator.created[0].InviteOpenID != "ou-sender" {
		t.Fatalf("created inputs = %#v", creator.created)
	}
	if got := workspaces.CWDFor("oc_created"); got != cwd {
		t.Fatalf("new chat cwd = %q, want %q", got, cwd)
	}
	if len(creator.sent) != 1 || creator.sent[0].chatID != "oc_created" || !strings.Contains(creator.sent[0].markdown, cwd) {
		t.Fatalf("welcome messages = %#v", creator.sent)
	}
}

func TestNewChatReportsWorkspaceInheritanceFailure(t *testing.T) {
	cfg, cap, cwd := commandTestConfig(t, profile.AgentCodex, permissions.AccessFull)
	persistErr := errors.New("disk full")
	workspaces := newFakeWorkspaceStore()
	workspaces.cwds["scope-1"] = cwd
	workspaces.setErr = persistErr
	creator := &fakeBoundChatCreator{chatID: "oc_created", name: "Pairing Room"}
	service := New(Options{
		ProfileName:     "codex-dev",
		ProfileConfig:   cfg,
		Capability:      cap,
		RuntimeControls: ownerControls(),
		Sessions:        appsession.NewStore(""),
		Workspaces:      workspaces,
		ChatCreator:     creator,
	})
	req := commandRequest("/new chat Pairing Room", "scope-1", "", ChatModeP2P)
	req.SenderID = "ou-sender"

	resp, err := service.Handle(context.Background(), req)
	if err != nil {
		t.Fatalf("/new chat returned error: %v", err)
	}
	if resp.Session == nil || resp.Session.CreatedChatID != "oc_created" || resp.Session.Failure != persistErr.Error() || resp.Session.CWD != "" {
		t.Fatalf("/new chat response = %#v", resp)
	}
	if got := workspaces.CWDFor("oc_created"); got != "" {
		t.Fatalf("new chat cwd = %q, want empty after persistence failure", got)
	}
	if !strings.Contains(resp.Markdown, "保存 cwd 继承失败") || !strings.Contains(resp.Markdown, cwd) {
		t.Fatalf("failure markdown = %q", resp.Markdown)
	}
	if len(creator.sent) != 1 || strings.Contains(creator.sent[0].markdown, "cwd 继承自原群") {
		t.Fatalf("welcome messages = %#v", creator.sent)
	}
}

func TestNewChatReportsCreateFailure(t *testing.T) {
	cfg, cap, cwd := commandTestConfig(t, profile.AgentCodex, permissions.AccessFull)
	service := New(Options{
		ProfileName:     "codex-dev",
		ProfileConfig:   cfg,
		Capability:      cap,
		RuntimeControls: ownerControls(),
		Sessions:        appsession.NewStore(""),
		ChatCreator:     &fakeBoundChatCreator{err: errors.New("forbidden")},
	})

	resp, err := service.Handle(context.Background(), commandRequest("/new chat", "scope-1", cwd, ChatModeP2P))
	if err != nil {
		t.Fatalf("/new chat returned error: %v", err)
	}
	if resp.Session == nil || resp.Session.Failure != "forbidden" || !strings.Contains(resp.Markdown, "im:chat") {
		t.Fatalf("/new chat failure response = %#v", resp)
	}
}

func TestDetectLarkCLIStatusFromTargetConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"apps":[{"appId":"app-1","brand":"feishu","defaultAs":"auto","strictMode":"off","users":[{"openId":"ou-user"}]}]}`), 0o600); err != nil {
		t.Fatalf("write lark-cli config: %v", err)
	}
	status, err := DetectLarkCLIStatus(LarkCLIConfig{
		TargetConfigFile: path,
		AppID:            "app-1",
		Tenant:           "feishu",
	}, false)
	if err != nil {
		t.Fatalf("DetectLarkCLIStatus returned error: %v", err)
	}
	if status != LarkCLIStatusUserReady {
		t.Fatalf("status = %q, want user-ready", status)
	}
}

func TestRemainingCommandsAdminGateDocDoctorReconnectAndExit(t *testing.T) {
	cfg, cap, cwd := commandTestConfig(t, profile.AgentClaude, permissions.AccessReadOnly)
	executor := &fakeCommandExecutor{scopes: []string{"scope-1"}}
	restarts := 0
	exited := false
	terminated := false
	service := New(Options{
		ProfileName:     "claude",
		ProfileConfig:   cfg,
		Capability:      cap,
		RuntimeControls: ownerControls(),
		Sessions:        appsession.NewStore(""),
		Executor:        executor,
		Processes: ProcessListerFunc(func() []ProcessEntry {
			return []ProcessEntry{
				{ID: "proc-1", PID: 101, AppID: "app-1", StartedAt: time.Unix(1, 0)},
				{ID: "proc-2", PID: 102, AppID: "app-1", StartedAt: time.Unix(2, 0)},
			}
		}),
		ProcessID: "proc-1",
		Reconnector: ReconnectorFunc(func(context.Context, bool) error {
			restarts++
			return nil
		}),
		ProcessController: ProcessControllerFunc{
			ExitSelfFunc: func(context.Context) error {
				exited = true
				return nil
			},
			TerminateFunc: func(_ context.Context, entry ProcessEntry) (bool, error) {
				terminated = entry.ID == "proc-2"
				return false, nil
			},
		},
	})

	denied := commandRequest("/doctor", "scope-1", cwd, ChatModeP2P)
	denied.SenderID = "ou-not-admin"
	denied.ActorID = "ou-not-admin"
	resp, err := service.Handle(context.Background(), denied)
	if err != nil {
		t.Fatalf("non-admin /doctor returned error: %v", err)
	}
	if !strings.Contains(resp.Markdown, "仅管理员可用") {
		t.Fatalf("non-admin response = %#v", resp)
	}

	doc, err := service.Handle(context.Background(), commandRequest("/doc ws bind doc-token /secret", "scope-1", cwd, ChatModeP2P))
	if err != nil {
		t.Fatalf("/doc returned error: %v", err)
	}
	if doc.Doc == nil || !doc.Doc.NoOp || strings.Contains(doc.Markdown, "/secret") {
		t.Fatalf("/doc response = %#v", doc)
	}

	doctor, err := service.Handle(context.Background(), commandRequest("/doctor", "scope-1", cwd, ChatModeP2P))
	if err != nil {
		t.Fatalf("/doctor returned error: %v", err)
	}
	if doctor.Doctor == nil || !strings.Contains(doctor.Doctor.Report, "self-check: ok") || !strings.Contains(doctor.Doctor.Report, "workspace check: ok") || strings.Contains(doctor.Doctor.Report, "secret") {
		t.Fatalf("/doctor response = %#v", doctor)
	}

	reconnect, err := service.Handle(context.Background(), commandRequest("/reconnect", "scope-1", cwd, ChatModeP2P))
	if err != nil {
		t.Fatalf("/reconnect returned error: %v", err)
	}
	if reconnect.Reconnect == nil || !reconnect.Reconnect.Restarted || restarts != 1 || !strings.Contains(reconnect.Markdown, "正在停止") {
		t.Fatalf("/reconnect response=%#v restarts=%d", reconnect, restarts)
	}

	exitOther, err := service.Handle(context.Background(), commandRequest("/exit #2", "scope-1", cwd, ChatModeP2P))
	if err != nil {
		t.Fatalf("/exit #2 returned error: %v", err)
	}
	if exitOther.Exit == nil || !exitOther.Exit.Terminated || !terminated {
		t.Fatalf("/exit #2 response=%#v terminated=%v", exitOther, terminated)
	}

	exitSelf, err := service.Handle(context.Background(), commandRequest("/exit proc-1", "scope-1", cwd, ChatModeP2P))
	if err != nil {
		t.Fatalf("/exit self returned error: %v", err)
	}
	if exitSelf.Exit == nil || !exitSelf.Exit.Self || !exited {
		t.Fatalf("/exit self response=%#v exited=%v", exitSelf, exited)
	}
}

func TestConfigInviteRemoveAndAccountPersistProfileRoot(t *testing.T) {
	cfg, cap, cwd := commandTestConfig(t, profile.AgentClaude, permissions.AccessReadOnly)
	rootDir := t.TempDir()
	configPath := filepath.Join(rootDir, "config.json")
	writeCommandRoot(t, configPath, "claude", cfg, map[string]any{"messageReply": "text", "messageReplyMigrated": true})
	keystore, err := secretstore.NewKeystore(secretstore.KeystoreOptions{
		Paths: secretstore.KeystorePaths{
			SecretsFile:         filepath.Join(rootDir, "profiles", "claude", "secrets.enc"),
			KeystoreSaltFile:    filepath.Join(rootDir, "profiles", "claude", "secrets.salt"),
			SecretsGetterScript: filepath.Join(rootDir, "secrets-getter"),
		},
		Seed: "test-seed",
	})
	if err != nil {
		t.Fatalf("NewKeystore returned error: %v", err)
	}
	restartCalls := 0
	service := New(Options{
		ProfileName:     "claude",
		ProfileConfig:   cfg,
		Capability:      cap,
		RuntimeControls: ownerControls(),
		Sessions:        appsession.NewStore(""),
		ConfigPath:      configPath,
		Keystore:        keystore,
		AccountValidator: AccountValidatorFunc(func(_ context.Context, appID, appSecret, tenant string) (AccountValidationResult, error) {
			if appID != "cli_new" || appSecret != "new-secret" || tenant != "lark" {
				return AccountValidationResult{OK: false, Reason: "bad input"}, nil
			}
			return AccountValidationResult{OK: true, BotName: "Bridge Bot"}, nil
		}),
		Reconnector: ReconnectorFunc(func(_ context.Context, wait bool) error {
			t.Fatalf("account submit should not restart inside command service, wait=%v", wait)
			restartCalls++
			return nil
		}),
		KnownChats: []KnownChat{{ID: "oc-group-1"}, {ID: "oc-group-2"}},
	})

	inviteUser := commandRequest("/invite user @Alice", "scope-1", cwd, ChatModeP2P)
	inviteUser.Mentions = []Mention{{OpenID: "ou-alice", Name: "Alice"}}
	if _, err := service.Handle(context.Background(), inviteUser); err != nil {
		t.Fatalf("/invite user returned error: %v", err)
	}
	inviteAdmin := commandRequest("/invite admin @Bob", "scope-1", cwd, ChatModeP2P)
	inviteAdmin.Mentions = []Mention{{OpenID: "ou-bob", Name: "Bob"}}
	if _, err := service.Handle(context.Background(), inviteAdmin); err != nil {
		t.Fatalf("/invite admin returned error: %v", err)
	}
	if _, err := service.Handle(context.Background(), commandRequest("/invite all group", "scope-1", cwd, ChatModeP2P)); err != nil {
		t.Fatalf("/invite all group returned error: %v", err)
	}
	removeUser := commandRequest("/remove user @Alice", "scope-1", cwd, ChatModeP2P)
	removeUser.Mentions = []Mention{{OpenID: "ou-alice", Name: "Alice"}}
	if _, err := service.Handle(context.Background(), removeUser); err != nil {
		t.Fatalf("/remove user returned error: %v", err)
	}

	configReq := commandRequest("/config submit", "scope-1", cwd, ChatModeP2P)
	configReq.FormValue = map[string]any{
		"message_reply":            "text",
		"show_tool_calls":          "hide",
		"cot_messages":             "brief",
		"max_concurrent_runs":      "7",
		"run_idle_timeout_minutes": "15",
		"require_mention_in_group": "no",
		"lark_cli_identity":        "user-default",
	}
	configResp, err := service.Handle(context.Background(), configReq)
	if err != nil {
		t.Fatalf("/config submit returned error: %v", err)
	}
	if configResp.Config == nil || !configResp.Config.Saved || configResp.Config.Snapshot.AdminsCount != 2 || configResp.Config.Snapshot.RequireMentionInGroup {
		t.Fatalf("/config submit response = %#v", configResp)
	}
	if strings.Contains(configResp.Markdown, "allowed_users") || strings.Contains(configResp.Markdown, "allowed_chats") || strings.Contains(configResp.Markdown, "admins") {
		t.Fatalf("/config leaked access field names: %q", configResp.Markdown)
	}

	accountReq := commandRequest("/account submit", "scope-1", cwd, ChatModeP2P)
	accountReq.FormValue = map[string]any{"app_id": "cli_new", "app_secret": "new-secret", "tenant": "lark"}
	accountResp, err := service.Handle(context.Background(), accountReq)
	if err != nil {
		t.Fatalf("/account submit returned error: %v", err)
	}
	if accountResp.Account == nil || !accountResp.Account.Saved || !accountResp.Account.SecretRedacted || strings.Contains(accountResp.Markdown, "new-secret") {
		t.Fatalf("/account submit response = %#v", accountResp)
	}
	if restartCalls != 0 {
		t.Fatalf("restartCalls = %d, want 0", restartCalls)
	}

	snapshot, err := configstore.Load(configPath, configstore.LoadOptions{Profile: "claude"})
	if err != nil {
		t.Fatalf("Load config returned error: %v", err)
	}
	if snapshot.Root.SchemaVersion != 2 || snapshot.Root.Profiles["codex-dev"].Accounts.App.ID != "cli_codex" {
		t.Fatalf("root shape not preserved: %#v", snapshot.Root)
	}
	access := snapshot.Root.Profiles["claude"].Access
	if contains(access.AllowedUsers, "ou-alice") || !contains(access.Admins, "ou-bob") || len(access.AllowedChats) != 2 {
		t.Fatalf("access not persisted: %#v", access)
	}
	prefs := snapshot.Root.Profiles["claude"].Preferences
	if prefs["messageReply"] != "text" || prefs["showToolCalls"] != false || prefs["maxConcurrentRuns"] != float64(7) {
		t.Fatalf("preferences not persisted: %#v", prefs)
	}
	if snapshot.Root.Profiles["claude"].LarkCli.IdentityPreset != configstore.LarkCliIdentityUserDefault {
		t.Fatalf("lark cli identity = %#v", snapshot.Root.Profiles["claude"].LarkCli)
	}
	secretRef, ok := snapshot.Root.Profiles["claude"].Accounts.App.Secret.(map[string]any)
	if !ok || secretRef["source"] != "exec" || secretRef["provider"] != "bridge" || secretRef["id"] != secretstore.SecretKeyForApp("cli_new") {
		t.Fatalf("account secret ref = %#v", snapshot.Root.Profiles["claude"].Accounts.App.Secret)
	}
	secret, ok, err := keystore.GetSecret(secretstore.SecretKeyForApp("cli_new"))
	if err != nil || !ok || secret != "new-secret" {
		t.Fatalf("keystore secret=%q ok=%v err=%v", secret, ok, err)
	}
}

func TestConfigSubmitAppliesLarkCLIIdentityPolicyBeforeSaving(t *testing.T) {
	cfg, cap, cwd := commandTestConfig(t, profile.AgentClaude, permissions.AccessReadOnly)
	rootDir := t.TempDir()
	configPath := filepath.Join(rootDir, "config.json")
	writeCommandRoot(t, configPath, "claude", cfg, map[string]any{"messageReply": "text", "messageReplyMigrated": true})
	calls := []string{}
	service := New(Options{
		ProfileName:     "claude",
		ProfileConfig:   cfg,
		Capability:      cap,
		RuntimeControls: ownerControls(),
		Sessions:        appsession.NewStore(""),
		ConfigPath:      configPath,
		LarkCLIIdentity: LarkCLIIdentityPolicyApplierFunc(func(_ context.Context, identity string) bool {
			calls = append(calls, identity)
			return identity != "user-default"
		}),
	})

	req := commandRequest("/config submit", "scope-1", cwd, ChatModeP2P)
	req.FormValue = map[string]any{
		"message_reply":            "text",
		"show_tool_calls":          "show",
		"cot_messages":             "off",
		"max_concurrent_runs":      "7",
		"run_idle_timeout_minutes": "15",
		"require_mention_in_group": "yes",
		"lark_cli_identity":        "user-default",
	}
	resp, err := service.Handle(context.Background(), req)
	if err != nil {
		t.Fatalf("/config submit returned error: %v", err)
	}
	if resp.Config == nil || resp.Config.Saved || !strings.Contains(resp.Config.Failure, "lark-cli identity policy apply failed") {
		t.Fatalf("/config submit response = %#v", resp)
	}
	if len(calls) != 1 || calls[0] != "user-default" {
		t.Fatalf("policy calls = %#v", calls)
	}
	snapshot, err := configstore.Load(configPath, configstore.LoadOptions{Profile: "claude"})
	if err != nil {
		t.Fatalf("Load config returned error: %v", err)
	}
	if got := snapshot.Runtime.LarkCli.IdentityPreset; got != configstore.LarkCliIdentityBotOnly {
		t.Fatalf("identity = %q, want bot-only", got)
	}
}

func issueFirstToken(t *testing.T, service *Service, cwd string, scope string) string {
	t.Helper()
	resp, err := service.Handle(context.Background(), commandRequest("/resume", scope, cwd, ChatModeP2P))
	if err != nil {
		t.Fatalf("issue /resume returned error: %v", err)
	}
	if resp.Resume == nil || len(resp.Resume.Entries) == 0 || resp.Resume.Entries[0].Token == "" {
		t.Fatalf("resume entries = %#v", resp.Resume)
	}
	return resp.Resume.Entries[0].Token
}

func commandTestConfig(t *testing.T, kind profile.AgentKind, accessMode permissions.AccessMode) (profile.Config, capability.Capability, string) {
	t.Helper()
	cfg := profile.DefaultConfig(kind)
	cfg.Access.Admins = []string{"ou-user"}
	cfg.Permissions = permissions.PermissionConfig{DefaultAccess: accessMode, MaxAccess: accessMode}
	cwd := t.TempDir()
	cfg.Workspaces.Default = cwd
	if kind == profile.AgentCodex {
		cfg.Codex = &profile.CodexConfig{BinaryPath: "codex", InheritCodexHome: true}
		return cfg, capability.Codex(cfg.Permissions.MaxAccess, ""), cwd
	}
	return cfg, capability.Claude(cfg.Permissions.MaxAccess, ""), cwd
}

func commandRequest(text string, scope string, cwd string, mode ChatMode) Request {
	return Request{
		CommandText: text,
		ScopeID:     scope,
		ChatID:      "chat-1",
		ActorID:     "ou-user",
		SenderID:    "ou-user",
		ChatMode:    mode,
		WorkingDir:  cwd,
	}
}

func ownerControls() access.RuntimeControls {
	return access.RuntimeControls{
		BotOwnerID:        "ou-user",
		OwnerRefreshState: access.OwnerRefreshOK,
	}
}

func identityForTest(t *testing.T, cfg profile.Config, cap capability.Capability, scope string, cwd string) appsession.CatalogIdentity {
	t.Helper()
	resolved := workspace.ResolveWorkingDirectory(cwd)
	if !resolved.OK {
		t.Fatalf("ResolveWorkingDirectory rejected %q: %s", cwd, resolved.UserVisible)
	}
	result, err := runpolicy.Evaluate(runpolicy.Input{
		Scope: runpolicy.ScopeContext{
			Source:  runpolicy.SourceIM,
			ChatID:  "chat-1",
			ActorID: "ou-user",
		},
		Prompt:        "",
		RequestedCWD:  cwd,
		CWDRealpath:   resolved.CWDRealpath,
		Access:        access.Decision{OK: true, Reason: access.ReasonOwner},
		Capability:    cap,
		ProfileConfig: cfg,
		Now:           time.Now(),
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if !result.OK {
		t.Fatalf("Evaluate rejected: %#v", result.RejectReason)
	}
	return appsession.CatalogIdentity{
		ScopeID:           scope,
		AgentID:           cap.AgentID,
		CWDRealpath:       resolved.CWDRealpath,
		PolicyFingerprint: result.Allow.PolicyFingerprint,
	}
}

func writeCommandRoot(t *testing.T, path string, active string, cfg profile.Config, preferences map[string]any) {
	t.Helper()
	root := configstore.RootConfig{
		SchemaVersion: 2,
		ActiveProfile: active,
		Preferences:   map[string]any{},
		Profiles: map[string]configstore.ProfileConfig{
			"claude":    storeProfileFromDomain(cfg, configstore.AgentClaude, "cli_old", "feishu", preferences),
			"codex-dev": storeProfileFromDomain(commandCodexConfig(t), configstore.AgentCodex, "cli_codex", "feishu", nil),
		},
	}
	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		t.Fatalf("marshal root: %v", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func commandCodexConfig(t *testing.T) profile.Config {
	t.Helper()
	cfg := profile.DefaultConfig(profile.AgentCodex)
	cfg.Permissions = permissions.PermissionConfig{DefaultAccess: permissions.AccessReadOnly, MaxAccess: permissions.AccessReadOnly}
	cfg.Codex = &profile.CodexConfig{BinaryPath: "codex", InheritCodexHome: true}
	return cfg
}

func storeProfileFromDomain(cfg profile.Config, kind configstore.AgentKind, appID string, tenant string, preferences map[string]any) configstore.ProfileConfig {
	if preferences == nil {
		preferences = map[string]any{}
	}
	sandbox, err := permissions.PermissionsToLegacySandbox(cfg.Permissions)
	if err != nil {
		sandbox = permissions.LegacySandbox{
			Default:     permissions.CodexSandboxReadOnly,
			Max:         permissions.CodexSandboxReadOnly,
			DefaultMode: permissions.CodexSandboxReadOnly,
			MaxMode:     permissions.CodexSandboxReadOnly,
		}
	}
	return configstore.ProfileConfig{
		SchemaVersion: 2,
		AgentKind:     kind,
		Accounts: larkcli.AccountsConfig{App: larkcli.AppCredentials{
			ID:     appID,
			Secret: "secret",
			Tenant: larkcli.TenantBrand(tenant),
		}},
		Preferences: preferences,
		Access: configstore.ProfileAccess{
			AllowedUsers:          append([]string(nil), cfg.Access.AllowedUsers...),
			AllowedChats:          append([]string(nil), cfg.Access.AllowedChats...),
			Admins:                append([]string(nil), cfg.Access.Admins...),
			RequireMentionInGroup: cfg.Access.RequireMentionInGroup,
		},
		Workspaces:  configstore.Workspaces{Default: cfg.Workspaces.Default},
		Sandbox:     sandbox,
		Permissions: cfg.Permissions,
		Codex:       storeCodexFromDomain(cfg.Codex),
		Attachments: configstore.AttachmentConfig{
			MaxCount:      cfg.Attachments.MaxCount,
			MaxBytes:      cfg.Attachments.MaxBytes,
			MaxFileBytes:  cfg.Attachments.MaxFileBytes,
			ImageMaxBytes: cfg.Attachments.ImageMaxBytes,
			CacheTTLMS:    cfg.Attachments.CacheTTLMS,
			CacheMaxBytes: cfg.Attachments.CacheMaxBytes,
		},
		Comments: map[string]any{},
		LarkCli:  configstore.LarkCliConfig{IdentityPreset: configstore.LarkCliIdentityBotOnly},
	}
}

func storeCodexFromDomain(input *profile.CodexConfig) *configstore.CodexConfig {
	if input == nil {
		return nil
	}
	return &configstore.CodexConfig{
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

type fakeCommandExecutor struct {
	interrupted map[string]bool
	scopes      []string
	pool        runexecutor.ProcessPoolSnapshot
}

func (f *fakeCommandExecutor) Interrupt(_ context.Context, scopeID string) bool {
	if f.interrupted == nil {
		f.interrupted = map[string]bool{}
	}
	f.interrupted[scopeID] = true
	return contains(f.scopes, scopeID) || scopeID == "scope-1"
}

func (f *fakeCommandExecutor) ActiveScopes() []string {
	return append([]string(nil), f.scopes...)
}

func (f *fakeCommandExecutor) PoolSnapshot() runexecutor.ProcessPoolSnapshot {
	return f.pool
}

type fakeWorkspaceStore struct {
	cwds      map[string]string
	named     map[string]string
	setErr    error
	saveErr   error
	removeErr error
}

type fakeBoundChatCreator struct {
	chatID  string
	name    string
	err     error
	created []CreateBoundChatInput
	sent    []fakeBoundChatMessage
}

type fakeBoundChatMessage struct {
	chatID   string
	markdown string
}

func (f *fakeBoundChatCreator) CreateBoundChat(_ context.Context, input CreateBoundChatInput) (CreatedChat, error) {
	f.created = append(f.created, input)
	if f.err != nil {
		return CreatedChat{}, f.err
	}
	return CreatedChat{ChatID: f.chatID, Name: f.name}, nil
}

func (f *fakeBoundChatCreator) SendMessageToChat(_ context.Context, chatID string, markdown string) error {
	f.sent = append(f.sent, fakeBoundChatMessage{chatID: chatID, markdown: markdown})
	return nil
}

func newFakeWorkspaceStore() *fakeWorkspaceStore {
	return &fakeWorkspaceStore{cwds: map[string]string{}, named: map[string]string{}}
}

func (s *fakeWorkspaceStore) CWDFor(scopeID string) string {
	return s.cwds[scopeID]
}

func (s *fakeWorkspaceStore) SetCWD(scopeID string, cwd string) error {
	if s.setErr != nil {
		return s.setErr
	}
	s.cwds[scopeID] = cwd
	return nil
}

func (s *fakeWorkspaceStore) ListNamed() map[string]string {
	out := make(map[string]string, len(s.named))
	for key, value := range s.named {
		out[key] = value
	}
	return out
}

func (s *fakeWorkspaceStore) GetNamed(name string) string {
	return s.named[name]
}

func (s *fakeWorkspaceStore) SaveNamed(name string, cwd string) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.named[name] = cwd
	return nil
}

func (s *fakeWorkspaceStore) RemoveNamed(name string) (bool, error) {
	if _, ok := s.named[name]; !ok {
		return false, nil
	}
	if s.removeErr != nil {
		return false, s.removeErr
	}
	delete(s.named, name)
	return true, nil
}
