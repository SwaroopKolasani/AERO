package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"aero-cache/internal/gate"
	"aero-cache/internal/httpapi"
	"aero-cache/internal/key"
	"aero-cache/internal/metrics"
)

func TestIntegrationTwiceFireChatCompletion(t *testing.T) {
	t.Setenv("AERO_L2_ADDR", "")
	t.Setenv("AERO_L3_ENABLED", "")

	var upstreamCalls atomic.Int64

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)

		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected upstream path: %s", r.URL.Path)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "chatcmpl-test",
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

	app := httptest.NewServer(newTestRouter(upstream.URL))
	defer app.Close()

	body := chatBody("Say exactly pong.")

	first := doJSON(t, app.URL+"/v1/chat/completions", body)
	defer first.Body.Close()

	if first.StatusCode != http.StatusOK {
		t.Fatalf("first status=%d, want 200", first.StatusCode)
	}

	firstCache := first.Header.Get("X-Aero-Cache")
	if firstCache != "miss" {
		t.Fatalf("first X-Aero-Cache=%q, want miss", firstCache)
	}

	firstBytes := readAll(t, first.Body)
	if !bytes.Contains(firstBytes, []byte(`"pong"`)) {
		t.Fatalf("first response does not contain pong: %s", string(firstBytes))
	}

	second, secondBytes := waitForCacheHit(t, app.URL+"/v1/chat/completions", body, 2*time.Second)
	defer second.Body.Close()

	if second.StatusCode != http.StatusOK {
		t.Fatalf("second status=%d, want 200", second.StatusCode)
	}

	if got := second.Header.Get("X-Aero-Cache"); got != "hit" {
		t.Fatalf("second X-Aero-Cache=%q, want hit", got)
	}

	if got := second.Header.Get("X-Aero-Tier"); got != "cache-l1" {
		t.Fatalf("second X-Aero-Tier=%q, want cache-l1", got)
	}

	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatalf("cached response bytes differ\nfirst=%s\nsecond=%s", string(firstBytes), string(secondBytes))
	}

	if calls := upstreamCalls.Load(); calls < 1 {
		t.Fatalf("upstream calls=%d, want at least 1", calls)
	}
}

func TestIntegrationStrictGateBypassNonDeterministic(t *testing.T) {
	t.Setenv("AERO_L2_ADDR", "")
	t.Setenv("AERO_L3_ENABLED", "")

	var upstreamCalls atomic.Int64

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	app := httptest.NewServer(newTestRouter(upstream.URL))
	defer app.Close()

	body := map[string]any{
		"model":       "test-model",
		"temperature": 0.7,
		"messages": []map[string]any{
			{"role": "user", "content": "This must bypass."},
		},
	}

	resp := doJSON(t, app.URL+"/v1/chat/completions", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}

	if got := resp.Header.Get("X-Aero-Cache"); got != "bypass" {
		t.Fatalf("X-Aero-Cache=%q, want bypass", got)
	}

	if upstreamCalls.Load() != 1 {
		t.Fatalf("upstream calls=%d, want 1", upstreamCalls.Load())
	}
}

func TestIntegrationCoalescesConcurrentIdenticalMisses(t *testing.T) {
	t.Setenv("AERO_L2_ADDR", "")
	t.Setenv("AERO_L3_ENABLED", "")

	var upstreamCalls atomic.Int64
	releaseUpstream := make(chan struct{})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)

		<-releaseUpstream

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "chatcmpl-coalesced",
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
			]
		}`))
	}))
	defer upstream.Close()

	app := httptest.NewServer(newTestRouter(upstream.URL))
	defer app.Close()

	body := chatBody("Concurrent same request.")

	const n = 20

	type result struct {
		status int
		cache  string
		body   []byte
		err    error
	}

	results := make([]result, n)

	var wg sync.WaitGroup
	wg.Add(n)

	for i := 0; i < n; i++ {
		i := i

		go func() {
			defer wg.Done()

			resp, err := postJSON(app.URL+"/v1/chat/completions", body)
			if err != nil {
				results[i].err = err
				return
			}
			defer resp.Body.Close()

			results[i].status = resp.StatusCode
			results[i].cache = resp.Header.Get("X-Aero-Cache")
			results[i].body = readAll(t, resp.Body)
		}()
	}

	waitUntil(t, 2*time.Second, func() bool {
		return upstreamCalls.Load() == 1
	})

	close(releaseUpstream)
	wg.Wait()

	if calls := upstreamCalls.Load(); calls != 1 {
		t.Fatalf("upstream calls=%d, want 1", calls)
	}

	coalescedCount := 0
	missCount := 0

	for i, r := range results {
		if r.err != nil {
			t.Fatalf("request %d failed: %v", i, r.err)
		}

		if r.status != http.StatusOK {
			t.Fatalf("request %d status=%d, want 200", i, r.status)
		}

		if !bytes.Contains(r.body, []byte(`"pong"`)) {
			t.Fatalf("request %d body does not contain pong: %s", i, string(r.body))
		}

		switch r.cache {
		case "miss":
			missCount++
		case "coalesced":
			coalescedCount++
		default:
			t.Fatalf("request %d X-Aero-Cache=%q, want miss or coalesced", i, r.cache)
		}
	}

	if missCount != 1 {
		t.Fatalf("miss responses=%d, want 1", missCount)
	}

	if coalescedCount != n-1 {
		t.Fatalf("coalesced responses=%d, want %d", coalescedCount, n-1)
	}
}

func newTestRouter(upstreamURL string) http.Handler {
	reg := metrics.NewRegistry()

	return httpapi.NewRouter(httpapi.Config{
		SPAPath:            "",
		Debug:              true,
		GateMode:           gate.ModeStrict,
		TokenizerAvailable: true,
		Epoch:              0,
		UpstreamURL:        upstreamURL,
		Tokenizer:          key.ByteTokenizer{},
		Renderer:           key.LegacyRenderer{},
		Fingerprint: key.Fingerprint{
			Model:  "test-model",
			Engine: "mock-upstream",
			Config: map[string]any{
				"tokenizer":          "byte-tokenizer-test-only",
				"chat_template_kind": "legacy-test-only",
				"dtype":              "test",
				"tp":                 1,
			},
		},
	}, reg)
}

func chatBody(content string) map[string]any {
	return map[string]any{
		"model":       "test-model",
		"temperature": 0,
		"messages": []map[string]any{
			{"role": "system", "content": "You are concise."},
			{"role": "user", "content": content},
		},
	}
}

func doJSON(t *testing.T, url string, body map[string]any) *http.Response {
	t.Helper()

	resp, err := postJSON(url, body)
	if err != nil {
		t.Fatalf("post json: %v", err)
	}

	return resp
}

func postJSON(url string, body map[string]any) (*http.Response, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")

	return http.DefaultClient.Do(req)
}

func waitForCacheHit(t *testing.T, url string, body map[string]any, timeout time.Duration) (*http.Response, []byte) {
	t.Helper()

	deadline := time.Now().Add(timeout)

	var lastCache string
	var lastStatus int
	var lastBody []byte

	for time.Now().Before(deadline) {
		resp := doJSON(t, url, body)
		bodyBytes := readAll(t, resp.Body)
		_ = resp.Body.Close()

		lastStatus = resp.StatusCode
		lastCache = resp.Header.Get("X-Aero-Cache")
		lastBody = bodyBytes

		if lastStatus == http.StatusOK && lastCache == "hit" {
			resp.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			return resp, bodyBytes
		}

		time.Sleep(25 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for cache hit; last status=%d cache=%q body=%s", lastStatus, lastCache, string(lastBody))
	return nil, nil
}

func waitUntil(t *testing.T, timeout time.Duration, fn func() bool) {
	t.Helper()

	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		if fn() {
			return
		}

		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for condition")
}

func readAll(t *testing.T, r io.Reader) []byte {
	t.Helper()

	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	return b
}
