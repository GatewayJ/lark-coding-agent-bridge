package cardrender

func NewRunState(input RunStateInput) RunState {
	status := input.Status
	if status == "" {
		status = StatusQueued
	}
	return RunState{
		RunID:     input.RunID,
		Scope:     input.Scope,
		CWD:       input.CWD,
		SessionID: input.SessionID,
		ThreadID:  input.ThreadID,
		Model:     input.Model,
		Status:    status,
		StartedAt: input.StartedAt,
		UpdatedAt: input.UpdatedAt,
		Elapsed:   input.Elapsed,
	}
}

func Reduce(state RunState, event Event) RunState {
	state = cloneState(state)
	state.applyMetadata(event)
	state.LastEvent = string(event.Type)
	if !event.At.IsZero() {
		if state.StartedAt.IsZero() {
			state.StartedAt = event.At
		}
		state.UpdatedAt = event.At
	}

	switch event.Type {
	case EventText:
		state.ensureRunning()
		delta := value(event.Delta)
		last := len(state.Blocks) - 1
		if last >= 0 && state.Blocks[last].Kind == BlockText && state.Blocks[last].Streaming {
			state.Blocks[last].Content += delta
		} else {
			state.Blocks = append(state.Blocks, Block{
				Kind:      BlockText,
				Content:   delta,
				Streaming: true,
			})
		}
		state.Reasoning.Active = false
		state.Footer = FooterStreaming
	case EventThinking:
		state.ensureRunning()
		state.Reasoning.Content += value(event.Delta)
		state.Reasoning.Active = true
		state.Footer = FooterThinking
	case EventToolUse:
		state.ensureRunning()
		state.closeStreamingText()
		state.Blocks = append(state.Blocks, Block{
			Kind: BlockTool,
			Tool: &ToolEntry{
				ID:     value(event.ID),
				Name:   value(event.Name),
				Input:  event.Input,
				Status: ToolRunning,
			},
		})
		state.Reasoning.Active = false
		state.Footer = FooterToolRunning
	case EventToolResult:
		for i := range state.Blocks {
			tool := state.Blocks[i].Tool
			if state.Blocks[i].Kind != BlockTool || tool == nil || tool.ID != value(event.ID) {
				continue
			}
			if boolValue(event.IsError) {
				tool.Status = ToolError
			} else {
				tool.Status = ToolDone
			}
			tool.Output = value(event.Output)
		}
	case EventUsage:
		state.ensureRunning()
		state.applyUsage(event)
	case EventError:
		state.closeStreamingText()
		state.Reasoning.Active = false
		state.Footer = ""
		state.Status = statusFromTermination(event.TerminationReason, true)
		if state.Status == StatusFailed {
			state.Error = value(event.Message)
		}
	case EventDone:
		state.closeStreamingText()
		state.Reasoning.Active = false
		state.Footer = ""
		state.Status = statusFromTermination(event.TerminationReason, false)
	case EventSystem:
		if state.Status == StatusQueued {
			state.Status = StatusRunning
			state.Footer = FooterThinking
		}
	}
	return state
}

func MarkCancelled(state RunState) RunState {
	state = cloneState(state)
	state.closeStreamingText()
	state.Reasoning.Active = false
	state.Footer = ""
	state.Status = StatusCancelled
	return state
}

func MarkTimeout(state RunState, minutes int) RunState {
	state = cloneState(state)
	state.closeStreamingText()
	state.Reasoning.Active = false
	state.Footer = ""
	state.Status = StatusTimeout
	state.TimeoutMinutes = minutes
	return state
}

func FinalizeIfRunning(state RunState) RunState {
	state = cloneState(state)
	if state.Status != StatusRunning && state.Status != StatusQueued {
		return state
	}
	state.closeStreamingText()
	state.Reasoning.Active = false
	state.Footer = ""
	state.Status = StatusSucceeded
	return state
}

func (s *RunState) applyMetadata(event Event) {
	if event.RunID != nil {
		s.RunID = *event.RunID
	}
	if event.Scope != nil {
		s.Scope = *event.Scope
	}
	if event.CWD != nil {
		s.CWD = *event.CWD
	}
	if event.SessionID != nil {
		s.SessionID = *event.SessionID
	}
	if event.ThreadID != nil {
		s.ThreadID = *event.ThreadID
	}
	if event.Model != nil {
		s.Model = *event.Model
	}
}

func (s *RunState) applyUsage(event Event) {
	if event.InputTokens != nil {
		s.Usage.InputTokens = *event.InputTokens
	}
	if event.OutputTokens != nil {
		s.Usage.OutputTokens = *event.OutputTokens
	}
	if event.CachedInputTokens != nil {
		s.Usage.CachedInputTokens = *event.CachedInputTokens
	}
	if event.ReasoningOutputTokens != nil {
		s.Usage.ReasoningOutputTokens = *event.ReasoningOutputTokens
	}
	if event.CostUSD != nil {
		v := *event.CostUSD
		s.Usage.CostUSD = &v
	}
}

func (s *RunState) ensureRunning() {
	if s.Status == "" || s.Status == StatusQueued {
		s.Status = StatusRunning
	}
	if s.Footer == "" {
		s.Footer = FooterThinking
	}
}

func (s *RunState) closeStreamingText() {
	for i := range s.Blocks {
		if s.Blocks[i].Kind == BlockText {
			s.Blocks[i].Streaming = false
		}
	}
}

func statusFromTermination(reason TerminationReason, fromError bool) RunStatus {
	switch reason {
	case TerminationInterrupted:
		return StatusCancelled
	case TerminationTimeout:
		return StatusTimeout
	case TerminationNormal, "":
		if fromError {
			return StatusFailed
		}
		return StatusSucceeded
	default:
		return StatusFailed
	}
}

func cloneState(state RunState) RunState {
	if len(state.Blocks) > 0 {
		blocks := make([]Block, len(state.Blocks))
		for i, block := range state.Blocks {
			blocks[i] = block
			if block.Tool != nil {
				tool := *block.Tool
				blocks[i].Tool = &tool
			}
		}
		state.Blocks = blocks
	}
	if state.Usage.CostUSD != nil {
		cost := *state.Usage.CostUSD
		state.Usage.CostUSD = &cost
	}
	return state
}

func value(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func boolValue(v *bool) bool {
	return v != nil && *v
}
