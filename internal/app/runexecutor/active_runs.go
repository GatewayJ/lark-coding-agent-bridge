package runexecutor

import (
	"context"
	"sync"

	agentport "github.com/zarazhangrui/lark-coding-agent-bridge/internal/ports/agent"
)

type RunHandle struct {
	Run         agentport.AgentRun
	mu          sync.Mutex
	interrupted bool
}

func (h *RunHandle) MarkInterrupted() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.interrupted = true
}

func (h *RunHandle) Interrupted() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.interrupted
}

type ActiveRuns struct {
	mu           sync.Mutex
	handles      map[string]*RunHandle
	reservations map[string]struct{}
	pauseDepth   int
	pauseReason  string
}

func NewActiveRuns() *ActiveRuns {
	return &ActiveRuns{
		handles:      make(map[string]*RunHandle),
		reservations: make(map[string]struct{}),
	}
}

func (a *ActiveRuns) Reserve(scopeID string) func() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.handles[scopeID]; ok {
		return nil
	}
	if _, ok := a.reservations[scopeID]; ok {
		return nil
	}
	a.reservations[scopeID] = struct{}{}
	var once sync.Once
	return func() {
		once.Do(func() {
			a.mu.Lock()
			defer a.mu.Unlock()
			delete(a.reservations, scopeID)
		})
	}
}

func (a *ActiveRuns) Register(scopeID string, run agentport.AgentRun) (*RunHandle, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.handles[scopeID]; ok {
		return nil, reject(RunRejectedAlreadyActive, "another run is already active for this scope")
	}
	delete(a.reservations, scopeID)
	handle := &RunHandle{Run: run}
	a.handles[scopeID] = handle
	return handle, nil
}

func (a *ActiveRuns) Unregister(scopeID string, run agentport.AgentRun) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if existing := a.handles[scopeID]; existing != nil && existing.Run == run {
		delete(a.handles, scopeID)
	}
}

func (a *ActiveRuns) PauseNewRuns(reason string) func() {
	a.mu.Lock()
	a.pauseDepth++
	a.pauseReason = reason
	a.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			a.mu.Lock()
			defer a.mu.Unlock()
			if a.pauseDepth > 0 {
				a.pauseDepth--
			}
			if a.pauseDepth == 0 {
				a.pauseReason = ""
			}
		})
	}
}

func (a *ActiveRuns) NewRunsPaused() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.pauseDepth > 0
}

func (a *ActiveRuns) NewRunsPauseReason() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.pauseReason
}

func (a *ActiveRuns) Get(scopeID string) *RunHandle {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.handles[scopeID]
}

func (a *ActiveRuns) Interrupt(ctx context.Context, scopeID string) bool {
	a.mu.Lock()
	handle := a.handles[scopeID]
	if handle == nil {
		a.mu.Unlock()
		return false
	}
	delete(a.reservations, scopeID)
	delete(a.handles, scopeID)
	handle.MarkInterrupted()
	a.mu.Unlock()

	_ = handle.Run.Stop(ctx)
	return true
}

func (a *ActiveRuns) StopAll(ctx context.Context) error {
	a.mu.Lock()
	handles := make([]*RunHandle, 0, len(a.handles))
	for _, handle := range a.handles {
		handle.MarkInterrupted()
		handles = append(handles, handle)
	}
	a.handles = make(map[string]*RunHandle)
	a.reservations = make(map[string]struct{})
	a.mu.Unlock()

	var firstErr error
	for _, handle := range handles {
		if err := handle.Run.Stop(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (a *ActiveRuns) WaitForAll(ctx context.Context) error {
	a.mu.Lock()
	handles := make([]*RunHandle, 0, len(a.handles))
	for _, handle := range a.handles {
		handles = append(handles, handle)
	}
	a.mu.Unlock()

	var firstErr error
	for _, handle := range handles {
		if _, err := handle.Run.WaitForExit(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (a *ActiveRuns) Snapshot() []*RunHandle {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]*RunHandle, 0, len(a.handles))
	for _, handle := range a.handles {
		out = append(out, handle)
	}
	return out
}

func (a *ActiveRuns) Scopes() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]string, 0, len(a.handles))
	for scopeID := range a.handles {
		out = append(out, scopeID)
	}
	return out
}
