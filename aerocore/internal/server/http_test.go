package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
