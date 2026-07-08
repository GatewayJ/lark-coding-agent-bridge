package bridge

import (
	"fmt"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/access"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/permissions"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/runpolicy"
	agentport "github.com/zarazhangrui/lark-coding-agent-bridge/internal/ports/agent"
)

func toInternalAccessMode(value AccessMode) (permissions.AccessMode, error) {
	switch value {
	case AccessReadOnly:
		return permissions.AccessReadOnly, nil
	case AccessWorkspace:
		return permissions.AccessWorkspace, nil
	case AccessFull:
		return permissions.AccessFull, nil
	default:
		return "", fmt.Errorf("invalid access mode %q", value)
	}
}

func toAccessDecision(input AccessDecision) access.Decision {
	return access.Decision{
		OK:     input.OK,
		Reason: access.Reason(input.Reason),
	}
}

func toRunPolicyScope(input Scope) runpolicy.ScopeContext {
	source := runpolicy.Source(input.Source)
	if source == "" {
		source = runpolicy.SourceIM
	}
	return runpolicy.ScopeContext{
		Source:         source,
		ChatID:         input.ChatID,
		ThreadID:       input.ThreadID,
		ActorID:        input.ActorID,
		CommentScopeID: input.CommentScopeID,
	}
}

func toRunPolicyAttachments(input []Attachment) []runpolicy.AgentAttachment {
	out := make([]runpolicy.AgentAttachment, 0, len(input))
	for _, attachment := range input {
		out = append(out, runpolicy.AgentAttachment{
			Kind:            attachment.Kind,
			Requiredness:    runpolicy.AttachmentRequiredness(attachment.Requiredness),
			Decision:        runpolicy.AttachmentDecision(attachment.Decision),
			RejectionReason: attachment.RejectionReason,
			OriginalName:    attachment.OriginalName,
			Size:            attachment.Size,
			Hash:            attachment.Hash,
			Path:            attachment.Path,
		})
	}
	return out
}

func fromAgentEvent(event agentport.AgentEvent) Event {
	return Event{
		Type:                  EventType(event.Type),
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
		TerminationReason:     TerminationReason(event.TerminationReason),
	}
}
