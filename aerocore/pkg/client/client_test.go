package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/swaroop/aero/aerocore/pkg/api"
)

func TestResolve(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}

		if r.URL.Path != "/resolve" {
			t.Fatalf("expected /resolve, got %s", r.URL.Path)
		}

		var req api.PlacementRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		if req.RequestID != "req_client" {
			t.Fatalf("unexpected request: %+v", req)
		}

		_ = json.NewEncoder(w).Encode(api.PlacementResponse{
			RequestID:  req.RequestID,
			Decision:   api.DecisionRoute,
			BackendID:  "mac-m2-ollama",
			BackendURL: "http://mac.local:11434",
			Rung:       api.RungFleet,
			Reason:     "cheapest_healthy_capable_backend",
			FailOpen:   false,
		})
	}))
	defer srv.Close()

	c := New(srv.URL)

	got, err := c.Resolve(context.Background(), api.PlacementRequest{
		RequestID: "req_client",
		Model:     "llama3.2:3b",
		Tier:      api.TierA,
	})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}

	if got.Decision != api.DecisionRoute {
		t.Fatalf("expected route, got %+v", got)
	}

	if got.BackendURL != "http://mac.local:11434" {
		t.Fatalf("expected backend URL, got %+v", got)
	}
}

func TestReadyAcceptsServiceUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/readyz" {
			t.Fatalf("expected /readyz, got %s", r.URL.Path)
		}

		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(ReadyResponse{
			Ready:  false,
			Reason: "no_healthy_backend_or_default_upstream",
		})
	}))
	defer srv.Close()

	c := New(srv.URL)

	got, err := c.Ready(context.Background())
	if err != nil {
		t.Fatalf("Ready returned error: %v", err)
	}

	if got.Ready {
		t.Fatalf("expected not ready, got %+v", got)
	}

	if got.Reason != "no_healthy_backend_or_default_upstream" {
		t.Fatalf("unexpected ready response: %+v", got)
	}
}

func TestConfig(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/config" {
			t.Fatalf("expected /config, got %s", r.URL.Path)
		}

		_ = json.NewEncoder(w).Encode(ConfigResponse{
			DefaultUpstreamConfigured: true,
			BackendCount:              2,
			HealthyBackendCount:       1,
			StaleBackendCount:         1,
		})
	}))
	defer srv.Close()

	c := New(srv.URL)

	got, err := c.Config(context.Background())
	if err != nil {
		t.Fatalf("Config returned error: %v", err)
	}

	if got.BackendCount != 2 {
		t.Fatalf("expected two backends, got %+v", got)
	}

	if !got.DefaultUpstreamConfigured {
		t.Fatalf("expected default upstream configured, got %+v", got)
	}
}

func TestEmptyBaseURLFails(t *testing.T) {
	c := New("")

	_, err := c.Resolve(context.Background(), api.PlacementRequest{
		RequestID: "req_empty",
		Model:     "llama3.2:3b",
		Tier:      api.TierA,
	})

	if err == nil {
		t.Fatal("expected error")
	}
}
