package bridge

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestClientHandleCommandStatusUsesExistingClientState(t *testing.T) {
	client, err := NewCodexClient(CodexClientOptions{
		DefaultWorkingDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewCodexClient returned error: %v", err)
	}

	resp, err := client.HandleCommand(context.Background(), CommandRequest{
		CommandText: "/status",
		ScopeID:     "scope-1",
		ChatID:      "chat-1",
		ActorID:     "ou-user",
		SenderID:    "ou-user",
		ChatMode:    CommandChatModeP2P,
	}, CommandOptions{
		ProfileName: "codex-dev",
		RuntimeControls: RuntimeControls{
			BotOwnerID:        "ou-user",
			OwnerRefreshState: "ok",
		},
		LarkCLIStatus: CommandLarkCLIStatusUserMissing,
	})
	if err != nil {
		t.Fatalf("HandleCommand returned error: %v", err)
	}
	if resp.Status == nil || resp.Status.ProfileName != "codex-dev" || resp.Status.LarkCLIStatus != CommandLarkCLIStatusUserMissing {
		t.Fatalf("status response = %#v", resp)
	}
}

func TestClientHandleCommandPSUsesDynamicProcessID(t *testing.T) {
	client, err := NewCodexClient(CodexClientOptions{DefaultWorkingDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewCodexClient returned error: %v", err)
	}
	resp, err := client.HandleCommand(context.Background(), CommandRequest{
		CommandText: "/ps",
		ScopeID:     "scope-1",
		ChatID:      "chat-1",
		ActorID:     "ou-owner",
		SenderID:    "ou-owner",
		ChatMode:    CommandChatModeP2P,
	}, CommandOptions{
		ProfileName: "codex-dev",
		RuntimeControls: RuntimeControls{
			BotOwnerID:        "ou-owner",
			OwnerRefreshState: "ok",
		},
		ProcessID:     "stale",
		ProcessIDFunc: func() string { return "proc-current" },
		Processes: commandProcessListerFunc(func() []CommandProcessEntry {
			return []CommandProcessEntry{{
				ID:        "proc-current",
				PID:       123,
				AppID:     "cli_test",
				StartedAt: time.Now().Add(-time.Minute),
			}}
		}),
	})
	if err != nil {
		t.Fatalf("/ps returned error: %v", err)
	}
	if resp.PS == nil || resp.PS.CurrentID != "proc-current" || len(resp.PS.Processes) != 1 || !resp.PS.Processes[0].Current {
		t.Fatalf("/ps response = %#v", resp)
	}
}

func TestClientHandleCommandCoreWorkspaceAndHelp(t *testing.T) {
	client, err := NewCodexClient(CodexClientOptions{
		DefaultWorkingDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewCodexClient returned error: %v", err)
	}
	target := t.TempDir()
	baseReq := CommandRequest{
		ScopeID:  "scope-1",
		ChatID:   "chat-1",
		ActorID:  "ou-user",
		SenderID: "ou-user",
		ChatMode: CommandChatModeP2P,
	}
	opts := CommandOptions{
		ProfileName: "codex-dev",
		RuntimeControls: RuntimeControls{
			BotOwnerID:        "ou-user",
			OwnerRefreshState: "ok",
		},
	}

	cdReq := baseReq
	cdReq.CommandText = "/cd " + target
	cd, err := client.HandleCommand(context.Background(), cdReq, opts)
	if err != nil {
		t.Fatalf("/cd returned error: %v", err)
	}
	if cd.Workspace == nil || cd.Workspace.CWD == "" {
		t.Fatalf("/cd response = %#v", cd)
	}

	saveReq := baseReq
	saveReq.CommandText = "/ws save main"
	save, err := client.HandleCommand(context.Background(), saveReq, opts)
	if err != nil {
		t.Fatalf("/ws save returned error: %v", err)
	}
	if save.Workspace == nil || save.Workspace.Name != "main" {
		t.Fatalf("/ws save response = %#v", save)
	}

	listReq := baseReq
	listReq.CommandText = "/ws list"
	list, err := client.HandleCommand(context.Background(), listReq, opts)
	if err != nil {
		t.Fatalf("/ws list returned error: %v", err)
	}
	if list.Workspace == nil || list.Workspace.Entries["main"] == "" {
		t.Fatalf("/ws list response = %#v", list)
	}

	helpReq := baseReq
	helpReq.CommandText = "/help"
	help, err := client.HandleCommand(context.Background(), helpReq, opts)
	if err != nil {
		t.Fatalf("/help returned error: %v", err)
	}
	if help.Help == nil || !strings.Contains(help.Markdown, "/new") || !strings.Contains(help.Markdown, "/account") {
		t.Fatalf("/help response = %#v", help)
	}
}

func TestClientHandleCommandWorkspaceStoreErrorReturnsFailure(t *testing.T) {
	client, err := NewCodexClient(CodexClientOptions{
		DefaultWorkingDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewCodexClient returned error: %v", err)
	}
	persistErr := errors.New("persist failed")
	resp, err := client.HandleCommand(context.Background(), CommandRequest{
		CommandText: "/cd " + t.TempDir(),
		ScopeID:     "scope-1",
		ChatID:      "chat-1",
		ActorID:     "ou-user",
		SenderID:    "ou-user",
		ChatMode:    CommandChatModeP2P,
	}, CommandOptions{
		ProfileName: "codex-dev",
		RuntimeControls: RuntimeControls{
			BotOwnerID:        "ou-user",
			OwnerRefreshState: "ok",
		},
		Workspaces: &failingCommandWorkspaceStore{setErr: persistErr},
	})
	if err != nil {
		t.Fatalf("/cd returned error: %v", err)
	}
	if resp.Workspace == nil || resp.Workspace.Failure != persistErr.Error() || !strings.Contains(resp.Markdown, "保存工作目录失败") {
		t.Fatalf("failed /cd response = %#v", resp)
	}
}

func TestCommandHandlerNewChatUsesInjectedCreator(t *testing.T) {
	client, err := NewCodexClient(CodexClientOptions{DefaultWorkingDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewCodexClient returned error: %v", err)
	}
	handler, err := NewCommandHandler(client)
	if err != nil {
		t.Fatalf("NewCommandHandler returned error: %v", err)
	}
	transport := NewFakeLarkTransport(LarkBotIdentity{OpenID: "ou_bot", Name: "Codex"})
	cwd := t.TempDir()
	resp, err := handler.HandleCommand(context.Background(), CommandRequest{
		CommandText: "/new chat Shipping Room",
		ScopeID:     "scope-1",
		ChatID:      "chat-1",
		ActorID:     "ou-user",
		SenderID:    "ou-user",
		ChatMode:    CommandChatModeP2P,
		WorkingDir:  cwd,
	}, CommandOptions{
		ChatCreator: transport,
	})
	if err != nil {
		t.Fatalf("/new chat returned error: %v", err)
	}
	if resp.Session == nil || resp.Session.CreatedChatID != "oc_fake_1" || !resp.Session.WelcomeSent {
		t.Fatalf("/new chat response = %#v", resp)
	}
	created := transport.CreatedChatSnapshot()
	if len(created) != 1 || created[0].Name != "Shipping Room" || created[0].InviteOpenID != "ou-user" {
		t.Fatalf("created chats = %#v", created)
	}
	sent := transport.SentMessageSnapshot()
	if len(sent) != 1 || sent[0].ChatID != "oc_fake_1" || !strings.Contains(sent[0].Content.Markdown, cwd) {
		t.Fatalf("sent messages = %#v", sent)
	}
}

func TestCommandHandlerOwnsCommandState(t *testing.T) {
	client, err := NewCodexClient(CodexClientOptions{DefaultWorkingDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewCodexClient returned error: %v", err)
	}
	first, err := NewCommandHandler(client)
	if err != nil {
		t.Fatalf("NewCommandHandler first returned error: %v", err)
	}
	second, err := NewCommandHandler(client)
	if err != nil {
		t.Fatalf("NewCommandHandler second returned error: %v", err)
	}
	target := t.TempDir()
	req := CommandRequest{
		ScopeID:  "scope-1",
		ChatID:   "chat-1",
		ActorID:  "ou-user",
		SenderID: "ou-user",
		ChatMode: CommandChatModeP2P,
	}
	opts := CommandOptions{
		ProfileName: "codex-dev",
		RuntimeControls: RuntimeControls{
			BotOwnerID:        "ou-user",
			OwnerRefreshState: "ok",
		},
	}

	saveReq := req
	saveReq.CommandText = "/cd " + target
	if _, err := first.HandleCommand(context.Background(), saveReq, opts); err != nil {
		t.Fatalf("first /cd returned error: %v", err)
	}
	saveReq.CommandText = "/ws save main"
	if _, err := first.HandleCommand(context.Background(), saveReq, opts); err != nil {
		t.Fatalf("first /ws save returned error: %v", err)
	}

	listReq := req
	listReq.CommandText = "/ws list"
	firstList, err := first.HandleCommand(context.Background(), listReq, opts)
	if err != nil {
		t.Fatalf("first /ws list returned error: %v", err)
	}
	if firstList.Workspace == nil || firstList.Workspace.Entries["main"] == "" {
		t.Fatalf("first handler workspace = %#v", firstList.Workspace)
	}
	secondList, err := second.HandleCommand(context.Background(), listReq, opts)
	if err != nil {
		t.Fatalf("second /ws list returned error: %v", err)
	}
	if secondList.Workspace != nil && secondList.Workspace.Entries["main"] != "" {
		t.Fatalf("second handler leaked first handler state: %#v", secondList.Workspace)
	}
}

func TestClientReleaseCommandStateDropsHandleCommandState(t *testing.T) {
	client, err := NewCodexClient(CodexClientOptions{DefaultWorkingDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewCodexClient returned error: %v", err)
	}
	req := CommandRequest{
		ScopeID:  "scope-1",
		ChatID:   "chat-1",
		ActorID:  "ou-user",
		SenderID: "ou-user",
		ChatMode: CommandChatModeP2P,
	}
	opts := CommandOptions{
		ProfileName: "codex-dev",
		RuntimeControls: RuntimeControls{
			BotOwnerID:        "ou-user",
			OwnerRefreshState: "ok",
		},
	}
	cdReq := req
	cdReq.CommandText = "/cd " + t.TempDir()
	if _, err := client.HandleCommand(context.Background(), cdReq, opts); err != nil {
		t.Fatalf("/cd returned error: %v", err)
	}
	saveReq := req
	saveReq.CommandText = "/ws save main"
	if _, err := client.HandleCommand(context.Background(), saveReq, opts); err != nil {
		t.Fatalf("/ws save returned error: %v", err)
	}

	client.ReleaseCommandState()

	listReq := req
	listReq.CommandText = "/ws list"
	resp, err := client.HandleCommand(context.Background(), listReq, opts)
	if err != nil {
		t.Fatalf("/ws list returned error: %v", err)
	}
	if resp.Workspace != nil && resp.Workspace.Entries["main"] != "" {
		t.Fatalf("released command state still has workspace alias: %#v", resp.Workspace)
	}
}

func TestClientHandleCommandRejectsUntrustedAllowAccess(t *testing.T) {
	client, err := NewCodexClient(CodexClientOptions{DefaultWorkingDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewCodexClient returned error: %v", err)
	}
	_, err = client.HandleCommand(context.Background(), CommandRequest{
		CommandText: "/resume",
		ScopeID:     "scope-1",
		ChatID:      "chat-1",
		ActorID:     "ou-user",
		SenderID:    "ou-user",
		ChatMode:    CommandChatModeP2P,
		Access:      AccessDecision{OK: true, Reason: AccessOwner},
	}, CommandOptions{})
	if !errors.Is(err, ErrUntrustedAccessDecision) {
		t.Fatalf("HandleCommand err = %v, want ErrUntrustedAccessDecision", err)
	}
}

type commandProcessListerFunc func() []CommandProcessEntry

func (f commandProcessListerFunc) ListProcesses() []CommandProcessEntry {
	return f()
}

type failingCommandWorkspaceStore struct {
	setErr error
}

func (s *failingCommandWorkspaceStore) CWDFor(string) string {
	return ""
}

func (s *failingCommandWorkspaceStore) SetCWD(string, string) error {
	return s.setErr
}

func (s *failingCommandWorkspaceStore) ListNamed() map[string]string {
	return nil
}

func (s *failingCommandWorkspaceStore) GetNamed(string) string {
	return ""
}

func (s *failingCommandWorkspaceStore) SaveNamed(string, string) error {
	return nil
}

func (s *failingCommandWorkspaceStore) RemoveNamed(string) (bool, error) {
	return false, nil
}

func TestClientHandleCommandRemainingFacadeFields(t *testing.T) {
	client, err := NewCodexClient(CodexClientOptions{
		DefaultWorkingDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewCodexClient returned error: %v", err)
	}
	restarts := 0
	baseReq := CommandRequest{
		ScopeID:  "scope-1",
		ChatID:   "chat-1",
		ActorID:  "ou-user",
		SenderID: "ou-user",
		ChatMode: CommandChatModeP2P,
	}
	opts := CommandOptions{
		ProfileName: "codex-dev",
		RuntimeControls: RuntimeControls{
			BotOwnerID:        "ou-user",
			OwnerRefreshState: "ok",
		},
		Reconnector: CommandReconnectorFunc(func(_ context.Context, wait bool) error {
			if wait {
				t.Fatalf("wait = true, want false")
			}
			restarts++
			return nil
		}),
	}

	docReq := baseReq
	docReq.CommandText = "/doc ws bind token /tmp/secret"
	doc, err := client.HandleCommand(context.Background(), docReq, opts)
	if err != nil {
		t.Fatalf("/doc returned error: %v", err)
	}
	if doc.Doc == nil || !doc.Doc.NoOp || strings.Contains(doc.Markdown, "/tmp/secret") {
		t.Fatalf("/doc response = %#v", doc)
	}

	reconnectReq := baseReq
	reconnectReq.CommandText = "/reconnect"
	reconnect, err := client.HandleCommand(context.Background(), reconnectReq, opts)
	if err != nil {
		t.Fatalf("/reconnect returned error: %v", err)
	}
	if reconnect.Reconnect == nil || !reconnect.Reconnect.Restarted || restarts != 1 {
		t.Fatalf("/reconnect response=%#v restarts=%d", reconnect, restarts)
	}
}
