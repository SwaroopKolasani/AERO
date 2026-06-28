//Thread-safe in-memory stats tracker & Prometheus exporter


package metrics

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type CacheResult string

const (
	ResultHit       CacheResult = "hit"
	ResultMiss      CacheResult = "miss"
	ResultCoalesced CacheResult = "coalesced"
	ResultBypass    CacheResult = "bypass"
	ResultError     CacheResult = "error"
)

type Observation struct {
	Endpoint        string
	Result          CacheResult
	Tier            string
	StatusCode      string
	BypassReason    string
	Latency         time.Duration
	TokensOut       int
	GPUSecondsSaved float64
	CostSavedUSD    float64
}

type TierStats struct {
	Requests  uint64  `json:"requests"`
	Hits      uint64  `json:"hits"`
	Misses    uint64  `json:"misses"`
	Bypass    uint64  `json:"bypass"`
	Coalesced uint64  `json:"coalesced"`
	Errors    uint64  `json:"errors"`
	LatencyMS float64 `json:"latency_ms_avg"`
}

type EndpointStats struct {
	Requests  uint64  `json:"requests"`
	LatencyMS float64 `json:"latency_ms_avg"`
}

type Snapshot struct {
	StartedAt               time.Time                `json:"started_at"`
	UptimeSeconds           float64                  `json:"uptime_seconds"`
	Requests                uint64                   `json:"requests"`
	Hits                    uint64                   `json:"hits"`
	Misses                  uint64                   `json:"misses"`
	Coalesced               uint64                   `json:"coalesced"`
	Bypass                  uint64                   `json:"bypass"`
	Errors                  uint64                   `json:"errors"`
	HitRatio                float64                  `json:"hit_ratio"`
	GPUSecondsSaved         float64                  `json:"gpu_seconds_saved"`
	USDSaved                float64                  `json:"usd_saved"`
	TokensOut               uint64                   `json:"tokens_out"`
	VerifyMismatch          uint64                   `json:"verify_mismatch"`
	WritebackQueueDepth     int64                    `json:"writeback_queue_depth"`
	WritebackDropped        uint64                   `json:"writeback_dropped"`
	UpstreamCalls           uint64                   `json:"upstream_calls"`
	PerTier                 map[string]TierStats     `json:"per_tier"`
	PerEndpoint             map[string]EndpointStats `json:"per_endpoint"`
	TierA                   TierExactStats           `json:"tier_a"`
	TierB                   TierSemanticStats        `json:"tier_b"`
}

type TierExactStats struct {
	Requests       uint64  `json:"requests"`
	Hits           uint64  `json:"hits"`
	HitRatio       float64 `json:"hit_ratio"`
	VerifyMismatch uint64  `json:"verify_mismatch"`
}

type TierSemanticStats struct {
	Requests uint64 `json:"requests"`
	Hits     uint64 `json:"hits"`
	Enabled  bool   `json:"enabled"`
}

type Registry struct {
	mu      sync.RWMutex
	started time.Time

	requests  uint64
	hits      uint64
	misses    uint64
	coalesced uint64
	bypass    uint64
	errors    uint64

	gpuSecondsSaved float64
	usdSaved        float64
	tokensOut       uint64

	verifyMismatch      uint64
	writebackQueueDepth int64
	writebackDropped    uint64
	upstreamCalls       uint64

	perTier     map[string]*tierAccumulator
	perEndpoint map[string]*endpointAccumulator

	prom *prometheus.Registry

	requestsTotal       *prometheus.CounterVec
	bypassTotal         *prometheus.CounterVec
	verifyMismatchTotal prometheus.Counter
	writebackDroppedCtr prometheus.Counter
	upstreamCallsTotal  prometheus.Counter

	hitRatioGauge            prometheus.Gauge
	writebackQueueDepthGauge prometheus.Gauge
	gpuSecondsSavedTotal     prometheus.Counter
	costSavedUSDTotal        prometheus.Counter

	requestLatency *prometheus.HistogramVec
}

type tierAccumulator struct {
	requests       uint64
	hits           uint64
	misses         uint64
	bypass         uint64
	coalesced      uint64
	errors         uint64
	latencyTotalMS float64
}

type endpointAccumulator struct {
	requests       uint64
	latencyTotalMS float64
}

func NewRegistry() *Registry {
	promReg := prometheus.NewRegistry()

	r := &Registry{
		started:     time.Now(),
		perTier:     map[string]*tierAccumulator{},
		perEndpoint: map[string]*endpointAccumulator{},
		prom:        promReg,

		requestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "aero_cache_requests_total",
				Help: "AeroCache requests partitioned by result, tier, endpoint, and status.",
			},
			[]string{"result", "tier", "endpoint", "status"},
		),

		bypassTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "aero_cache_bypass_total",
				Help: "AeroCache bypasses partitioned by reason.",
			},
			[]string{"reason"},
		),

		verifyMismatchTotal: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "aero_cache_verify_mismatch_total",
				Help: "Verified-hit candidates rejected because stored material did not match live request or response hash.",
			},
		),

		writebackDroppedCtr: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "aero_cache_writeback_dropped_total",
				Help: "Write-back jobs dropped because the bounded queue was full.",
			},
		),

		upstreamCallsTotal: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "aero_upstream_calls_total",
				Help: "Requests forwarded to the upstream model or placement backend.",
			},
		),

		hitRatioGauge: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "aero_cache_hit_ratio",
				Help: "Exact Tier-A cache hit ratio.",
			},
		),

		writebackQueueDepthGauge: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "aero_cache_writeback_queue_depth",
				Help: "Current write-back queue depth.",
			},
		),

		gpuSecondsSavedTotal: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "aero_gpu_seconds_saved_total",
				Help: "Estimated GPU seconds saved by verified exact cache hits.",
			},
		),

		costSavedUSDTotal: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "aero_cost_saved_usd_total",
				Help: "Estimated USD saved by verified exact cache hits.",
			},
		),

		requestLatency: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "aero_cache_request_seconds",
				Help:    "AeroCache request latency by endpoint and result.",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"endpoint", "result"},
		),
	}

	promReg.MustRegister(
		r.requestsTotal,
		r.bypassTotal,
		r.verifyMismatchTotal,
		r.writebackDroppedCtr,
		r.upstreamCallsTotal,
		r.hitRatioGauge,
		r.writebackQueueDepthGauge,
		r.gpuSecondsSavedTotal,
		r.costSavedUSDTotal,
		r.requestLatency,
	)

	return r
}

func (r *Registry) Prometheus() *prometheus.Registry {
	return r.prom
}

func (r *Registry) ObserveRequest(obs Observation) {
	if obs.Endpoint == "" {
		obs.Endpoint = "unknown"
	}
	if obs.Tier == "" {
		obs.Tier = "none"
	}
	if obs.StatusCode == "" {
		obs.StatusCode = "0"
	}
	if obs.Result == "" {
		obs.Result = ResultError
	}

	latencyMS := float64(obs.Latency.Microseconds()) / 1000.0

	r.mu.Lock()

	r.requests++
	r.tokensOut += uint64(max(obs.TokensOut, 0))
	r.gpuSecondsSaved += maxFloat(obs.GPUSecondsSaved, 0)
	r.usdSaved += maxFloat(obs.CostSavedUSD, 0)

	switch obs.Result {
	case ResultHit:
		r.hits++
	case ResultMiss:
		r.misses++
	case ResultCoalesced:
		r.coalesced++
	case ResultBypass:
		r.bypass++
	default:
		r.errors++
	}

	tier := r.perTier[obs.Tier]
	if tier == nil {
		tier = &tierAccumulator{}
		r.perTier[obs.Tier] = tier
	}
	tier.requests++
	tier.latencyTotalMS += latencyMS

	switch obs.Result {
	case ResultHit:
		tier.hits++
	case ResultMiss:
		tier.misses++
	case ResultCoalesced:
		tier.coalesced++
	case ResultBypass:
		tier.bypass++
	default:
		tier.errors++
	}

	endpoint := r.perEndpoint[obs.Endpoint]
	if endpoint == nil {
		endpoint = &endpointAccumulator{}
		r.perEndpoint[obs.Endpoint] = endpoint
	}
	endpoint.requests++
	endpoint.latencyTotalMS += latencyMS

	hitRatio := r.hitRatioLocked()

	r.mu.Unlock()

	r.requestsTotal.WithLabelValues(
		string(obs.Result),
		obs.Tier,
		obs.Endpoint,
		obs.StatusCode,
	).Inc()

	if obs.Result == ResultBypass {
		reason := obs.BypassReason
		if reason == "" {
			reason = "unspecified"
		}
		r.bypassTotal.WithLabelValues(reason).Inc()
	}

	if obs.GPUSecondsSaved > 0 {
		r.gpuSecondsSavedTotal.Add(obs.GPUSecondsSaved)
	}

	if obs.CostSavedUSD > 0 {
		r.costSavedUSDTotal.Add(obs.CostSavedUSD)
	}

	r.hitRatioGauge.Set(hitRatio)
	r.requestLatency.WithLabelValues(obs.Endpoint, string(obs.Result)).Observe(obs.Latency.Seconds())
}

func (r *Registry) IncVerifyMismatch() {
	r.mu.Lock()
	r.verifyMismatch++
	r.mu.Unlock()

	r.verifyMismatchTotal.Inc()
}

func (r *Registry) IncWritebackDropped() {
	r.mu.Lock()
	r.writebackDropped++
	r.mu.Unlock()

	r.writebackDroppedCtr.Inc()
}

func (r *Registry) SetWritebackQueueDepth(depth int64) {
	if depth < 0 {
		depth = 0
	}

	r.mu.Lock()
	r.writebackQueueDepth = depth
	r.mu.Unlock()

	r.writebackQueueDepthGauge.Set(float64(depth))
}

func (r *Registry) IncUpstreamCall() {
	r.mu.Lock()
	r.upstreamCalls++
	r.mu.Unlock()

	r.upstreamCallsTotal.Inc()
}

func (r *Registry) Snapshot() Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()

	perTier := make(map[string]TierStats, len(r.perTier))
	for name, acc := range r.perTier {
		var latencyAvg float64
		if acc.requests > 0 {
			latencyAvg = acc.latencyTotalMS / float64(acc.requests)
		}

		perTier[name] = TierStats{
			Requests:  acc.requests,
			Hits:      acc.hits,
			Misses:    acc.misses,
			Bypass:    acc.bypass,
			Coalesced: acc.coalesced,
			Errors:    acc.errors,
			LatencyMS: latencyAvg,
		}
	}

	perEndpoint := make(map[string]EndpointStats, len(r.perEndpoint))
	for name, acc := range r.perEndpoint {
		var latencyAvg float64
		if acc.requests > 0 {
			latencyAvg = acc.latencyTotalMS / float64(acc.requests)
		}

		perEndpoint[name] = EndpointStats{
			Requests:  acc.requests,
			LatencyMS: latencyAvg,
		}
	}

	hitRatio := r.hitRatioLocked()

	return Snapshot{
		StartedAt:           r.started,
		UptimeSeconds:       time.Since(r.started).Seconds(),
		Requests:            r.requests,
		Hits:                r.hits,
		Misses:              r.misses,
		Coalesced:           r.coalesced,
		Bypass:              r.bypass,
		Errors:              r.errors,
		HitRatio:            hitRatio,
		GPUSecondsSaved:     r.gpuSecondsSaved,
		USDSaved:            r.usdSaved,
		TokensOut:           r.tokensOut,
		VerifyMismatch:      r.verifyMismatch,
		WritebackQueueDepth: r.writebackQueueDepth,
		WritebackDropped:    r.writebackDropped,
		UpstreamCalls:       r.upstreamCalls,
		PerTier:             perTier,
		PerEndpoint:         perEndpoint,

		TierA: TierExactStats{
			Requests:       r.requests,
			Hits:           r.hits,
			HitRatio:       hitRatio,
			VerifyMismatch: r.verifyMismatch,
		},

		TierB: TierSemanticStats{
			Requests: 0,
			Hits:     0,
			Enabled:  false,
		},
	}
}

func (r *Registry) hitRatioLocked() float64 {
	denom := r.hits + r.misses + r.coalesced + r.bypass + r.errors
	if denom == 0 {
		return 0
	}
	return float64(r.hits) / float64(denom)
}

func max(v int, floor int) int {
	if v < floor {
		return floor
	}
	return v
}

func maxFloat(v float64, floor float64) float64 {
	if v < floor {
		return floor
	}
	return v
}