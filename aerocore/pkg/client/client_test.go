package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
func TestResolveSendsAeroRequestIDHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(api.IncomingRequestIDHeader); got != "req_trace_client" {
			t.Fatalf("expected %s header, got %q", api.IncomingRequestIDHeader, got)
		}

		_ = json.NewEncoder(w).Encode(api.PlacementResponse{
			RequestID:  "req_trace_client",
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

	_, err := c.Resolve(context.Background(), api.PlacementRequest{
		RequestID: "req_trace_client",
		Model:     "llama3.2:3b",
		Tier:      api.TierA,
	})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
}

func TestResolveDoesNotSendEmptyAeroRequestIDHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(api.IncomingRequestIDHeader); got != "" {
			t.Fatalf("expected empty %s header, got %q", api.IncomingRequestIDHeader, got)
		}

		_ = json.NewEncoder(w).Encode(api.PlacementResponse{
			Decision:   api.DecisionFailOpen,
			BackendID:  "default-upstream",
			BackendURL: "http://localhost:11434",
			Rung:       api.RungUpstream,
			Reason:     "no_healthy_capable_backend",
			FailOpen:   true,
		})
	}))
	defer srv.Close()

	c := New(srv.URL)

	_, err := c.Resolve(context.Background(), api.PlacementRequest{
		Model: "llama3.2:3b",
		Tier:  api.TierA,
	})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
}
func TestResolveTargetReturnsRouteBackendURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(api.PlacementResponse{
			RequestID:  "req_route_target",
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

	targetURL, decision, err := c.ResolveTarget(context.Background(), api.PlacementRequest{
		RequestID: "req_route_target",
		Model:     "llama3.2:3b",
		Tier:      api.TierA,
	})
	if err != nil {
		t.Fatalf("ResolveTarget returned error: %v", err)
	}

	if targetURL != "http://mac.local:11434" {
		t.Fatalf("expected route target URL, got %q", targetURL)
	}

	if decision.Decision != api.DecisionRoute {
		t.Fatalf("expected route decision, got %+v", decision)
	}
}

func TestResolveTargetReturnsFailOpenBackendURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(api.PlacementResponse{
			RequestID:  "req_fail_open_target",
			Decision:   api.DecisionFailOpen,
			BackendID:  "default-upstream",
			BackendURL: "http://localhost:11434",
			Rung:       api.RungUpstream,
			Reason:     "no_healthy_capable_backend",
			FailOpen:   true,
		})
	}))
	defer srv.Close()

	c := New(srv.URL)

	targetURL, decision, err := c.ResolveTarget(context.Background(), api.PlacementRequest{
		RequestID: "req_fail_open_target",
		Model:     "llama3.2:3b",
		Tier:      api.TierA,
	})
	if err != nil {
		t.Fatalf("ResolveTarget returned error: %v", err)
	}

	if targetURL != "http://localhost:11434" {
		t.Fatalf("expected fail-open target URL, got %q", targetURL)
	}

	if decision.Decision != api.DecisionFailOpen {
		t.Fatalf("expected fail-open decision, got %+v", decision)
	}
}

func TestResolveTargetRejectReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(api.PlacementResponse{
			RequestID: "req_reject_target",
			Decision:  api.DecisionReject,
			Reason:    "tier_b_not_supported_in_m3",
			FailOpen:  false,
		})
	}))
	defer srv.Close()

	c := New(srv.URL)

	_, decision, err := c.ResolveTarget(context.Background(), api.PlacementRequest{
		RequestID: "req_reject_target",
		Model:     "llama3.2:3b",
		Tier:      api.TierB,
	})
	if err == nil {
		t.Fatal("expected reject error")
	}

	if decision.Decision != api.DecisionReject {
		t.Fatalf("expected reject decision, got %+v", decision)
	}
}

func TestResolveTargetEmptyBackendURLReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(api.PlacementResponse{
			RequestID: "req_empty_url",
			Decision:  api.DecisionRoute,
			BackendID: "mac-m2-ollama",
			Rung:      api.RungFleet,
			Reason:    "cheapest_healthy_capable_backend",
			FailOpen:  false,
		})
	}))
	defer srv.Close()

	c := New(srv.URL)

	_, _, err := c.ResolveTarget(context.Background(), api.PlacementRequest{
		RequestID: "req_empty_url",
		Model:     "llama3.2:3b",
		Tier:      api.TierA,
	})
	if err == nil {
		t.Fatal("expected empty backend URL error")
	}

	if !strings.Contains(err.Error(), "target url is empty") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveTargetNetworkErrorUsesConfiguredFallbackURL(t *testing.T) {
	c := New("http://127.0.0.1:1", WithFallbackURL("http://localhost:11434"))

	targetURL, decision, err := c.ResolveTarget(context.Background(), api.PlacementRequest{
		RequestID: "req_network_fallback",
		Model:     "llama3.2:3b",
		Tier:      api.TierA,
	})
	if err != nil {
		t.Fatalf("ResolveTarget returned error: %v", err)
	}

	if targetURL != "http://localhost:11434" {
		t.Fatalf("expected fallback target URL, got %q", targetURL)
	}

	if decision.Decision != api.DecisionFailOpen {
		t.Fatalf("expected fail-open decision, got %+v", decision)
	}

	if decision.BackendID != "client-local-fallback" {
		t.Fatalf("expected client fallback backend ID, got %+v", decision)
	}

	if decision.Reason != "aerocore_unavailable_local_fallback" {
		t.Fatalf("expected fallback reason, got %+v", decision)
	}
}

func TestResolveTargetNetworkErrorWithoutFallbackReturnsError(t *testing.T) {
	c := New("http://127.0.0.1:1")

	_, _, err := c.ResolveTarget(context.Background(), api.PlacementRequest{
		RequestID: "req_network_no_fallback",
		Model:     "llama3.2:3b",
		Tier:      api.TierA,
	})
	if err == nil {
		t.Fatal("expected network error")
	}
}
