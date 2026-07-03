package key

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const llama3DefaultDateString = "02 Jul 2026"

type Llama3Renderer struct{}

func (Llama3Renderer) Name() string {
	return "llama3"
}

func (Llama3Renderer) Render(req map[string]any) (string, error) {
	if messagesRaw, ok := req["messages"]; ok {
		return renderLlama3Messages(req, messagesRaw)
	}

	if promptRaw, ok := req["prompt"]; ok {
		return renderPromptValue(promptRaw)
	}

	if inputRaw, ok := req["input"]; ok {
		return renderPromptValue(inputRaw)
	}

	return "", errors.New("request has no messages, prompt, or input")
}

func renderLlama3Messages(req map[string]any, v any) (string, error) {
	messages, ok := v.([]any)
	if !ok {
		return "", errors.New("messages must be an array")
	}

	tools, hasTools := req["tools"]
	if !hasTools {
		tools = nil
	}

	toolsInUserMessage := true

	systemMessage := ""
	if len(messages) > 0 {
		first, ok := messages[0].(map[string]any)
		if !ok {
			return "", errors.New("message 0 must be an object")
		}

		if role, _ := first["role"].(string); role == "system" {
			systemMessage = strings.TrimSpace(valueToText(first["content"]))
			messages = messages[1:]
		}
	}

	var b strings.Builder

	b.WriteString("<|begin_of_text|>")
	b.WriteString("<|start_header_id|>system<|end_header_id|>\n\n")

	if hasTools && tools != nil {
		b.WriteString("Environment: ipython\n")
	}

	b.WriteString("Cutting Knowledge Date: December 2023\n")
	b.WriteString("Today Date: ")
	b.WriteString(llama3DefaultDateString)
	b.WriteString("\n\n")

	if hasTools && tools != nil && !toolsInUserMessage {
		b.WriteString("You have access to the following functions. To call a function, please respond with JSON for a function call.")
		b.WriteString(`Respond in the format {"name": function name, "parameters": dictionary of argument name and its value}.`)
		b.WriteString("Do not use variables.\n\n")

		renderedTools, err := jsonIndent(tools, 4)
		if err != nil {
			return "", err
		}
		b.WriteString(renderedTools)
		b.WriteString("\n\n")
	}

	b.WriteString(systemMessage)
	b.WriteString("<|eot_id|>")

	if hasTools && tools != nil && toolsInUserMessage {
		if len(messages) == 0 {
			return "", errors.New("cannot put tools in first user message when there is no user message")
		}

		first, ok := messages[0].(map[string]any)
		if !ok {
			return "", errors.New("first user message must be an object")
		}

		if role, _ := first["role"].(string); role != "user" {
			return "", errors.New("tools_in_user_message requires first remaining message to be user")
		}

		firstUserMessage := strings.TrimSpace(valueToText(first["content"]))
		messages = messages[1:]

		b.WriteString("<|start_header_id|>user<|end_header_id|>\n\n")
		b.WriteString("Given the following functions, please respond with a JSON for a function call with its proper arguments that best answers the given prompt.\n\n")
		b.WriteString(`Respond in the format {"name": function name, "parameters": dictionary of argument name and its value}.`)
		b.WriteString("Do not use variables.\n\n")

		renderedTools, err := jsonIndent(tools, 4)
		if err != nil {
			return "", err
		}

		b.WriteString(renderedTools)
		b.WriteString("\n\n")
		b.WriteString(firstUserMessage)
		b.WriteString("<|eot_id|>")
	}

	for i, raw := range messages {
		msg, ok := raw.(map[string]any)
		if !ok {
			return "", fmt.Errorf("message %d must be an object", i)
		}

		if _, ok := msg["tool_calls"]; ok {
			if err := renderLlama3ToolCall(&b, msg); err != nil {
				return "", err
			}
			continue
		}

		role, _ := msg["role"].(string)

		if role == "tool" || role == "ipython" {
			if err := renderLlama3ToolResult(&b, msg); err != nil {
				return "", err
			}
			continue
		}

		if role == "" {
			return "", fmt.Errorf("message %d missing role", i)
		}

		content := strings.TrimSpace(valueToText(msg["content"]))

		b.WriteString("<|start_header_id|>")
		b.WriteString(role)
		b.WriteString("<|end_header_id|>\n\n")
		b.WriteString(content)
		b.WriteString("<|eot_id|>")
	}

	b.WriteString("<|start_header_id|>assistant<|end_header_id|>\n\n")

	return b.String(), nil
}

func renderLlama3ToolCall(b *strings.Builder, msg map[string]any) error {
	rawCalls, ok := msg["tool_calls"].([]any)
	if !ok {
		return errors.New("tool_calls must be an array")
	}

	if len(rawCalls) != 1 {
		return errors.New("llama3 renderer only supports one tool call at a time")
	}

	call, ok := rawCalls[0].(map[string]any)
	if !ok {
		return errors.New("tool_call must be an object")
	}

	fn, ok := call["function"].(map[string]any)
	if !ok {
		return errors.New("tool_call.function must be an object")
	}

	name, _ := fn["name"].(string)
	if name == "" {
		return errors.New("tool_call.function.name is required")
	}

	argsJSON, err := jsonCompactToolArguments(fn["arguments"])
	if err != nil {
		return err
	}

	b.WriteString("<|start_header_id|>assistant<|end_header_id|>\n\n")
	b.WriteString(`{"name": "`)
	b.WriteString(name)
	b.WriteString(`", "parameters": `)
	b.WriteString(argsJSON)
	b.WriteString("}")
	b.WriteString("<|eot_id|>")

	return nil
}

func renderLlama3ToolResult(b *strings.Builder, msg map[string]any) error {
	content, ok := msg["content"]
	if !ok {
		content = ""
	}

	b.WriteString("<|start_header_id|>ipython<|end_header_id|>\n\n")

	switch content.(type) {
	case map[string]any, []any:
		j, err := jsonCompact(content)
		if err != nil {
			return err
		}
		b.WriteString(j)
	default:
		b.WriteString(valueToText(content))
	}

	b.WriteString("<|eot_id|>")

	return nil
}

func valueToText(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	default:
		b, err := json.Marshal(x)
		if err != nil {
			return fmt.Sprint(x)
		}
		return string(b)
	}
}

func jsonCompact(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func jsonCompactToolArguments(v any) (string, error) {
	switch x := v.(type) {
	case string:
		trimmed := strings.TrimSpace(x)
		if trimmed == "" {
			return "{}", nil
		}

		var decoded any
		if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
			return "", err
		}

		return jsonCompact(decoded)

	default:
		return jsonCompact(x)
	}
}

func jsonIndent(v any, indent int) (string, error) {
	prefix := ""
	indentStr := strings.Repeat(" ", indent)

	b, err := json.MarshalIndent(v, prefix, indentStr)
	if err != nil {
		return "", err
	}

	return string(b), nil
}
