//go:build !windows

package codexcli

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/permissions"
	agentport "github.com/zarazhangrui/lark-coding-agent-bridge/internal/ports/agent"
)

func TestAdapterStopTerminatesChildProcessGroup(t *testing.T) {
	cwd := t.TempDir()
	binary := writeFakeCodex(t, `
sleep 30 >/dev/null 2>&1 &
printf '%s' "$!" > child_pid.txt
printf '%s\n' '{"type":"thread.started","thread_id":"thread-stop"}'
wait
`)

	adapter := New(Options{
		Binary:          binary,
		ProfileStateDir: t.TempDir(),
		StopGrace:       50 * time.Millisecond,
	})
	run, err := adapter.Run(context.Background(), agentport.AgentRunOptions{
		RunID:   "run-stop-group",
		Prompt:  "stop process group",
		CWD:     cwd,
		Sandbox: permissions.CodexSandboxDangerFullAccess,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	first := <-run.Events()
	if first.Type != agentport.EventSystem {
		t.Fatalf("unexpected first event: %#v", first)
	}
	childPID := readPID(t, filepath.Join(cwd, "child_pid.txt"))
	if !processExists(childPID) {
		t.Fatalf("child process %d was not running before Stop", childPID)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := run.Stop(ctx); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
	for range run.Events() {
	}

	deadline := time.Now().Add(2 * time.Second)
	for processExists(childPID) && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
	}
	if processExists(childPID) {
		_ = syscall.Kill(childPID, syscall.SIGKILL)
		t.Fatalf("child process %d survived Stop; process group was not terminated", childPID)
	}
}

func readPID(t *testing.T, path string) int {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read pid file: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("parse pid %q: %v", raw, err)
	}
	return pid
}

func processExists(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
