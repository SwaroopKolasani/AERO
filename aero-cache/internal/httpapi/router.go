//HTTP Handlers (OpenAI SDK endpoints + UI assets)

package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"aero-cache/internal/gate"
	"aero-cache/internal/metrics"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const maxRequestBodyBytes = 32 << 20 // 32 MiB

type Config struct {
	SPAPath            string
	Debug              bool
	GateMode           gate.Mode
	TokenizerAvailable bool
}

type Server struct {
	cfg   Config
	stats *metrics.Registry
	gate  *gate.Decider
}

func NewRouter(cfg Config, stats *metrics.Registry) http.Handler {
	if cfg.SPAPath == "" {
		cfg.SPAPath = "web/dist"
	}

	if cfg.GateMode == "" {
		cfg.GateMode = gate.ModeStrict
	}

	s := &Server{
		cfg:   cfg,
		stats: stats,
		gate: gate.NewDecider(gate.Config{
			Mode:               cfg.GateMode,
			TokenizerAvailable: cfg.TokenizerAvailable,
		}),
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", s.healthz)
	mux.HandleFunc("/readyz", s.readyz)
	mux.HandleFunc("/stats", s.statsJSON)

	mux.Handle("/metrics", promhttp.HandlerFor(
		stats.Prometheus(),
		promhttp.HandlerOpts{},
	))

	mux.HandleFunc("/v1/chat/completions", s.openAIHandler("/v1/chat/completions"))
	mux.HandleFunc("/v1/completions", s.openAIHandler("/v1/completions"))
	mux.HandleFunc("/v1/embeddings", s.openAIHandler("/v1/embeddings"))

	mux.HandleFunc("/aerobench/", s.spa)
	mux.HandleFunc("/", s.spa)

	return withCommonMiddleware(mux)
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
	})
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}

	// Later: check Valkey, tokenizer registry, upstream config.
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ready",
	})
}

func (s *Server) statsJSON(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, s.stats.Snapshot())
}

func (s *Server) openAIHandler(endpoint string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		if !allowMethod(w, r, http.MethodPost) {
			return
		}

		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBodyBytes))
		if err != nil {
			s.observe(endpoint, metrics.ResultError, "none", http.StatusRequestEntityTooLarge, start, "")
			writeAeroHeaders(w, metrics.ResultError, "none", start, 0, 0)
			writeOpenAIError(w, http.StatusRequestEntityTooLarge, "request body too large", "request_too_large")
			return
		}

		if len(body) == 0 || !json.Valid(body) {
			s.observe(endpoint, metrics.ResultError, "none", http.StatusBadRequest, start, "")
			writeAeroHeaders(w, metrics.ResultError, "none", start, 0, 0)
			writeOpenAIError(w, http.StatusBadRequest, "invalid JSON request body", "invalid_json")
			return
		}

		decision := s.gate.Evaluate(body)

		if !decision.Cacheable {
			// Correct behavior once upstream exists:
			// bypass cache entirely and forward body to upstream.
			s.observe(endpoint, metrics.ResultBypass, "none", http.StatusNotImplemented, start, decision.Reason)
			writeAeroHeaders(w, metrics.ResultBypass, "none", start, 0, 0)
			w.Header().Set("X-Aero-Bypass-Reason", decision.Reason)

			writeOpenAIError(
				w,
				http.StatusNotImplemented,
				"AeroCache bypassed cache, but upstream path is not wired yet",
				"upstream_not_wired",
			)
			return
		}

		// Correct behavior once cache exists:
		// key -> lookup -> verify -> hit OR miss -> singleflight -> upstream.
		s.observe(endpoint, metrics.ResultMiss, "none", http.StatusNotImplemented, start, "")
		writeAeroHeaders(w, metrics.ResultMiss, "none", start, 0, 0)
		w.Header().Set("X-Aero-Gate", "cacheable")
		w.Header().Set("X-Aero-Gate-Reason", decision.Reason)

		writeOpenAIError(
			w,
			http.StatusNotImplemented,
			"AeroCache determinism gate passed, but cache path is not wired yet",
			"cache_path_not_wired",
		)
	}
}

func (s *Server) observe(endpoint string, result metrics.CacheResult, tier string, status int, start time.Time, reason string) {
	s.stats.ObserveRequest(metrics.Observation{
		Endpoint:     endpoint,
		Result:       result,
		Tier:         tier,
		StatusCode:   strconv.Itoa(status),
		BypassReason: reason,
		Latency:      time.Since(start),
	})
}

func (s *Server) spa(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, r.Method, http.MethodGet, http.MethodHead)
		return
	}

	if strings.HasPrefix(r.URL.Path, "/v1/") {
		http.NotFound(w, r)
		return
	}

	rel := r.URL.Path
	if strings.HasPrefix(rel, "/aerobench") {
		rel = strings.TrimPrefix(rel, "/aerobench")
	}

	rel = path.Clean("/" + rel)
	if rel == "/" || rel == "." {
		s.serveIndexOrFallback(w, r)
		return
	}

	fullPath := filepath.Join(s.cfg.SPAPath, filepath.FromSlash(rel))

	info, err := os.Stat(fullPath)
	if err == nil && !info.IsDir() {
		http.ServeFile(w, r, fullPath)
		return
	}

	s.serveIndexOrFallback(w, r)
}

func (s *Server) serveIndexOrFallback(w http.ResponseWriter, r *http.Request) {
	indexPath := filepath.Join(s.cfg.SPAPath, "index.html")

	if _, err := os.Stat(indexPath); err == nil {
		http.ServeFile(w, r, indexPath)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, `<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <title>AeroBench</title>
</head>
<body>
  <main>
    <h1>AeroCache</h1>
    <p>AeroBench static bundle is not built yet.</p>
    <p>Available endpoints: /healthz, /readyz, /stats, /metrics.</p>
  </main>
</body>
</html>`)
}

func writeAeroHeaders(w http.ResponseWriter, result metrics.CacheResult, tier string, start time.Time, tokensOut int, costUSD float64) {
	if tier == "" {
		tier = "none"
	}

	w.Header().Set("X-Aero-Cache", string(result))
	w.Header().Set("X-Aero-Tier", tier)
	w.Header().Set("X-Aero-Latency-Ms", fmt.Sprintf("%.3f", float64(time.Since(start).Microseconds())/1000.0))
	w.Header().Set("X-Aero-Tokens-Out", strconv.Itoa(tokensOut))
	w.Header().Set("X-Aero-Cost-Estimate-USD", fmt.Sprintf("%.8f", costUSD))
}

func writeOpenAIError(w http.ResponseWriter, status int, message string, code string) {
	writeJSONStatus(w, status, map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    "aero_error",
			"param":   nil,
			"code":    code,
		},
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	writeJSONStatus(w, status, v)
}

func writeJSONStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func allowMethod(w http.ResponseWriter, r *http.Request, allowed string) bool {
	if r.Method == allowed {
		return true
	}

	methodNotAllowed(w, r.Method, allowed)
	return false
}

func methodNotAllowed(w http.ResponseWriter, got string, allowed ...string) {
	w.Header().Set("Allow", strings.Join(allowed, ", "))
	writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]any{
		"error": map[string]any{
			"message": fmt.Sprintf("method %s not allowed", got),
			"type":    "invalid_request_error",
			"param":   nil,
			"code":    "method_not_allowed",
		},
	})
}

func withCommonMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Aero-Proxy", "aerocache")
		next.ServeHTTP(w, r)
	})
}
