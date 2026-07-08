package runexecutor

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/permissions"
	agentport "github.com/zarazhangrui/lark-coding-agent-bridge/internal/ports/agent"
)

func TestSubmitStartsRunFanoutAndCleansUp(t *testing.T) {
	run := newFakeRun("run-1")
	close(run.exited)
	agent := &fakeAgent{runs: []*fakeRun{run}}
	active := NewActiveRuns()
	pool := NewProcessPool(func() int { return 1 })
	executor := New(testOptions(agent, pool, active))

	execution, err := executor.Submit(context.Background(), testInput("scope-1"))
	if err != nil {
		t.Fatalf("Submit returned error: %v", err)
	}
	if execution.RunID != "run-1" || execution.ScopeID != "scope-1" {
		t.Fatalf("unexpected execution identity: %#v", execution)
	}
	if active.Get("scope-1") == nil {
		t.Fatalf("active run was not registered")
	}
	if got := pool.Snapshot().Active; got != 1 {
		t.Fatalf("pool active mismatch: %d", got)
	}
	if agent.prepareCalls != 1 || agent.runCalls != 1 {
		t.Fatalf("prepare/run calls mismatch: prepare=%d run=%d", agent.prepareCalls, agent.runCalls)
	}
	if agent.runOpts.Prompt != "prompt" || agent.runOpts.CWD != "/repo" || agent.runOpts.Sandbox != permissions.CodexSandboxDangerFullAccess {
		t.Fatalf("run options were not mapped from policy: %#v", agent.runOpts)
	}

	run.events <- textEvent("hello")
	run.events <- doneEvent()
	events := collectSubscription(t, execution.Subscribe(context.Background()), 2)
	if len(events) != 2 || events[0].Type != agentport.EventText || events[1].Type != agentport.EventDone {
		t.Fatalf("unexpected events: %#v", events)
	}
	waitUntil(t, func() bool {
		return active.Get("scope-1") == nil && pool.Snapshot().Active == 0
	})

	late := collectSubscription(t, execution.Subscribe(context.Background()), 2)
	if len(late) != 2 || late[0].Type != agentport.EventText || late[1].Type != agentport.EventDone {
		t.Fatalf("late subscriber did not receive buffered events: %#v", late)
	}
}

func TestSubmitLogsRunLifecycleEvents(t *testing.T) {
	run := newFakeRun("run-1")
	close(run.exited)
	agent := &fakeAgent{runs: []*fakeRun{run}}
	logger := &recordingLogger{}
	opts := testOptions(agent, NewProcessPool(func() int { return 1 }), NewActiveRuns())
	opts.Logger = logger
	executor := New(opts)
	input := testInput("scope-1")
	input.Observability = Observability{
		Profile: "codex",
		Source:  "im",
		Stage:   "submit",
	}

	execution, err := executor.Submit(context.Background(), input)
	if err != nil {
		t.Fatalf("Submit returned error: %v", err)
	}
	run.events <- doneEvent()
	_ = collectSubscription(t, execution.Subscribe(context.Background()), 1)

	entries := logger.entriesSnapshot()
	if len(entries) != 2 {
		t.Fatalf("logger entries = %#v, want run.started and run.completed", entries)
	}
	if entries[0].msg != "run.started" || entries[1].msg != "run.completed" {
		t.Fatalf("logger messages = %#v", entries)
	}
	if entries[0].fields["profile"] != "codex" || entries[0].fields["source"] != "im" || entries[0].fields["agent"] != "fake" {
		t.Fatalf("unexpected run.started fields: %#v", entries[0].fields)
	}
	if entries[1].fields["result"] != string(agentport.TerminationNormal) {
		t.Fatalf("unexpected run.completed fields: %#v", entries[1].fields)
	}
}

func TestTerminalErrorRunIsSubmittedAndCleansUp(t *testing.T) {
	run := newFakeRun("run-1")
	close(run.exited)
	agent := &fakeAgent{runs: []*fakeRun{run}}
	active := NewActiveRuns()
	pool := NewProcessPool(func() int { return 1 })
	logger := &recordingLogger{}
	opts := testOptions(agent, pool, active)
	opts.Logger = logger
	executor := New(opts)

	execution, err := executor.Submit(context.Background(), testInput("scope-1"))
	if err != nil {
		t.Fatalf("Submit returned error: %v", err)
	}
	run.events <- errorEvent("failed to spawn fake")

	events := collectSubscription(t, execution.Subscribe(context.Background()), 1)
	if len(events) != 1 || events[0].Type != agentport.EventError || events[0].Message == nil {
		t.Fatalf("unexpected events: %#v", events)
	}
	waitUntil(t, func() bool {
		return active.Get("scope-1") == nil && pool.Snapshot().Active == 0
	})

	entries := logger.entriesSnapshot()
	if len(entries) != 2 || entries[0].msg != "run.started" || entries[1].msg != "run.failed" {
		t.Fatalf("logger entries = %#v", entries)
	}
	if entries[1].fields["result"] != string(agentport.TerminationFailed) || entries[1].fields["error"] != "failed to spawn fake" {
		t.Fatalf("run.failed fields = %#v", entries[1].fields)
	}
}

func TestSubmitRejectsSameScopeWhileActive(t *testing.T) {
	agent := &fakeAgent{runs: []*fakeRun{newFakeRun("run-1")}}
	executor := New(testOptions(agent, NewProcessPool(func() int { return 2 }), NewActiveRuns()))

	if _, err := executor.Submit(context.Background(), testInput("scope-1")); err != nil {
		t.Fatalf("first Submit returned error: %v", err)
	}
	_, err := executor.Submit(context.Background(), testInput("scope-1"))
	var rejected *RunRejected
	if !errors.As(err, &rejected) || rejected.Code != RunRejectedAlreadyActive {
		t.Fatalf("expected run-already-active, got %#v", err)
	}
}

func TestNowaitPoolFullReleasesReservation(t *testing.T) {
	run1 := newFakeRun("run-1")
	run2 := newFakeRun("run-2")
	close(run2.exited)
	agent := &fakeAgent{runs: []*fakeRun{run1, run2}}
	active := NewActiveRuns()
	pool := NewProcessPool(func() int { return 1 })
	executor := New(testOptions(agent, pool, active))

	first, err := executor.Submit(context.Background(), testInput("scope-1"))
	if err != nil {
		t.Fatalf("first Submit returned error: %v", err)
	}
	secondInput := testInput("scope-2")
	secondInput.Nowait = true
	_, err = executor.Submit(context.Background(), secondInput)
	var rejected *RunRejected
	if !errors.As(err, &rejected) || rejected.Code != RunRejectedPoolFull {
		t.Fatalf("expected pool-full, got %#v", err)
	}

	close(run1.exited)
	run1.events <- doneEvent()
	_ = collectSubscription(t, first.Subscribe(context.Background()), 1)
	waitUntil(t, func() bool { return pool.Snapshot().Active == 0 })

	second, err := executor.Submit(context.Background(), secondInput)
	if err != nil {
		t.Fatalf("second Submit after release returned error: %v", err)
	}
	run2.events <- doneEvent()
	_ = collectSubscription(t, second.Subscribe(context.Background()), 1)
}

func TestPrepareFailureReleasesScopeAndPool(t *testing.T) {
	prepareErr := errors.New("prepare failed")
	run := newFakeRun("run-1")
	close(run.exited)
	agent := &fakeAgent{prepareErr: prepareErr, runs: []*fakeRun{run}}
	active := NewActiveRuns()
	pool := NewProcessPool(func() int { return 1 })
	executor := New(testOptions(agent, pool, active))

	_, err := executor.Submit(context.Background(), testInput("scope-1"))
	var spawn *SpawnFailed
	if !errors.As(err, &spawn) || spawn.Code != SpawnFailedAgentPrepare {
		t.Fatalf("expected agent prepare failure, got %#v", err)
	}
	if active.Get("scope-1") != nil || pool.Snapshot().Active != 0 {
		t.Fatalf("prepare failure leaked active run or pool slot")
	}

	agent.prepareErr = nil
	execution, err := executor.Submit(context.Background(), testInput("scope-1"))
	if err != nil {
		t.Fatalf("Submit after prepare failure returned error: %v", err)
	}
	run.events <- doneEvent()
	_ = collectSubscription(t, execution.Subscribe(context.Background()), 1)
}

func TestStopInterruptsRunAndCleansUp(t *testing.T) {
	run := newFakeRun("run-1")
	agent := &fakeAgent{runs: []*fakeRun{run}}
	active := NewActiveRuns()
	pool := NewProcessPool(func() int { return 1 })
	executor := New(testOptions(agent, pool, active))

	execution, err := executor.Submit(context.Background(), testInput("scope-1"))
	if err != nil {
		t.Fatalf("Submit returned error: %v", err)
	}
	if err := execution.Stop(context.Background()); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
	if run.stopCallCount() != 1 {
		t.Fatalf("stop call mismatch: %d", run.stopCallCount())
	}
	if active.Get("scope-1") != nil || pool.Snapshot().Active != 0 {
		t.Fatalf("stop leaked active run or pool slot")
	}
}

func TestFanoutDrainsWithoutSubscribers(t *testing.T) {
	run := newFakeRun("run-1")
	close(run.exited)
	agent := &fakeAgent{runs: []*fakeRun{run}}
	active := NewActiveRuns()
	pool := NewProcessPool(func() int { return 1 })
	executor := New(testOptions(agent, pool, active))

	execution, err := executor.Submit(context.Background(), testInput("scope-1"))
	if err != nil {
		t.Fatalf("Submit returned error: %v", err)
	}

	sent := make(chan struct{})
	go func() {
		for i := 0; i < 80; i++ {
			run.events <- textEvent("event")
		}
		run.events <- doneEvent()
		close(sent)
	}()

	select {
	case <-sent:
	case <-time.After(time.Second):
		t.Fatalf("executor did not drain agent events without subscribers")
	}
	waitUntil(t, func() bool {
		return active.Get("scope-1") == nil && pool.Snapshot().Active == 0
	})

	events := collectSubscription(t, execution.Subscribe(context.Background()), 81)
	if len(events) != 81 || events[80].Type != agentport.EventDone {
		t.Fatalf("unexpected drained events: len=%d last=%#v", len(events), events[len(events)-1])
	}
}

func TestStopIsIdempotentUnderConcurrency(t *testing.T) {
	run := newFakeRun("run-1")
	agent := &fakeAgent{runs: []*fakeRun{run}}
	executor := New(testOptions(agent, NewProcessPool(func() int { return 1 }), NewActiveRuns()))

	execution, err := executor.Submit(context.Background(), testInput("scope-1"))
	if err != nil {
		t.Fatalf("Submit returned error: %v", err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 100)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- execution.Stop(context.Background())
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Stop returned error: %v", err)
		}
	}
	if got := run.stopCallCount(); got != 1 {
		t.Fatalf("bottom Stop called %d times", got)
	}
}

func TestBlockingAcquireWaitsForPoolRelease(t *testing.T) {
	run1 := newFakeRun("run-1")
	run2 := newFakeRun("run-2")
	close(run1.exited)
	close(run2.exited)
	agent := &fakeAgent{runs: []*fakeRun{run1, run2}}
	pool := NewProcessPool(func() int { return 1 })
	executor := New(testOptions(agent, pool, NewActiveRuns()))

	first, err := executor.Submit(context.Background(), testInput("scope-1"))
	if err != nil {
		t.Fatalf("first Submit returned error: %v", err)
	}
	secondDone := make(chan error, 1)
	go func() {
		_, err := executor.Submit(context.Background(), testInput("scope-2"))
		secondDone <- err
	}()

	select {
	case err := <-secondDone:
		t.Fatalf("second submit completed before pool release: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	run1.events <- doneEvent()
	_ = collectSubscription(t, first.Subscribe(context.Background()), 1)
	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatalf("second submit returned error after pool release: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatalf("second submit did not resume after pool release")
	}
}

func TestPauseInterruptAndStopAllControls(t *testing.T) {
	run1 := newFakeRun("run-1")
	run2 := newFakeRun("run-2")
	agent := &fakeAgent{runs: []*fakeRun{run1, run2}}
	active := NewActiveRuns()
	executor := New(testOptions(agent, NewProcessPool(func() int { return 2 }), active))

	resume := executor.PauseNewRuns("reconnecting")
	_, err := executor.Submit(context.Background(), testInput("scope-paused"))
	var rejected *RunRejected
	if !errors.As(err, &rejected) || rejected.Code != RunRejectedReconnectInProgress {
		t.Fatalf("expected reconnect-in-progress, got %#v", err)
	}
	resume()

	first, err := executor.Submit(context.Background(), testInput("scope-1"))
	if err != nil {
		t.Fatalf("Submit scope-1 returned error: %v", err)
	}
	if !executor.Interrupt(context.Background(), "scope-1") {
		t.Fatalf("Interrupt returned false")
	}
	if run1.stopCallCount() != 1 || active.Get("scope-1") != nil {
		t.Fatalf("interrupt did not stop and unregister run")
	}
	first.cleanup(false)

	if _, err := executor.Submit(context.Background(), testInput("scope-2")); err != nil {
		t.Fatalf("Submit scope-2 returned error: %v", err)
	}
	if err := executor.StopAll(context.Background()); err != nil {
		t.Fatalf("StopAll returned error: %v", err)
	}
	if run2.stopCallCount() != 1 || len(active.Snapshot()) != 0 {
		t.Fatalf("StopAll did not stop and clear runs")
	}
}

func testOptions(agent *fakeAgent, pool *ProcessPool, active *ActiveRuns) Options {
	return Options{
		Agent:                agent,
		Pool:                 pool,
		ActiveRuns:           active,
		CreateRunID:          agent.nextRunID,
		Now:                  func() time.Time { return time.Unix(100, 0) },
		PostDoneExitGrace:    20 * time.Millisecond,
		StopAfterDoneTimeout: 20 * time.Millisecond,
	}
}

func testInput(scopeID string) SubmitRunInput {
	return SubmitRunInput{
		ScopeID: scopeID,
		Policy: RunPolicy{
			Prompt:         "prompt",
			CWDRealpath:    "/repo",
			AccessMode:     permissions.AccessFull,
			Sandbox:        permissions.CodexSandboxDangerFullAccess,
			PermissionMode: permissions.ClaudePermissionBypassPermissions,
			ExpiresAt:      time.Unix(200, 0),
		},
		SessionID: "session-1",
		ThreadID:  "thread-1",
		Model:     "model-1",
		Images:    []string{"/tmp/image.png"},
	}
}

type fakeAgent struct {
	prepareErr error
	runErr     error
	runs       []*fakeRun

	prepareCalls int
	runCalls     int
	prepareOpts  agentport.AgentRunOptions
	runOpts      agentport.AgentRunOptions
}

func (a *fakeAgent) ID() string {
	return "fake"
}

func (a *fakeAgent) DisplayName() string {
	return "Fake Agent"
}

func (a *fakeAgent) IsAvailable(context.Context) (bool, error) {
	return true, nil
}

func (a *fakeAgent) PrepareRun(_ context.Context, opts agentport.AgentRunOptions) error {
	a.prepareCalls++
	a.prepareOpts = opts
	return a.prepareErr
}

func (a *fakeAgent) Run(_ context.Context, opts agentport.AgentRunOptions) (agentport.AgentRun, error) {
	a.runCalls++
	a.runOpts = opts
	if a.runErr != nil {
		return nil, a.runErr
	}
	if len(a.runs) == 0 {
		return nil, errors.New("no fake runs left")
	}
	run := a.runs[0]
	a.runs = a.runs[1:]
	return run, nil
}

func (a *fakeAgent) nextRunID() string {
	if len(a.runs) == 0 {
		return "run"
	}
	return a.runs[0].runID
}

type fakeRun struct {
	runID     string
	events    chan agentport.AgentEvent
	exited    chan struct{}
	mu        sync.Mutex
	stopCalls int
}

type logEntry struct {
	level  string
	msg    string
	fields map[string]any
}

type recordingLogger struct {
	mu      sync.Mutex
	entries []logEntry
}

func (l *recordingLogger) Info(msg string, fields map[string]any) {
	l.append("info", msg, fields)
}

func (l *recordingLogger) Warn(msg string, fields map[string]any) {
	l.append("warn", msg, fields)
}

func (l *recordingLogger) append(level string, msg string, fields map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	copied := map[string]any{}
	for key, value := range fields {
		copied[key] = value
	}
	l.entries = append(l.entries, logEntry{level: level, msg: msg, fields: copied})
}

func (l *recordingLogger) entriesSnapshot() []logEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]logEntry, len(l.entries))
	copy(out, l.entries)
	return out
}

func newFakeRun(runID string) *fakeRun {
	return &fakeRun{
		runID:  runID,
		events: make(chan agentport.AgentEvent, 8),
		exited: make(chan struct{}),
	}
}

func (r *fakeRun) RunID() string {
	return r.runID
}

func (r *fakeRun) Events() <-chan agentport.AgentEvent {
	return r.events
}

func (r *fakeRun) Stop(context.Context) error {
	r.mu.Lock()
	r.stopCalls++
	r.mu.Unlock()
	select {
	case <-r.exited:
	default:
		close(r.exited)
	}
	return nil
}

func (r *fakeRun) WaitForExit(ctx context.Context) (bool, error) {
	select {
	case <-r.exited:
		return true, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

func (r *fakeRun) stopCallCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stopCalls
}

func collectSubscription(t *testing.T, events <-chan agentport.AgentEvent, count int) []agentport.AgentEvent {
	t.Helper()
	out := make([]agentport.AgentEvent, 0, count)
	timeout := time.After(time.Second)
	for len(out) < count {
		select {
		case event, ok := <-events:
			if !ok {
				return out
			}
			out = append(out, event)
		case <-timeout:
			t.Fatalf("timed out waiting for events; got %#v", out)
		}
	}
	return out
}

func waitUntil(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.After(time.Second)
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		if predicate() {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("condition did not become true")
		case <-tick.C:
		}
	}
}

func textEvent(text string) agentport.AgentEvent {
	return agentport.AgentEvent{
		Type:  agentport.EventText,
		Delta: &text,
	}
}

func doneEvent() agentport.AgentEvent {
	return agentport.AgentEvent{
		Type:              agentport.EventDone,
		TerminationReason: agentport.TerminationNormal,
	}
}

func errorEvent(message string) agentport.AgentEvent {
	return agentport.AgentEvent{
		Type:              agentport.EventError,
		Message:           &message,
		TerminationReason: agentport.TerminationFailed,
	}
}
