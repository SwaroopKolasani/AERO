package probe

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const ChatStreamResultSchemaV1 = "aerorig.chat_stream_probe.v1"

type ChatStreamRequest struct {
	RunID      string
	Sample     int
	TargetName string
	TargetURL  string
	Model      string
	Prompt     string
	APIKey     string
	Timeout    time.Duration
}

type ChatStreamResult struct {
	SchemaVersion string            `json:"schema_version"`
	Probe         string            `json:"probe"`
	RunID         string            `json:"run_id"`
	Sample        int               `json:"sample"`
	TargetName    string            `json:"target_name"`
	TargetURL     string            `json:"target_url"`
	Model         string            `json:"model"`
	PromptSHA256  string            `json:"prompt_sha256"`
	StartedAt     string            `json:"started_at"`
	TTFTMS        float64           `json:"ttft_ms,omitempty"`
	DurationMS    float64           `json:"duration_ms"`
	StatusCode    int               `json:"status_code,omitempty"`
	OK            bool              `json:"ok"`
	Error         string            `json:"error,omitempty"`
	ResponseID    string            `json:"response_id,omitempty"`
	ResponseModel string            `json:"response_model,omitempty"`
	FinishReason  string            `json:"finish_reason,omitempty"`
	AnswerSHA256  string            `json:"answer_sha256,omitempty"`
	AnswerBytes   int               `json:"answer_bytes"`
	SSEEvents     int               `json:"sse_events"`
	ContentChunks int               `json:"content_chunks"`
	PromptTokens  int               `json:"prompt_tokens,omitempty"`
	OutputTokens  int               `json:"output_tokens,omitempty"`
	TotalTokens   int               `json:"total_tokens,omitempty"`
	AeroHeaders   map[string]string `json:"aero_headers,omitempty"`
}

type openAIChatStreamRequest struct {
	Model         string                 `json:"model"`
	Messages      []openAIMessage        `json:"messages"`
	Stream        bool                   `json:"stream"`
	Temperature   float64                `json:"temperature"`
	StreamOptions map[string]interface{} `json:"stream_options,omitempty"`
}

type openAIStreamEvent struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Delta        struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage,omitempty"`
}

func ChatStream(ctx context.Context, client *http.Client, req ChatStreamRequest) ChatStreamResult {
	res := ChatStreamResult{
		SchemaVersion: ChatStreamResultSchemaV1,
		Probe:         "chat_stream",
		RunID:         req.RunID,
		Sample:        req.Sample,
		TargetName:    req.TargetName,
		TargetURL:     req.TargetURL,
		Model:         req.Model,
		PromptSHA256:  sha256Hex([]byte(req.Prompt)),
		StartedAt:     time.Now().UTC().Format(time.RFC3339Nano),
	}

	if strings.TrimSpace(req.Model) == "" {
		res.Error = "model is required"
		return res
	}
	if strings.TrimSpace(req.Prompt) == "" {
		res.Error = "prompt is required"
		return res
	}

	parsed, err := url.Parse(req.TargetURL)
	if err != nil {
		res.Error = fmt.Sprintf("invalid target url: %v", err)
		return res
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		res.Error = "target url scheme must be http or https"
		return res
	}
	if parsed.Host == "" {
		res.Error = "target url must include host"
		return res
	}

	body := openAIChatStreamRequest{
		Model: req.Model,
		Messages: []openAIMessage{
			{Role: "user", Content: req.Prompt},
		},
		Stream:      true,
		Temperature: 0,
		StreamOptions: map[string]interface{}{
			"include_usage": true,
		},
	}

	payload, err := json.Marshal(body)
	if err != nil {
		res.Error = fmt.Sprintf("marshal request: %v", err)
		return res
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, req.TargetURL, bytes.NewReader(payload))
	if err != nil {
		res.Error = fmt.Sprintf("build request: %v", err)
		return res
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("User-Agent", "aerorig/0.1")
	if strings.TrimSpace(req.APIKey) != "" {
		httpReq.Header.Set("Authorization", "Bearer "+strings.TrimSpace(req.APIKey))
	}

	start := time.Now()
	httpRes, err := client.Do(httpReq)
	if err != nil {
		res.DurationMS = float64(time.Since(start).Microseconds()) / 1000.0
		res.Error = err.Error()
		return res
	}
	defer httpRes.Body.Close()

	res.StatusCode = httpRes.StatusCode
	res.AeroHeaders = collectAeroHeaders(httpRes.Header)

	if httpRes.StatusCode < 200 || httpRes.StatusCode >= 300 {
		scanner := bufio.NewScanner(httpRes.Body)
		var bodyText strings.Builder
		for scanner.Scan() {
			if bodyText.Len() > 512 {
				break
			}
			bodyText.WriteString(scanner.Text())
			bodyText.WriteByte('\n')
		}
		res.DurationMS = float64(time.Since(start).Microseconds()) / 1000.0
		res.Error = fmt.Sprintf("non-success status: %d body=%s", httpRes.StatusCode, trimForError(bodyText.String()))
		return res
	}

	scanner := bufio.NewScanner(httpRes.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	var answer strings.Builder
	gotFirstContent := false

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}

		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			break
		}

		res.SSEEvents++

		var ev openAIStreamEvent
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			res.DurationMS = float64(time.Since(start).Microseconds()) / 1000.0
			res.Error = fmt.Sprintf("parse stream event: %v data=%s", err, trimForError(data))
			return res
		}

		if ev.ID != "" && res.ResponseID == "" {
			res.ResponseID = ev.ID
		}
		if ev.Model != "" && res.ResponseModel == "" {
			res.ResponseModel = ev.Model
		}
		if ev.Usage != nil {
			res.PromptTokens = ev.Usage.PromptTokens
			res.OutputTokens = ev.Usage.CompletionTokens
			res.TotalTokens = ev.Usage.TotalTokens
		}

		for _, choice := range ev.Choices {
			if choice.FinishReason != "" {
				res.FinishReason = choice.FinishReason
			}

			content := choice.Delta.Content
			if content == "" {
				continue
			}

			if !gotFirstContent {
				gotFirstContent = true
				res.TTFTMS = float64(time.Since(start).Microseconds()) / 1000.0
			}

			res.ContentChunks++
			answer.WriteString(content)
		}
	}

	res.DurationMS = float64(time.Since(start).Microseconds()) / 1000.0

	if err := scanner.Err(); err != nil {
		res.Error = fmt.Sprintf("scan stream: %v", err)
		return res
	}

	answerText := answer.String()
	res.AnswerBytes = len([]byte(answerText))
	if answerText != "" {
		res.AnswerSHA256 = sha256Hex([]byte(answerText))
	}

	res.OK = true
	return res
}
