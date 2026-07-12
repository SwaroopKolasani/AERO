package metrics

import (
	"bytes"
	"fmt"
	"sort"
	"sync"

	"github.com/swaroop/aero/aerocore/internal/placement"
)

type Metrics struct {
	mu               sync.Mutex
	resolveTotal     map[string]int64
	backendMutations map[string]int64
}

func New() *Metrics {
	return &Metrics{
		resolveTotal:     make(map[string]int64),
		backendMutations: make(map[string]int64),
	}
}

func (m *Metrics) IncResolve(resp placement.PlacementResponse) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := resolveKey(resp.Decision, resp.Rung, resp.FailOpen)
	m.resolveTotal[key]++
}

func (m *Metrics) IncBackendMutation(operation string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.backendMutations[operation]++
}

func (m *Metrics) Render(backends []placement.Backend, ready bool) string {
	m.mu.Lock()
	defer m.mu.Unlock()

	var buf bytes.Buffer

	buf.WriteString("# HELP aerocore_resolve_total Total placement resolve decisions.\n")
	buf.WriteString("# TYPE aerocore_resolve_total counter\n")

	resolveKeys := sortedKeys(m.resolveTotal)
	for _, key := range resolveKeys {
		decision, rung, failOpen := splitResolveKey(key)
		fmt.Fprintf(
			&buf,
			"aerocore_resolve_total{decision=%q,rung=%q,fail_open=%q} %d\n",
			decision,
			rung,
			failOpen,
			m.resolveTotal[key],
		)
	}

	buf.WriteString("# HELP aerocore_backend_mutations_total Total backend registry mutations.\n")
	buf.WriteString("# TYPE aerocore_backend_mutations_total counter\n")

	mutationKeys := sortedKeys(m.backendMutations)
	for _, op := range mutationKeys {
		fmt.Fprintf(
			&buf,
			"aerocore_backend_mutations_total{operation=%q} %d\n",
			op,
			m.backendMutations[op],
		)
	}

	healthy := 0
	unhealthy := 0

	for _, b := range backends {
		if b.Healthy {
			healthy++
		} else {
			unhealthy++
		}
	}

	buf.WriteString("# HELP aerocore_backends Current backend count by health state.\n")
	buf.WriteString("# TYPE aerocore_backends gauge\n")
	fmt.Fprintf(&buf, "aerocore_backends{state=%q} %d\n", "healthy", healthy)
	fmt.Fprintf(&buf, "aerocore_backends{state=%q} %d\n", "unhealthy", unhealthy)
	fmt.Fprintf(&buf, "aerocore_backends{state=%q} %d\n", "total", len(backends))

	buf.WriteString("# HELP aerocore_ready Readiness state, 1 ready and 0 not ready.\n")
	buf.WriteString("# TYPE aerocore_ready gauge\n")

	readyValue := 0
	if ready {
		readyValue = 1
	}
	fmt.Fprintf(&buf, "aerocore_ready %d\n", readyValue)

	return buf.String()
}

func resolveKey(decision placement.Decision, rung placement.Rung, failOpen bool) string {
	return string(decision) + "|" + string(rung) + "|" + fmt.Sprintf("%t", failOpen)
}

func splitResolveKey(key string) (decision string, rung string, failOpen string) {
	parts := [3]string{}

	part := 0
	start := 0
	for i := 0; i <= len(key); i++ {
		if i == len(key) || key[i] == '|' {
			if part < len(parts) {
				parts[part] = key[start:i]
			}
			part++
			start = i + 1
		}
	}

	return parts[0], parts[1], parts[2]
}

func sortedKeys(values map[string]int64) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
