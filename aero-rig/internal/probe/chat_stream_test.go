package probe

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestChatStreamProbeOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Aero-Cache", "hit")
		w.Header().Set("X-Aero-Verified", "true")

		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected flusher")
		}

		fmt.Fprint(w, `data: {"id":"chatcmpl-test","model":"tiny","choices":[{"delta":{"role":"assistant"}}]}`+"\n\n")
		flusher.Flush()

		fmt.Fprint(w, `data: {"id":"chatcmpl-test","model":"tiny","choices":[{"delta":{"content":"po"}}]}`+"\n\n")
		flusher.Flush()

		fmt.Fprint(w, `data: {"id":"chatcmpl-test","model":"tiny","choices":[{"delta":{"content":"ng"},"finish_reason":"stop"}]}`+"\n\n")
		flusher.Flush()

		fmt.Fprint(w, `data: {"id":"chatcmpl-test","model":"tiny","choices":[],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`+"\n\n")
		flusher.Flush()

		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	client := &http.Client{Timeout: time.Second}
	res := ChatStream(context.Background(), client, ChatStreamRequest{
		RunID:      "test-run",
		Sample:     1,
		TargetName: "local",
		TargetURL:  srv.URL,
		Model:      "tiny",
		Prompt:     "ping",
	})

	if !res.OK {
		t.Fatalf("expected ok result, got error=%q status=%d", res.Error, res.StatusCode)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusOK)
	}
	if res.ResponseID != "chatcmpl-test" {
		t.Fatalf("response id = %q", res.ResponseID)
	}
	if res.ContentChunks != 2 {
		t.Fatalf("content chunks = %d, want 2", res.ContentChunks)
	}
	if res.SSEEvents != 4 {
		t.Fatalf("sse events = %d, want 4", res.SSEEvents)
	}
	if res.AnswerBytes != 4 {
		t.Fatalf("answer bytes = %d, want 4", res.AnswerBytes)
	}
	if res.AnswerSHA256 == "" {
		t.Fatal("expected answer hash")
	}
	if res.TTFTMS <= 0 {
		t.Fatalf("expected ttft > 0, got %f", res.TTFTMS)
	}
	if res.OutputTokens != 1 || res.TotalTokens != 4 {
		t.Fatalf("unexpected usage: output=%d total=%d", res.OutputTokens, res.TotalTokens)
	}
	if res.AeroHeaders["X-Aero-Cache"] != "hit" {
		t.Fatalf("missing aero header: %+v", res.AeroHeaders)
	}
}

func TestChatStreamProbeNonSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "model not found", http.StatusNotFound)
	}))
	defer srv.Close()

	client := &http.Client{Timeout: time.Second}
	res := ChatStream(context.Background(), client, ChatStreamRequest{
		RunID:      "test-run",
		Sample:     1,
		TargetName: "bad",
		TargetURL:  srv.URL,
		Model:      "missing",
		Prompt:     "ping",
	})

	if res.OK {
		t.Fatal("expected non-success response to fail")
	}
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusNotFound)
	}
	if !strings.Contains(res.Error, "non-success status") {
		t.Fatalf("unexpected error: %q", res.Error)
	}
}
