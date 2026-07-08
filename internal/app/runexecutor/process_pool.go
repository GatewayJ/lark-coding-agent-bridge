package runexecutor

import (
	"context"
	"sync"
)

type ProcessPool struct {
	mu      sync.Mutex
	active  int
	waiters []chan struct{}
	cap     func() int
}

type ProcessPoolSnapshot struct {
	Active  int
	Waiting int
	Cap     int
}

func NewProcessPool(cap func() int) *ProcessPool {
	if cap == nil {
		cap = func() int { return 1 }
	}
	return &ProcessPool{cap: cap}
}

func (p *ProcessPool) Acquire(ctx context.Context) (func(), error) {
	for {
		p.mu.Lock()
		if p.active < p.currentCapLocked() {
			p.active++
			p.mu.Unlock()
			return p.releaseFunc(), nil
		}
		waiter := make(chan struct{})
		p.waiters = append(p.waiters, waiter)
		p.mu.Unlock()

		select {
		case <-waiter:
		case <-ctx.Done():
			p.removeWaiter(waiter)
			return nil, ctx.Err()
		}
	}
}

func (p *ProcessPool) TryAcquire() func() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.active >= p.currentCapLocked() {
		return nil
	}
	p.active++
	return p.releaseFunc()
}

func (p *ProcessPool) Snapshot() ProcessPoolSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	return ProcessPoolSnapshot{
		Active:  p.active,
		Waiting: len(p.waiters),
		Cap:     p.currentCapLocked(),
	}
}

func (p *ProcessPool) releaseFunc() func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			p.mu.Lock()
			defer p.mu.Unlock()
			if p.active > 0 {
				p.active--
			}
			p.wakeNextLocked()
		})
	}
}

func (p *ProcessPool) removeWaiter(waiter chan struct{}) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, candidate := range p.waiters {
		if candidate == waiter {
			p.waiters = append(p.waiters[:i], p.waiters[i+1:]...)
			return
		}
	}
}

func (p *ProcessPool) wakeNextLocked() {
	if p.active >= p.currentCapLocked() || len(p.waiters) == 0 {
		return
	}
	next := p.waiters[0]
	p.waiters = p.waiters[1:]
	close(next)
}

func (p *ProcessPool) currentCapLocked() int {
	capacity := p.cap()
	if capacity < 1 {
		return 1
	}
	return capacity
}
