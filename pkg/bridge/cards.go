package bridge

import (
	"time"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/cardrender"
)

type RunCardStatus string

const (
	RunCardQueued    RunCardStatus = "queued"
	RunCardRunning   RunCardStatus = "running"
	RunCardSucceeded RunCardStatus = "succeeded"
	RunCardFailed    RunCardStatus = "failed"
	RunCardCancelled RunCardStatus = "cancelled"
	RunCardTimeout   RunCardStatus = "timeout"
)

type RunCardFooterStatus string

const (
	RunCardFooterThinking    RunCardFooterStatus = "thinking"
	RunCardFooterToolRunning RunCardFooterStatus = "tool_running"
	RunCardFooterStreaming   RunCardFooterStatus = "streaming"
)

type RunCardToolStatus string

const (
	RunCardToolRunning RunCardToolStatus = "running"
	RunCardToolDone    RunCardToolStatus = "done"
	RunCardToolError   RunCardToolStatus = "error"
)

type RunCardBlockKind string

const (
	RunCardBlockText RunCardBlockKind = "text"
	RunCardBlockTool RunCardBlockKind = "tool"
)

type RunCardActionStyle string

const (
	RunCardActionDefault RunCardActionStyle = "default"
	RunCardActionPrimary RunCardActionStyle = "primary"
	RunCardActionDanger  RunCardActionStyle = "danger"
)

type RunCardUsage struct {
	InputTokens           int      `json:"inputTokens,omitempty"`
	OutputTokens          int      `json:"outputTokens,omitempty"`
	CachedInputTokens     int      `json:"cachedInputTokens,omitempty"`
	ReasoningOutputTokens int      `json:"reasoningOutputTokens,omitempty"`
	CostUSD               *float64 `json:"costUsd,omitempty"`
}

func (u RunCardUsage) TotalTokens() int {
	return u.InputTokens + u.OutputTokens + u.CachedInputTokens + u.ReasoningOutputTokens
}

func (u RunCardUsage) Empty() bool {
	return u.InputTokens == 0 &&
		u.OutputTokens == 0 &&
		u.CachedInputTokens == 0 &&
		u.ReasoningOutputTokens == 0 &&
		u.CostUSD == nil
}

type RunCardToolEntry struct {
	ID     string            `json:"id"`
	Name   string            `json:"name"`
	Input  any               `json:"input,omitempty"`
	Status RunCardToolStatus `json:"status"`
	Output string            `json:"output,omitempty"`
}

type RunCardBlock struct {
	Kind      RunCardBlockKind  `json:"kind"`
	Content   string            `json:"content,omitempty"`
	Streaming bool              `json:"streaming,omitempty"`
	Tool      *RunCardToolEntry `json:"tool,omitempty"`
}

type RunCardReasoning struct {
	Content string `json:"content,omitempty"`
	Active  bool   `json:"active"`
}

type RunCardState struct {
	RunID     string              `json:"runId,omitempty"`
	Scope     string              `json:"scope,omitempty"`
	CWD       string              `json:"cwd,omitempty"`
	SessionID string              `json:"sessionId,omitempty"`
	ThreadID  string              `json:"threadId,omitempty"`
	Model     string              `json:"model,omitempty"`
	Status    RunCardStatus       `json:"status"`
	Blocks    []RunCardBlock      `json:"blocks,omitempty"`
	Reasoning RunCardReasoning    `json:"reasoning"`
	Footer    RunCardFooterStatus `json:"footer,omitempty"`
	Error     string              `json:"error,omitempty"`
	Usage     RunCardUsage        `json:"usage,omitempty"`
	LastEvent string              `json:"lastEvent,omitempty"`
	StartedAt time.Time           `json:"startedAt,omitempty"`
	UpdatedAt time.Time           `json:"updatedAt,omitempty"`
	Elapsed   time.Duration       `json:"elapsed,omitempty"`

	TimeoutMinutes int `json:"timeoutMinutes,omitempty"`
}

type RunCardStateInput struct {
	RunID     string
	Scope     string
	CWD       string
	SessionID string
	ThreadID  string
	Model     string
	Status    RunCardStatus
	StartedAt time.Time
	UpdatedAt time.Time
	Elapsed   time.Duration
}

type CardView struct {
	Schema    string        `json:"schema"`
	Status    RunCardStatus `json:"status"`
	Summary   string        `json:"summary"`
	Streaming bool          `json:"streaming"`
	Elements  []CardElement `json:"elements"`
	Actions   []CardAction  `json:"actions,omitempty"`
}

type CardElementKind string

const (
	CardElementMarkdown CardElementKind = "markdown"
	CardElementNote     CardElementKind = "note"
	CardElementPanel    CardElementKind = "panel"
)

type CardElement struct {
	Kind     CardElementKind `json:"kind"`
	Text     string          `json:"text,omitempty"`
	TextSize string          `json:"textSize,omitempty"`
	Panel    *PanelView      `json:"panel,omitempty"`
}

type PanelView struct {
	Title    string `json:"title"`
	Body     string `json:"body"`
	Expanded bool   `json:"expanded"`
	Border   string `json:"border"`
}

type TextView struct {
	Status  RunCardStatus `json:"status"`
	Summary string        `json:"summary"`
	Content string        `json:"content"`
}

type CardAction struct {
	ID    string             `json:"id,omitempty"`
	Text  string             `json:"text"`
	Style RunCardActionStyle `json:"style,omitempty"`
	Value map[string]any     `json:"value,omitempty"`
}

type CardRenderOptions struct {
	SignCallback func(action string) string
	ShowMetadata bool
}

type CardActionParseInput struct {
	Value     map[string]any
	Raw       any
	FormValue map[string]any
}

type ParsedCardAction struct {
	Command        string         `json:"command,omitempty"`
	BridgeCallback bool           `json:"bridgeCallback"`
	LegacyCallback bool           `json:"legacyCallback"`
	BridgeToken    string         `json:"bridgeToken,omitempty"`
	Value          map[string]any `json:"value,omitempty"`
	FormValue      map[string]any `json:"formValue,omitempty"`
	AgentPayload   map[string]any `json:"agentPayload,omitempty"`
}

func InitialRunCardState() RunCardState {
	return RunCardState{
		Status: RunCardRunning,
		Reasoning: RunCardReasoning{
			Content: "",
			Active:  false,
		},
		Footer: RunCardFooterThinking,
	}
}

func NewRunCardState(input RunCardStateInput) RunCardState {
	return fromInternalRunCardState(cardrender.NewRunState(cardrender.RunStateInput{
		RunID:     input.RunID,
		Scope:     input.Scope,
		CWD:       input.CWD,
		SessionID: input.SessionID,
		ThreadID:  input.ThreadID,
		Model:     input.Model,
		Status:    cardrender.RunStatus(input.Status),
		StartedAt: input.StartedAt,
		UpdatedAt: input.UpdatedAt,
		Elapsed:   input.Elapsed,
	}))
}

func ReduceRunCardState(state RunCardState, event Event) RunCardState {
	return fromInternalRunCardState(cardrender.Reduce(toInternalRunCardState(state), toCardEvent(event)))
}

func MarkRunCardCancelled(state RunCardState) RunCardState {
	return fromInternalRunCardState(cardrender.MarkCancelled(toInternalRunCardState(state)))
}

func MarkRunCardInterrupted(state RunCardState) RunCardState {
	return MarkRunCardCancelled(state)
}

func MarkRunCardTimeout(state RunCardState, minutes int) RunCardState {
	return fromInternalRunCardState(cardrender.MarkTimeout(toInternalRunCardState(state), minutes))
}

func FinalizeRunCardIfRunning(state RunCardState) RunCardState {
	return fromInternalRunCardState(cardrender.FinalizeIfRunning(toInternalRunCardState(state)))
}

func RenderRunCard(state RunCardState, options CardRenderOptions) CardView {
	return fromInternalCardView(cardrender.RenderCard(toInternalRunCardState(state), toInternalCardRenderOptions(options)))
}

func RenderRunText(state RunCardState) TextView {
	return fromInternalTextView(cardrender.RenderText(toInternalRunCardState(state)))
}

func RunCardSummary(state RunCardState) string {
	return cardrender.SummaryText(toInternalRunCardState(state))
}

func ParseCardAction(input CardActionParseInput) ParsedCardAction {
	return fromInternalParsedCardAction(cardrender.ParseAction(cardrender.ActionParseInput{
		Value:     input.Value,
		Raw:       input.Raw,
		FormValue: input.FormValue,
	}))
}

func IsBridgeCardCallback(value map[string]any) bool {
	return cardrender.IsBridgeCallback(value)
}

func IsLegacyCardCallback(value map[string]any) bool {
	return cardrender.IsLegacyCallback(value)
}

func toCardEvent(event Event) cardrender.Event {
	return cardrender.Event{
		Type:                  cardrender.EventType(event.Type),
		At:                    event.At,
		RunID:                 event.RunID,
		Scope:                 event.ScopeID,
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

func toInternalRunCardState(state RunCardState) cardrender.RunState {
	return cardrender.RunState{
		RunID:          state.RunID,
		Scope:          state.Scope,
		CWD:            state.CWD,
		SessionID:      state.SessionID,
		ThreadID:       state.ThreadID,
		Model:          state.Model,
		Status:         cardrender.RunStatus(state.Status),
		Blocks:         toInternalRunCardBlocks(state.Blocks),
		Reasoning:      cardrender.Reasoning{Content: state.Reasoning.Content, Active: state.Reasoning.Active},
		Footer:         cardrender.FooterStatus(state.Footer),
		Error:          state.Error,
		Usage:          toInternalRunCardUsage(state.Usage),
		LastEvent:      state.LastEvent,
		StartedAt:      state.StartedAt,
		UpdatedAt:      state.UpdatedAt,
		Elapsed:        state.Elapsed,
		TimeoutMinutes: state.TimeoutMinutes,
	}
}

func fromInternalRunCardState(state cardrender.RunState) RunCardState {
	return RunCardState{
		RunID:          state.RunID,
		Scope:          state.Scope,
		CWD:            state.CWD,
		SessionID:      state.SessionID,
		ThreadID:       state.ThreadID,
		Model:          state.Model,
		Status:         RunCardStatus(state.Status),
		Blocks:         fromInternalRunCardBlocks(state.Blocks),
		Reasoning:      RunCardReasoning{Content: state.Reasoning.Content, Active: state.Reasoning.Active},
		Footer:         RunCardFooterStatus(state.Footer),
		Error:          state.Error,
		Usage:          fromInternalRunCardUsage(state.Usage),
		LastEvent:      state.LastEvent,
		StartedAt:      state.StartedAt,
		UpdatedAt:      state.UpdatedAt,
		Elapsed:        state.Elapsed,
		TimeoutMinutes: state.TimeoutMinutes,
	}
}

func toInternalRunCardUsage(usage RunCardUsage) cardrender.Usage {
	return cardrender.Usage{
		InputTokens:           usage.InputTokens,
		OutputTokens:          usage.OutputTokens,
		CachedInputTokens:     usage.CachedInputTokens,
		ReasoningOutputTokens: usage.ReasoningOutputTokens,
		CostUSD:               usage.CostUSD,
	}
}

func fromInternalRunCardUsage(usage cardrender.Usage) RunCardUsage {
	return RunCardUsage{
		InputTokens:           usage.InputTokens,
		OutputTokens:          usage.OutputTokens,
		CachedInputTokens:     usage.CachedInputTokens,
		ReasoningOutputTokens: usage.ReasoningOutputTokens,
		CostUSD:               usage.CostUSD,
	}
}

func toInternalRunCardBlocks(blocks []RunCardBlock) []cardrender.Block {
	if len(blocks) == 0 {
		return nil
	}
	out := make([]cardrender.Block, 0, len(blocks))
	for _, block := range blocks {
		out = append(out, cardrender.Block{
			Kind:      cardrender.BlockKind(block.Kind),
			Content:   block.Content,
			Streaming: block.Streaming,
			Tool:      toInternalRunCardTool(block.Tool),
		})
	}
	return out
}

func fromInternalRunCardBlocks(blocks []cardrender.Block) []RunCardBlock {
	if len(blocks) == 0 {
		return nil
	}
	out := make([]RunCardBlock, 0, len(blocks))
	for _, block := range blocks {
		out = append(out, RunCardBlock{
			Kind:      RunCardBlockKind(block.Kind),
			Content:   block.Content,
			Streaming: block.Streaming,
			Tool:      fromInternalRunCardTool(block.Tool),
		})
	}
	return out
}

func toInternalRunCardTool(tool *RunCardToolEntry) *cardrender.ToolEntry {
	if tool == nil {
		return nil
	}
	return &cardrender.ToolEntry{
		ID:     tool.ID,
		Name:   tool.Name,
		Input:  tool.Input,
		Status: cardrender.ToolStatus(tool.Status),
		Output: tool.Output,
	}
}

func fromInternalRunCardTool(tool *cardrender.ToolEntry) *RunCardToolEntry {
	if tool == nil {
		return nil
	}
	return &RunCardToolEntry{
		ID:     tool.ID,
		Name:   tool.Name,
		Input:  tool.Input,
		Status: RunCardToolStatus(tool.Status),
		Output: tool.Output,
	}
}

func toInternalCardRenderOptions(options CardRenderOptions) cardrender.RenderOptions {
	return cardrender.RenderOptions{
		SignCallback: options.SignCallback,
		ShowMetadata: options.ShowMetadata,
	}
}

func fromInternalCardView(view cardrender.CardView) CardView {
	return CardView{
		Schema:    view.Schema,
		Status:    RunCardStatus(view.Status),
		Summary:   view.Summary,
		Streaming: view.Streaming,
		Elements:  fromInternalCardElements(view.Elements),
		Actions:   fromInternalCardActions(view.Actions),
	}
}

func fromInternalCardElements(elements []cardrender.CardElement) []CardElement {
	if len(elements) == 0 {
		return nil
	}
	out := make([]CardElement, 0, len(elements))
	for _, element := range elements {
		out = append(out, CardElement{
			Kind:     CardElementKind(element.Kind),
			Text:     element.Text,
			TextSize: element.TextSize,
			Panel:    fromInternalPanelView(element.Panel),
		})
	}
	return out
}

func fromInternalPanelView(panel *cardrender.PanelView) *PanelView {
	if panel == nil {
		return nil
	}
	return &PanelView{
		Title:    panel.Title,
		Body:     panel.Body,
		Expanded: panel.Expanded,
		Border:   panel.Border,
	}
}

func toInternalPanelView(panel *PanelView) *cardrender.PanelView {
	if panel == nil {
		return nil
	}
	return &cardrender.PanelView{
		Title:    panel.Title,
		Body:     panel.Body,
		Expanded: panel.Expanded,
		Border:   panel.Border,
	}
}

func fromInternalCardActions(actions []cardrender.Action) []CardAction {
	if len(actions) == 0 {
		return nil
	}
	out := make([]CardAction, 0, len(actions))
	for _, action := range actions {
		out = append(out, CardAction{
			ID:    action.ID,
			Text:  action.Text,
			Style: RunCardActionStyle(action.Style),
			Value: action.Value,
		})
	}
	return out
}

func toInternalCardView(view CardView) cardrender.CardView {
	return cardrender.CardView{
		Schema:    view.Schema,
		Status:    cardrender.RunStatus(view.Status),
		Summary:   view.Summary,
		Streaming: view.Streaming,
		Elements:  toInternalCardElements(view.Elements),
		Actions:   toInternalCardActions(view.Actions),
	}
}

func toInternalCardElements(elements []CardElement) []cardrender.CardElement {
	if len(elements) == 0 {
		return nil
	}
	out := make([]cardrender.CardElement, 0, len(elements))
	for _, element := range elements {
		out = append(out, cardrender.CardElement{
			Kind:     cardrender.ElementKind(element.Kind),
			Text:     element.Text,
			TextSize: element.TextSize,
			Panel:    toInternalPanelView(element.Panel),
		})
	}
	return out
}

func toInternalCardActions(actions []CardAction) []cardrender.Action {
	if len(actions) == 0 {
		return nil
	}
	out := make([]cardrender.Action, 0, len(actions))
	for _, action := range actions {
		out = append(out, cardrender.Action{
			ID:    action.ID,
			Text:  action.Text,
			Style: cardrender.ActionStyle(action.Style),
			Value: action.Value,
		})
	}
	return out
}

func fromInternalTextView(view cardrender.TextView) TextView {
	return TextView{
		Status:  RunCardStatus(view.Status),
		Summary: view.Summary,
		Content: view.Content,
	}
}

func fromInternalParsedCardAction(parsed cardrender.ParsedAction) ParsedCardAction {
	return ParsedCardAction{
		Command:        parsed.Command,
		BridgeCallback: parsed.BridgeCallback,
		LegacyCallback: parsed.LegacyCallback,
		BridgeToken:    parsed.BridgeToken,
		Value:          parsed.Value,
		FormValue:      parsed.FormValue,
		AgentPayload:   parsed.AgentPayload,
	}
}
