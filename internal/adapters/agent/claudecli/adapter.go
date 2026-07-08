package claudecli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	agentpreflight "github.com/zarazhangrui/lark-coding-agent-bridge/internal/adapters/agent/preflight"
	agentport "github.com/zarazhangrui/lark-coding-agent-bridge/internal/ports/agent"
	promptport "github.com/zarazhangrui/lark-coding-agent-bridge/internal/presentation/prompt"
)

const (
	defaultBinary    = "claude"
	defaultStopGrace = 5 * time.Second
	maxScannerToken  = 4 * 1024 * 1024
	maxStderrDetail  = 500
)

type Options struct {
	Binary         string
	StopGrace      time.Duration
	LarkChannelEnv map[string]string
	AdditionalEnv  map[string]string
}

type Adapter struct {
	binary    string
	stopGrace time.Duration
	env       map[string]string

	mu          sync.RWMutex
	botIdentity *agentport.AgentBotIdentity
}

func New(opts Options) *Adapter {
	binary := opts.Binary
	if binary == "" {
		binary = defaultBinary
	}
	stopGrace := opts.StopGrace
	if stopGrace <= 0 {
		stopGrace = defaultStopGrace
	}
	env := map[string]string{"LARK_CHANNEL": "1"}
	for key, value := range opts.LarkChannelEnv {
		env[key] = value
	}
	for key, value := range opts.AdditionalEnv {
		env[key] = value
	}

	return &Adapter{
		binary:    binary,
		stopGrace: stopGrace,
		env:       env,
	}
}

func (a *Adapter) ID() string {
	return "claude"
}

func (a *Adapter) DisplayName() string {
	return "Claude Code"
}

func (a *Adapter) IsAvailable(ctx context.Context) (bool, error) {
	availability, err := a.CheckAvailability(ctx)
	return availability.OK, err
}

func (a *Adapter) CheckAvailability(ctx context.Context) (agentport.AgentAvailability, error) {
	return agentpreflight.CheckAvailability(ctx, agentpreflight.CheckInput{
		AgentID:   "claude",
		AgentName: "Claude Code",
		Command:   a.binary,
	})
}

func (a *Adapter) PrepareRun(context.Context, agentport.AgentRunOptions) error {
	return nil
}

func (a *Adapter) SetBotIdentity(identity agentport.AgentBotIdentity) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.botIdentity = &agentport.AgentBotIdentity{
		OpenID: identity.OpenID,
		Name:   identity.Name,
	}
}

func (a *Adapter) MergeEnv(values map[string]string) {
	if len(values) == 0 {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.env == nil {
		a.env = map[string]string{}
	}
	for key, value := range values {
		if key == "" {
			continue
		}
		a.env[key] = value
	}
}

func (a *Adapter) Run(ctx context.Context, opts agentport.AgentRunOptions) (agentport.AgentRun, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if opts.CWD == "" {
		return nil, errors.New("cwd is required for ClaudeAdapter.run")
	}

	args, err := BuildArgs(BuildArgsInput{
		Prompt:         opts.Prompt,
		PermissionMode: opts.PermissionMode,
		SystemPrompt:   promptport.BuildBridgeSystemPrompt(a.promptIdentity()),
		SessionID:      opts.SessionID,
		Model:          opts.Model,
	})
	if err != nil {
		return nil, err
	}

	cmd := exec.Command(a.binary, args...)
	cmd.Dir = opts.CWD
	cmd.Env = mergeEnv(os.Environ(), a.envSnapshot())

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return newFailedRun(opts.RunID, fmt.Sprintf("failed to spawn claude: %s", err.Error())), nil
	}

	stopGrace := a.stopGrace
	if opts.StopGraceMs > 0 {
		stopGrace = time.Duration(opts.StopGraceMs) * time.Millisecond
	}
	run := &processRun{
		runID:      opts.RunID,
		cmd:        cmd,
		events:     make(chan agentport.AgentEvent, 32),
		done:       make(chan struct{}),
		stderrDone: make(chan struct{}),
		stopGrace:  stopGrace,
	}

	go run.captureStderr(stderr)
	go run.stream(stdout)

	return run, nil
}

type failedRun struct {
	runID  string
	events chan agentport.AgentEvent
	done   chan struct{}
}

func newFailedRun(runID string, message string) *failedRun {
	run := &failedRun{
		runID:  runID,
		events: make(chan agentport.AgentEvent, 1),
		done:   make(chan struct{}),
	}
	run.events <- terminalError(message)
	close(run.events)
	close(run.done)
	return run
}

func (r *failedRun) RunID() string {
	return r.runID
}

func (r *failedRun) Events() <-chan agentport.AgentEvent {
	return r.events
}

func (r *failedRun) Stop(context.Context) error {
	return nil
}

func (r *failedRun) WaitForExit(context.Context) (bool, error) {
	return true, nil
}

func (a *Adapter) promptIdentity() *promptport.AgentBotIdentity {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.botIdentity == nil || a.botIdentity.OpenID == "" {
		return nil
	}
	return &promptport.AgentBotIdentity{
		OpenID: a.botIdentity.OpenID,
		Name:   a.botIdentity.Name,
	}
}

func (a *Adapter) envSnapshot() map[string]string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	env := make(map[string]string, len(a.env))
	for key, value := range a.env {
		env[key] = value
	}
	return env
}

type processRun struct {
	runID      string
	cmd        *exec.Cmd
	events     chan agentport.AgentEvent
	done       chan struct{}
	stderrDone chan struct{}
	stopGrace  time.Duration

	mu           sync.Mutex
	resultErr    error
	stopReason   ClaudeFinishReason
	runtimeError error
	stderr       bytes.Buffer
}

func (r *processRun) RunID() string {
	return r.runID
}

func (r *processRun) Events() <-chan agentport.AgentEvent {
	return r.events
}

func (r *processRun) Stop(ctx context.Context) error {
	r.setStopReason(ClaudeFinishInterrupted)
	if r.cmd.Process == nil {
		return nil
	}
	select {
	case <-r.done:
		return nil
	default:
	}

	if err := terminateProcess(r.cmd.Process); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}

	timer := time.NewTimer(r.stopGrace)
	defer timer.Stop()
	select {
	case <-r.done:
		return nil
	case <-timer.C:
		if err := killProcess(r.cmd.Process); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return err
		}
		select {
		case <-r.done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *processRun) WaitForExit(ctx context.Context) (bool, error) {
	select {
	case <-r.done:
		r.mu.Lock()
		defer r.mu.Unlock()
		return true, r.resultErr
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

func (r *processRun) captureStderr(stderr io.Reader) {
	defer close(r.stderrDone)

	scanner := bufio.NewScanner(stderr)
	scanner.Buffer(make([]byte, 0, 64*1024), maxScannerToken)
	for scanner.Scan() {
		line := scanner.Text()
		r.appendStderr(line)
		if isWindowsCommandNotFoundLine(line) {
			r.setRuntimeError(fmt.Errorf("failed to spawn claude: %s", strings.TrimSpace(line)))
			if r.cmd.Process != nil {
				_ = r.cmd.Process.Kill()
			}
		}
	}
	if err := scanner.Err(); err != nil {
		r.setRuntimeError(fmt.Errorf("claude stderr read error: %w", err))
	}
}

func (r *processRun) stream(stdout io.Reader) {
	defer close(r.events)
	defer close(r.done)

	translator := NewStreamTranslator()
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), maxScannerToken)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		events, err := translator.TranslateLine([]byte(line))
		if err != nil {
			continue
		}
		r.emit(events)
	}
	if err := scanner.Err(); err != nil {
		r.setRuntimeError(fmt.Errorf("claude stdout read error: %w", err))
	}

	exitCode := r.waitProcess()
	<-r.stderrDone
	if reason := r.getStopReason(); reason != "" {
		r.emit(translator.Finish(reason))
		return
	}
	if runtimeErr := r.getRuntimeError(); runtimeErr != nil && exitCode == -1 {
		r.emit([]agentport.AgentEvent{terminalError(fmt.Sprintf("claude runtime error: %s", runtimeErr.Error()))})
		return
	}
	if exitCode != 0 {
		detail := r.stderrDetail()
		if detail != "" {
			detail = ": " + detail
		}
		r.emit([]agentport.AgentEvent{terminalError(fmt.Sprintf("claude exited with code %d%s", exitCode, detail))})
		return
	}
	if runtimeErr := r.getRuntimeError(); runtimeErr != nil {
		r.emit([]agentport.AgentEvent{terminalError(fmt.Sprintf("claude runtime error: %s", runtimeErr.Error()))})
		return
	}
}

func (r *processRun) waitProcess() int {
	err := r.cmd.Wait()
	exitCode := 0
	if r.cmd.ProcessState != nil {
		exitCode = r.cmd.ProcessState.ExitCode()
	} else if err != nil {
		exitCode = -1
	}

	var resultErr error
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			resultErr = err
		}
	}
	r.mu.Lock()
	r.resultErr = resultErr
	r.mu.Unlock()
	return exitCode
}

func (r *processRun) emit(events []agentport.AgentEvent) {
	for _, event := range events {
		r.events <- event
	}
}

func (r *processRun) setStopReason(reason ClaudeFinishReason) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopReason == "" {
		r.stopReason = reason
	}
}

func (r *processRun) getStopReason() ClaudeFinishReason {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stopReason
}

func (r *processRun) setRuntimeError(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.runtimeError == nil {
		r.runtimeError = err
	}
}

func (r *processRun) getRuntimeError() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.runtimeError
}

func (r *processRun) appendStderr(line string) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	remaining := maxStderrDetail - r.stderr.Len()
	if remaining <= 0 {
		return
	}
	if len(trimmed) > remaining {
		trimmed = trimmed[:remaining]
	}
	if r.stderr.Len() > 0 {
		_ = r.stderr.WriteByte('\n')
	}
	_, _ = r.stderr.WriteString(trimmed)
}

func (r *processRun) stderrDetail() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stderr.String()
}

func terminalError(message string) agentport.AgentEvent {
	return agentport.AgentEvent{
		Type:              agentport.EventError,
		Message:           stringPtr(message),
		TerminationReason: agentport.TerminationFailed,
	}
}

func mergeEnv(base []string, overrides map[string]string) []string {
	if len(overrides) == 0 {
		return base
	}
	merged := make(map[string]string, len(base)+len(overrides))
	for _, entry := range base {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			merged[key] = value
		}
	}
	for key, value := range overrides {
		merged[key] = value
	}
	out := make([]string, 0, len(merged))
	for key, value := range merged {
		out = append(out, key+"="+value)
	}
	return out
}

func isWindowsCommandNotFoundLine(line string) bool {
	return runtime.GOOS == "windows" &&
		strings.Contains(strings.ToLower(line), "is not recognized as an internal or external command")
}
