package httpapi_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func TestIntegrationL2PersistenceAcrossRouterRestart(t *testing.T) {
	if os.Getenv("AERO_L2_ADDR") == "" {
		t.Skip("set AERO_L2_ADDR to run L2 persistence integration test")
	}

	t.Setenv("AERO_L3_ENABLED", "")

	var upstreamCalls atomic.Int64

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "chatcmpl-l2-persistence",
			"object": "chat.completion",
			"created": 123,
			"model": "test-model",
			"choices": [
				{
					"index": 0,
					"message": {
						"role": "assistant",
						"content": "pong"
					},
					"finish_reason": "stop"
				}
			],
			"usage": {
				"prompt_tokens": 4,
				"completion_tokens": 1,
				"total_tokens": 5
			}
		}`))
	}))
	defer upstream.Close()

	uniquePrompt := fmt.Sprintf("L2 persistence test %d", time.Now().UnixNano())
	body := chatBody(uniquePrompt)

	app1 := httptest.NewServer(newTestRouter(upstream.URL))

	first := doJSON(t, app1.URL+"/v1/chat/completions", body)
	firstBytes := readAll(t, first.Body)
	_ = first.Body.Close()

	if first.StatusCode != http.StatusOK {
		t.Fatalf("first status=%d, want 200", first.StatusCode)
	}

	if got := first.Header.Get("X-Aero-Cache"); got != "miss" {
		t.Fatalf("first X-Aero-Cache=%q, want miss", got)
	}

	if upstreamCalls.Load() != 1 {
		t.Fatalf("upstream calls after first request=%d, want 1", upstreamCalls.Load())
	}

	// Wait until app1 can hit L1. This also gives async writeback time to commit to L2.
	second, _ := waitForCacheHit(t, app1.URL+"/v1/chat/completions", body, 3*time.Second)
	_ = second.Body.Close()

	app1.Close()

	app2 := httptest.NewServer(newTestRouter(upstream.URL))
	defer app2.Close()

	var lastCache string
	var lastTier string
	var lastStatus int
	var lastBody []byte

	deadline := time.Now().Add(3 * time.Second)

	for time.Now().Before(deadline) {
		resp := doJSON(t, app2.URL+"/v1/chat/completions", body)
		bodyBytes := readAll(t, resp.Body)
		_ = resp.Body.Close()

		lastStatus = resp.StatusCode
		lastCache = resp.Header.Get("X-Aero-Cache")
		lastTier = resp.Header.Get("X-Aero-Tier")
		lastBody = bodyBytes

		if lastStatus == http.StatusOK && lastCache == "hit" && lastTier == "cache-l2" {
			if string(firstBytes) != string(bodyBytes) {
				t.Fatalf("L2 response bytes differ\nfirst=%s\nl2=%s", string(firstBytes), string(bodyBytes))
			}

			if upstreamCalls.Load() != 1 {
				t.Fatalf("upstream calls after L2 hit=%d, want 1", upstreamCalls.Load())
			}

			return
		}

		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf(
		"timed out waiting for L2 hit; last status=%d cache=%q tier=%q upstreamCalls=%d body=%s",
		lastStatus,
		lastCache,
		lastTier,
		upstreamCalls.Load(),
		string(lastBody),
	)
}
