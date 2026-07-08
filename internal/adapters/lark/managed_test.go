package lark

import (
	"context"
	"errors"
	"testing"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/cardmanaged"
)

func TestFakeTransportSupportsManagedCardLifecycle(t *testing.T) {
	ctx := context.Background()
	transport := NewFakeTransport(BotIdentity{})
	store := cardmanaged.NewStore()

	result, err := SendManagedCard(ctx, store, transport, "oc_chat", map[string]any{"schema": "2.0"}, cardmanaged.SendOptions{
		ReplyTo:       "om_parent",
		ReplyInThread: true,
	})
	if err != nil {
		t.Fatalf("SendManagedCard returned error: %v", err)
	}
	if result.MessageID == "" || result.CardID == "" || result.Fallback {
		t.Fatalf("result = %#v", result)
	}
	if len(transport.SentCardIDs) != 1 || transport.SentCardIDs[0].CardID != result.CardID || transport.SentCardIDs[0].Options.ReplyTo != "om_parent" {
		t.Fatalf("sent card IDs = %#v", transport.SentCardIDs)
	}

	if err := UpdateManagedCard(ctx, store, transport, result.MessageID, map[string]any{"updated": true}); err != nil {
		t.Fatalf("UpdateManagedCard returned error: %v", err)
	}
	if len(transport.UpdatedCardIDs) != 1 || transport.UpdatedCardIDs[0].CardID != result.CardID || transport.UpdatedCardIDs[0].Sequence != 1 {
		t.Fatalf("updated card IDs = %#v", transport.UpdatedCardIDs)
	}
}

func TestFakeTransportManagedCardRawFallback(t *testing.T) {
	ctx := context.Background()
	transport := NewFakeTransport(BotIdentity{})
	transport.SendCardIDErr = errors.New("cardid is invalid")
	store := cardmanaged.NewStore()

	result, err := SendManagedCard(ctx, store, transport, "oc_chat", map[string]any{"schema": "2.0"}, cardmanaged.SendOptions{})
	if err != nil {
		t.Fatalf("SendManagedCard returned error: %v", err)
	}
	if !result.Fallback || len(transport.SentCards) != 1 {
		t.Fatalf("result = %#v sent cards=%#v", result, transport.SentCards)
	}
	if err := UpdateManagedCard(ctx, store, transport, result.MessageID, map[string]any{"updated": true}); err != nil {
		t.Fatalf("UpdateManagedCard returned error: %v", err)
	}
	if len(transport.UpdatedCards) != 1 || transport.UpdatedCards[0].MessageID != result.MessageID {
		t.Fatalf("updated raw cards = %#v", transport.UpdatedCards)
	}
}

func TestAdapterManagedCardMethodsUseInternalStore(t *testing.T) {
	ctx := context.Background()
	transport := NewFakeTransport(BotIdentity{})
	adapter, err := NewAdapter(AdapterOptions{Transport: transport})
	if err != nil {
		t.Fatalf("NewAdapter returned error: %v", err)
	}

	result, err := adapter.SendManagedCard(ctx, "oc_chat", map[string]any{"schema": "2.0"}, cardmanaged.SendOptions{})
	if err != nil {
		t.Fatalf("SendManagedCard returned error: %v", err)
	}
	if !adapter.IsManagedCard(result.MessageID) {
		t.Fatalf("message %s not marked managed", result.MessageID)
	}
	if err := adapter.UpdateManagedCard(ctx, result.MessageID, map[string]any{"updated": true}); err != nil {
		t.Fatalf("UpdateManagedCard returned error: %v", err)
	}
	if len(transport.UpdatedCardIDs) != 1 || transport.UpdatedCardIDs[0].Sequence != 1 {
		t.Fatalf("updated card ids = %#v", transport.UpdatedCardIDs)
	}
	adapter.ForgetManagedCard(result.MessageID)
	if adapter.IsManagedCard(result.MessageID) {
		t.Fatalf("message %s still marked managed after forget", result.MessageID)
	}
}

func TestAdapterManagedCardMethodsRequireCardEntityTransport(t *testing.T) {
	adapter, err := NewAdapter(AdapterOptions{Transport: messageOnlyTransport{}})
	if err != nil {
		t.Fatalf("NewAdapter returned error: %v", err)
	}
	_, err = adapter.SendManagedCard(context.Background(), "oc_chat", map[string]any{}, cardmanaged.SendOptions{})
	if !errors.Is(err, ErrManagedCardTransport) {
		t.Fatalf("SendManagedCard error = %v, want ErrManagedCardTransport", err)
	}
}

type messageOnlyTransport struct{}

func (messageOnlyTransport) Connect(context.Context, TransportHandler) error { return nil }
func (messageOnlyTransport) Disconnect(context.Context) error                { return nil }
func (messageOnlyTransport) BotIdentity(context.Context) (BotIdentity, error) {
	return BotIdentity{}, nil
}
func (messageOnlyTransport) SendMessage(context.Context, SendMessageRequest) (SendResult, error) {
	return SendResult{}, nil
}
func (messageOnlyTransport) SendCard(context.Context, SendCardRequest) (SendResult, error) {
	return SendResult{}, nil
}
func (messageOnlyTransport) UpdateCard(context.Context, UpdateCardRequest) error { return nil }
