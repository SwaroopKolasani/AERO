package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/swaroop/aero/aerocore/internal/placement"
	"github.com/swaroop/aero/aerocore/internal/registry"
)

func TestHealthz(t *testing.T) {
	srv := New(registry.NewMemoryRegistry())

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestPutAndListBackends(t *testing.T) {
	srv := New(registry.NewMemoryRegistry())

	body := []byte(`{
		"rung": "fleet",
		"url": "http://mac.local:11434",
		"healthy": true,
		"loaded_models": ["llama3.2:3b"],
		"cost_per_1k_tokens": 0,
		"p95_latency_ms": 900,
		"max_context": 8192
	}`)

	putReq := httptest.NewRequest(http.MethodPut, "/backends/mac-m2-ollama", bytes.NewReader(body))
	putRec := httptest.NewRecorder()

	srv.ServeHTTP(putRec, putReq)

	if putRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", putRec.Code, putRec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/backends", nil)
	getRec := httptest.NewRecorder()

	srv.ServeHTTP(getRec, getReq)

	if getRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", getRec.Code, getRec.Body.String())
	}

	var got []placement.Backend
	if err := json.NewDecoder(getRec.Body).Decode(&got); err != nil {
		t.Fatalf("decode backends: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("expected 1 backend, got %d", len(got))
	}

	if got[0].ID != "mac-m2-ollama" {
		t.Fatalf("expected backend id from path, got %+v", got[0])
	}
}

func TestResolveOverHTTP(t *testing.T) {
	reg := registry.NewMemoryRegistry()
	reg.UpsertBackend(placement.Backend{
		ID:              "mac-m2-ollama",
		Rung:            placement.RungFleet,
		URL:             "http://mac.local:11434",
		Healthy:         true,
		LoadedModels:    []string{"llama3.2:3b"},
		CostPer1KTokens: 0,
		P95LatencyMS:    900,
		MaxContext:      8192,
	})

	srv := New(reg)

	body := []byte(`{
		"request_id": "req_http",
		"model": "llama3.2:3b",
		"deadline_ms": 2000,
		"estimated_input_tokens": 512,
		"estimated_output_tokens": 128,
		"stream": true,
		"tier": "A"
	}`)

	req := httptest.NewRequest(http.MethodPost, "/resolve", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var got placement.PlacementResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode resolve response: %v", err)
	}

	if got.Decision != placement.DecisionRoute {
		t.Fatalf("expected route decision, got %+v", got)
	}

	if got.BackendID != "mac-m2-ollama" {
		t.Fatalf("expected mac backend, got %+v", got)
	}

	if got.Rung != placement.RungFleet {
		t.Fatalf("expected fleet rung, got %+v", got)
	}
}

func TestResolveFailOpenOverHTTP(t *testing.T) {
	srv := New(registry.NewMemoryRegistry())

	body := []byte(`{
		"request_id": "req_fail_open",
		"model": "llama3.2:3b",
		"deadline_ms": 2000,
		"tier": "A"
	}`)

	req := httptest.NewRequest(http.MethodPost, "/resolve", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var got placement.PlacementResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode resolve response: %v", err)
	}

	if got.Decision != placement.DecisionFailOpen || !got.FailOpen {
		t.Fatalf("expected fail-open, got %+v", got)
	}
}

func TestPutBackendRejectsPathBodyMismatch(t *testing.T) {
	srv := New(registry.NewMemoryRegistry())

	body := []byte(`{
		"id": "different-id",
		"rung": "fleet",
		"healthy": true
	}`)

	req := httptest.NewRequest(http.MethodPut, "/backends/mac-m2-ollama", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}
func TestPatchBackendHealth(t *testing.T) {
	reg := registry.NewMemoryRegistry()
	reg.UpsertBackend(placement.Backend{
		ID:      "mac-m2-ollama",
		Rung:    placement.RungFleet,
		Healthy: true,
	})

	srv := New(reg)

	body := []byte(`{
		"healthy": false
	}`)

	req := httptest.NewRequest(http.MethodPatch, "/backends/mac-m2-ollama/health", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var got placement.Backend
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode backend: %v", err)
	}

	if got.Healthy {
		t.Fatalf("expected backend unhealthy, got %+v", got)
	}

	if got.UpdatedAt.IsZero() {
		t.Fatalf("expected updated_at, got %+v", got)
	}
}

func TestDeleteBackendOverHTTP(t *testing.T) {
	reg := registry.NewMemoryRegistry()
	reg.UpsertBackend(placement.Backend{
		ID:      "mac-m2-ollama",
		Rung:    placement.RungFleet,
		Healthy: true,
	})

	srv := New(reg)

	req := httptest.NewRequest(http.MethodDelete, "/backends/mac-m2-ollama", nil)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", rec.Code, rec.Body.String())
	}

	if _, ok := reg.GetBackend("mac-m2-ollama"); ok {
		t.Fatal("expected backend deleted")
	}
}
func TestResolveResponseIncludesBackendURLOverHTTP(t *testing.T) {
	reg := registry.NewMemoryRegistry()
	reg.UpsertBackend(placement.Backend{
		ID:           "mac-m2-ollama",
		Rung:         placement.RungFleet,
		URL:          "http://mac.local:11434",
		Healthy:      true,
		LoadedModels: []string{"llama3.2:3b"},
		P95LatencyMS: 900,
		MaxContext:   8192,
	})

	srv := New(reg)

	body := []byte(`{
		"request_id": "req_url_contract",
		"model": "llama3.2:3b",
		"deadline_ms": 2000,
		"estimated_input_tokens": 512,
		"estimated_output_tokens": 128,
		"tier": "A"
	}`)

	req := httptest.NewRequest(http.MethodPost, "/resolve", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var got placement.PlacementResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode resolve response: %v", err)
	}

	if got.BackendURL != "http://mac.local:11434" {
		t.Fatalf("expected backend_url, got %+v", got)
	}
}
func TestResolveFailOpenIncludesConfiguredDefaultUpstreamURLOverHTTP(t *testing.T) {
	srv := NewWithConfig(registry.NewMemoryRegistry(), Config{
		DefaultUpstreamURL: "http://localhost:11434",
	})

	body := []byte(`{
		"request_id": "req_fail_open_with_url",
		"model": "llama3.2:3b",
		"deadline_ms": 2000,
		"tier": "A"
	}`)

	req := httptest.NewRequest(http.MethodPost, "/resolve", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var got placement.PlacementResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode resolve response: %v", err)
	}

	if got.Decision != placement.DecisionFailOpen {
		t.Fatalf("expected fail-open, got %+v", got)
	}

	if got.BackendURL != "http://localhost:11434" {
		t.Fatalf("expected configured default upstream URL, got %+v", got)
	}
}
func TestReadyzFailsWhenNoBackendAndNoDefaultUpstream(t *testing.T) {
	srv := New(registry.NewMemoryRegistry())

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", rec.Code, rec.Body.String())
	}

	var got readyResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode ready response: %v", err)
	}

	if got.Ready {
		t.Fatalf("expected not ready, got %+v", got)
	}

	if got.Reason != "no_healthy_backend_or_default_upstream" {
		t.Fatalf("unexpected reason, got %+v", got)
	}
}

func TestReadyzPassesWithDefaultUpstream(t *testing.T) {
	srv := NewWithConfig(registry.NewMemoryRegistry(), Config{
		DefaultUpstreamURL: "http://localhost:11434",
	})

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var got readyResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode ready response: %v", err)
	}

	if !got.Ready {
		t.Fatalf("expected ready, got %+v", got)
	}

	if !got.DefaultUpstreamConfigured {
		t.Fatalf("expected default upstream configured, got %+v", got)
	}
}

func TestReadyzPassesWithHealthyBackend(t *testing.T) {
	reg := registry.NewMemoryRegistry()
	reg.UpsertBackend(placement.Backend{
		ID:            "mac-m2-ollama",
		Rung:          placement.RungFleet,
		URL:           "http://mac.local:11434",
		Healthy:       true,
		CapableModels: []string{"llama3.2:3b"},
		P95LatencyMS:  900,
		MaxContext:    8192,
	})

	srv := New(reg)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var got readyResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode ready response: %v", err)
	}

	if !got.Ready {
		t.Fatalf("expected ready, got %+v", got)
	}

	if got.HealthyBackendCount != 1 {
		t.Fatalf("expected one healthy backend, got %+v", got)
	}
}

func TestConfigEndpointRedactsDefaultUpstreamURL(t *testing.T) {
	reg := registry.NewMemoryRegistry()
	reg.UpsertBackend(placement.Backend{
		ID:            "mac-m2-ollama",
		Rung:          placement.RungFleet,
		URL:           "http://mac.local:11434",
		Healthy:       true,
		CapableModels: []string{"llama3.2:3b"},
		P95LatencyMS:  900,
		MaxContext:    8192,
	})
	reg.UpsertBackend(placement.Backend{
		ID:            "cloud-gate",
		Rung:          placement.RungGate,
		URL:           "https://cloud.example/v1",
		Healthy:       false,
		CapableModels: []string{"llama3.2:70b"},
		P95LatencyMS:  1200,
		MaxContext:    32768,
	})

	srv := NewWithConfig(reg, Config{
		DefaultUpstreamURL: "http://localhost:11434",
	})

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var got configResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode config response: %v", err)
	}

	if !got.DefaultUpstreamConfigured {
		t.Fatalf("expected default upstream configured, got %+v", got)
	}

	if got.BackendCount != 2 {
		t.Fatalf("expected two backends, got %+v", got)
	}

	if got.HealthyBackendCount != 1 {
		t.Fatalf("expected one healthy backend, got %+v", got)
	}

	if got.StaleBackendCount != 1 {
		t.Fatalf("expected one stale backend, got %+v", got)
	}

	if strings.Contains(rec.Body.String(), "localhost:11434") {
		t.Fatalf("config endpoint leaked upstream URL: %s", rec.Body.String())
	}
}
func TestMetricsEndpointReportsResolveAndBackendMutationCounts(t *testing.T) {
	reg := registry.NewMemoryRegistry()
	srv := NewWithConfig(reg, Config{
		DefaultUpstreamURL: "http://localhost:11434",
	})

	putBody := []byte(`{
		"rung": "fleet",
		"url": "http://mac.local:11434",
		"healthy": true,
		"loaded_models": ["llama3.2:3b"],
		"capable_models": ["llama3.2:3b"],
		"p95_latency_ms": 900,
		"max_context": 8192
	}`)

	putReq := httptest.NewRequest(http.MethodPut, "/backends/mac-m2-ollama", bytes.NewReader(putBody))
	putRec := httptest.NewRecorder()
	srv.ServeHTTP(putRec, putReq)

	if putRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", putRec.Code, putRec.Body.String())
	}

	resolveBody := []byte(`{
		"request_id": "req_metrics",
		"model": "llama3.2:3b",
		"deadline_ms": 2000,
		"estimated_input_tokens": 512,
		"estimated_output_tokens": 128,
		"tier": "A"
	}`)

	resolveReq := httptest.NewRequest(http.MethodPost, "/resolve", bytes.NewReader(resolveBody))
	resolveRec := httptest.NewRecorder()
	srv.ServeHTTP(resolveRec, resolveReq)

	if resolveRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", resolveRec.Code, resolveRec.Body.String())
	}

	metricsReq := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	metricsRec := httptest.NewRecorder()
	srv.ServeHTTP(metricsRec, metricsReq)

	if metricsRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", metricsRec.Code, metricsRec.Body.String())
	}

	body := metricsRec.Body.String()

	if !strings.Contains(body, `aerocore_backend_mutations_total{operation="upsert"} 1`) {
		t.Fatalf("missing upsert metric:\n%s", body)
	}

	if !strings.Contains(body, `aerocore_resolve_total{decision="route",rung="fleet",fail_open="false"} 1`) {
		t.Fatalf("missing route metric:\n%s", body)
	}

	if !strings.Contains(body, `aerocore_backends{state="healthy"} 1`) {
		t.Fatalf("missing backend gauge:\n%s", body)
	}

	if !strings.Contains(body, "aerocore_ready 1") {
		t.Fatalf("missing ready gauge:\n%s", body)
	}
}
func TestPutBackendRejectsMissingURL(t *testing.T) {
	srv := New(registry.NewMemoryRegistry())

	body := []byte(`{
		"rung": "fleet",
		"healthy": true,
		"capable_models": ["llama3.2:3b"]
	}`)

	req := httptest.NewRequest(http.MethodPut, "/backends/mac-m2-ollama", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}

	if !strings.Contains(rec.Body.String(), "backend_url_required") {
		t.Fatalf("expected backend_url_required error, got %s", rec.Body.String())
	}
}

func TestPutBackendRejectsMissingModels(t *testing.T) {
	srv := New(registry.NewMemoryRegistry())

	body := []byte(`{
		"rung": "fleet",
		"url": "http://mac.local:11434",
		"healthy": true
	}`)

	req := httptest.NewRequest(http.MethodPut, "/backends/mac-m2-ollama", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}

	if !strings.Contains(rec.Body.String(), "backend_model_required") {
		t.Fatalf("expected backend_model_required error, got %s", rec.Body.String())
	}
}

func TestResolveRejectsMissingRequestID(t *testing.T) {
	srv := New(registry.NewMemoryRegistry())

	body := []byte(`{
		"model": "llama3.2:3b",
		"tier": "A"
	}`)

	req := httptest.NewRequest(http.MethodPost, "/resolve", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}

	if !strings.Contains(rec.Body.String(), "request_id_required") {
		t.Fatalf("expected request_id_required error, got %s", rec.Body.String())
	}
}

func TestResolveRejectsMissingModel(t *testing.T) {
	srv := New(registry.NewMemoryRegistry())

	body := []byte(`{
		"request_id": "req_missing_model",
		"tier": "A"
	}`)

	req := httptest.NewRequest(http.MethodPost, "/resolve", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}

	if !strings.Contains(rec.Body.String(), "model_required") {
		t.Fatalf("expected model_required error, got %s", rec.Body.String())
	}
}

func TestResolveRejectsUnknownTier(t *testing.T) {
	srv := New(registry.NewMemoryRegistry())

	body := []byte(`{
		"request_id": "req_bad_tier",
		"model": "llama3.2:3b",
		"tier": "C"
	}`)

	req := httptest.NewRequest(http.MethodPost, "/resolve", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}

	if !strings.Contains(rec.Body.String(), "invalid_tier") {
		t.Fatalf("expected invalid_tier error, got %s", rec.Body.String())
	}
}
