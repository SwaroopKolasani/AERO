package placement

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestResolverDisabledUsesDefaultFallback(t *testing.T) {
	r := NewResolver(Config{
		Enabled:     false,
		FallbackURL: "http://localhost:11434",
	})

	got := r.Resolve(context.Background(), Request{
		RequestID: "req_1",
		Model:     "llama3.2:3b",
		Tier:      "A",
	})

	if got.Source != SourceDefault {
		t.Fatalf("source=%q, want %q", got.Source, SourceDefault)
	}
	if got.BaseURL != "http://localhost:11434" {
		t.Fatalf("baseURL=%q", got.BaseURL)
	}
}

func TestResolverRouteUsesAeroCoreBackendURL(t *testing.T) {
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/resolve" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		if got := r.Header.Get("X-Aero-Request-Id"); got != "req_1" {
			t.Fatalf("request id header=%q", got)
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"request_id":  "req_1",
			"decision":    "route",
			"backend_id":  "mac-m2-ollama",
			"backend_url": "http://mac.local:11434",
			"rung":        "fleet",
			"reason":      "cheapest_healthy_capable_backend",
			"fail_open":   false,
		})
	}))
	defer core.Close()

	r := NewResolver(Config{
		Enabled:     true,
		BaseURL:     core.URL,
		FallbackURL: "http://localhost:11434",
		Timeout:     time.Second,
	})

	got := r.Resolve(context.Background(), Request{
		RequestID: "req_1",
		Model:     "llama3.2:3b",
		Stream:    true,
		Tier:      "A",
	})

	if got.Source != SourceAeroCoreRoute {
		t.Fatalf("source=%q, want %q", got.Source, SourceAeroCoreRoute)
	}
	if got.BaseURL != "http://mac.local:11434" {
		t.Fatalf("baseURL=%q", got.BaseURL)
	}
	if got.Rung != "fleet" {
		t.Fatalf("rung=%q", got.Rung)
	}
}

func TestResolverFailOpenUsesAeroCoreBackendURL(t *testing.T) {
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"request_id":  "req_1",
			"decision":    "fail_open",
			"backend_id":  "default-upstream",
			"backend_url": "http://localhost:11434",
			"rung":        "upstream",
			"reason":      "no_healthy_backend",
			"fail_open":   true,
		})
	}))
	defer core.Close()

	r := NewResolver(Config{
		Enabled:     true,
		BaseURL:     core.URL,
		FallbackURL: "http://fallback.local:11434",
	})

	got := r.Resolve(context.Background(), Request{
		RequestID: "req_1",
		Model:     "llama3.2:3b",
		Tier:      "A",
	})

	if got.Source != SourceAeroCoreFailOpen {
		t.Fatalf("source=%q, want %q", got.Source, SourceAeroCoreFailOpen)
	}
	if got.BaseURL != "http://localhost:11434" {
		t.Fatalf("baseURL=%q", got.BaseURL)
	}
	if !got.FailOpen {
		t.Fatal("expected fail_open=true")
	}
}

func TestResolverMalformedResponseFallsBack(t *testing.T) {
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`not-json`))
	}))
	defer core.Close()

	r := NewResolver(Config{
		Enabled:     true,
		BaseURL:     core.URL,
		FallbackURL: "http://localhost:11434",
	})

	got := r.Resolve(context.Background(), Request{
		RequestID: "req_1",
		Model:     "llama3.2:3b",
		Tier:      "A",
	})

	if got.Source != SourceFallback {
		t.Fatalf("source=%q, want %q", got.Source, SourceFallback)
	}
	if got.BaseURL != "http://localhost:11434" {
		t.Fatalf("baseURL=%q", got.BaseURL)
	}
}