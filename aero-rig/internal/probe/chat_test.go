package probe

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestChatProbeOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("authorization = %q", got)
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Aero-Cache", "hit")
		w.Header().Set("X-Aero-Verified", "true")
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl-test",
			"model":"tiny",
			"choices":[
				{
					"finish_reason":"stop",
					"message":{"role":"assistant","content":"pong"}
				}
			],
			"usage":{
				"prompt_tokens":3,
				"completion_tokens":1,
				"total_tokens":4
			}
		}`))
	}))
	defer srv.Close()

	client := &http.Client{Timeout: time.Second}
	res := Chat(context.Background(), client, ChatRequest{
		RunID:      "test-run",
		Sample:     1,
		TargetName: "local",
		TargetURL:  srv.URL,
		Model:      "tiny",
		Prompt:     "ping",
		APIKey:     "test-key",
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
	if res.OutputTokens != 1 || res.TotalTokens != 4 {
		t.Fatalf("unexpected usage: output=%d total=%d", res.OutputTokens, res.TotalTokens)
	}
	if res.AnswerBytes != 4 {
		t.Fatalf("answer bytes = %d, want 4", res.AnswerBytes)
	}
	if res.AnswerSHA256 == "" {
		t.Fatal("expected answer hash")
	}
	if res.AeroHeaders["X-Aero-Cache"] != "hit" {
		t.Fatalf("missing aero header: %+v", res.AeroHeaders)
	}
}

func TestChatProbeNonSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "model not found", http.StatusNotFound)
	}))
	defer srv.Close()

	client := &http.Client{Timeout: time.Second}
	res := Chat(context.Background(), client, ChatRequest{
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

func TestChatProbeRequiresModelAndPrompt(t *testing.T) {
	client := &http.Client{Timeout: time.Second}

	res := Chat(context.Background(), client, ChatRequest{
		TargetURL: "http://127.0.0.1/v1/chat/completions",
		Prompt:    "ping",
	})
	if res.OK || !strings.Contains(res.Error, "model is required") {
		t.Fatalf("expected model error, got %+v", res)
	}

	res = Chat(context.Background(), client, ChatRequest{
		TargetURL: "http://127.0.0.1/v1/chat/completions",
		Model:     "tiny",
	})
	if res.OK || !strings.Contains(res.Error, "prompt is required") {
		t.Fatalf("expected prompt error, got %+v", res)
	}
}
