package metrics

import (
	"strings"
	"testing"

	"github.com/swaroop/aero/aerocore/internal/placement"
)

func TestRenderResolveMetrics(t *testing.T) {
	m := New()

	m.IncResolve(placement.PlacementResponse{
		Decision: placement.DecisionRoute,
		Rung:     placement.RungFleet,
		FailOpen: false,
	})

	body := m.Render(nil, true)

	if !strings.Contains(body, `aerocore_resolve_total{decision="route",rung="fleet",fail_open="false"} 1`) {
		t.Fatalf("missing resolve metric:\n%s", body)
	}

	if !strings.Contains(body, "aerocore_ready 1") {
		t.Fatalf("missing ready metric:\n%s", body)
	}
}

func TestRenderBackendMutationMetrics(t *testing.T) {
	m := New()

	m.IncBackendMutation("upsert")
	m.IncBackendMutation("upsert")
	m.IncBackendMutation("delete")

	body := m.Render(nil, false)

	if !strings.Contains(body, `aerocore_backend_mutations_total{operation="upsert"} 2`) {
		t.Fatalf("missing upsert metric:\n%s", body)
	}

	if !strings.Contains(body, `aerocore_backend_mutations_total{operation="delete"} 1`) {
		t.Fatalf("missing delete metric:\n%s", body)
	}

	if !strings.Contains(body, "aerocore_ready 0") {
		t.Fatalf("missing not-ready metric:\n%s", body)
	}
}

func TestRenderBackendGaugeMetrics(t *testing.T) {
	m := New()

	body := m.Render([]placement.Backend{
		{
			ID:      "healthy",
			Healthy: true,
		},
		{
			ID:      "unhealthy",
			Healthy: false,
		},
	}, true)

	if !strings.Contains(body, `aerocore_backends{state="healthy"} 1`) {
		t.Fatalf("missing healthy backend gauge:\n%s", body)
	}

	if !strings.Contains(body, `aerocore_backends{state="unhealthy"} 1`) {
		t.Fatalf("missing unhealthy backend gauge:\n%s", body)
	}

	if !strings.Contains(body, `aerocore_backends{state="total"} 2`) {
		t.Fatalf("missing total backend gauge:\n%s", body)
	}
}
