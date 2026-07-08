package codexcli

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/permissions"
	agentport "github.com/zarazhangrui/lark-coding-agent-bridge/internal/ports/agent"
)

func TestAdapterRunWritesPromptAndTranslatesEvents(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake uses POSIX sh")
	}
	cwd := t.TempDir()
	binary := writeFakeCodex(t, `
cat > prompt.txt
printf '%s\n' '{"type":"thread.started","thread_id":"thread-1"}'
printf '%s\n' '{"type":"agent_message","message":"hello from codex"}'
printf '%s\n' '{"type":"turn.completed","usage":{"input_tokens":3,"output_tokens":5}}'
`)

	adapter := New(Options{Binary: binary, ProfileStateDir: t.TempDir()})
	adapter.SetBotIdentity(agentport.AgentBotIdentity{OpenID: "ou_bot", Name: "Bridge Bot"})
	run, err := adapter.Run(context.Background(), agentport.AgentRunOptions{
		RunID:   "run-1",
		Prompt:  "hello",
		CWD:     cwd,
		Sandbox: permissions.CodexSandboxDangerFullAccess,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	events := collectEvents(t, run)
	if len(events) != 4 {
		t.Fatalf("expected 4 events, got %#v", events)
	}
	if events[0].Type != agentport.EventSystem || events[0].ThreadID == nil || *events[0].ThreadID != "thread-1" {
		t.Fatalf("unexpected system event: %#v", events[0])
	}
	if events[1].Type != agentport.EventText || events[1].Delta == nil || *events[1].Delta != "hello from codex" {
		t.Fatalf("unexpected text event: %#v", events[1])
	}
	if events[2].Type != agentport.EventUsage || events[2].InputTokens == nil || *events[2].InputTokens != 3 {
		t.Fatalf("unexpected usage event: %#v", events[2])
	}
	if events[3].Type != agentport.EventDone || events[3].TerminationReason != agentport.TerminationNormal {
		t.Fatalf("unexpected done event: %#v", events[3])
	}

	promptBytes, err := os.ReadFile(filepath.Join(cwd, "prompt.txt"))
	if err != nil {
		t.Fatalf("read prompt: %v", err)
	}
	prompt := string(promptBytes)
	for _, fragment := range []string{"## user_message", "hello", "ou_bot", "Bridge Bot"} {
		if !strings.Contains(prompt, fragment) {
			t.Fatalf("prompt missing %q in:\n%s", fragment, prompt)
		}
	}
}

func TestAdapterUsesProfileLocalCodexHomeWhenInheritanceDisabled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake uses POSIX sh")
	}
	cwd := t.TempDir()
	profileStateDir := t.TempDir()
	inherit := false
	binary := writeFakeCodex(t, `
printf '%s' "$CODEX_HOME" > codex_home.txt
printf '%s' "$LARK_CHANNEL" > lark_channel.txt
printf '%s' "$LARK_CHANNEL_TOKEN" > lark_env.txt
printf '%s' "$LARK_CHANNEL_PROFILE" > lark_profile.txt
printf '%s\n' '{"type":"turn.completed"}'
`)

	adapter := New(Options{
		Binary:           binary,
		ProfileStateDir:  profileStateDir,
		InheritCodexHome: &inherit,
		LarkChannelEnv:   map[string]string{"LARK_CHANNEL_TOKEN": "token-1"},
	})
	adapter.MergeEnv(map[string]string{
		"LARK_CHANNEL_TOKEN":   "token-2",
		"LARK_CHANNEL_PROFILE": "codex-projected",
	})
	run, err := adapter.Run(context.Background(), agentport.AgentRunOptions{
		RunID:   "run-1",
		Prompt:  "hello",
		CWD:     cwd,
		Sandbox: permissions.CodexSandboxDangerFullAccess,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	_ = collectEvents(t, run)

	gotHome := readFile(t, filepath.Join(cwd, "codex_home.txt"))
	wantHome := filepath.Join(profileStateDir, "codex-home")
	if gotHome != wantHome {
		t.Fatalf("CODEX_HOME mismatch: want %q got %q", wantHome, gotHome)
	}
	if got := readFile(t, filepath.Join(cwd, "lark_channel.txt")); got != "1" {
		t.Fatalf("LARK_CHANNEL mismatch: %q", got)
	}
	if got := readFile(t, filepath.Join(cwd, "lark_env.txt")); got != "token-2" {
		t.Fatalf("Lark env mismatch: %q", got)
	}
	if got := readFile(t, filepath.Join(cwd, "lark_profile.txt")); got != "codex-projected" {
		t.Fatalf("Lark profile env mismatch: %q", got)
	}
}

func TestAdapterReportsCodexJsonlDrift(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake uses POSIX sh")
	}
	binary := writeFakeCodex(t, `
printf '%s\n' '{"type":"unknown.future","value":1}'
printf '%s\n' '{"type":"error","message":"temporary transport issue"}'
printf '%s\n' '{"type":"turn.completed"}'
`)
	logger := &recordingWarnLogger{}
	adapter := New(Options{Binary: binary, ProfileStateDir: t.TempDir(), Logger: logger})
	run, err := adapter.Run(context.Background(), agentport.AgentRunOptions{
		RunID:   "run-drift",
		Prompt:  "hello",
		CWD:     t.TempDir(),
		Sandbox: permissions.CodexSandboxDangerFullAccess,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	events := collectEvents(t, run)
	if len(events) != 1 || events[0].Type != agentport.EventDone {
		t.Fatalf("unexpected events: %#v", events)
	}
	messages := logger.messages()
	if len(messages) != 2 || messages[0] != "jsonl.unknown_event" || messages[1] != "jsonl.error_event" {
		t.Fatalf("logger messages = %#v", messages)
	}
}

func TestMergeEnvOverridesCaseInsensitiveKeys(t *testing.T) {
	got := mergeEnv([]string{
		"Path=/bin",
		"lark_channel=0",
		"Codex_Home=/old",
	}, map[string]string{
		"LARK_CHANNEL": "1",
		"CODEX_HOME":   "/new",
	})
	values := envMap(got)
	if values["lark_channel"] != "" || values["Codex_Home"] != "" {
		t.Fatalf("mergeEnv kept case-variant stale keys: %#v", got)
	}
	if values["LARK_CHANNEL"] != "1" || values["CODEX_HOME"] != "/new" || values["Path"] != "/bin" {
		t.Fatalf("mergeEnv = %#v", got)
	}
}

func TestCheckAvailabilityRunsVersionCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake uses POSIX sh")
	}
	binary := writeRawExecutable(t, "codex", `
if [ "$1" = "--version" ]; then
  printf '%s\n' 'codex 1.2.3'
  exit 0
fi
exit 1
`)
	availability, err := New(Options{Binary: binary}).CheckAvailability(context.Background())
	if err != nil {
		t.Fatalf("CheckAvailability returned error: %v", err)
	}
	if !availability.OK || availability.Version != "codex 1.2.3" {
		t.Fatalf("availability = %#v", availability)
	}
}

func TestCheckAvailabilityRejectsEmptyVersionOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake uses POSIX sh")
	}
	binary := writeRawExecutable(t, "codex", `
if [ "$1" = "--version" ]; then
  exit 0
fi
exit 1
`)
	availability, err := New(Options{Binary: binary}).CheckAvailability(context.Background())
	if err != nil {
		t.Fatalf("CheckAvailability returned error: %v", err)
	}
	if availability.OK || availability.Diagnostic == nil || availability.Diagnostic.Code != "agent-version-check-empty-output" {
		t.Fatalf("availability = %#v", availability)
	}
}

func TestAdapterStopEmitsInterruptedDone(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake uses POSIX sh")
	}
	binary := writeFakeCodex(t, `
printf '%s\n' '{"type":"thread.started","thread_id":"thread-stop"}'
sleep 30 >/dev/null 2>&1 &
wait
`)

	adapter := New(Options{
		Binary:          binary,
		ProfileStateDir: t.TempDir(),
		StopGrace:       50 * time.Millisecond,
	})
	run, err := adapter.Run(context.Background(), agentport.AgentRunOptions{
		RunID:   "run-stop",
		Prompt:  "stop me",
		CWD:     t.TempDir(),
		Sandbox: permissions.CodexSandboxDangerFullAccess,
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
}

func envMap(values []string) map[string]string {
	out := make(map[string]string, len(values))
	for _, entry := range values {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			out[key] = value
		}
	}
	return out
}

type recordingWarnLogger struct {
	mu   sync.Mutex
	msgs []string
}

func (l *recordingWarnLogger) Warn(msg string, _ map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.msgs = append(l.msgs, msg)
}

func (l *recordingWarnLogger) messages() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, len(l.msgs))
	copy(out, l.msgs)
	return out
}

func TestAdapterExitErrorEmitsTerminalError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake uses POSIX sh")
	}
	binary := writeFakeCodex(t, `
printf '%s\n' 'boom' >&2
exit 7
`)
	adapter := New(Options{Binary: binary, ProfileStateDir: t.TempDir()})
	run, err := adapter.Run(context.Background(), agentport.AgentRunOptions{
		RunID:   "run-error",
		Prompt:  "hello",
		CWD:     t.TempDir(),
		Sandbox: permissions.CodexSandboxDangerFullAccess,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	events := collectEvents(t, run)
	if len(events) != 1 || events[0].Type != agentport.EventError || events[0].Message == nil {
		t.Fatalf("unexpected error events: %#v", events)
	}
	if !strings.Contains(*events[0].Message, "codex exited with code 7: boom") {
		t.Fatalf("unexpected error message: %q", *events[0].Message)
	}
}

func TestAdapterSpawnFailureEmitsTerminalErrorEvent(t *testing.T) {
	adapter := New(Options{Binary: filepath.Join(t.TempDir(), "missing-codex"), ProfileStateDir: t.TempDir()})
	run, err := adapter.Run(context.Background(), agentport.AgentRunOptions{
		RunID:   "run-missing",
		Prompt:  "hello",
		CWD:     t.TempDir(),
		Sandbox: permissions.CodexSandboxDangerFullAccess,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	events := collectEvents(t, run)
	if len(events) != 1 || events[0].Type != agentport.EventError || events[0].Message == nil {
		t.Fatalf("unexpected spawn failure events: %#v", events)
	}
	if !strings.Contains(*events[0].Message, "failed to spawn codex") || events[0].TerminationReason != agentport.TerminationFailed {
		t.Fatalf("unexpected spawn failure event: %#v", events[0])
	}
}

func writeFakeCodex(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "codex")
	content := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then printf '%s\\n' 'codex 0.0.0'; exit 0; fi\n" + body
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	return path
}

func writeRawExecutable(t *testing.T, name string, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatalf("write executable: %v", err)
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
