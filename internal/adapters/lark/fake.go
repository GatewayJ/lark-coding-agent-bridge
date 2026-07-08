package lark

import (
	"context"
	"errors"
	"os"
	"strconv"
	"sync"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/cardmanaged"
	appcot "github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/cotpresenter"
	appmedia "github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/media"
)

var ErrFakeTransportNotConnected = errors.New("fake lark transport is not connected")

type FakeTransport struct {
	mu sync.Mutex

	Identity BotIdentity

	ConnectErr      error
	DisconnectErr   error
	SendErr         error
	UpdateErr       error
	IdentityErr     error
	CreateChatErr   error
	CreateCardErr   error
	SendCardIDErr   error
	SendRawCardErr  error
	UpdateCardIDErr error
	ReactionErr     error
	COTCreateErr    error
	COTUpdateErr    error
	COTCompleteErr  error

	handler      TransportHandler
	connected    bool
	nextMessage  int
	nextReaction int
	nextCOT      int
	nextChat     int

	SentMessages     []SendMessageRequest
	SentCards        []SendCardRequest
	CreatedChats     []CreateBoundChatRequest
	UpdatedCards     []UpdateCardRequest
	UpdatedMessages  []UpdateMessageRequest
	AddedReactions   []MessageReactionRequest
	DeletedReactions []MessageReactionRequest
	CreatedCOTs      []appcot.CreateRequest
	UpdatedCOTs      []appcot.UpdateRequest
	CompletedCOTs    []appcot.CompleteRequest
	CreatedCards     map[string]map[string]any
	SentCardIDs      []fakeCardIDSend
	UpdatedCardIDs   []fakeCardIDUpdate

	CarrierThreads map[string]string
	Resources      map[string]fakeResourceDownload
}

type FakeTransportErrors struct {
	ConnectErr      error
	DisconnectErr   error
	SendErr         error
	UpdateErr       error
	IdentityErr     error
	CreateChatErr   error
	CreateCardErr   error
	SendCardIDErr   error
	SendRawCardErr  error
	UpdateCardIDErr error
	ReactionErr     error
	COTCreateErr    error
	COTUpdateErr    error
	COTCompleteErr  error
}

func NewFakeTransport(identity BotIdentity) *FakeTransport {
	return &FakeTransport{
		Identity:       identity,
		CarrierThreads: make(map[string]string),
		CreatedCards:   make(map[string]map[string]any),
		Resources:      make(map[string]fakeResourceDownload),
	}
}

func (t *FakeTransport) SetErrors(errors FakeTransportErrors) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ConnectErr = errors.ConnectErr
	t.DisconnectErr = errors.DisconnectErr
	t.SendErr = errors.SendErr
	t.UpdateErr = errors.UpdateErr
	t.IdentityErr = errors.IdentityErr
	t.CreateChatErr = errors.CreateChatErr
	t.CreateCardErr = errors.CreateCardErr
	t.SendCardIDErr = errors.SendCardIDErr
	t.SendRawCardErr = errors.SendRawCardErr
	t.UpdateCardIDErr = errors.UpdateCardIDErr
	t.ReactionErr = errors.ReactionErr
	t.COTCreateErr = errors.COTCreateErr
	t.COTUpdateErr = errors.COTUpdateErr
	t.COTCompleteErr = errors.COTCompleteErr
}

func (t *FakeTransport) Connect(_ context.Context, handler TransportHandler) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ConnectErr != nil {
		return t.ConnectErr
	}
	t.handler = handler
	t.connected = true
	return nil
}

func (t *FakeTransport) Disconnect(_ context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.DisconnectErr != nil {
		return t.DisconnectErr
	}
	t.connected = false
	t.handler = nil
	return nil
}

func (t *FakeTransport) BotIdentity(_ context.Context) (BotIdentity, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.IdentityErr != nil {
		return BotIdentity{}, t.IdentityErr
	}
	return t.Identity, nil
}

func (t *FakeTransport) Emit(ctx context.Context, event IncomingEvent) error {
	t.mu.Lock()
	handler := t.handler
	connected := t.connected
	t.mu.Unlock()
	if !connected || handler == nil {
		return ErrFakeTransportNotConnected
	}
	return handler.HandleLarkTransportEvent(ctx, event)
}

func (t *FakeTransport) SendMessage(_ context.Context, req SendMessageRequest) (SendResult, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.SendErr != nil {
		return SendResult{}, t.SendErr
	}
	t.nextMessage++
	t.SentMessages = append(t.SentMessages, req)
	return SendResult{MessageID: fakeMessageID(t.nextMessage)}, nil
}

func (t *FakeTransport) CreateBoundChat(_ context.Context, req CreateBoundChatRequest) (CreatedChat, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.CreateChatErr != nil {
		return CreatedChat{}, t.CreateChatErr
	}
	t.nextChat++
	t.CreatedChats = append(t.CreatedChats, req)
	name := req.Name
	if name == "" {
		name = "Chat"
	}
	return CreatedChat{ChatID: "oc_fake_" + strconv.Itoa(t.nextChat), Name: name}, nil
}

func (t *FakeTransport) CreatedChatSnapshot() []CreateBoundChatRequest {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]CreateBoundChatRequest(nil), t.CreatedChats...)
}

func (t *FakeTransport) SentMessageSnapshot() []SendMessageRequest {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]SendMessageRequest(nil), t.SentMessages...)
}

func (t *FakeTransport) SendCard(_ context.Context, req SendCardRequest) (SendResult, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.SendErr != nil {
		return SendResult{}, t.SendErr
	}
	t.nextMessage++
	t.SentCards = append(t.SentCards, req)
	return SendResult{MessageID: fakeMessageID(t.nextMessage)}, nil
}

func (t *FakeTransport) SentCardSnapshot() []SendCardRequest {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]SendCardRequest(nil), t.SentCards...)
}

func (t *FakeTransport) UpdatedCardSnapshot() []UpdateCardRequest {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]UpdateCardRequest(nil), t.UpdatedCards...)
}

func (t *FakeTransport) CreateCard(_ context.Context, card map[string]any) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.CreateCardErr != nil {
		return "", t.CreateCardErr
	}
	t.nextMessage++
	cardID := "card_fake_" + strconv.Itoa(t.nextMessage)
	if t.CreatedCards == nil {
		t.CreatedCards = make(map[string]map[string]any)
	}
	t.CreatedCards[cardID] = cloneCard(card)
	return cardID, nil
}

func (t *FakeTransport) SendCardID(_ context.Context, recipientID string, cardID string, opts cardmanaged.SendOptions) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.SendCardIDErr != nil {
		return "", t.SendCardIDErr
	}
	t.nextMessage++
	t.SentCardIDs = append(t.SentCardIDs, fakeCardIDSend{RecipientID: recipientID, CardID: cardID, Options: opts})
	return fakeMessageID(t.nextMessage), nil
}

func (t *FakeTransport) SendRawCard(ctx context.Context, recipientID string, card map[string]any, opts cardmanaged.SendOptions) (string, error) {
	t.mu.Lock()
	err := t.SendRawCardErr
	t.mu.Unlock()
	if err != nil {
		return "", err
	}
	result, err := t.SendCard(ctx, SendCardRequest{
		ChatID: recipientID,
		Card:   card,
		Options: SendOptions{
			ReplyTo:       opts.ReplyTo,
			ReplyInThread: opts.ReplyInThread,
		},
	})
	return result.MessageID, err
}

func (t *FakeTransport) UpdateCardByID(_ context.Context, cardID string, card map[string]any, sequence int) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.UpdateCardIDErr != nil {
		return t.UpdateCardIDErr
	}
	t.UpdatedCardIDs = append(t.UpdatedCardIDs, fakeCardIDUpdate{CardID: cardID, Card: cloneCard(card), Sequence: sequence})
	return nil
}

func (t *FakeTransport) UpdateRawCard(ctx context.Context, messageID string, card map[string]any) error {
	return t.UpdateCard(ctx, UpdateCardRequest{MessageID: messageID, Card: card})
}

func (t *FakeTransport) UpdateCard(_ context.Context, req UpdateCardRequest) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.UpdateErr != nil {
		return t.UpdateErr
	}
	t.UpdatedCards = append(t.UpdatedCards, req)
	return nil
}

func (t *FakeTransport) UpdateMessage(_ context.Context, req UpdateMessageRequest) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.UpdateErr != nil {
		return t.UpdateErr
	}
	t.UpdatedMessages = append(t.UpdatedMessages, req)
	return nil
}

func (t *FakeTransport) UpdatedMessageSnapshot() []UpdateMessageRequest {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]UpdateMessageRequest(nil), t.UpdatedMessages...)
}

func (t *FakeTransport) AddMessageReaction(_ context.Context, req MessageReactionRequest) (MessageReactionResult, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ReactionErr != nil {
		return MessageReactionResult{}, t.ReactionErr
	}
	t.nextReaction++
	t.AddedReactions = append(t.AddedReactions, req)
	return MessageReactionResult{ReactionID: "reaction_fake_" + strconv.Itoa(t.nextReaction)}, nil
}

func (t *FakeTransport) DeleteMessageReaction(_ context.Context, req MessageReactionRequest) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ReactionErr != nil {
		return t.ReactionErr
	}
	t.DeletedReactions = append(t.DeletedReactions, req)
	return nil
}

func (t *FakeTransport) AddedReactionSnapshot() []MessageReactionRequest {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]MessageReactionRequest(nil), t.AddedReactions...)
}

func (t *FakeTransport) DeletedReactionSnapshot() []MessageReactionRequest {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]MessageReactionRequest(nil), t.DeletedReactions...)
}

func (t *FakeTransport) CreateMessageCOT(_ context.Context, req appcot.CreateRequest) (appcot.Ref, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.COTCreateErr != nil {
		return appcot.Ref{}, t.COTCreateErr
	}
	t.nextCOT++
	t.CreatedCOTs = append(t.CreatedCOTs, req)
	id := strconv.Itoa(t.nextCOT)
	return appcot.Ref{COTID: "cot_fake_" + id, MessageID: "cot_message_fake_" + id}, nil
}

func (t *FakeTransport) UpdateMessageCOT(_ context.Context, req appcot.UpdateRequest) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.COTUpdateErr != nil {
		return t.COTUpdateErr
	}
	copied := req
	copied.Events = append([]appcot.Event(nil), req.Events...)
	t.UpdatedCOTs = append(t.UpdatedCOTs, copied)
	return nil
}

func (t *FakeTransport) CompleteMessageCOT(_ context.Context, req appcot.CompleteRequest) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.COTCompleteErr != nil {
		return t.COTCompleteErr
	}
	t.CompletedCOTs = append(t.CompletedCOTs, req)
	return nil
}

func (t *FakeTransport) CreatedCOTSnapshot() []appcot.CreateRequest {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]appcot.CreateRequest(nil), t.CreatedCOTs...)
}

func (t *FakeTransport) UpdatedCOTSnapshot() []appcot.UpdateRequest {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := append([]appcot.UpdateRequest(nil), t.UpdatedCOTs...)
	for i := range out {
		out[i].Events = append([]appcot.Event(nil), out[i].Events...)
	}
	return out
}

func (t *FakeTransport) CompletedCOTSnapshot() []appcot.CompleteRequest {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]appcot.CompleteRequest(nil), t.CompletedCOTs...)
}

func (t *FakeTransport) ResolveCarrierThreadID(_ context.Context, chatID, messageID string) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.CarrierThreads[carrierKey(chatID, messageID)], nil
}

func (t *FakeTransport) DownloadResource(_ context.Context, req appmedia.DownloadRequest) (appmedia.DownloadResult, error) {
	t.mu.Lock()
	download, ok := t.Resources[req.FileKey]
	t.mu.Unlock()
	if !ok {
		return appmedia.DownloadResult{}, errors.New("fake lark resource not found")
	}
	if err := os.WriteFile(req.DestinationPath, download.Content, 0o600); err != nil {
		return appmedia.DownloadResult{}, err
	}
	return appmedia.DownloadResult{
		ContentType:  download.ContentType,
		BytesWritten: int64(len(download.Content)),
	}, nil
}

func (t *FakeTransport) SetCarrierThread(chatID, messageID, threadID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.CarrierThreads == nil {
		t.CarrierThreads = make(map[string]string)
	}
	t.CarrierThreads[carrierKey(chatID, messageID)] = threadID
}

func (t *FakeTransport) SetResourceDownload(fileKey string, content []byte, contentType string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.Resources == nil {
		t.Resources = make(map[string]fakeResourceDownload)
	}
	t.Resources[fileKey] = fakeResourceDownload{
		Content:     append([]byte(nil), content...),
		ContentType: contentType,
	}
}

func fakeMessageID(seq int) string {
	return "om_fake_" + strconv.Itoa(seq)
}

func carrierKey(chatID, messageID string) string {
	return chatID + "\x00" + messageID
}

type fakeCardIDSend struct {
	RecipientID string
	CardID      string
	Options     cardmanaged.SendOptions
}

type fakeCardIDUpdate struct {
	CardID   string
	Card     map[string]any
	Sequence int
}

type fakeResourceDownload struct {
	Content     []byte
	ContentType string
}

func cloneCard(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
