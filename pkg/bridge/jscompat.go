package bridge

import "encoding/json"

type ToolStatus = RunCardToolStatus
type ToolEntry = RunCardToolEntry
type Block = RunCardBlock
type FooterStatus = RunCardFooterStatus

type Terminal string

const (
	ToolStatusRunning ToolStatus = RunCardToolRunning
	ToolStatusDone    ToolStatus = RunCardToolDone
	ToolStatusError   ToolStatus = RunCardToolError

	FooterThinking    FooterStatus = RunCardFooterThinking
	FooterToolRunning FooterStatus = RunCardFooterToolRunning
	FooterStreaming   FooterStatus = RunCardFooterStreaming

	TerminalRunning     Terminal = "running"
	TerminalDone        Terminal = "done"
	TerminalInterrupted Terminal = "interrupted"
	TerminalError       Terminal = "error"
	TerminalIdleTimeout Terminal = "idle_timeout"
)

// RunState mirrors the JavaScript package-root RunState shape. New Go callers
// can use the richer RunCardState facade directly; this type is kept so a JS
// consumer can port rendering/state code without learning the card-specific
// status vocabulary first.
type RunState struct {
	Blocks             []Block          `json:"blocks"`
	Reasoning          RunCardReasoning `json:"reasoning"`
	Footer             FooterStatus     `json:"footer,omitempty"`
	Terminal           Terminal         `json:"terminal"`
	ErrorMsg           string           `json:"errorMsg,omitempty"`
	IdleTimeoutMinutes int              `json:"idleTimeoutMinutes,omitempty"`
}

func (s RunState) MarshalJSON() ([]byte, error) {
	type runStateJSON struct {
		Blocks             []Block          `json:"blocks"`
		Reasoning          RunCardReasoning `json:"reasoning"`
		Footer             *FooterStatus    `json:"footer"`
		Terminal           Terminal         `json:"terminal"`
		ErrorMsg           string           `json:"errorMsg,omitempty"`
		IdleTimeoutMinutes int              `json:"idleTimeoutMinutes,omitempty"`
	}
	blocks := s.Blocks
	if blocks == nil {
		blocks = []Block{}
	}
	var footer *FooterStatus
	if s.Footer != "" {
		value := s.Footer
		footer = &value
	}
	return json.Marshal(runStateJSON{
		Blocks:             blocks,
		Reasoning:          s.Reasoning,
		Footer:             footer,
		Terminal:           s.Terminal,
		ErrorMsg:           s.ErrorMsg,
		IdleTimeoutMinutes: s.IdleTimeoutMinutes,
	})
}

func InitialState() RunState {
	return RunStateFromRunCardState(InitialRunCardState())
}

func Reduce(state RunState, event Event) RunState {
	return RunStateFromRunCardState(ReduceRunCardState(RunStateToRunCardState(state), event))
}

func FinalizeIfRunning(state RunState) RunState {
	return RunStateFromRunCardState(FinalizeRunCardIfRunning(RunStateToRunCardState(state)))
}

func MarkInterrupted(state RunState) RunState {
	return RunStateFromRunCardState(MarkRunCardInterrupted(RunStateToRunCardState(state)))
}

func MarkIdleTimeout(state RunState, minutes int) RunState {
	return RunStateFromRunCardState(MarkRunCardTimeout(RunStateToRunCardState(state), minutes))
}

func RenderCard(state RunState, options CardRenderOptions) CardKitJSON {
	return RenderRunCardKit(RunStateToRunCardState(state), options)
}

func RenderText(state RunState) string {
	return RenderRunText(RunStateToRunCardState(state)).Content
}

func RunStateFromRunCardState(state RunCardState) RunState {
	blocks := state.Blocks
	if blocks == nil {
		blocks = []Block{}
	}
	return RunState{
		Blocks:             blocks,
		Reasoning:          state.Reasoning,
		Footer:             FooterStatus(state.Footer),
		Terminal:           terminalFromRunCardStatus(state.Status),
		ErrorMsg:           state.Error,
		IdleTimeoutMinutes: state.TimeoutMinutes,
	}
}

func RunStateToRunCardState(state RunState) RunCardState {
	status := runCardStatusFromTerminal(state.Terminal)
	footer := RunCardFooterStatus(state.Footer)
	if state.Terminal == "" {
		status = RunCardRunning
	}
	if status != RunCardRunning && status != RunCardQueued {
		footer = ""
	}
	return RunCardState{
		Status:         status,
		Blocks:         state.Blocks,
		Reasoning:      state.Reasoning,
		Footer:         footer,
		Error:          state.ErrorMsg,
		TimeoutMinutes: state.IdleTimeoutMinutes,
	}
}

func terminalFromRunCardStatus(status RunCardStatus) Terminal {
	switch status {
	case RunCardSucceeded:
		return TerminalDone
	case RunCardCancelled:
		return TerminalInterrupted
	case RunCardFailed:
		return TerminalError
	case RunCardTimeout:
		return TerminalIdleTimeout
	default:
		return TerminalRunning
	}
}

func runCardStatusFromTerminal(terminal Terminal) RunCardStatus {
	switch terminal {
	case TerminalDone:
		return RunCardSucceeded
	case TerminalInterrupted:
		return RunCardCancelled
	case TerminalError:
		return RunCardFailed
	case TerminalIdleTimeout:
		return RunCardTimeout
	default:
		return RunCardRunning
	}
}
