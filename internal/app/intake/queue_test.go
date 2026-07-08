package intake

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestQueueFlushesSameScopeInOrder(t *testing.T) {
	var mu sync.Mutex
	var seen [][]string
	queue := NewQueue(QueueOptions{
		Handler: func(ctx context.Context, batch Batch) error {
			mu.Lock()
			defer mu.Unlock()
			ids := make([]string, 0, len(batch.Events))
			for _, event := range batch.Events {
				ids = append(ids, event.Message.MessageID)
			}
			seen = append(seen, ids)
			return nil
		},
	})
	defer queue.Close()

	first := messageEvent("oc_group:omt_topic", "om_1")
	second := messageEvent("oc_group:omt_topic", "om_2")
	if _, err := queue.Push(first); err != nil {
		t.Fatalf("push first: %v", err)
	}
	if _, err := queue.Push(second); err != nil {
		t.Fatalf("push second: %v", err)
	}
	if err := queue.Flush(context.Background(), "oc_group:omt_topic"); err != nil {
		t.Fatalf("flush first batch: %v", err)
	}
	if _, err := queue.Push(messageEvent("oc_group:omt_topic", "om_3")); err != nil {
		t.Fatalf("push third: %v", err)
	}
	if err := queue.Flush(context.Background(), "oc_group:omt_topic"); err != nil {
		t.Fatalf("flush second batch: %v", err)
	}

	if len(seen) != 2 {
		t.Fatalf("seen = %#v, want two batches", seen)
	}
	if got := seen[0]; len(got) != 2 || got[0] != "om_1" || got[1] != "om_2" {
		t.Fatalf("first batch = %#v", got)
	}
	if got := seen[1]; len(got) != 1 || got[0] != "om_3" {
		t.Fatalf("second batch = %#v", got)
	}
}

func TestQueueAllowsDifferentScopesToRunConcurrently(t *testing.T) {
	started := make(chan string, 2)
	release := make(chan struct{})
	queue := NewQueue(QueueOptions{
		Handler: func(ctx context.Context, batch Batch) error {
			started <- batch.Scope.Key
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	})
	defer queue.Close()

	if _, err := queue.Push(messageEvent("scope-a", "om_a")); err != nil {
		t.Fatalf("push scope-a: %v", err)
	}
	if _, err := queue.Push(messageEvent("scope-b", "om_b")); err != nil {
		t.Fatalf("push scope-b: %v", err)
	}
	errs := make(chan error, 2)
	go func() { errs <- queue.Flush(context.Background(), "scope-a") }()
	go func() { errs <- queue.Flush(context.Background(), "scope-b") }()

	got := map[string]bool{}
	deadline := time.After(500 * time.Millisecond)
	for len(got) < 2 {
		select {
		case scope := <-started:
			got[scope] = true
		case <-deadline:
			t.Fatalf("handlers did not start concurrently, got %#v", got)
		}
	}
	close(release)
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("flush error: %v", err)
		}
	}
}

func TestQueueCancelReturnsPendingEvents(t *testing.T) {
	queue := NewQueue(QueueOptions{})
	defer queue.Close()
	if _, err := queue.Push(messageEvent("scope-a", "om_a")); err != nil {
		t.Fatalf("push: %v", err)
	}
	cancelled := queue.Cancel("scope-a")
	if len(cancelled) != 1 || cancelled[0].Message.MessageID != "om_a" {
		t.Fatalf("cancelled = %#v", cancelled)
	}
	if err := queue.Flush(context.Background(), "scope-a"); err != nil {
		t.Fatalf("flush empty cancelled scope: %v", err)
	}
}

func TestQueueBlockPausesTimerUntilUnblock(t *testing.T) {
	flushed := make(chan Batch, 1)
	queue := NewQueue(QueueOptions{
		QuietPeriod: 20 * time.Millisecond,
		Handler: func(ctx context.Context, batch Batch) error {
			flushed <- batch
			return nil
		},
	})
	defer queue.Close()

	queue.Block("scope-a")
	if _, err := queue.Push(messageEvent("scope-a", "om_a")); err != nil {
		t.Fatalf("push: %v", err)
	}
	select {
	case batch := <-flushed:
		t.Fatalf("flushed while blocked: %#v", batch)
	case <-time.After(60 * time.Millisecond):
	}

	queue.Unblock("scope-a")
	select {
	case batch := <-flushed:
		if len(batch.Events) != 1 || batch.Events[0].Message.MessageID != "om_a" {
			t.Fatalf("batch = %#v", batch)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("did not flush after unblock")
	}
}

func TestQueueCloseUnblocksInFlightFlush(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	queue := NewQueue(QueueOptions{
		Handler: func(ctx context.Context, batch Batch) error {
			close(started)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	})

	if _, err := queue.Push(messageEvent("scope-a", "om_a")); err != nil {
		t.Fatalf("push: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		done <- queue.Flush(context.Background(), "scope-a")
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatalf("handler did not start")
	}
	queue.Close()
	select {
	case err := <-done:
		if !errors.Is(err, ErrQueueClosed) {
			t.Fatalf("Flush error = %v, want ErrQueueClosed", err)
		}
	case <-time.After(time.Second):
		t.Fatalf("Flush did not unblock after Close")
	}
	close(release)
}

func messageEvent(scopeKey, messageID string) NormalizedEvent {
	return NormalizedEvent{
		Kind: EventMessage,
		Scope: Scope{
			Key:    scopeKey,
			Source: ScopeSourceIM,
		},
		Message: &MessageInput{
			MessageID: messageID,
			ChatID:    "oc_group",
			ThreadID:  "omt_topic",
			Sender:    Actor{OpenID: "ou_user"},
		},
	}
}
