package placement

import "testing"

type fakeBackendSource struct {
	backends []Backend
}

func (f fakeBackendSource) ListBackends() []Backend {
	return f.backends
}

func baseRequest() PlacementRequest {
	return PlacementRequest{
		RequestID:             "req_test",
		Model:                 "llama3.2:3b",
		DeadlineMS:            2000,
		EstimatedInputTokens:  512,
		EstimatedOutputTokens: 128,
		Stream:                true,
		Tier:                  TierA,
	}
}

func TestHealthyCapableMacWinsOverCloud(t *testing.T) {
	src := fakeBackendSource{backends: []Backend{
		{
			ID:              "mac-m2-ollama",
			URL:             "http://mac.local:11434",
			Rung:            RungFleet,
			Healthy:         true,
			LoadedModels:    []string{"llama3.2:3b"},
			CostPer1KTokens: 0,
			P95LatencyMS:    900,
			MaxContext:      8192,
		},
		{
			ID:              "cloud-gate",
			URL:             "https://cloud.example/v1",
			Rung:            RungGate,
			Healthy:         true,
			CapableModels:   []string{"llama3.2:3b"},
			CostPer1KTokens: 0.01,
			P95LatencyMS:    500,
			MaxContext:      8192,
		},
	}}

	got := NewResolver(src).Resolve(baseRequest())

	if got.Decision != DecisionRoute || got.BackendID != "mac-m2-ollama" || got.Rung != RungFleet {
		t.Fatalf("expected mac fleet route, got %+v", got)
	}
}

func TestUnhealthyMacIsSkipped(t *testing.T) {
	src := fakeBackendSource{backends: []Backend{
		{
			ID:           "mac-m2-ollama",
			URL:          "http://mac.local:11434",
			Rung:         RungFleet,
			Healthy:      false,
			LoadedModels: []string{"llama3.2:3b"},
			P95LatencyMS: 400,
		},
		{
			ID:            "cloud-gate",
			URL:           "https://cloud.example/v1",
			Rung:          RungGate,
			Healthy:       true,
			CapableModels: []string{"llama3.2:3b"},
			P95LatencyMS:  800,
		},
	}}

	got := NewResolver(src).Resolve(baseRequest())

	if got.BackendID != "cloud-gate" {
		t.Fatalf("expected cloud fallback, got %+v", got)
	}
}

func TestIncapableMacIsSkipped(t *testing.T) {
	src := fakeBackendSource{backends: []Backend{
		{
			ID:            "mac-m2-ollama",
			URL:           "http://mac.local:11434",
			Rung:          RungFleet,
			Healthy:       true,
			CapableModels: []string{"qwen2.5:3b"},
			P95LatencyMS:  400,
		},
		{
			ID:            "cloud-gate",
			URL:           "https://cloud.example/v1",
			Rung:          RungGate,
			Healthy:       true,
			CapableModels: []string{"llama3.2:3b"},
			P95LatencyMS:  800,
		},
	}}

	got := NewResolver(src).Resolve(baseRequest())

	if got.BackendID != "cloud-gate" {
		t.Fatalf("expected cloud because mac cannot run model, got %+v", got)
	}
}

func TestDeadlineFiltersSlowBackend(t *testing.T) {
	src := fakeBackendSource{backends: []Backend{
		{
			ID:            "slow-mac",
			URL:           "http://mac.local:11434",
			Rung:          RungFleet,
			Healthy:       true,
			CapableModels: []string{"llama3.2:3b"},
			P95LatencyMS:  3000,
		},
		{
			ID:            "fast-cloud",
			URL:           "https://cloud.example/v1",
			Rung:          RungGate,
			Healthy:       true,
			CapableModels: []string{"llama3.2:3b"},
			P95LatencyMS:  1000,
		},
	}}

	got := NewResolver(src).Resolve(baseRequest())

	if got.BackendID != "fast-cloud" {
		t.Fatalf("expected fast cloud, got %+v", got)
	}
}

func TestCheaperBackendWinsBeforeFasterExpensiveBackend(t *testing.T) {
	src := fakeBackendSource{backends: []Backend{
		{
			ID:              "free-mac",
			URL:             "http://mac.local:11434",
			Rung:            RungFleet,
			Healthy:         true,
			CapableModels:   []string{"llama3.2:3b"},
			CostPer1KTokens: 0,
			P95LatencyMS:    1500,
		},
		{
			ID:              "paid-cloud",
			URL:             "https://cloud.example/v1",
			Rung:            RungGate,
			Healthy:         true,
			CapableModels:   []string{"llama3.2:3b"},
			CostPer1KTokens: 0.01,
			P95LatencyMS:    200,
		},
	}}

	got := NewResolver(src).Resolve(baseRequest())

	if got.BackendID != "free-mac" {
		t.Fatalf("expected free mac before faster paid cloud, got %+v", got)
	}
}

func TestFailOpenWhenNoBackendQualifies(t *testing.T) {
	src := fakeBackendSource{backends: []Backend{
		{
			ID:            "wrong-model",
			URL:           "http://mac.local:11434",
			Rung:          RungFleet,
			Healthy:       true,
			CapableModels: []string{"qwen2.5:3b"},
			P95LatencyMS:  500,
		},
	}}

	got := NewResolver(src).Resolve(baseRequest())

	if got.Decision != DecisionFailOpen || !got.FailOpen || got.BackendID != "default-upstream" {
		t.Fatalf("expected fail-open, got %+v", got)
	}
}

func TestTierBRejectedInM3(t *testing.T) {
	src := fakeBackendSource{backends: []Backend{
		{
			ID:            "mac-m2-ollama",
			URL:           "http://mac.local:11434",
			Rung:          RungFleet,
			Healthy:       true,
			CapableModels: []string{"llama3.2:3b"},
			P95LatencyMS:  500,
		},
	}}

	req := baseRequest()
	req.Tier = TierB

	got := NewResolver(src).Resolve(req)

	if got.Decision != DecisionReject || got.Reason != "tier_b_not_supported_in_m3" {
		t.Fatalf("expected Tier-B rejection, got %+v", got)
	}
}

func TestRouteResponseIncludesBackendURL(t *testing.T) {
	src := fakeBackendSource{backends: []Backend{
		{
			ID:            "mac-m2-ollama",
			URL:           "http://mac.local:11434",
			Rung:          RungFleet,
			Healthy:       true,
			CapableModels: []string{"llama3.2:3b"},
			P95LatencyMS:  900,
			MaxContext:    8192,
		},
	}}

	got := NewResolver(src).Resolve(baseRequest())

	if got.Decision != DecisionRoute {
		t.Fatalf("expected route, got %+v", got)
	}

	if got.BackendURL != "http://mac.local:11434" {
		t.Fatalf("expected backend_url, got %+v", got)
	}
}

func TestBackendWithoutURLIsSkipped(t *testing.T) {
	src := fakeBackendSource{backends: []Backend{
		{
			ID:            "mac-m2-ollama",
			URL:           "http://mac.local:11434",
			Rung:          RungFleet,
			Healthy:       true,
			CapableModels: []string{"llama3.2:3b"},
			P95LatencyMS:  900,
			MaxContext:    8192,
		},
	}}

	got := NewResolver(src).Resolve(baseRequest())

	if got.Decision != DecisionFailOpen {
		t.Fatalf("expected fail-open because backend has no URL, got %+v", got)
	}
}
func TestFailOpenIncludesConfiguredDefaultUpstreamURL(t *testing.T) {
	src := fakeBackendSource{backends: nil}

	got := NewResolver(
		src,
		WithDefaultUpstreamURL("http://localhost:11434"),
	).Resolve(baseRequest())

	if got.Decision != DecisionFailOpen {
		t.Fatalf("expected fail-open, got %+v", got)
	}

	if got.BackendID != "default-upstream" {
		t.Fatalf("expected default upstream backend id, got %+v", got)
	}

	if got.BackendURL != "http://localhost:11434" {
		t.Fatalf("expected configured default upstream URL, got %+v", got)
	}

	if got.Rung != RungUpstream {
		t.Fatalf("expected upstream rung, got %+v", got)
	}
}
