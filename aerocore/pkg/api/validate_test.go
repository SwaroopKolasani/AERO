package api

import "testing"

func TestValidatePlacementRequestAcceptsTierA(t *testing.T) {
	req := PlacementRequest{
		RequestID: "req_valid",
		Model:     "llama3.2:3b",
		Tier:      TierA,
	}

	if err := ValidatePlacementRequest(req); err != nil {
		t.Fatalf("expected valid request, got %v", err)
	}
}

func TestValidatePlacementRequestRejectsMissingRequestID(t *testing.T) {
	req := PlacementRequest{
		Model: "llama3.2:3b",
		Tier:  TierA,
	}

	if err := ValidatePlacementRequest(req); err == nil {
		t.Fatal("expected request_id_required error")
	}
}

func TestValidatePlacementRequestRejectsMissingModel(t *testing.T) {
	req := PlacementRequest{
		RequestID: "req_missing_model",
		Tier:      TierA,
	}

	if err := ValidatePlacementRequest(req); err == nil {
		t.Fatal("expected model_required error")
	}
}

func TestValidatePlacementRequestRejectsUnknownTier(t *testing.T) {
	req := PlacementRequest{
		RequestID: "req_bad_tier",
		Model:     "llama3.2:3b",
		Tier:      Tier("C"),
	}

	if err := ValidatePlacementRequest(req); err == nil {
		t.Fatal("expected invalid_tier error")
	}
}

func TestValidatePlacementRequestAllowsTierBForResolverRejectPath(t *testing.T) {
	req := PlacementRequest{
		RequestID: "req_tier_b",
		Model:     "llama3.2:3b",
		Tier:      TierB,
	}

	if err := ValidatePlacementRequest(req); err != nil {
		t.Fatalf("expected Tier-B to pass API validation, got %v", err)
	}
}

func TestValidatePlacementRequestRejectsNegativeTokens(t *testing.T) {
	req := PlacementRequest{
		RequestID:             "req_negative",
		Model:                 "llama3.2:3b",
		EstimatedInputTokens:  -1,
		EstimatedOutputTokens: 0,
		Tier:                  TierA,
	}

	if err := ValidatePlacementRequest(req); err == nil {
		t.Fatal("expected negative token error")
	}
}

func TestValidateBackendAcceptsValidBackend(t *testing.T) {
	b := Backend{
		ID:            "mac-m2-ollama",
		Rung:          RungFleet,
		URL:           "http://mac.local:11434",
		Healthy:       true,
		LoadedModels:  []string{"llama3.2:3b"},
		CapableModels: []string{"llama3.2:3b"},
		P95LatencyMS:  900,
		MaxContext:    8192,
	}

	if err := ValidateBackend(b); err != nil {
		t.Fatalf("expected valid backend, got %v", err)
	}
}

func TestValidateBackendRejectsMissingID(t *testing.T) {
	b := Backend{
		Rung:          RungFleet,
		URL:           "http://mac.local:11434",
		CapableModels: []string{"llama3.2:3b"},
	}

	if err := ValidateBackend(b); err == nil {
		t.Fatal("expected backend_id_required error")
	}
}

func TestValidateBackendRejectsInvalidRung(t *testing.T) {
	b := Backend{
		ID:            "bad-rung",
		Rung:          Rung("bad"),
		URL:           "http://mac.local:11434",
		CapableModels: []string{"llama3.2:3b"},
	}

	if err := ValidateBackend(b); err == nil {
		t.Fatal("expected invalid backend rung error")
	}
}

func TestValidateBackendRejectsMissingURL(t *testing.T) {
	b := Backend{
		ID:            "missing-url",
		Rung:          RungFleet,
		CapableModels: []string{"llama3.2:3b"},
	}

	if err := ValidateBackend(b); err == nil {
		t.Fatal("expected backend_url_required error")
	}
}

func TestValidateBackendRejectsNonHTTPURL(t *testing.T) {
	b := Backend{
		ID:            "bad-url",
		Rung:          RungFleet,
		URL:           "file:///tmp/socket",
		CapableModels: []string{"llama3.2:3b"},
	}

	if err := ValidateBackend(b); err == nil {
		t.Fatal("expected backend_url_must_be_http_or_https error")
	}
}

func TestValidateBackendRejectsMissingModels(t *testing.T) {
	b := Backend{
		ID:   "no-models",
		Rung: RungFleet,
		URL:  "http://mac.local:11434",
	}

	if err := ValidateBackend(b); err == nil {
		t.Fatal("expected backend_model_required error")
	}
}

func TestValidateBackendRejectsNegativeLatency(t *testing.T) {
	b := Backend{
		ID:            "negative-latency",
		Rung:          RungFleet,
		URL:           "http://mac.local:11434",
		CapableModels: []string{"llama3.2:3b"},
		P95LatencyMS:  -1,
	}

	if err := ValidateBackend(b); err == nil {
		t.Fatal("expected negative latency error")
	}
}
