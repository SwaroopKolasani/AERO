package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"aero-cache/internal/gate"
	"aero-cache/internal/key"
	"aero-cache/internal/metrics"
)

func TestAeroCorePlacementMissOnlySecondRequestHitsCache(t *testing.T) {
	var coreCalls atomic.Int64
	var placedCalls atomic.Int64
	var fallbackCalls atomic.Int64

	placed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		placedCalls.Add(1)

		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("placed upstream path=%s, want /v1/chat/completions", r.URL.Path)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl-placed",
			"object":"chat.completion",
			"created":1783919167,
			"model":"llama3.2:3b",
			"choices":[{"index":0,"message":{"role":"assistant","content":"Aero"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":30,"completion_tokens":3,"total_tokens":33}
		}`))
	}))
	defer placed.Close()

	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl-fallback",
			"object":"chat.completion",
			"choices":[{"index":0,"message":{"role":"assistant","content":"fallback"},"finish_reason":"stop"}],
			"usage":{"completion_tokens":1}
		}`))
	}))
	defer fallback.Close()

	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		coreCalls.Add(1)

		if r.URL.Path != "/resolve" {
			t.Errorf("aerocore path=%s, want /resolve", r.URL.Path)
		}

		if got := r.Header.Get("X-Aero-Request-Id"); got == "" {
			t.Error("missing X-Aero-Request-Id header sent to AeroCore")
		}

		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode aerocore request: %v", err)
		}

		if got := req["model"]; got != "llama3.2:3b" {
			t.Errorf("placement model=%v, want llama3.2:3b", got)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"request_id":  req["request_id"],
			"decision":    "route",
			"backend_id":  "placed-upstream",
			"backend_url": placed.URL,
			"rung":        "fleet",
			"reason":      "test_route",
			"fail_open":   false,
		})
	}))
	defer core.Close()

	app := httptest.NewServer(newAeroCoreTestRouter(t, fallback.URL, core.URL, true))
	defer app.Close()

	body := aeroCoreTestBody()

	first := postAeroCoreJSON(t, app.URL+"/v1/chat/completions", body)
	defer first.Body.Close()

	if got := first.Header.Get("X-Aero-Cache"); got != "miss" {
		t.Fatalf("first X-Aero-Cache=%q, want miss", got)
	}

	if got := first.Header.Get("X-Aero-Verified"); got != "false" {
		t.Fatalf("first X-Aero-Verified=%q, want false", got)
	}

	if got := coreCalls.Load(); got != 1 {
		t.Fatalf("core calls after first request=%d, want 1", got)
	}

	if got := placedCalls.Load(); got != 1 {
		t.Fatalf("placed upstream calls after first request=%d, want 1", got)
	}

	if got := fallbackCalls.Load(); got != 0 {
		t.Fatalf("fallback upstream calls after first request=%d, want 0", got)
	}

	second := waitForAeroCoreCacheHit(t, app.URL+"/v1/chat/completions", body, 2*time.Second)
	defer second.Body.Close()

	if got := second.Header.Get("X-Aero-Verified"); got != "true" {
		t.Fatalf("second X-Aero-Verified=%q, want true", got)
	}

	if got := coreCalls.Load(); got != 1 {
		t.Fatalf("core calls after second identical request=%d, want still 1", got)
	}

	if got := placedCalls.Load(); got != 1 {
		t.Fatalf("placed upstream calls after second identical request=%d, want still 1", got)
	}

	if got := fallbackCalls.Load(); got != 0 {
		t.Fatalf("fallback upstream calls after second identical request=%d, want 0", got)
	}
}

func TestAeroCoreUnavailableFailsOpenToDefaultUpstream(t *testing.T) {
	var fallbackCalls atomic.Int64

	deadCore := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadCoreURL := deadCore.URL
	deadCore.Close()

	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackCalls.Add(1)

		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("fallback upstream path=%s, want /v1/chat/completions", r.URL.Path)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl-fallback",
			"object":"chat.completion",
			"created":1783919167,
			"model":"llama3.2:3b",
			"choices":[{"index":0,"message":{"role":"assistant","content":"Fallback"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":30,"completion_tokens":2,"total_tokens":32}
		}`))
	}))
	defer fallback.Close()

	app := httptest.NewServer(newAeroCoreTestRouter(t, fallback.URL, deadCoreURL, true))
	defer app.Close()

	resp := postAeroCoreJSON(t, app.URL+"/v1/chat/completions", aeroCoreTestBody())
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}

	if got := resp.Header.Get("X-Aero-Cache"); got != "miss" {
		t.Fatalf("X-Aero-Cache=%q, want miss", got)
	}

	if got := fallbackCalls.Load(); got != 1 {
		t.Fatalf("fallback calls=%d, want 1", got)
	}
}

func TestAeroCoreNotCalledForBypass(t *testing.T) {
	var coreCalls atomic.Int64
	var upstreamCalls atomic.Int64

	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		coreCalls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer core.Close()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl-bypass",
			"object":"chat.completion",
			"choices":[{"index":0,"message":{"role":"assistant","content":"Bypass"},"finish_reason":"stop"}],
			"usage":{"completion_tokens":2}
		}`))
	}))
	defer upstream.Close()

	app := httptest.NewServer(newAeroCoreTestRouter(t, upstream.URL, core.URL, true))
	defer app.Close()

	body := map[string]any{
		"model":       "llama3.2:3b",
		"messages":    []map[string]any{{"role": "user", "content": "Say aero once."}},
		"temperature": 0.7,
		"stream":      false,
	}

	resp := postAeroCoreJSON(t, app.URL+"/v1/chat/completions", body)
	defer resp.Body.Close()

	if got := resp.Header.Get("X-Aero-Cache"); got != "bypass" {
		t.Fatalf("X-Aero-Cache=%q, want bypass", got)
	}

	if got := coreCalls.Load(); got != 0 {
		t.Fatalf("core calls=%d, want 0 for bypass", got)
	}

	if got := upstreamCalls.Load(); got != 1 {
		t.Fatalf("upstream calls=%d, want 1", got)
	}
}

func newAeroCoreTestRouter(t *testing.T, upstreamURL string, aeroCoreURL string, aeroCoreEnabled bool) http.Handler {
	t.Helper()

	t.Setenv("AERO_L2_ADDR", "")
	t.Setenv("AERO_L3_ENABLED", "0")

	return NewRouter(Config{
		SPAPath:            "testdata/missing-spa",
		Debug:              true,
		GateMode:           gate.Mode("strict"),
		TokenizerAvailable: true,
		Epoch:              uint64(time.Now().UnixNano()),
		UpstreamURL:        upstreamURL,
		AeroCoreEnabled:    aeroCoreEnabled,
		AeroCoreURL:        aeroCoreURL,
		AeroCoreTimeout:    100 * time.Millisecond,
		Tokenizer:          key.ByteTokenizer{},
		Renderer:           key.LegacyRenderer{},
		Fingerprint: key.Fingerprint{
			Model:  "llama3.2:3b",
			Engine: "mock-upstream",
			Config: map[string]any{
				"test": strconv.FormatInt(time.Now().UnixNano(), 10),
			},
		},
	}, metrics.NewRegistry())
}

func aeroCoreTestBody() map[string]any {
	return map[string]any{
		"model":       "llama3.2:3b",
		"messages":    []map[string]any{{"role": "user", "content": "Say aero once."}},
		"temperature": 0,
		"stream":      false,
	}
}

func postAeroCoreJSON(t *testing.T, url string, body map[string]any) *http.Response {
	t.Helper()

	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	resp, err := http.Post(url, "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("post json: %v", err)
	}

	return resp
}

func waitForAeroCoreCacheHit(t *testing.T, url string, body map[string]any, timeout time.Duration) *http.Response {
	t.Helper()

	deadline := time.Now().Add(timeout)

	var lastStatus int
	var lastCache string

	for time.Now().Before(deadline) {
		resp := postAeroCoreJSON(t, url, body)

		lastStatus = resp.StatusCode
		lastCache = resp.Header.Get("X-Aero-Cache")

		if lastStatus == http.StatusOK && lastCache == "hit" {
			return resp
		}

		_ = resp.Body.Close()
		time.Sleep(25 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for cache hit; last status=%d cache=%q", lastStatus, lastCache)
	return nil
}
