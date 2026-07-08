package cardmanaged

import (
	"context"
	"errors"
	"testing"
)

func TestSendFallsBackToRawCardWhenCardIDSendFails(t *testing.T) {
	channel := &fakeChannel{
		createCardID:    "card_1",
		sendCardIDErr:   errors.New("cardid is invalid"),
		rawMessageID:    "om_raw",
		cardIDMessageID: "om_card",
	}
	store := NewStore()

	result, err := store.Send(context.Background(), channel, "oc_chat", map[string]any{"body": "form"}, SendOptions{
		ReplyTo:       "om_parent",
		ReplyInThread: true,
	})
	if err != nil {
		t.Fatalf("Send returned error: %v", err)
	}
	if result.MessageID != "om_raw" || result.CardID != "card_1" || !result.Fallback {
		t.Fatalf("result = %#v", result)
	}
	if channel.createCalls != 1 || channel.sendCardIDCalls != 1 || channel.sendRawCalls != 1 {
		t.Fatalf("calls create=%d cardID=%d raw=%d", channel.createCalls, channel.sendCardIDCalls, channel.sendRawCalls)
	}
	if channel.lastRecipient != "oc_chat" || channel.lastOpts.ReplyTo != "om_parent" || !channel.lastOpts.ReplyInThread {
		t.Fatalf("send context recipient=%q opts=%#v", channel.lastRecipient, channel.lastOpts)
	}
	entry, ok := store.Entry("om_raw")
	if !ok || entry.Kind != EntryRawCard || entry.Sequence != 0 {
		t.Fatalf("entry = %#v ok=%v", entry, ok)
	}
}

func TestUpdateCardIDManagedMessageUsesCardIDAndSequence(t *testing.T) {
	channel := &fakeChannel{
		createCardID:    "card_normal",
		cardIDMessageID: "om_normal",
	}
	store := NewStore()
	if _, err := store.Send(context.Background(), channel, "oc_chat", map[string]any{"body": "form"}, SendOptions{}); err != nil {
		t.Fatalf("Send returned error: %v", err)
	}

	if err := store.Update(context.Background(), channel, "om_normal", map[string]any{"body": "cancelled"}); err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if channel.updateByIDCalls != 1 || channel.updateRawCalls != 0 {
		t.Fatalf("update calls byID=%d raw=%d", channel.updateByIDCalls, channel.updateRawCalls)
	}
	if channel.lastUpdateCardID != "card_normal" || channel.lastSequence != 1 {
		t.Fatalf("update cardID=%q seq=%d", channel.lastUpdateCardID, channel.lastSequence)
	}
	entry, _ := store.Entry("om_normal")
	if entry.Sequence != 1 {
		t.Fatalf("entry sequence = %d, want 1", entry.Sequence)
	}
}

func TestUpdateRawFallbackMessageUsesMessageID(t *testing.T) {
	channel := &fakeChannel{
		createCardID:    "card_raw",
		sendCardIDErr:   errors.New("cardid is invalid"),
		rawMessageID:    "om_raw_update",
		cardIDMessageID: "om_card",
	}
	store := NewStore()
	if _, err := store.Send(context.Background(), channel, "oc_chat", map[string]any{"body": "form"}, SendOptions{}); err != nil {
		t.Fatalf("Send returned error: %v", err)
	}

	if err := store.Update(context.Background(), channel, "om_raw_update", map[string]any{"body": "cancelled"}); err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if channel.updateRawCalls != 1 || channel.updateByIDCalls != 0 {
		t.Fatalf("update calls raw=%d byID=%d", channel.updateRawCalls, channel.updateByIDCalls)
	}
	if channel.lastUpdateMessageID != "om_raw_update" {
		t.Fatalf("raw update messageID = %q", channel.lastUpdateMessageID)
	}
}

func TestUpdateMissingManagedCardReturnsSentinel(t *testing.T) {
	err := NewStore().Update(context.Background(), &fakeChannel{}, "om_missing", map[string]any{})
	if !errors.Is(err, ErrManagedCardMissing) {
		t.Fatalf("Update error = %v, want ErrManagedCardMissing", err)
	}
}

func TestNilChannelReturnsSentinel(t *testing.T) {
	store := NewStore()
	if _, err := store.Send(context.Background(), nil, "oc_chat", map[string]any{}, SendOptions{}); !errors.Is(err, ErrNilChannel) {
		t.Fatalf("Send nil channel error = %v, want ErrNilChannel", err)
	}
	if err := store.Update(context.Background(), nil, "om_1", map[string]any{}); !errors.Is(err, ErrNilChannel) {
		t.Fatalf("Update nil channel error = %v, want ErrNilChannel", err)
	}
}

type fakeChannel struct {
	createCardID    string
	cardIDMessageID string
	rawMessageID    string
	sendCardIDErr   error

	createCalls     int
	sendCardIDCalls int
	sendRawCalls    int
	updateByIDCalls int
	updateRawCalls  int

	lastRecipient       string
	lastOpts            SendOptions
	lastUpdateCardID    string
	lastUpdateMessageID string
	lastSequence        int
}

func (c *fakeChannel) CreateCard(context.Context, map[string]any) (string, error) {
	c.createCalls++
	return c.createCardID, nil
}

func (c *fakeChannel) SendCardID(_ context.Context, recipientID string, _ string, opts SendOptions) (string, error) {
	c.sendCardIDCalls++
	c.lastRecipient = recipientID
	c.lastOpts = opts
	if c.sendCardIDErr != nil {
		return "", c.sendCardIDErr
	}
	return c.cardIDMessageID, nil
}

func (c *fakeChannel) SendRawCard(_ context.Context, recipientID string, _ map[string]any, opts SendOptions) (string, error) {
	c.sendRawCalls++
	c.lastRecipient = recipientID
	c.lastOpts = opts
	return c.rawMessageID, nil
}

func (c *fakeChannel) UpdateCardByID(_ context.Context, cardID string, _ map[string]any, sequence int) error {
	c.updateByIDCalls++
	c.lastUpdateCardID = cardID
	c.lastSequence = sequence
	return nil
}

func (c *fakeChannel) UpdateRawCard(_ context.Context, messageID string, _ map[string]any) error {
	c.updateRawCalls++
	c.lastUpdateMessageID = messageID
	return nil
}
