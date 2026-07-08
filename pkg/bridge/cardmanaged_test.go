package bridge

import (
	"context"
	"errors"
	"testing"
)

func TestManagedCardFacadeWithFakeLarkTransport(t *testing.T) {
	ctx := context.Background()
	transport := NewFakeLarkTransport(LarkBotIdentity{})
	store := NewManagedCardStore()

	result, err := SendManagedCard(ctx, store, transport, "oc_chat", map[string]any{"schema": "2.0"}, ManagedCardSendOptions{})
	if err != nil {
		t.Fatalf("SendManagedCard returned error: %v", err)
	}
	if result.MessageID == "" || result.CardID == "" {
		t.Fatalf("result = %#v", result)
	}
	if err := UpdateManagedCard(ctx, store, transport, result.MessageID, map[string]any{"updated": true}); err != nil {
		t.Fatalf("UpdateManagedCard returned error: %v", err)
	}
	entry, ok := store.Entry(result.MessageID)
	if !ok || entry.Kind != ManagedCardEntryCardID || entry.Sequence != 1 {
		t.Fatalf("entry = %#v ok=%v", entry, ok)
	}
	store.Forget(result.MessageID)
	if err := UpdateManagedCard(ctx, store, transport, result.MessageID, map[string]any{}); !errors.Is(err, ErrManagedCardMissing) {
		t.Fatalf("UpdateManagedCard missing error = %v", err)
	}
}
