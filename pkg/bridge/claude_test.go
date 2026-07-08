package bridge

import (
	"testing"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/permissions"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/profile"
)

func TestNewClaudeClientValidatesAccessPair(t *testing.T) {
	_, err := NewClaudeClient(ClaudeClientOptions{
		Binary:        "claude",
		DefaultAccess: AccessFull,
		MaxAccess:     AccessWorkspace,
	})
	if err == nil {
		t.Fatalf("NewClaudeClient accepted defaultAccess above maxAccess")
	}
}

func TestNewClaudeClientCreatesUsableFacade(t *testing.T) {
	client, err := NewClaudeClient(ClaudeClientOptions{
		Binary:            "claude",
		DefaultWorkingDir: t.TempDir(),
		DefaultAccess:     AccessWorkspace,
		MaxAccess:         AccessWorkspace,
		PermissionMode:    ClaudePermissionAcceptEdits,
		BotOpenID:         "ou_bot",
		BotName:           "Claude",
		MaxProcesses:      2,
	})
	if err != nil {
		t.Fatalf("NewClaudeClient returned error: %v", err)
	}
	if client == nil {
		t.Fatalf("NewClaudeClient returned nil client")
	}
	if client.profile.AgentKind != profile.AgentClaude {
		t.Fatalf("AgentKind = %q, want claude", client.profile.AgentKind)
	}
	if client.profile.Permissions.Claude == nil ||
		client.profile.Permissions.Claude.PermissionMode != permissions.ClaudePermissionAcceptEdits {
		t.Fatalf("Claude permissions = %#v, want acceptEdits", client.profile.Permissions.Claude)
	}
	status := client.Status()
	if status.AgentName != "Claude Code" || status.Pool.Cap != 2 {
		t.Fatalf("unexpected status: %#v", status)
	}
}

func TestNewClaudeClientRejectsInvalidPermissionMode(t *testing.T) {
	_, err := NewClaudeClient(ClaudeClientOptions{
		Binary:         "claude",
		PermissionMode: ClaudePermissionMode("root"),
	})
	if err == nil {
		t.Fatalf("NewClaudeClient accepted invalid permission mode")
	}
}
