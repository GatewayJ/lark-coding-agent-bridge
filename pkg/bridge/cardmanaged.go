package bridge

import (
	"context"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/cardmanaged"
)

var (
	ErrManagedCardMissing    = cardmanaged.ErrManagedCardMissing
	ErrManagedCardNilChannel = cardmanaged.ErrNilChannel
)

type ManagedCardEntryKind string

const (
	ManagedCardEntryCardID  ManagedCardEntryKind = "card-id"
	ManagedCardEntryRawCard ManagedCardEntryKind = "raw-card"
)

type ManagedCardEntry struct {
	Kind     ManagedCardEntryKind `json:"kind"`
	CardID   string               `json:"cardId,omitempty"`
	Sequence int                  `json:"sequence"`
}

type ManagedCardSendOptions struct {
	ReplyTo       string `json:"replyTo,omitempty"`
	ReplyInThread bool   `json:"replyInThread,omitempty"`
}

type ManagedCardSendResult struct {
	MessageID string `json:"messageId"`
	CardID    string `json:"cardId"`
	Fallback  bool   `json:"fallback,omitempty"`
}

type ManagedCardChannel interface {
	CreateCard(ctx context.Context, card map[string]any) (string, error)
	SendCardID(ctx context.Context, recipientID string, cardID string, opts ManagedCardSendOptions) (string, error)
	SendRawCard(ctx context.Context, recipientID string, card map[string]any, opts ManagedCardSendOptions) (string, error)
	UpdateCardByID(ctx context.Context, cardID string, card map[string]any, sequence int) error
	UpdateRawCard(ctx context.Context, messageID string, card map[string]any) error
}

type ManagedCardStore struct {
	inner *cardmanaged.Store
}

func NewManagedCardStore() *ManagedCardStore {
	return &ManagedCardStore{inner: cardmanaged.NewStore()}
}

func (s *ManagedCardStore) Send(ctx context.Context, channel ManagedCardChannel, recipientID string, card map[string]any, opts ManagedCardSendOptions) (ManagedCardSendResult, error) {
	result, err := internalManagedCardStore(s).Send(ctx, managedCardChannelAdapter{channel: channel}, recipientID, card, toInternalManagedCardSendOptions(opts))
	return fromInternalManagedCardSendResult(result), err
}

func (s *ManagedCardStore) Update(ctx context.Context, channel ManagedCardChannel, messageID string, card map[string]any) error {
	return internalManagedCardStore(s).Update(ctx, managedCardChannelAdapter{channel: channel}, messageID, card)
}

func (s *ManagedCardStore) IsManaged(messageID string) bool {
	if s == nil || s.inner == nil {
		return false
	}
	return s.inner.IsManaged(messageID)
}

func (s *ManagedCardStore) Forget(messageID string) {
	if s != nil && s.inner != nil {
		s.inner.Forget(messageID)
	}
}

func (s *ManagedCardStore) Entry(messageID string) (ManagedCardEntry, bool) {
	if s == nil || s.inner == nil {
		return ManagedCardEntry{}, false
	}
	entry, ok := s.inner.Entry(messageID)
	return fromInternalManagedCardEntry(entry), ok
}

func SendManagedCard(ctx context.Context, store *ManagedCardStore, channel ManagedCardChannel, recipientID string, card map[string]any, opts ManagedCardSendOptions) (ManagedCardSendResult, error) {
	return store.Send(ctx, channel, recipientID, card, opts)
}

func UpdateManagedCard(ctx context.Context, store *ManagedCardStore, channel ManagedCardChannel, messageID string, card map[string]any) error {
	return store.Update(ctx, channel, messageID, card)
}

func internalManagedCardStore(store *ManagedCardStore) *cardmanaged.Store {
	if store == nil {
		return cardmanaged.NewStore()
	}
	if store.inner == nil {
		store.inner = cardmanaged.NewStore()
	}
	return store.inner
}

func toInternalManagedCardSendOptions(options ManagedCardSendOptions) cardmanaged.SendOptions {
	return cardmanaged.SendOptions{
		ReplyTo:       options.ReplyTo,
		ReplyInThread: options.ReplyInThread,
	}
}

func fromInternalManagedCardSendOptions(options cardmanaged.SendOptions) ManagedCardSendOptions {
	return ManagedCardSendOptions{
		ReplyTo:       options.ReplyTo,
		ReplyInThread: options.ReplyInThread,
	}
}

func fromInternalManagedCardEntry(entry cardmanaged.Entry) ManagedCardEntry {
	return ManagedCardEntry{
		Kind:     ManagedCardEntryKind(entry.Kind),
		CardID:   entry.CardID,
		Sequence: entry.Sequence,
	}
}

func fromInternalManagedCardSendResult(result cardmanaged.SendResult) ManagedCardSendResult {
	return ManagedCardSendResult{
		MessageID: result.MessageID,
		CardID:    result.CardID,
		Fallback:  result.Fallback,
	}
}

type managedCardChannelAdapter struct {
	channel ManagedCardChannel
}

func (a managedCardChannelAdapter) CreateCard(ctx context.Context, card map[string]any) (string, error) {
	if a.channel == nil {
		return "", ErrManagedCardNilChannel
	}
	return a.channel.CreateCard(ctx, card)
}

func (a managedCardChannelAdapter) SendCardID(ctx context.Context, recipientID string, cardID string, opts cardmanaged.SendOptions) (string, error) {
	if a.channel == nil {
		return "", ErrManagedCardNilChannel
	}
	return a.channel.SendCardID(ctx, recipientID, cardID, fromInternalManagedCardSendOptions(opts))
}

func (a managedCardChannelAdapter) SendRawCard(ctx context.Context, recipientID string, card map[string]any, opts cardmanaged.SendOptions) (string, error) {
	if a.channel == nil {
		return "", ErrManagedCardNilChannel
	}
	return a.channel.SendRawCard(ctx, recipientID, card, fromInternalManagedCardSendOptions(opts))
}

func (a managedCardChannelAdapter) UpdateCardByID(ctx context.Context, cardID string, card map[string]any, sequence int) error {
	if a.channel == nil {
		return ErrManagedCardNilChannel
	}
	return a.channel.UpdateCardByID(ctx, cardID, card, sequence)
}

func (a managedCardChannelAdapter) UpdateRawCard(ctx context.Context, messageID string, card map[string]any) error {
	if a.channel == nil {
		return ErrManagedCardNilChannel
	}
	return a.channel.UpdateRawCard(ctx, messageID, card)
}
