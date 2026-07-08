package intake

type Normalizer struct {
	SelfLoop SelfLoopPolicy
}

func NewNormalizer(policy SelfLoopPolicy) Normalizer {
	return Normalizer{SelfLoop: policy}
}

func (n Normalizer) NormalizeMessage(input MessageInput) NormalizedEvent {
	return NormalizedEvent{
		Kind:    EventMessage,
		Scope:   MessageScope(input),
		Self:    n.SelfLoop.EvaluateMessage(input),
		Message: &input,
	}
}

func (n Normalizer) NormalizeComment(input CommentInput) NormalizedEvent {
	return NormalizedEvent{
		Kind:    EventComment,
		Scope:   CommentScope(input),
		Self:    n.SelfLoop.EvaluateComment(input),
		Comment: &input,
	}
}

func (n Normalizer) NormalizeCardAction(input CardActionInput) NormalizedEvent {
	return NormalizedEvent{
		Kind:       EventCardAction,
		Scope:      CardActionScope(input),
		Self:       n.SelfLoop.EvaluateCardAction(input),
		CardAction: &input,
	}
}

func NormalizeReconnect(input ReconnectInput) NormalizedEvent {
	return NormalizedEvent{
		Kind:      EventReconnect,
		Scope:     LifecycleScope(ScopeSourceReconnect),
		Reconnect: &input,
	}
}

func NormalizeKeepalive(input KeepaliveInput) NormalizedEvent {
	return NormalizedEvent{
		Kind:      EventKeepalive,
		Scope:     LifecycleScope(ScopeSourceKeepalive),
		Keepalive: &input,
	}
}

func NormalizeDisconnect(input DisconnectInput) NormalizedEvent {
	return NormalizedEvent{
		Kind:       EventDisconnect,
		Scope:      LifecycleScope(ScopeSourceDisconnect),
		Disconnect: &input,
	}
}
