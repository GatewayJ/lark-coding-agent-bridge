package lark

import (
	"context"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/cardmanaged"
)

type ManagedCardChannel interface {
	cardmanaged.Channel
}

func SendManagedCard(ctx context.Context, store *cardmanaged.Store, channel ManagedCardChannel, recipientID string, card map[string]any, opts cardmanaged.SendOptions) (cardmanaged.SendResult, error) {
	return store.Send(ctx, channel, recipientID, card, opts)
}

func UpdateManagedCard(ctx context.Context, store *cardmanaged.Store, channel ManagedCardChannel, messageID string, card map[string]any) error {
	return store.Update(ctx, channel, messageID, card)
}
