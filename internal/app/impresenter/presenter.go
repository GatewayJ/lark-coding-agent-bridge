package impresenter

import (
	"context"
	"strings"
	"time"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/cardkit"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/cardrender"
	agentport "github.com/zarazhangrui/lark-coding-agent-bridge/internal/ports/agent"
)

type ReplyMode string

const (
	ReplyMarkdown ReplyMode = "markdown"
	ReplyText     ReplyMode = "text"
	ReplyCard     ReplyMode = "card"
)

const defaultStreamThrottle = 400 * time.Millisecond
const defaultIdleStopTimeout = 5 * time.Second

type SendOptions struct {
	ReplyTo       string
	ReplyInThread bool
	ThreadID      string
	Metadata      map[string]any
}

type MessageContent struct {
	Text     string
	Markdown string
	Card     map[string]any
}

type SendMessageRequest struct {
	ChatID  string
	Content MessageContent
	Options SendOptions
}

type SendMessageResult struct {
	MessageID string
}

type SendCardRequest struct {
	ChatID  string
	Card    map[string]any
	Options SendOptions
}

type UpdateCardRequest struct {
	MessageID string
	Card      map[string]any
}

type SendCardResult struct {
	MessageID string
}

type Channel interface {
	SendMessage(ctx context.Context, req SendMessageRequest) (SendMessageResult, error)
	SendCard(ctx context.Context, req SendCardRequest) (SendCardResult, error)
}

type CardUpdater interface {
	UpdateCard(ctx context.Context, req UpdateCardRequest) error
}

type MessageUpdater interface {
	UpdateMessage(ctx context.Context, req UpdateMessageRequest) error
}

type UpdateMessageRequest struct {
	MessageID string
	Content   MessageContent
}

type Run interface {
	Events(ctx context.Context) <-chan agentport.AgentEvent
}

type RunStopper interface {
	Stop(ctx context.Context) error
}

type Input struct {
	Run             Run
	Channel         Channel
	ChatID          string
	Options         SendOptions
	ReplyMode       ReplyMode
	RenderOptions   cardrender.RenderOptions
	StartedAt       time.Time
	StreamThrottle  time.Duration
	HideToolCalls   bool
	IdleTimeout     time.Duration
	DeferUntilDone  bool
	FinalAnswerOnly bool
	BeforeFinal     func(context.Context, cardrender.RunState) error
}

func Present(ctx context.Context, input Input) (cardrender.RunState, error) {
	state := cardrender.NewRunState(cardrender.RunStateInput{StartedAt: input.StartedAt})
	var cardMessageID string
	var markdownMessageID string
	lastCardUpdate := time.Time{}
	lastMarkdownUpdate := time.Time{}
	if normalizeReplyMode(input.ReplyMode) == ReplyCard && !input.DeferUntilDone {
		result, err := sendCard(ctx, input, state)
		if err != nil {
			return state, err
		}
		cardMessageID = result.MessageID
		lastCardUpdate = time.Now()
	}
	idleFired := false
	if input.Run != nil {
		events := input.Run.Events(ctx)
		inFlightTools := map[string]struct{}{}
		var idleTimer *time.Timer
		var idleC <-chan time.Time
		stopIdleTimer := func() {
			if idleTimer == nil {
				return
			}
			if !idleTimer.Stop() {
				select {
				case <-idleTimer.C:
				default:
				}
			}
			idleTimer = nil
			idleC = nil
		}
		armIdleTimer := func() {
			if input.IdleTimeout <= 0 {
				return
			}
			stopIdleTimer()
			if len(inFlightTools) > 0 {
				return
			}
			idleTimer = time.NewTimer(input.IdleTimeout)
			idleC = idleTimer.C
		}
		armIdleTimer()
		for {
			select {
			case <-ctx.Done():
				stopIdleTimer()
				return state, ctx.Err()
			case <-idleC:
				idleFired = true
				stopRunAfterIdle(input.Run)
				goto done
			case event, ok := <-events:
				if !ok {
					goto done
				}
				trackToolFlight(inFlightTools, event)
				armIdleTimer()
				state = cardrender.Reduce(state, toCardEvent(event))
				if !input.DeferUntilDone && shouldStreamCardUpdate(input, cardMessageID, lastCardUpdate) {
					if err := updateRunCard(ctx, input, state, cardMessageID); err == nil {
						lastCardUpdate = time.Now()
					}
				}
				if !input.DeferUntilDone && shouldStreamMarkdownUpdate(input, markdownMessageID, lastMarkdownUpdate, state) {
					messageID, err := streamMarkdown(ctx, input, state, markdownMessageID)
					if err == nil {
						markdownMessageID = messageID
						lastMarkdownUpdate = time.Now()
					}
				}
				if !isActive(state) {
					goto done
				}
			}
		}
	done:
		stopIdleTimer()
	}
	if isActive(state) {
		if idleFired {
			state = cardrender.MarkTimeout(state, idleTimeoutMinutes(input.IdleTimeout))
		} else {
			state = cardrender.FinalizeIfRunning(state)
		}
	}
	if input.BeforeFinal != nil {
		if err := input.BeforeFinal(ctx, state); err != nil {
			return state, err
		}
	}
	return state, sendFinal(ctx, input, state, cardMessageID, markdownMessageID)
}

func trackToolFlight(inFlight map[string]struct{}, event agentport.AgentEvent) {
	id := ""
	if event.ID != nil {
		id = *event.ID
	}
	if id == "" {
		return
	}
	switch event.Type {
	case agentport.EventToolUse:
		inFlight[id] = struct{}{}
	case agentport.EventToolResult:
		delete(inFlight, id)
	}
}

func stopRunAfterIdle(run Run) {
	stopper, ok := run.(RunStopper)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultIdleStopTimeout)
	defer cancel()
	_ = stopper.Stop(ctx)
}

func idleTimeoutMinutes(timeout time.Duration) int {
	if timeout <= 0 {
		return 0
	}
	minutes := int((timeout + time.Minute - 1) / time.Minute)
	if minutes < 1 {
		return 1
	}
	return minutes
}

func isActive(state cardrender.RunState) bool {
	return state.Status == "" || state.Status == cardrender.StatusQueued || state.Status == cardrender.StatusRunning
}

func shouldStreamCardUpdate(input Input, cardMessageID string, lastUpdate time.Time) bool {
	if cardMessageID == "" || normalizeReplyMode(input.ReplyMode) != ReplyCard {
		return false
	}
	if _, ok := input.Channel.(CardUpdater); !ok {
		return false
	}
	if lastUpdate.IsZero() {
		return true
	}
	return time.Since(lastUpdate) >= streamThrottle(input.StreamThrottle)
}

func shouldStreamMarkdownUpdate(input Input, messageID string, lastUpdate time.Time, state cardrender.RunState) bool {
	if normalizeReplyMode(input.ReplyMode) != ReplyMarkdown {
		return false
	}
	if _, ok := input.Channel.(MessageUpdater); !ok {
		return false
	}
	if strings.TrimSpace(renderRunMarkdown(input, state)) == "" {
		return false
	}
	if messageID == "" || lastUpdate.IsZero() {
		return true
	}
	return time.Since(lastUpdate) >= streamThrottle(input.StreamThrottle)
}

func sendFinal(ctx context.Context, input Input, state cardrender.RunState, cardMessageID string, markdownMessageID string) error {
	if input.Channel == nil {
		return nil
	}
	if normalizeReplyMode(input.ReplyMode) == ReplyCard {
		if cardMessageID != "" {
			if _, ok := input.Channel.(CardUpdater); ok {
				return updateRunCard(ctx, input, state, cardMessageID)
			}
		}
		_, err := sendCardWithCard(ctx, input, renderRunCard(input, state))
		return err
	}
	if normalizeReplyMode(input.ReplyMode) == ReplyMarkdown && markdownMessageID != "" {
		if _, ok := input.Channel.(MessageUpdater); ok {
			_, err := streamMarkdown(ctx, input, state, markdownMessageID)
			return err
		}
	}
	body := renderRunMarkdown(input, state)
	if strings.TrimSpace(body) == "" {
		return nil
	}
	_, err := input.Channel.SendMessage(ctx, SendMessageRequest{
		ChatID: input.ChatID,
		Content: MessageContent{
			Markdown: body,
		},
		Options: input.Options,
	})
	return err
}

func updateRunCard(ctx context.Context, input Input, state cardrender.RunState, messageID string) error {
	updater, ok := input.Channel.(CardUpdater)
	if !ok || messageID == "" {
		return nil
	}
	return updater.UpdateCard(ctx, UpdateCardRequest{
		MessageID: messageID,
		Card:      renderRunCard(input, state),
	})
}

func streamMarkdown(ctx context.Context, input Input, state cardrender.RunState, messageID string) (string, error) {
	body := renderRunMarkdown(input, state)
	if strings.TrimSpace(body) == "" {
		return messageID, nil
	}
	if messageID == "" {
		result, err := input.Channel.SendMessage(ctx, SendMessageRequest{
			ChatID: input.ChatID,
			Content: MessageContent{
				Markdown: body,
			},
			Options: input.Options,
		})
		return result.MessageID, err
	}
	updater, ok := input.Channel.(MessageUpdater)
	if !ok {
		return messageID, nil
	}
	return messageID, updater.UpdateMessage(ctx, UpdateMessageRequest{
		MessageID: messageID,
		Content: MessageContent{
			Markdown: body,
		},
	})
}

func sendCard(ctx context.Context, input Input, state cardrender.RunState) (SendCardResult, error) {
	return sendCardWithCard(ctx, input, renderRunCard(input, state))
}

func sendCardWithCard(ctx context.Context, input Input, card map[string]any) (SendCardResult, error) {
	if input.Channel == nil {
		return SendCardResult{}, nil
	}
	return input.Channel.SendCard(ctx, SendCardRequest{
		ChatID:  input.ChatID,
		Card:    card,
		Options: input.Options,
	})
}

func renderRunCard(input Input, state cardrender.RunState) map[string]any {
	return cardkit.RenderRunCardKit(renderRunState(input, state), input.RenderOptions)
}

func renderRunMarkdown(input Input, state cardrender.RunState) string {
	return cardrender.RenderText(renderRunState(input, state)).Content
}

func renderRunState(input Input, state cardrender.RunState) cardrender.RunState {
	if input.FinalAnswerOnly {
		state = finalAnswerOnlyState(state)
	}
	if !input.HideToolCalls || len(state.Blocks) == 0 {
		return state
	}
	filtered := state
	filtered.Blocks = make([]cardrender.Block, 0, len(state.Blocks))
	for _, block := range state.Blocks {
		if block.Kind == cardrender.BlockTool {
			continue
		}
		filtered.Blocks = append(filtered.Blocks, block)
	}
	return filtered
}

func finalAnswerOnlyState(state cardrender.RunState) cardrender.RunState {
	filtered := state
	filtered.Reasoning.Content = ""
	filtered.Reasoning.Active = false
	filtered.Footer = ""
	filtered.Blocks = make([]cardrender.Block, 0, len(state.Blocks))
	for _, block := range state.Blocks {
		if block.Kind == cardrender.BlockText {
			filtered.Blocks = append(filtered.Blocks, block)
		}
	}
	return filtered
}

func normalizeReplyMode(mode ReplyMode) ReplyMode {
	switch mode {
	case ReplyCard, ReplyText, ReplyMarkdown:
		return mode
	default:
		return ReplyMarkdown
	}
}

func streamThrottle(delay time.Duration) time.Duration {
	if delay <= 0 {
		return defaultStreamThrottle
	}
	return delay
}

func toCardEvent(event agentport.AgentEvent) cardrender.Event {
	return cardrender.Event{
		Type:                  cardrender.EventType(event.Type),
		SessionID:             event.SessionID,
		ThreadID:              event.ThreadID,
		CWD:                   event.CWD,
		Model:                 event.Model,
		Delta:                 event.Delta,
		ID:                    event.ID,
		Name:                  event.Name,
		Input:                 event.Input,
		Output:                event.Output,
		IsError:               event.IsError,
		InputTokens:           event.InputTokens,
		OutputTokens:          event.OutputTokens,
		CachedInputTokens:     event.CachedInputTokens,
		ReasoningOutputTokens: event.ReasoningOutputTokens,
		CostUSD:               event.CostUSD,
		Message:               event.Message,
		TerminationReason:     cardrender.TerminationReason(event.TerminationReason),
	}
}
