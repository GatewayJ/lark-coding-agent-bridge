package lark

import (
	"context"
	"errors"
	"time"

	appdispatch "github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/carddispatch"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/cardmanaged"
	appintake "github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/intake"
)

var (
	ErrNilTransport          = errors.New("lark transport is required")
	ErrUnsupportedEventKind  = errors.New("unsupported lark event kind")
	ErrMissingEventPayload   = errors.New("lark event payload is required")
	ErrAdapterNotStarted     = errors.New("lark adapter is not started")
	ErrCarrierThreadResolver = errors.New("lark transport cannot resolve carrier threads")
	ErrManagedCardTransport  = errors.New("lark transport cannot manage card entities")
)

type AdapterOptions struct {
	Transport Transport
	Intake    IntakeSink

	CardActions                CardActionDispatcher
	ForwardCardPromptsToIntake bool
	SelfLoopPolicy             appintake.SelfLoopPolicy
	UseBotIdentityForSelfLoop  *bool
	ProfileProjection          ProfileProjectionHook
	Now                        func() time.Time
}

type Adapter struct {
	transport                  Transport
	intake                     IntakeSink
	cardActions                CardActionDispatcher
	forwardCardPromptsToIntake bool
	selfLoopPolicy             appintake.SelfLoopPolicy
	useBotIdentityForSelfLoop  bool
	profileProjection          ProfileProjectionHook
	now                        func() time.Time
	managedCards               *cardmanaged.Store

	started          bool
	botIdentity      BotIdentity
	projectionResult ProfileProjectionResult
}

type HandleResult struct {
	Normalized      appintake.NormalizedEvent
	CardDispatch    *appdispatch.Result
	DroppedSelfLoop bool
}

func NewAdapter(options AdapterOptions) (*Adapter, error) {
	if options.Transport == nil {
		return nil, ErrNilTransport
	}
	useBotIdentity := true
	if options.UseBotIdentityForSelfLoop != nil {
		useBotIdentity = *options.UseBotIdentityForSelfLoop
	}
	return &Adapter{
		transport:                  options.Transport,
		intake:                     options.Intake,
		cardActions:                options.CardActions,
		forwardCardPromptsToIntake: options.ForwardCardPromptsToIntake,
		selfLoopPolicy:             options.SelfLoopPolicy,
		useBotIdentityForSelfLoop:  useBotIdentity,
		profileProjection:          options.ProfileProjection,
		now:                        options.Now,
		managedCards:               cardmanaged.NewStore(),
	}, nil
}

func (a *Adapter) Start(ctx context.Context) error {
	if a == nil || a.transport == nil {
		return ErrNilTransport
	}
	if err := a.transport.Connect(ctx, a); err != nil {
		return err
	}
	connected := true
	defer func() {
		if connected && !a.started {
			_ = a.transport.Disconnect(context.Background())
		}
	}()
	identity, err := a.transport.BotIdentity(ctx)
	if err != nil {
		return err
	}
	a.botIdentity = identity
	if a.useBotIdentityForSelfLoop && a.selfLoopPolicy.BotOpenID == "" && identity.OpenID != "" {
		a.selfLoopPolicy.BotOpenID = identity.OpenID
	}
	if a.profileProjection != nil {
		result, err := a.profileProjection.ProjectLarkProfile(ctx, ProfileProjectionRequest{
			BotIdentity: identity,
			StartedAt:   a.timeNow(),
		})
		if err != nil {
			return err
		}
		a.projectionResult = result
	}
	if starter, ok := a.intake.(interface {
		Start(context.Context) error
	}); ok {
		if err := starter.Start(ctx); err != nil {
			return err
		}
	}
	a.started = true
	connected = false
	return nil
}

func (a *Adapter) Disconnect(ctx context.Context) error {
	if a == nil || a.transport == nil {
		return nil
	}
	a.started = false
	if closer, ok := a.intake.(interface{ Close() }); ok {
		closer.Close()
	}
	return a.transport.Disconnect(ctx)
}

func (a *Adapter) BotIdentity() BotIdentity {
	if a == nil {
		return BotIdentity{}
	}
	return a.botIdentity
}

func (a *Adapter) ProjectionResult() ProfileProjectionResult {
	if a == nil {
		return ProfileProjectionResult{}
	}
	return a.projectionResult
}

func (a *Adapter) Started() bool {
	return a != nil && a.started
}

func (a *Adapter) HandleLarkTransportEvent(ctx context.Context, event IncomingEvent) error {
	_, err := a.HandleTransportEvent(ctx, event)
	return err
}

func (a *Adapter) HandleTransportEvent(ctx context.Context, event IncomingEvent) (HandleResult, error) {
	if a == nil {
		return HandleResult{}, ErrNilTransport
	}
	normalized, err := a.normalize(event)
	if err != nil {
		return HandleResult{}, err
	}
	result := HandleResult{Normalized: normalized}
	if normalized.Self.Drop {
		result.DroppedSelfLoop = true
		return result, nil
	}
	if event.Kind == appintake.EventCardAction {
		cardResult, err := a.dispatchCardAction(ctx, event, normalized)
		if cardResult != nil {
			result.CardDispatch = cardResult
		}
		return result, err
	}
	if a.intake != nil {
		if err := a.intake.HandleLarkEvent(ctx, normalized); err != nil {
			return result, err
		}
	}
	return result, nil
}

func (a *Adapter) SendMessage(ctx context.Context, req SendMessageRequest) (SendResult, error) {
	if a == nil || a.transport == nil {
		return SendResult{}, ErrNilTransport
	}
	return a.transport.SendMessage(ctx, req)
}

func (a *Adapter) SendCard(ctx context.Context, req SendCardRequest) (SendResult, error) {
	if a == nil || a.transport == nil {
		return SendResult{}, ErrNilTransport
	}
	return a.transport.SendCard(ctx, req)
}

func (a *Adapter) UpdateCard(ctx context.Context, req UpdateCardRequest) error {
	if a == nil || a.transport == nil {
		return ErrNilTransport
	}
	return a.transport.UpdateCard(ctx, req)
}

func (a *Adapter) SendManagedCard(ctx context.Context, recipientID string, card map[string]any, opts cardmanaged.SendOptions) (cardmanaged.SendResult, error) {
	channel, err := a.managedCardChannel()
	if err != nil {
		return cardmanaged.SendResult{}, err
	}
	return a.managedCards.Send(ctx, channel, recipientID, card, opts)
}

func (a *Adapter) UpdateManagedCard(ctx context.Context, messageID string, card map[string]any) error {
	channel, err := a.managedCardChannel()
	if err != nil {
		return err
	}
	return a.managedCards.Update(ctx, channel, messageID, card)
}

func (a *Adapter) ForgetManagedCard(messageID string) {
	if a == nil || a.managedCards == nil {
		return
	}
	a.managedCards.Forget(messageID)
}

func (a *Adapter) IsManagedCard(messageID string) bool {
	return a != nil && a.managedCards != nil && a.managedCards.IsManaged(messageID)
}

func (a *Adapter) managedCardChannel() (cardmanaged.Channel, error) {
	if a == nil || a.transport == nil {
		return nil, ErrNilTransport
	}
	channel, ok := a.transport.(cardmanaged.Channel)
	if !ok {
		return nil, ErrManagedCardTransport
	}
	if a.managedCards == nil {
		a.managedCards = cardmanaged.NewStore()
	}
	return channel, nil
}

func (a *Adapter) ResolveCarrierThreadID(ctx context.Context, chatID, messageID string) (string, error) {
	resolver, ok := a.transport.(CarrierThreadResolver)
	if !ok {
		return "", ErrCarrierThreadResolver
	}
	return resolver.ResolveCarrierThreadID(ctx, chatID, messageID)
}

func (a *Adapter) normalize(event IncomingEvent) (appintake.NormalizedEvent, error) {
	normalizer := appintake.NewNormalizer(a.selfLoopPolicy)
	switch event.Kind {
	case appintake.EventMessage:
		if event.Message == nil {
			return appintake.NormalizedEvent{}, ErrMissingEventPayload
		}
		input := *event.Message
		return normalizer.NormalizeMessage(input), nil
	case appintake.EventComment:
		if event.Comment == nil {
			return appintake.NormalizedEvent{}, ErrMissingEventPayload
		}
		input := *event.Comment
		return normalizer.NormalizeComment(input), nil
	case appintake.EventCardAction:
		if event.CardAction == nil {
			return appintake.NormalizedEvent{}, ErrMissingEventPayload
		}
		input := *event.CardAction
		return normalizer.NormalizeCardAction(input), nil
	case appintake.EventReconnect:
		if event.Reconnect == nil {
			return appintake.NormalizedEvent{}, ErrMissingEventPayload
		}
		input := *event.Reconnect
		return appintake.NormalizeReconnect(input), nil
	case appintake.EventKeepalive:
		if event.Keepalive == nil {
			return appintake.NormalizedEvent{}, ErrMissingEventPayload
		}
		input := *event.Keepalive
		return appintake.NormalizeKeepalive(input), nil
	case appintake.EventDisconnect:
		if event.Disconnect == nil {
			return appintake.NormalizedEvent{}, ErrMissingEventPayload
		}
		input := *event.Disconnect
		return appintake.NormalizeDisconnect(input), nil
	default:
		return appintake.NormalizedEvent{}, ErrUnsupportedEventKind
	}
}

func (a *Adapter) dispatchCardAction(ctx context.Context, event IncomingEvent, normalized appintake.NormalizedEvent) (*appdispatch.Result, error) {
	if a.cardActions == nil {
		if a.intake != nil {
			if err := a.intake.HandleLarkEvent(ctx, normalized); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}
	cardResult, err := a.cardActions.Dispatch(ctx, *event.CardAction)
	if a.forwardCardPromptsToIntake && a.intake != nil && cardResult.Enqueued != nil {
		if intakeErr := a.intake.HandleLarkEvent(ctx, *cardResult.Enqueued); err == nil && intakeErr != nil {
			err = intakeErr
		}
	}
	return &cardResult, err
}

func (a *Adapter) timeNow() time.Time {
	if a.now != nil {
		return a.now()
	}
	return time.Now()
}
