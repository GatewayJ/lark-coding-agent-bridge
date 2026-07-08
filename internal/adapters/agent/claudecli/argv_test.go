package claudecli

import (
	"reflect"
	"strings"
	"testing"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/permissions"
)

func TestBuildArgsFreshRun(t *testing.T) {
	args, err := BuildArgs(BuildArgsInput{
		Prompt:         "hello",
		PermissionMode: permissions.ClaudePermissionAcceptEdits,
		SystemPrompt:   "system prompt",
	})
	if err != nil {
		t.Fatalf("BuildArgs returned error: %v", err)
	}

	want := []string{
		"-p",
		"hello",
		"--output-format",
		"stream-json",
		"--verbose",
		"--permission-mode",
		"acceptEdits",
		"--append-system-prompt",
		"system prompt",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args mismatch:\nwant %#v\n got %#v", want, args)
	}
}

func TestBuildArgsDefaultsPermissionAndAppendsResumeModel(t *testing.T) {
	args, err := BuildArgs(BuildArgsInput{
		Prompt:       "continue",
		SystemPrompt: "system prompt",
		SessionID:    "sess-old",
		Model:        "sonnet",
	})
	if err != nil {
		t.Fatalf("BuildArgs returned error: %v", err)
	}

	wantSuffix := []string{"--resume", "sess-old", "--model", "sonnet"}
	if got := args[6]; got != "bypassPermissions" {
		t.Fatalf("permission mode = %q, want bypassPermissions", got)
	}
	if !reflect.DeepEqual(args[len(args)-len(wantSuffix):], wantSuffix) {
		t.Fatalf("suffix mismatch: %#v", args)
	}
}

func TestBuildArgsRejectsUnknownPermissionMode(t *testing.T) {
	_, err := BuildArgs(BuildArgsInput{
		Prompt:         "hello",
		PermissionMode: permissions.ClaudePermissionMode("root"),
		SystemPrompt:   "system prompt",
	})
	if err == nil || !strings.Contains(err.Error(), "unsafe claude permission mode") {
		t.Fatalf("error = %v, want unsafe permission mode", err)
	}
}
