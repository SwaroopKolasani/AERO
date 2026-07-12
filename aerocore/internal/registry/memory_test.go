package registry

import (
	"testing"
	"time"

	"github.com/swaroop/aero/aerocore/internal/placement"
)

func TestDeleteBackend(t *testing.T) {
	reg := NewMemoryRegistry()
	reg.UpsertBackend(placement.Backend{
		ID:      "mac-m2-ollama",
		Rung:    placement.RungFleet,
		Healthy: true,
	})

	if !reg.DeleteBackend("mac-m2-ollama") {
		t.Fatal("expected delete to return true")
	}

	if _, ok := reg.GetBackend("mac-m2-ollama"); ok {
		t.Fatal("expected backend to be deleted")
	}
}

func TestSetHealthUpdatesBackend(t *testing.T) {
	reg := NewMemoryRegistry()
	reg.UpsertBackend(placement.Backend{
		ID:      "mac-m2-ollama",
		Rung:    placement.RungFleet,
		Healthy: true,
	})

	got, ok := reg.SetHealth("mac-m2-ollama", false)
	if !ok {
		t.Fatal("expected backend to exist")
	}

	if got.Healthy {
		t.Fatalf("expected backend unhealthy, got %+v", got)
	}

	if got.UpdatedAt.IsZero() {
		t.Fatalf("expected updated_at to be set, got %+v", got)
	}
}

func TestBackendBecomesUnhealthyWhenHeartbeatStale(t *testing.T) {
	reg := NewMemoryRegistryWithTTL(10 * time.Millisecond)

	reg.UpsertBackend(placement.Backend{
		ID:        "mac-m2-ollama",
		Rung:      placement.RungFleet,
		Healthy:   true,
		UpdatedAt: time.Now().Add(-time.Second).UTC(),
	})

	got, ok := reg.GetBackend("mac-m2-ollama")
	if !ok {
		t.Fatal("expected backend to exist")
	}

	if got.Healthy {
		t.Fatalf("expected stale backend to be marked unhealthy, got %+v", got)
	}
}
