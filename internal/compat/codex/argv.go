package codex

import (
	"fmt"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/permissions"
)

type SandboxMode = permissions.CodexSandboxMode

const (
	SandboxReadOnly         = permissions.CodexSandboxReadOnly
	SandboxWorkspaceWrite   = permissions.CodexSandboxWorkspaceWrite
	SandboxDangerFullAccess = permissions.CodexSandboxDangerFullAccess
)

type BuildExecArgsInput struct {
	CWD              string
	Sandbox          SandboxMode
	ThreadID         string
	Images           []string
	IgnoreUserConfig bool
	IgnoreRules      *bool
}

func BuildExecArgs(input BuildExecArgsInput) ([]string, error) {
	if !isSafeSandbox(input.Sandbox) {
		return nil, fmt.Errorf("unsafe sandbox mode: %s", input.Sandbox)
	}

	globalFlags := []string{
		"--sandbox",
		string(input.Sandbox),
		"-c",
		"approval_policy=\"never\"",
		"-c",
		"shell_environment_policy.inherit=\"all\"",
	}
	if input.IgnoreUserConfig {
		globalFlags = append(globalFlags, "--ignore-user-config")
	}
	if input.IgnoreRules == nil || *input.IgnoreRules {
		globalFlags = append(globalFlags, "--ignore-rules")
	}
	globalFlags = append(globalFlags,
		"--skip-git-repo-check",
		"-C",
		input.CWD,
	)

	imageFlags := make([]string, 0, len(input.Images)*2)
	for _, path := range input.Images {
		imageFlags = append(imageFlags, "--image", path)
	}

	if input.ThreadID != "" {
		args := []string{"exec"}
		args = append(args, globalFlags...)
		args = append(args, "resume", "--json")
		args = append(args, imageFlags...)
		args = append(args, input.ThreadID, "-")
		return args, nil
	}

	args := []string{"exec", "--json"}
	args = append(args, globalFlags...)
	args = append(args, imageFlags...)
	if len(imageFlags) > 0 {
		args = append(args, "--")
	}
	args = append(args, "-")
	return args, nil
}

func isSafeSandbox(sandbox SandboxMode) bool {
	return permissions.IsCodexSandboxMode(sandbox)
}
