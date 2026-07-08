package intake

type SelfLoopReason string

const (
	SelfLoopNotSelf        SelfLoopReason = ""
	SelfLoopBotOpenID      SelfLoopReason = "bot-open-id"
	SelfLoopBridgeReply    SelfLoopReason = "bridge-reply"
	SelfLoopBridgeMetadata SelfLoopReason = "bridge-metadata"
)

type SelfLoopPolicy struct {
	BotOpenID              string
	DropMessagesFromBot    bool
	DropCommentsFromBot    bool
	DropCardActionsFromBot bool
}

func DefaultSelfLoopPolicy(botOpenID string) SelfLoopPolicy {
	return SelfLoopPolicy{
		BotOpenID:              botOpenID,
		DropMessagesFromBot:    true,
		DropCommentsFromBot:    true,
		DropCardActionsFromBot: true,
	}
}

type SelfLoopDecision struct {
	Drop   bool
	Reason SelfLoopReason
}

func (p SelfLoopPolicy) EvaluateMessage(input MessageInput) SelfLoopDecision {
	if !p.DropMessagesFromBot {
		return SelfLoopDecision{}
	}
	return p.evaluate(input.Sender.OpenID, false, input.Metadata)
}

func (p SelfLoopPolicy) EvaluateComment(input CommentInput) SelfLoopDecision {
	if !p.DropCommentsFromBot {
		return SelfLoopDecision{}
	}
	return p.evaluate(input.Operator.OpenID, input.BridgeReply, input.Metadata)
}

func (p SelfLoopPolicy) EvaluateCardAction(input CardActionInput) SelfLoopDecision {
	if !p.DropCardActionsFromBot {
		return SelfLoopDecision{}
	}
	return p.evaluate(input.Operator.OpenID, false, input.Metadata)
}

func (p SelfLoopPolicy) evaluate(openID string, bridgeReply bool, metadata map[string]any) SelfLoopDecision {
	if p.BotOpenID != "" && openID == p.BotOpenID {
		return SelfLoopDecision{Drop: true, Reason: SelfLoopBotOpenID}
	}
	if bridgeReply || boolMetadata(metadata, "bridgeReply") || boolMetadata(metadata, "bridge_reply") {
		return SelfLoopDecision{Drop: true, Reason: SelfLoopBridgeReply}
	}
	for _, key := range []string{"replyMetadata", "reply_metadata", "metadata"} {
		nested, _ := metadata[key].(map[string]any)
		if nested == nil {
			continue
		}
		if boolMetadata(nested, "bridge") || boolMetadata(nested, "bridgeReply") {
			return SelfLoopDecision{Drop: true, Reason: SelfLoopBridgeMetadata}
		}
		if source, _ := nested["source"].(string); source == "lark-channel-bridge" {
			return SelfLoopDecision{Drop: true, Reason: SelfLoopBridgeMetadata}
		}
	}
	return SelfLoopDecision{}
}

func boolMetadata(metadata map[string]any, key string) bool {
	if metadata == nil {
		return false
	}
	value, _ := metadata[key].(bool)
	return value
}
