package key

import (
	"errors"
	"fmt"
	"strings"
)

type Llama3Renderer struct{}

func (Llama3Renderer) Name() string {
	return "llama3"
}

func (Llama3Renderer) Render(req map[string]any) (string, error) {
	if _, ok := req["tools"]; ok {
		return "", errors.New("llama3 renderer does not support tools yet")
	}

	if messagesRaw, ok := req["messages"]; ok {
		return renderLlama3Messages(messagesRaw)
	}

	if promptRaw, ok := req["prompt"]; ok {
		return renderPromptValue(promptRaw)
	}

	if inputRaw, ok := req["input"]; ok {
		return renderPromptValue(inputRaw)
	}

	return "", errors.New("request has no messages, prompt, or input")
}

func renderLlama3Messages(v any) (string, error) {
	messages, ok := v.([]any)
	if !ok {
		return "", errors.New("messages must be an array")
	}

	var b strings.Builder
	b.WriteString("<|begin_of_text|>")

	for i, raw := range messages {
		msg, ok := raw.(map[string]any)
		if !ok {
			return "", fmt.Errorf("message %d must be an object", i)
		}

		if _, ok := msg["tool_calls"]; ok {
			return "", errors.New("llama3 renderer does not support tool_calls yet")
		}

		role, ok := msg["role"].(string)
		if !ok || role == "" {
			return "", fmt.Errorf("message %d missing role", i)
		}

		content, ok := msg["content"]
		if !ok {
			content = ""
		}

		renderedContent, err := renderContent(content)
		if err != nil {
			return "", fmt.Errorf("message %d content: %w", i, err)
		}

		b.WriteString("<|start_header_id|>")
		b.WriteString(role)
		b.WriteString("<|end_header_id|>\n\n")
		b.WriteString(renderedContent)
		b.WriteString("<|eot_id|>")
	}

	// Equivalent to add_generation_prompt=true for normal chat completion.
	b.WriteString("<|start_header_id|>assistant<|end_header_id|>\n\n")

	return b.String(), nil
}
