package prompt

import (
	"encoding/json"
	"fmt"
	"strings"
)

type userInputSection struct {
	Text        string                   `json:"text"`
	Attachments []BridgePromptAttachment `json:"attachments,omitempty"`
}

func BuildAgentPrompt(input BuildAgentPromptInput) string {
	sections := []string{
		PromptSection("bridge_context", input.Context),
	}

	if len(input.Instructions) > 0 {
		sections = append(sections, PromptSection("bridge_instructions", input.Instructions))
	}
	if len(input.QuotedMessages) > 0 {
		sections = append(sections, PromptSection("quoted_messages", input.QuotedMessages))
	}
	if len(input.InteractiveCards) > 0 {
		sections = append(sections, PromptSection("interactive_cards", input.InteractiveCards))
	}
	if input.Comment != nil {
		sections = append(sections, PromptSection("comment_context", input.Comment))
	}

	sections = append(sections, PromptSection("user_input", userInputSection{
		Text:        input.UserInput,
		Attachments: input.Attachments,
	}))

	return strings.Join(sections, "\n\n")
}

func PromptSection(tag string, value any) string {
	return fmt.Sprintf("<%s>\n%s\n</%s>", tag, SafeJSONStringify(value), tag)
}

func SafeJSONStringify(value any) string {
	bytes, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Errorf("prompt json stringify: %w", err))
	}
	return string(bytes)
}
