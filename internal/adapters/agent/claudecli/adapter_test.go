package claudecli

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/permissions"
	agentport "github.com/zarazhangrui/lark-coding-agent-bridge/internal/ports/agent"
)

func TestAdapterRunSpawnsWithEnvSystemPromptAndTranslatesEvents(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake uses POSIX sh")
	}
	cwd := t.TempDir()
	binary := writeFakeClaude(t, `
if [ "$1" = "-p" ] && [ "$2" = "hello" ] && [ "$3" = "--output-format" ] && [ "$4" = "stream-json" ] && [ "$5" = "--verbose" ] && [ "$6" = "--permission-mode" ] && [ "$7" = "acceptEdits" ] && [ "$8" = "--append-system-prompt" ]; then
  printf '%s' ok > args_ok.txt
fi
printf '%s' "$9" > system_prompt.txt
printf '%s' "$LARK_CHANNEL" > lark_channel.txt
printf '%s' "$LARK_CHANNEL_PROFILE" > lark_profile.txt
printf '%s\n' '{"type":"system","subtype":"init","session_id":"sess-1","cwd":"/repo","model":"sonnet"}'
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"hello from claude"}]}}'
printf '%s\n' '{"type":"result","session_id":"sess-1","usage":{"input_tokens":3,"output_tokens":5}}'
`)

	adapter := New(Options{
		Binary: binary,
		LarkChannelEnv: map[string]string{
			"LARK_CHANNEL_PROFILE": "claude-dev",
		},
	})
	adapter.MergeEnv(map[string]string{"LARK_CHANNEL_PROFILE": "claude-projected"})
	adapter.SetBotIdentity(agentport.AgentBotIdentity{OpenID: "ou_bot", Name: "Bridge Bot"})
	run, err := adapter.Run(context.Background(), agentport.AgentRunOptions{
		RunID:          "run-1",
		Prompt:         "hello",
		CWD:            cwd,
		PermissionMode: permissions.ClaudePermissionAcceptEdits,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	events := collectEvents(t, run)
	if len(events) != 4 {
		t.Fatalf("expected 4 events, got %#v", events)
	}
	if events[0].Type != agentport.EventSystem || events[0].SessionID == nil || *events[0].SessionID != "sess-1" {
		t.Fatalf("unexpected system event: %#v", events[0])
	}
	if events[1].Type != agentport.EventText || events[1].Delta == nil || *events[1].Delta != "hello from claude" {
		t.Fatalf("unexpected text event: %#v", events[1])
	}
	if events[2].Type != agentport.EventUsage || events[2].InputTokens == nil || *events[2].InputTokens != 3 {
		t.Fatalf("unexpected usage event: %#v", events[2])
	}
	if events[3].Type != agentport.EventDone || events[3].SessionID == nil || *events[3].SessionID != "sess-1" {
		t.Fatalf("unexpected done event: %#v", events[3])
	}

	if got := readFile(t, filepath.Join(cwd, "args_ok.txt")); got != "ok" {
		t.Fatalf("args marker = %q, want ok", got)
	}
	if got := readFile(t, filepath.Join(cwd, "lark_channel.txt")); got != "1" {
		t.Fatalf("LARK_CHANNEL = %q, want 1", got)
	}
	if got := readFile(t, filepath.Join(cwd, "lark_profile.txt")); got != "claude-projected" {
		t.Fatalf("LARK_CHANNEL_PROFILE = %q, want claude-projected", got)
	}
	systemPrompt := readFile(t, filepath.Join(cwd, "system_prompt.txt"))
	for _, fragment := range []string{"lark-channel-bridge", "ou_bot", "Bridge Bot"} {
		if !strings.Contains(systemPrompt, fragment) {
			t.Fatalf("system prompt missing %q in:\n%s", fragment, systemPrompt)
		}
	}
}

func TestAdapterExitErrorEmitsTerminalError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake uses POSIX sh")
	}
	binary := writeFakeClaude(t, `
printf '%s\n' 'not json'
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"before failure"}]}}'
printf '%s\n' 'boom' >&2
exit 42
`)
	adapter := New(Options{Binary: binary})
	run, err := adapter.Run(context.Background(), agentport.AgentRunOptions{
		RunID:  "run-error",
		Prompt: "fail",
		CWD:    t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	events := collectEvents(t, run)
	if len(events) != 2 || events[0].Type != agentport.EventText || events[1].Type != agentport.EventError || events[1].Message == nil {
		t.Fatalf("unexpected events: %#v", events)
	}
	if !strings.Contains(*events[1].Message, "claude exited with code 42: boom") {
		t.Fatalf("unexpected error message: %q", *events[1].Message)
	}
}

func TestAdapterStopEmitsInterruptedDone(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake uses POSIX sh")
	}
	binary := writeFakeClaude(t, `
printf '%s\n' '{"type":"system","subtype":"init","session_id":"sess-stop"}'
trap 'exit 143' TERM
while true; do sleep 1; done
`)
	adapter := New(Options{
		Binary:    binary,
		StopGrace: 50 * time.Millisecond,
	})
	run, err := adapter.Run(context.Background(), agentport.AgentRunOptions{
		RunID:  "run-stop",
		Prompt: "stop me",
		CWD:    t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	first := <-run.Events()
	if first.Type != agentport.EventSystem {
		t.Fatalf("unexpected first event: %#v", first)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := run.Stop(ctx); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}

	var rest []agentport.AgentEvent
	for event := range run.Events() {
		rest = append(rest, event)
	}
	if len(rest) != 1 || rest[0].Type != agentport.EventDone || rest[0].TerminationReason != agentport.TerminationInterrupted {
		t.Fatalf("unexpected stop events: %#v", rest)
	}
	if rest[0].SessionID == nil || *rest[0].SessionID != "sess-stop" {
		t.Fatalf("interrupted done session = %#v, want sess-stop", rest[0].SessionID)
	}
}

func TestAdapterRequiresCWD(t *testing.T) {
	adapter := New(Options{Binary: "unused"})
	_, err := adapter.Run(context.Background(), agentport.AgentRunOptions{
		RunID:  "run-no-cwd",
		Prompt: "hi",
	})
	if err == nil || !strings.Contains(err.Error(), "cwd is required") {
		t.Fatalf("error = %v, want cwd required", err)
	}
}

func TestAdapterSpawnFailureEmitsTerminalErrorEvent(t *testing.T) {
	adapter := New(Options{Binary: filepath.Join(t.TempDir(), "missing-claude")})
	run, err := adapter.Run(context.Background(), agentport.AgentRunOptions{
		RunID:  "run-missing",
		Prompt: "hello",
		CWD:    t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	events := collectEvents(t, run)
	if len(events) != 1 || events[0].Type != agentport.EventError || events[0].Message == nil {
		t.Fatalf("unexpected spawn failure events: %#v", events)
	}
	if !strings.Contains(*events[0].Message, "failed to spawn claude") || events[0].TerminationReason != agentport.TerminationFailed {
		t.Fatalf("unexpected spawn failure event: %#v", events[0])
	}
}

func writeFakeClaude(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude")
	content := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then printf '%s\\n' 'claude 0.0.0'; exit 0; fi\n" + body
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
	return path
}

func collectEvents(t *testing.T, run agentport.AgentRun) []agentport.AgentEvent {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var events []agentport.AgentEvent
	for {
		select {
		case event, ok := <-run.Events():
			if !ok {
				waitCtx, waitCancel := context.WithTimeout(context.Background(), time.Second)
				defer waitCancel()
				if ok, err := run.WaitForExit(waitCtx); !ok || err != nil {
					t.Fatalf("WaitForExit ok=%v err=%v", ok, err)
				}
				return events
			}
			events = append(events, event)
		case <-ctx.Done():
			t.Fatalf("timed out collecting events")
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(bytes)
}
