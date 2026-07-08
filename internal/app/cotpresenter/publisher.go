package cotpresenter

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	agentport "github.com/zarazhangrui/lark-coding-agent-bridge/internal/ports/agent"
)

const (
	defaultUpdateThrottle = 600 * time.Millisecond
	toolOutputMax         = 1200
	textMax               = 1200
)

type PublisherOptions struct {
	Client          Client
	ChatID          string
	OriginMessageID string
	RunID           string
	Scope           string
	InputPreview    string
	UpdateThrottle  time.Duration
	Now             func() time.Time
}

type Publisher struct {
	client          Client
	chatID          string
	originMessageID string
	runID           string
	scope           string
	inputPreview    string
	updateThrottle  time.Duration
	now             func() time.Time

	mu             sync.Mutex
	ref            Ref
	disabled       bool
	degradedReason string
	buffer         []Event
	timer          *time.Timer
	flushing       bool
}

func NewPublisher(opts PublisherOptions) *Publisher {
	throttle := opts.UpdateThrottle
	if throttle <= 0 {
		throttle = defaultUpdateThrottle
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Publisher{
		client:          opts.Client,
		chatID:          opts.ChatID,
		originMessageID: opts.OriginMessageID,
		runID:           opts.RunID,
		scope:           opts.Scope,
		inputPreview:    opts.InputPreview,
		updateThrottle:  throttle,
		now:             now,
	}
}

func (p *Publisher) Start(ctx context.Context) bool {
	if p == nil || p.client == nil {
		return false
	}
	ref, err := p.client.CreateMessageCOT(ctx, CreateRequest{
		ReceiveID:       p.chatID,
		OriginMessageID: p.originMessageID,
	})
	if err != nil || ref.COTID == "" || ref.MessageID == "" {
		p.mu.Lock()
		p.disabled = true
		if err != nil {
			p.degradedReason = err.Error()
		} else {
			p.degradedReason = "CreateCOT missing ids"
		}
		p.mu.Unlock()
		return false
	}
	p.mu.Lock()
	p.ref = ref
	p.mu.Unlock()
	p.Enqueue("RUN_STARTED", map[string]any{
		"threadId": p.scope,
		"runId":    p.runID,
		"input":    map[string]any{"query": p.inputPreview},
	})
	p.Enqueue("STEP_STARTED", map[string]any{
		"stepId":   "step-understand-" + p.runID,
		"stepName": "理解用户问题",
	})
	return true
}

func (p *Publisher) Disabled() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.disabled
}

func (p *Publisher) DegradedReason() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.degradedReason
}

func (p *Publisher) Enqueue(eventType string, content any) {
	if p == nil {
		return
	}
	payload, err := json.Marshal(content)
	if err != nil {
		payload = []byte(fmt.Sprint(content))
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.disabled || p.ref.COTID == "" || p.ref.MessageID == "" {
		return
	}
	p.buffer = append(p.buffer, Event{
		EventType: eventType,
		Content:   string(payload),
		Timestamp: p.now().UnixMilli(),
	})
	if p.timer == nil && !p.flushing {
		p.timer = time.AfterFunc(p.updateThrottle, func() {
			_ = p.Flush(context.Background())
		})
	}
}

func (p *Publisher) Finish(ctx context.Context, reason string) error {
	if p == nil {
		return nil
	}
	p.stopTimer()
	_ = p.Flush(ctx)
	p.mu.Lock()
	disabled := p.disabled
	ref := p.ref
	p.mu.Unlock()
	if disabled || ref.COTID == "" || ref.MessageID == "" {
		return nil
	}
	if strings.TrimSpace(reason) == "" {
		reason = "done"
	}
	_ = p.client.CompleteMessageCOT(ctx, CompleteRequest{Ref: ref, Reason: reason})
	return nil
}

func (p *Publisher) Flush(ctx context.Context) error {
	if p == nil || p.client == nil {
		return nil
	}
	for {
		p.mu.Lock()
		if p.flushing {
			p.mu.Unlock()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Millisecond):
				continue
			}
		}
		if p.disabled || p.ref.COTID == "" || p.ref.MessageID == "" || len(p.buffer) == 0 {
			p.mu.Unlock()
			return nil
		}
		p.flushing = true
		ref := p.ref
		events := append([]Event(nil), p.buffer...)
		p.buffer = nil
		if p.timer != nil {
			p.timer.Stop()
			p.timer = nil
		}
		p.mu.Unlock()

		err := p.client.UpdateMessageCOT(ctx, UpdateRequest{Ref: ref, Events: events})

		p.mu.Lock()
		p.flushing = false
		if err != nil {
			p.disabled = true
			p.degradedReason = err.Error()
			p.mu.Unlock()
			return err
		}
		hasMore := len(p.buffer) > 0 && !p.disabled
		p.mu.Unlock()
		if !hasMore {
			return nil
		}
	}
}

func (p *Publisher) stopTimer() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.timer != nil {
		p.timer.Stop()
		p.timer = nil
	}
}

func ConsumeEvents(ctx context.Context, events <-chan agentport.AgentEvent, publisher *Publisher, mode Mode) error {
	if publisher == nil {
		return nil
	}
	reasoningOpen := false
	textStepOpen := false
	textMessageOpen := false
	textMessageIndex := 0
	textMessageID := ""
	toolBrief := map[string]struct {
		name  string
		input any
	}{}
	reasoningMessageID := "reasoning-" + publisher.runID
	finalStepID := "step-process-" + publisher.runID

	closeReasoning := func() {
		if !reasoningOpen {
			return
		}
		reasoningOpen = false
		publisher.Enqueue("REASONING_MESSAGE_END", map[string]any{"messageId": reasoningMessageID})
		publisher.Enqueue("REASONING_END", map[string]any{"messageId": reasoningMessageID})
	}
	closeText := func() {
		if !textMessageOpen || textMessageID == "" {
			return
		}
		publisher.Enqueue("TEXT_MESSAGE_END", map[string]any{"messageId": textMessageID})
		textMessageOpen = false
		textMessageID = ""
	}

	finish := func(reason string) error {
		return publisher.Finish(ctx, reason)
	}

	for {
		select {
		case <-ctx.Done():
			closeReasoning()
			closeText()
			_ = finish("error")
			return ctx.Err()
		case evt, ok := <-events:
			if !ok {
				closeReasoning()
				closeText()
				return finish("done")
			}
			switch evt.Type {
			case agentport.EventSystem, agentport.EventUsage:
				continue
			case agentport.EventThinking:
				closeText()
				if !reasoningOpen {
					reasoningOpen = true
					publisher.Enqueue("REASONING_START", map[string]any{"messageId": reasoningMessageID})
					publisher.Enqueue("REASONING_MESSAGE_START", map[string]any{
						"messageId": reasoningMessageID,
						"role":      "reasoning",
					})
				}
				publisher.Enqueue("REASONING_MESSAGE_CONTENT", map[string]any{
					"messageId": reasoningMessageID,
					"delta":     truncate(value(evt.Delta), textMax),
				})
			case agentport.EventToolUse:
				closeReasoning()
				closeText()
				id := value(evt.ID)
				name := value(evt.Name)
				detailed := mode == ModeDetailed
				showSummary := mode == ModeBrief || detailed
				title := "正在调用工具"
				toolCallName := "tool"
				icon := "default"
				if showSummary {
					title = briefToolTitle(name, evt.Input, "running")
					toolCallName = name
					icon = toolIcon(name)
				}
				toolBrief[id] = struct {
					name  string
					input any
				}{name: name, input: evt.Input}
				publisher.Enqueue("TOOL_CALL_START", map[string]any{
					"toolCallId":   id,
					"icon":         icon,
					"title":        title,
					"toolCallName": toolCallName,
				})
				if detailed && evt.Input != nil {
					inputJSON, _ := json.Marshal(evt.Input)
					publisher.Enqueue("TOOL_CALL_ARGS", map[string]any{
						"toolCallId": id,
						"delta":      string(inputJSON),
					})
				}
				publisher.Enqueue("TOOL_CALL_END", map[string]any{"toolCallId": id})
			case agentport.EventToolResult:
				id := value(evt.ID)
				content := "工具调用已完成"
				if mode == ModeDetailed {
					content = truncate(value(evt.Output), toolOutputMax)
				} else if brief, ok := toolBrief[id]; ok {
					status := "done"
					if boolValue(evt.IsError) {
						status = "error"
					}
					content = briefToolTitle(brief.name, brief.input, status)
				}
				publisher.Enqueue("TOOL_CALL_RESULT", map[string]any{
					"messageId":  "tool-result-" + id,
					"toolCallId": id,
					"role":       "tool",
					"content":    content,
				})
				delete(toolBrief, id)
			case agentport.EventText:
				closeReasoning()
				if !textStepOpen {
					textStepOpen = true
					publisher.Enqueue("STEP_STARTED", map[string]any{
						"stepId":   finalStepID,
						"stepName": "输出过程",
					})
				}
				if !textMessageOpen {
					textMessageOpen = true
					textMessageIndex++
					textMessageID = fmt.Sprintf("text-%s-%d", publisher.runID, textMessageIndex)
					publisher.Enqueue("TEXT_MESSAGE_START", map[string]any{
						"messageId": textMessageID,
						"role":      "assistant",
					})
				}
				publisher.Enqueue("TEXT_MESSAGE_CONTENT", map[string]any{
					"messageId": textMessageID,
					"delta":     truncate(value(evt.Delta), textMax),
				})
			case agentport.EventDone, agentport.EventError:
				closeReasoning()
				closeText()
				if textStepOpen {
					publisher.Enqueue("STEP_FINISHED", map[string]any{
						"stepId":   finalStepID,
						"stepName": "输出过程",
					})
				}
				if evt.Type == agentport.EventError {
					publisher.Enqueue("RUN_ERROR", map[string]any{
						"message": value(evt.Message),
						"code":    string(evt.TerminationReason),
					})
					return finish("error")
				}
				status := string(evt.TerminationReason)
				if status == "" || evt.TerminationReason == agentport.TerminationNormal {
					status = "done"
				}
				publisher.Enqueue("RUN_FINISHED", map[string]any{
					"threadId": publisher.scope,
					"runId":    publisher.runID,
					"status":   status,
				})
				if status == "done" {
					return finish("done")
				}
				return finish("error")
			}
		}
	}
}

func truncate(input string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(input)
	if len(runes) <= max {
		return input
	}
	return string(runes[:max]) + "..."
}

func value(input *string) string {
	if input == nil {
		return ""
	}
	return *input
}

func boolValue(input *bool) bool {
	return input != nil && *input
}

func briefToolTitle(name string, input any, status string) string {
	icon := "⏳"
	if status == "done" {
		icon = "✅"
	} else if status == "error" {
		icon = "❌"
	}
	summary := summarizeInput(name, input)
	if summary != "" {
		return fmt.Sprintf("%s %s — %s", icon, name, summary)
	}
	if name == "" {
		name = "tool"
	}
	return fmt.Sprintf("%s %s", icon, name)
}

func summarizeInput(name string, input any) string {
	rec, ok := input.(map[string]any)
	if !ok {
		return ""
	}
	str := func(key string) string {
		if v, ok := rec[key].(string); ok {
			return v
		}
		return ""
	}
	switch name {
	case "Bash", "command_execution":
		return truncate(str("command"), 80)
	case "Read", "Edit", "Write", "NotebookEdit":
		return str("file_path")
	case "Grep":
		parts := []string{}
		if pattern := str("pattern"); pattern != "" {
			parts = append(parts, "pattern="+pattern)
		}
		if path := str("path"); path != "" {
			parts = append(parts, "path="+path)
		}
		return truncate(strings.Join(parts, " "), 80)
	case "WebFetch":
		return str("url")
	case "WebSearch":
		return truncate(str("query"), 80)
	default:
		return ""
	}
}

func toolIcon(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "search"), strings.Contains(lower, "grep"), strings.Contains(lower, "rg"):
		return "search"
	case strings.Contains(lower, "read"):
		return "read"
	case strings.Contains(lower, "write"), strings.Contains(lower, "edit"):
		return "write"
	case strings.Contains(lower, "doc"):
		return "doc"
	case strings.Contains(lower, "calendar"):
		return "calendar"
	case strings.Contains(lower, "task"):
		return "task"
	case strings.Contains(lower, "command"), strings.Contains(lower, "bash"):
		return "bash"
	default:
		return "default"
	}
}
