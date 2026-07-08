package cardmanaged

import (
	"context"
	"errors"
	"sync"
)

var (
	ErrManagedCardMissing = errors.New("managed card is not registered")
	ErrNilChannel         = errors.New("managed card channel is nil")
)

type EntryKind string

const (
	EntryCardID  EntryKind = "card-id"
	EntryRawCard EntryKind = "raw-card"
)

type Entry struct {
	Kind     EntryKind `json:"kind"`
	CardID   string    `json:"cardId,omitempty"`
	Sequence int       `json:"sequence"`
}

type SendOptions struct {
	ReplyTo       string `json:"replyTo,omitempty"`
	ReplyInThread bool   `json:"replyInThread,omitempty"`
}

type SendResult struct {
	MessageID string `json:"messageId"`
	CardID    string `json:"cardId"`
	Fallback  bool   `json:"fallback,omitempty"`
}

type Channel interface {
	CreateCard(ctx context.Context, card map[string]any) (string, error)
	SendCardID(ctx context.Context, recipientID string, cardID string, opts SendOptions) (string, error)
	SendRawCard(ctx context.Context, recipientID string, card map[string]any, opts SendOptions) (string, error)
	UpdateCardByID(ctx context.Context, cardID string, card map[string]any, sequence int) error
	UpdateRawCard(ctx context.Context, messageID string, card map[string]any) error
}

type Store struct {
	mu          sync.Mutex
	byMessageID map[string]Entry
}

func NewStore() *Store {
	return &Store{byMessageID: map[string]Entry{}}
}

func (s *Store) Send(ctx context.Context, channel Channel, recipientID string, card map[string]any, opts SendOptions) (SendResult, error) {
	if s == nil {
		s = NewStore()
	}
	if channel == nil {
		return SendResult{}, ErrNilChannel
	}
	cardID, err := channel.CreateCard(ctx, card)
	if err != nil {
		return SendResult{}, err
	}
	messageID, err := channel.SendCardID(ctx, recipientID, cardID, opts)
	if err == nil {
		s.set(messageID, Entry{Kind: EntryCardID, CardID: cardID})
		return SendResult{MessageID: messageID, CardID: cardID}, nil
	}
	messageID, rawErr := channel.SendRawCard(ctx, recipientID, card, opts)
	if rawErr != nil {
		return SendResult{}, errors.Join(err, rawErr)
	}
	s.set(messageID, Entry{Kind: EntryRawCard})
	return SendResult{MessageID: messageID, CardID: cardID, Fallback: true}, nil
}

func (s *Store) Update(ctx context.Context, channel Channel, messageID string, card map[string]any) error {
	if channel == nil {
		return ErrNilChannel
	}
	entry, ok := s.lookup(messageID)
	if !ok {
		return ErrManagedCardMissing
	}
	entry.Sequence++
	if entry.Kind == EntryCardID {
		if err := channel.UpdateCardByID(ctx, entry.CardID, card, entry.Sequence); err != nil {
			return err
		}
	} else {
		if err := channel.UpdateRawCard(ctx, messageID, card); err != nil {
			return err
		}
	}
	s.set(messageID, entry)
	return nil
}

func (s *Store) IsManaged(messageID string) bool {
	_, ok := s.lookup(messageID)
	return ok
}

func (s *Store) Forget(messageID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byMessageID, messageID)
}

func (s *Store) Entry(messageID string) (Entry, bool) {
	return s.lookup(messageID)
}

func (s *Store) set(messageID string, entry Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byMessageID == nil {
		s.byMessageID = map[string]Entry{}
	}
	s.byMessageID[messageID] = entry
}

func (s *Store) lookup(messageID string) (Entry, bool) {
	if s == nil {
		return Entry{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.byMessageID[messageID]
	return entry, ok
}
