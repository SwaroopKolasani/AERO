package registry

import (
	"sync"

	"github.com/swaroop/aero/aerocore/internal/placement"
)

type MemoryRegistry struct {
	mu       sync.RWMutex
	backends map[string]placement.Backend
}

func NewMemoryRegistry() *MemoryRegistry {
	return &MemoryRegistry{
		backends: make(map[string]placement.Backend),
	}
}

func (r *MemoryRegistry) UpsertBackend(b placement.Backend) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.backends[b.ID] = b
}

func (r *MemoryRegistry) ListBackends() []placement.Backend {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]placement.Backend, 0, len(r.backends))
	for _, b := range r.backends {
		out = append(out, b)
	}
	return out
}