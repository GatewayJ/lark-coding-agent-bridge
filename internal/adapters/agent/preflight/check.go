package preflight

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"

	agentport "github.com/zarazhangrui/lark-coding-agent-bridge/internal/ports/agent"
)

const (
	defaultVersionTimeout = 5 * time.Second
	excerptMax            = 500
)

type CheckInput struct {
	AgentID   string
	AgentName string
	Command   string
	Args      []string
	Timeout   time.Duration
}

func CheckAvailability(ctx context.Context, input CheckInput) (agentport.AgentAvailability, error) {
	if err := ctx.Err(); err != nil {
		return agentport.AgentAvailability{}, err
	}
	if input.Command == "" {
		return unavailable(input, "agent-binary-not-found", "agent binary is required", nil), nil
	}
	path, err := exec.LookPath(input.Command)
	if err != nil {
		availability := unavailable(input, "agent-binary-not-found", err.Error(), nil)
		availability.Diagnostic.BinaryPath = input.Command
		return availability, nil
	}

	args := input.Args
	if len(args) == 0 {
		args = []string{"--version"}
	}
	timeout := input.Timeout
	if timeout <= 0 {
		timeout = defaultVersionTimeout
	}
	checkCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd := exec.CommandContext(checkCtx, path, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	diag := &agentport.AvailabilityDiagnostic{
		Command:       input.Command,
		BinaryPath:    path,
		Args:          append([]string(nil), args...),
		TimeoutMs:     int(timeout / time.Millisecond),
		StdoutExcerpt: excerpt(stdout.String()),
		StderrExcerpt: excerpt(stderr.String()),
	}
	if errors.Is(checkCtx.Err(), context.DeadlineExceeded) {
		diag.Code = "agent-version-check-timeout"
		diag.Message = "version check timed out"
		return agentport.AgentAvailability{OK: false, Error: diag.Message, Diagnostic: diag}, nil
	}
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code := exitErr.ExitCode()
			diag.Code = "agent-version-check-nonzero-exit"
			diag.ExitCode = &code
			diag.Message = err.Error()
			return agentport.AgentAvailability{OK: false, Error: diag.Message, Diagnostic: diag}, nil
		}
		diag.Code = "agent-version-check-spawn-failed"
		diag.Message = err.Error()
		return agentport.AgentAvailability{OK: false, Error: diag.Message, Diagnostic: diag}, nil
	}
	version := firstLine(stdout.String())
	if version == "" {
		version = firstLine(stderr.String())
	}
	if version == "" {
		diag.Code = "agent-version-check-empty-output"
		diag.Message = "version check returned empty output"
		return agentport.AgentAvailability{OK: false, Error: diag.Message, Diagnostic: diag}, nil
	}
	return agentport.AgentAvailability{
		OK:      true,
		Version: version,
		Diagnostic: &agentport.AvailabilityDiagnostic{
			Command:    input.Command,
			BinaryPath: path,
			Args:       append([]string(nil), args...),
		},
	}, nil
}

func unavailable(input CheckInput, code string, message string, args []string) agentport.AgentAvailability {
	return agentport.AgentAvailability{
		OK:    false,
		Error: message,
		Diagnostic: &agentport.AvailabilityDiagnostic{
			Code:    code,
			Command: input.Command,
			Args:    append([]string(nil), args...),
			Message: message,
		},
	}
}

func firstLine(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	line, _, _ := strings.Cut(trimmed, "\n")
	return strings.TrimSpace(line)
}

func excerpt(value string) string {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) > excerptMax {
		return trimmed[:excerptMax]
	}
	return trimmed
}
