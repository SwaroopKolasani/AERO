package probe

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const ChatResultSchemaV1 = "aerorig.chat_probe.v1"

type ChatRequest struct {
	RunID      string
	Sample     int
	TargetName string
	TargetURL  string
	Model      string
	Prompt     string
	APIKey     string
	Timeout    time.Duration
}

type ChatResult struct {
	SchemaVersion string            `json:"schema_version"`
	Probe         string            `json:"probe"`
	RunID         string            `json:"run_id"`
	Sample        int               `json:"sample"`
	TargetName    string            `json:"target_name"`
	TargetURL     string            `json:"target_url"`
	Model         string            `json:"model"`
	PromptSHA256  string            `json:"prompt_sha256"`
	StartedAt     string            `json:"started_at"`
	DurationMS    float64           `json:"duration_ms"`
	StatusCode    int               `json:"status_code,omitempty"`
	OK            bool              `json:"ok"`
	Error         string            `json:"error,omitempty"`
	ResponseID    string            `json:"response_id,omitempty"`
	ResponseModel string            `json:"response_model,omitempty"`
	FinishReason  string            `json:"finish_reason,omitempty"`
	AnswerSHA256  string            `json:"answer_sha256,omitempty"`
	AnswerBytes   int               `json:"answer_bytes"`
	PromptTokens  int               `json:"prompt_tokens,omitempty"`
	OutputTokens  int               `json:"output_tokens,omitempty"`
	TotalTokens   int               `json:"total_tokens,omitempty"`
	AeroHeaders   map[string]string `json:"aero_headers,omitempty"`
}

type openAIChatRequest struct {
	Model       string          `json:"model"`
	Messages    []openAIMessage `json:"messages"`
	Stream      bool            `json:"stream"`
	Temperature float64         `json:"temperature"`
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIChatResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

func Chat(ctx context.Context, client *http.Client, req ChatRequest) ChatResult {
	res := ChatResult{
		SchemaVersion: ChatResultSchemaV1,
		Probe:         "chat",
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

	body := openAIChatRequest{
		Model: req.Model,
		Messages: []openAIMessage{
			{Role: "user", Content: req.Prompt},
		},
		Stream:      false,
		Temperature: 0,
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
	httpReq.Header.Set("User-Agent", "aerorig/0.1")
	if strings.TrimSpace(req.APIKey) != "" {
		httpReq.Header.Set("Authorization", "Bearer "+strings.TrimSpace(req.APIKey))
	}

	start := time.Now()
	httpRes, err := client.Do(httpReq)
	res.DurationMS = float64(time.Since(start).Microseconds()) / 1000.0
	if err != nil {
		res.Error = err.Error()
		return res
	}
	defer httpRes.Body.Close()

	res.StatusCode = httpRes.StatusCode
	res.AeroHeaders = collectAeroHeaders(httpRes.Header)

	raw, readErr := io.ReadAll(io.LimitReader(httpRes.Body, 16<<20))
	if readErr != nil {
		res.Error = fmt.Sprintf("read response: %v", readErr)
		return res
	}

	if httpRes.StatusCode < 200 || httpRes.StatusCode >= 300 {
		res.Error = fmt.Sprintf("non-success status: %d body=%s", httpRes.StatusCode, trimForError(string(raw)))
		return res
	}

	var parsedResp openAIChatResponse
	if err := json.Unmarshal(raw, &parsedResp); err != nil {
		res.Error = fmt.Sprintf("parse response: %v", err)
		return res
	}

	res.ResponseID = parsedResp.ID
	res.ResponseModel = parsedResp.Model
	res.PromptTokens = parsedResp.Usage.PromptTokens
	res.OutputTokens = parsedResp.Usage.CompletionTokens
	res.TotalTokens = parsedResp.Usage.TotalTokens

	if len(parsedResp.Choices) > 0 {
		answer := parsedResp.Choices[0].Message.Content
		res.FinishReason = parsedResp.Choices[0].FinishReason
		res.AnswerSHA256 = sha256Hex([]byte(answer))
		res.AnswerBytes = len([]byte(answer))
	}

	res.OK = true
	return res
}

func collectAeroHeaders(h http.Header) map[string]string {
	out := map[string]string{}

	for key, values := range h {
		if strings.HasPrefix(strings.ToLower(key), "x-aero-") && len(values) > 0 {
			out[key] = values[0]
		}
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func trimForError(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= 512 {
		return s
	}
	return s[:512] + "...truncated"
}
