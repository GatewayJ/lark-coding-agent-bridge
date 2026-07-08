package claudecli

import (
	"fmt"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/permissions"
)

const DefaultPermissionMode = permissions.ClaudePermissionBypassPermissions

type BuildArgsInput struct {
	Prompt         string
	PermissionMode permissions.ClaudePermissionMode
	SystemPrompt   string
	SessionID      string
	Model          string
}

func BuildArgs(input BuildArgsInput) ([]string, error) {
	permissionMode := input.PermissionMode
	if permissionMode == "" {
		permissionMode = DefaultPermissionMode
	}
	if !permissions.IsClaudePermissionMode(permissionMode) {
		return nil, fmt.Errorf("unsafe claude permission mode: %s", permissionMode)
	}

	args := []string{
		"-p",
		input.Prompt,
		"--output-format",
		"stream-json",
		"--verbose",
		"--permission-mode",
		string(permissionMode),
		"--append-system-prompt",
		input.SystemPrompt,
	}
	if input.SessionID != "" {
		args = append(args, "--resume", input.SessionID)
	}
	if input.Model != "" {
		args = append(args, "--model", input.Model)
	}
	return args, nil
}
