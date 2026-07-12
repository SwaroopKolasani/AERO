package registry

import (
	"sync"
	"time"

	"github.com/swaroop/aero/aerocore/internal/placement"
)

const defaultHeartbeatTTL = 30 * time.Second

type MemoryRegistry struct {
	mu           sync.RWMutex
	backends     map[string]placement.Backend
	heartbeatTTL time.Duration
	now          func() time.Time
}

func NewMemoryRegistry() *MemoryRegistry {
	return &MemoryRegistry{
		backends:     make(map[string]placement.Backend),
		heartbeatTTL: defaultHeartbeatTTL,
		now:          time.Now,
	}
}

func NewMemoryRegistryWithTTL(ttl time.Duration) *MemoryRegistry {
	r := NewMemoryRegistry()
	r.heartbeatTTL = ttl
	return r
}

func (r *MemoryRegistry) UpsertBackend(b placement.Backend) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if b.UpdatedAt.IsZero() {
		b.UpdatedAt = r.now().UTC()
	}

	r.backends[b.ID] = b
}

func (r *MemoryRegistry) GetBackend(id string) (placement.Backend, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.markStaleLocked()

	b, ok := r.backends[id]
	return b, ok
}

func (r *MemoryRegistry) DeleteBackend(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.backends[id]; !ok {
		return false
	}

	delete(r.backends, id)
	return true
}

func (r *MemoryRegistry) SetHealth(id string, healthy bool) (placement.Backend, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	b, ok := r.backends[id]
	if !ok {
		return placement.Backend{}, false
	}

	b.Healthy = healthy
	b.UpdatedAt = r.now().UTC()
	r.backends[id] = b

	return b, true
}

func (r *MemoryRegistry) Heartbeat(id string) (placement.Backend, bool) {
	return r.SetHealth(id, true)
}

func (r *MemoryRegistry) ListBackends() []placement.Backend {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.markStaleLocked()

	out := make([]placement.Backend, 0, len(r.backends))
	for _, b := range r.backends {
		out = append(out, b)
	}
	return out
}

func (r *MemoryRegistry) markStaleLocked() {
	if r.heartbeatTTL <= 0 {
		return
	}

	now := r.now().UTC()

	for id, b := range r.backends {
		if b.UpdatedAt.IsZero() {
			b.UpdatedAt = now
			r.backends[id] = b
			continue
		}

		if b.Healthy && now.Sub(b.UpdatedAt) > r.heartbeatTTL {
			b.Healthy = false
			r.backends[id] = b
		}
	}
}
