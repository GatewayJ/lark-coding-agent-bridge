package runexecutor

import (
	"context"
	"sync"

	agentport "github.com/zarazhangrui/lark-coding-agent-bridge/internal/ports/agent"
)

type eventFanout struct {
	source <-chan agentport.AgentEvent
	onDone func()

	mu      sync.Mutex
	buffer  []agentport.AgentEvent
	waiters map[chan struct{}]struct{}
	done    bool
}

func newEventFanout(source <-chan agentport.AgentEvent, onDone func()) *eventFanout {
	fanout := &eventFanout{
		source:  source,
		onDone:  onDone,
		waiters: make(map[chan struct{}]struct{}),
	}
	go fanout.pump()
	return fanout
}

func (f *eventFanout) Subscribe(ctx context.Context) <-chan agentport.AgentEvent {
	out := make(chan agentport.AgentEvent, 16)
	go func() {
		defer close(out)
		index := 0
		for {
			f.mu.Lock()
			if index < len(f.buffer) {
				event := f.buffer[index]
				index++
				f.mu.Unlock()
				select {
				case out <- event:
				case <-ctx.Done():
					return
				}
				continue
			}
			if f.done {
				f.mu.Unlock()
				return
			}
			waiter := make(chan struct{})
			f.waiters[waiter] = struct{}{}
			f.mu.Unlock()

			select {
			case <-waiter:
			case <-ctx.Done():
				f.mu.Lock()
				delete(f.waiters, waiter)
				f.mu.Unlock()
				return
			}
		}
	}()
	return out
}

func (f *eventFanout) pump() {
	defer func() {
		if f.onDone != nil {
			f.onDone()
		}
		f.mu.Lock()
		f.done = true
		f.wakeAllLocked()
		f.mu.Unlock()
	}()

	for event := range f.source {
		f.mu.Lock()
		f.buffer = append(f.buffer, event)
		f.wakeAllLocked()
		f.mu.Unlock()
		if isTerminalEvent(event) {
			return
		}
	}
}

func (f *eventFanout) wakeAllLocked() {
	for waiter := range f.waiters {
		close(waiter)
		delete(f.waiters, waiter)
	}
}

func isTerminalEvent(event agentport.AgentEvent) bool {
	return event.Type == agentport.EventDone || event.Type == agentport.EventError
}
