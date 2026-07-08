package intake

import (
	"context"
	"errors"
	"sync"
	"time"
)

var ErrQueueClosed = errors.New("intake queue is closed")

type Batch struct {
	Scope  Scope
	Events []NormalizedEvent
}

type BatchHandler func(context.Context, Batch) error

type QueueOptions struct {
	QuietPeriod  time.Duration
	FlushTimeout time.Duration
	Handler      BatchHandler
}

type Queue struct {
	mu           sync.Mutex
	quietPeriod  time.Duration
	flushTimeout time.Duration
	handler      BatchHandler
	pending      map[string]*pendingBatch
	workers      map[string]chan workItem
	blocked      map[string]bool
	done         chan struct{}
	closed       bool
}

type pendingBatch struct {
	scope  Scope
	events []NormalizedEvent
	timer  *time.Timer
}

type workItem struct {
	batch Batch
	done  chan error
}

func NewQueue(options QueueOptions) *Queue {
	return &Queue{
		quietPeriod:  options.QuietPeriod,
		flushTimeout: options.FlushTimeout,
		handler:      options.Handler,
		pending:      make(map[string]*pendingBatch),
		workers:      make(map[string]chan workItem),
		blocked:      make(map[string]bool),
		done:         make(chan struct{}),
	}
}

func (q *Queue) Push(event NormalizedEvent) (int, error) {
	if event.Scope.Key == "" {
		return 0, errors.New("scope key is required")
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return 0, ErrQueueClosed
	}
	entry := q.pending[event.Scope.Key]
	if entry == nil {
		entry = &pendingBatch{scope: event.Scope}
		q.pending[event.Scope.Key] = entry
	}
	if entry.timer != nil {
		entry.timer.Stop()
	}
	entry.scope = event.Scope
	entry.events = append(entry.events, event)
	if q.quietPeriod > 0 && !q.blocked[event.Scope.Key] {
		scopeKey := event.Scope.Key
		entry.timer = time.AfterFunc(q.quietPeriod, func() {
			_ = q.Flush(context.Background(), scopeKey)
		})
	}
	return len(entry.events), nil
}

func (q *Queue) Block(scopeKey string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	q.blocked[scopeKey] = true
	if entry := q.pending[scopeKey]; entry != nil && entry.timer != nil {
		entry.timer.Stop()
		entry.timer = nil
	}
}

func (q *Queue) Unblock(scopeKey string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	delete(q.blocked, scopeKey)
	entry := q.pending[scopeKey]
	if entry == nil || len(entry.events) == 0 || q.quietPeriod <= 0 {
		return
	}
	if entry.timer != nil {
		entry.timer.Stop()
	}
	entry.timer = time.AfterFunc(q.quietPeriod, func() {
		_ = q.Flush(context.Background(), scopeKey)
	})
}

func (q *Queue) Flush(ctx context.Context, scopeKey string) error {
	batch, ok, err := q.drain(scopeKey)
	if err != nil || !ok {
		return err
	}
	return q.submit(ctx, batch)
}

func (q *Queue) FlushAll(ctx context.Context) error {
	for _, scopeKey := range q.ScopeKeys() {
		if err := q.Flush(ctx, scopeKey); err != nil {
			return err
		}
	}
	return nil
}

func (q *Queue) Cancel(scopeKey string) []NormalizedEvent {
	q.mu.Lock()
	defer q.mu.Unlock()
	entry := q.pending[scopeKey]
	if entry == nil {
		return nil
	}
	if entry.timer != nil {
		entry.timer.Stop()
	}
	delete(q.pending, scopeKey)
	return append([]NormalizedEvent(nil), entry.events...)
}

func (q *Queue) CancelAll() {
	q.mu.Lock()
	defer q.mu.Unlock()
	for key, entry := range q.pending {
		if entry.timer != nil {
			entry.timer.Stop()
		}
		delete(q.pending, key)
	}
	clear(q.blocked)
}

func (q *Queue) ScopeKeys() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	keys := make([]string, 0, len(q.pending))
	for key := range q.pending {
		keys = append(keys, key)
	}
	return keys
}

func (q *Queue) Close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	for key, entry := range q.pending {
		if entry.timer != nil {
			entry.timer.Stop()
		}
		delete(q.pending, key)
	}
	clear(q.blocked)
	q.closed = true
	close(q.done)
	for key := range q.workers {
		delete(q.workers, key)
	}
}

func (q *Queue) drain(scopeKey string) (Batch, bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return Batch{}, false, ErrQueueClosed
	}
	entry := q.pending[scopeKey]
	if entry == nil || len(entry.events) == 0 {
		return Batch{}, false, nil
	}
	if entry.timer != nil {
		entry.timer.Stop()
	}
	delete(q.pending, scopeKey)
	return Batch{
		Scope:  entry.scope,
		Events: append([]NormalizedEvent(nil), entry.events...),
	}, true, nil
}

func (q *Queue) submit(ctx context.Context, batch Batch) error {
	if q.handler == nil {
		return nil
	}
	worker, err := q.worker(batch.Scope.Key)
	if err != nil {
		return err
	}
	done := make(chan error, 1)
	select {
	case worker <- workItem{batch: batch, done: done}:
	case <-ctx.Done():
		return ctx.Err()
	case <-q.done:
		return ErrQueueClosed
	}
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-q.done:
		return ErrQueueClosed
	}
}

func (q *Queue) worker(scopeKey string) (chan workItem, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return nil, ErrQueueClosed
	}
	worker := q.workers[scopeKey]
	if worker != nil {
		return worker, nil
	}
	worker = make(chan workItem, 16)
	q.workers[scopeKey] = worker
	go q.runWorker(worker)
	return worker, nil
}

func (q *Queue) runWorker(worker <-chan workItem) {
	for {
		var item workItem
		select {
		case item = <-worker:
		case <-q.done:
			return
		}
		ctx := context.Background()
		cancel := func() {}
		if q.flushTimeout > 0 {
			ctx, cancel = context.WithTimeout(ctx, q.flushTimeout)
		}
		err := q.handler(ctx, item.batch)
		cancel()
		item.done <- err
	}
}
