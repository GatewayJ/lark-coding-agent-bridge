package codex

import (
	"reflect"
	"strings"
	"testing"
)

func TestBuildExecArgsFreshExec(t *testing.T) {
	args, err := BuildExecArgs(BuildExecArgsInput{
		CWD:     "/repo",
		Sandbox: SandboxReadOnly,
	})
	if err != nil {
		t.Fatalf("BuildExecArgs returned error: %v", err)
	}

	want := []string{
		"exec",
		"--json",
		"--sandbox",
		"read-only",
		"-c",
		"approval_policy=\"never\"",
		"-c",
		"shell_environment_policy.inherit=\"all\"",
		"--ignore-rules",
		"--skip-git-repo-check",
		"-C",
		"/repo",
		"-",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args mismatch\nwant: %#v\n got: %#v", want, args)
	}
}

func TestBuildExecArgsResume(t *testing.T) {
	args, err := BuildExecArgs(BuildExecArgsInput{
		CWD:      "/repo",
		Sandbox:  SandboxWorkspaceWrite,
		ThreadID: "thread-123",
	})
	if err != nil {
		t.Fatalf("BuildExecArgs returned error: %v", err)
	}

	want := []string{
		"exec",
		"--sandbox",
		"workspace-write",
		"-c",
		"approval_policy=\"never\"",
		"-c",
		"shell_environment_policy.inherit=\"all\"",
		"--ignore-rules",
		"--skip-git-repo-check",
		"-C",
		"/repo",
		"resume",
		"--json",
		"thread-123",
		"-",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args mismatch\nwant: %#v\n got: %#v", want, args)
	}
}

func TestBuildExecArgsAllowsDangerFullAccess(t *testing.T) {
	args, err := BuildExecArgs(BuildExecArgsInput{
		CWD:     "/repo",
		Sandbox: SandboxDangerFullAccess,
	})
	if err != nil {
		t.Fatalf("BuildExecArgs returned error: %v", err)
	}
	if !contains(args, "danger-full-access") {
		t.Fatalf("expected danger-full-access in args: %#v", args)
	}
}

func TestBuildExecArgsImageFlags(t *testing.T) {
	t.Run("fresh exec separates images from stdin prompt", func(t *testing.T) {
		args, err := BuildExecArgs(BuildExecArgsInput{
			CWD:     "/repo",
			Sandbox: SandboxWorkspaceWrite,
			Images:  []string{"/tmp/image.png"},
		})
		if err != nil {
			t.Fatalf("BuildExecArgs returned error: %v", err)
		}

		want := []string{
			"exec",
			"--json",
			"--sandbox",
			"workspace-write",
			"-c",
			"approval_policy=\"never\"",
			"-c",
			"shell_environment_policy.inherit=\"all\"",
			"--ignore-rules",
			"--skip-git-repo-check",
			"-C",
			"/repo",
			"--image",
			"/tmp/image.png",
			"--",
			"-",
		}
		if !reflect.DeepEqual(args, want) {
			t.Fatalf("args mismatch\nwant: %#v\n got: %#v", want, args)
		}
	})

	t.Run("resume puts images after resume local json flag", func(t *testing.T) {
		args, err := BuildExecArgs(BuildExecArgsInput{
			CWD:      "/repo",
			Sandbox:  SandboxWorkspaceWrite,
			ThreadID: "thread-123",
			Images:   []string{"/tmp/image.png"},
		})
		if err != nil {
			t.Fatalf("BuildExecArgs returned error: %v", err)
		}

		want := []string{
			"exec",
			"--sandbox",
			"workspace-write",
			"-c",
			"approval_policy=\"never\"",
			"-c",
			"shell_environment_policy.inherit=\"all\"",
			"--ignore-rules",
			"--skip-git-repo-check",
			"-C",
			"/repo",
			"resume",
			"--json",
			"--image",
			"/tmp/image.png",
			"thread-123",
			"-",
		}
		if !reflect.DeepEqual(args, want) {
			t.Fatalf("args mismatch\nwant: %#v\n got: %#v", want, args)
		}
	})
}

func TestBuildExecArgsIgnoreUserConfig(t *testing.T) {
	args, err := BuildExecArgs(BuildExecArgsInput{
		CWD:              "/repo",
		Sandbox:          SandboxReadOnly,
		IgnoreUserConfig: true,
	})
	if err != nil {
		t.Fatalf("BuildExecArgs returned error: %v", err)
	}
	if !contains(args, "--ignore-user-config") {
		t.Fatalf("expected --ignore-user-config in args: %#v", args)
	}
}

func TestBuildExecArgsIgnoreRulesFalse(t *testing.T) {
	ignoreRules := false
	args, err := BuildExecArgs(BuildExecArgsInput{
		CWD:         "/repo",
		Sandbox:     SandboxReadOnly,
		IgnoreRules: &ignoreRules,
	})
	if err != nil {
		t.Fatalf("BuildExecArgs returned error: %v", err)
	}
	if contains(args, "--ignore-rules") {
		t.Fatalf("expected --ignore-rules to be omitted: %#v", args)
	}
}

func TestBuildExecArgsRejectsUnsafeSandbox(t *testing.T) {
	_, err := BuildExecArgs(BuildExecArgsInput{
		CWD:     "/repo",
		Sandbox: SandboxMode("unsafe"),
	})
	if err == nil {
		t.Fatal("expected error for unsafe sandbox")
	}
	if !strings.Contains(err.Error(), "unsafe sandbox mode: unsafe") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func contains(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}
