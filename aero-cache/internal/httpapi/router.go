// HTTP handlers: OpenAI-compatible endpoints, stats, metrics, and UI assets.
package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"aero-cache/internal/coalesce"
	"aero-cache/internal/gate"
	"aero-cache/internal/key"
	"aero-cache/internal/lookup"
	"aero-cache/internal/metrics"
	"aero-cache/internal/store"
	"aero-cache/internal/store/l1ristretto"
	"aero-cache/internal/store/l2valkey"
	"aero-cache/internal/store/l3r2"
	"aero-cache/internal/upstream"
	"aero-cache/internal/writeback"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const maxRequestBodyBytes = 32 << 20 // 32 MiB

type Config struct {
	SPAPath            string
	Debug              bool
	GateMode           gate.Mode
	TokenizerAvailable bool
	Fingerprint        key.Fingerprint
	Epoch              uint64
	UpstreamURL        string
}

type Server struct {
	cfg        Config
	stats      *metrics.Registry
	gate       *gate.Decider
	keyBuilder *key.Builder
	lookup     *lookup.Orchestrator
	upstream   *upstream.Client
	coalescer  *coalesce.Group
	writeback  *writeback.Queue
}

func NewRouter(cfg Config, stats *metrics.Registry) http.Handler {
	if cfg.SPAPath == "" {
		cfg.SPAPath = "web/dist"
	}

	if cfg.GateMode == "" {
		cfg.GateMode = gate.ModeStrict
	}

	kb, err := key.NewBuilder(key.BuilderConfig{
		Fingerprint: cfg.Fingerprint,
		Epoch:       cfg.Epoch,
		Tokenizer:   key.ByteTokenizer{},
	})
	if err != nil {
		panic(err)
	}

	tiers, writeStores := buildStores()

	upstreamURL := cfg.UpstreamURL
	if upstreamURL == "" {
		upstreamURL = getenvLocal("AERO_UPSTREAM_URL", "http://localhost:11434")
	}

	wb := writeback.NewQueue(writeback.Config{
		Workers: 4,
		Size:    1024,
		TTL:     24 * time.Hour,
	}, writeStores, stats)

	s := &Server{
		cfg:   cfg,
		stats: stats,
		gate: gate.NewDecider(gate.Config{
			Mode:               cfg.GateMode,
			TokenizerAvailable: cfg.TokenizerAvailable,
		}),
		keyBuilder: kb,
		lookup:     lookup.New(tiers, stats),
		upstream: upstream.NewClient(upstream.Config{
			BaseURL: upstreamURL,
		}),
		coalescer: coalesce.New(),
		writeback: wb,
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

func buildStores() ([]lookup.Tier, []store.Store) {
	l1, err := l1ristretto.New(l1ristretto.Config{
		MaxBytes:      512 << 20,
		MaxEntryBytes: 256 << 10,
		TTL:           time.Hour,
	})
	if err != nil {
		panic(err)
	}

	tiers := []lookup.Tier{
		{
			Store:  l1,
			Budget: time.Millisecond,
		},
	}

	writeStores := []store.Store{l1}

	if addr := getenvLocal("AERO_L2_ADDR", ""); addr != "" {
		l2, err := l2valkey.New(l2valkey.Config{
			Addr:        addr,
			GetBudget:   5 * time.Millisecond,
			TTL:         24 * time.Hour,
			Compression: true,
		})
		if err != nil {
			log.Printf("aerocache: L2 Valkey disabled; continuing with L1 only: %v", err)
		} else {
			tiers = append(tiers, lookup.Tier{
				Store:  l2,
				Budget: 5 * time.Millisecond,
			})

			writeStores = append(writeStores, l2)
		}
	}

	l3 := l3r2.NewDisabled()

	tiers = append(tiers, lookup.Tier{
		Store:  l3,
		Budget: 50 * time.Millisecond,
	})

	return tiers, writeStores
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
		requestID := newRequestID()

		if !allowMethod(w, r, http.MethodPost) {
			return
		}

		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBodyBytes))
		if err != nil {
			s.observe(endpoint, metrics.ResultError, "none", http.StatusRequestEntityTooLarge, start, "")
			writeAeroHeaders(w, metrics.ResultError, "none", start, 0, 0)
			writeProofHeaders(w, requestID, false)
			writeOpenAIError(w, http.StatusRequestEntityTooLarge, "request body too large", "request_too_large")
			return
		}

		if len(body) == 0 || !json.Valid(body) {
			s.observe(endpoint, metrics.ResultError, "none", http.StatusBadRequest, start, "")
			writeAeroHeaders(w, metrics.ResultError, "none", start, 0, 0)
			writeProofHeaders(w, requestID, false)
			writeOpenAIError(w, http.StatusBadRequest, "invalid JSON request body", "invalid_json")
			return
		}

		wantsStream := requestWantsStream(body)
		decision := s.gate.Evaluate(body)

		if !decision.Cacheable {
			s.proxyBypass(w, r, endpoint, body, start, requestID, wantsStream, decision.Reason)
			return
		}

		material, err := s.keyBuilder.Build(body)
		if err != nil {
			s.proxyBypass(w, r, endpoint, body, start, requestID, wantsStream, "key_build_failed")
			return
		}

		hit := s.lookup.Lookup(r.Context(), material)
		if hit.Hit {
			s.serveCacheHit(w, endpoint, material, hit, start, requestID, wantsStream)
			return
		}

		s.handleMiss(w, r, endpoint, body, material, decision.Reason, start, requestID, wantsStream)
	}
}

func (s *Server) proxyBypass(
	w http.ResponseWriter,
	r *http.Request,
	endpoint string,
	body []byte,
	start time.Time,
	requestID string,
	wantsStream bool,
	reason string,
) {
	tw := &trackedResponseWriter{ResponseWriter: w}

	initialCost := estimateCostUSD(metrics.ResultBypass, "dev", 0)

	writeAeroHeaders(tw, metrics.ResultBypass, "dev", start, 0, initialCost)
	writeProofHeaders(tw, requestID, false)
	tw.Header().Set("X-Aero-Bypass-Reason", reason)

	s.stats.IncUpstreamCall()

	res, err := s.upstream.Do(r.Context(), endpoint, body, tw, nil)
	if err != nil {
		s.observe(endpoint, metrics.ResultError, "dev", http.StatusBadGateway, start, reason)

		if !tw.WroteHeader() {
			writeAeroHeaders(tw, metrics.ResultError, "dev", start, 0, 0)
			writeProofHeaders(tw, requestID, false)
			writeOpenAIError(tw, http.StatusBadGateway, "upstream request failed", "upstream_failed")
		}

		return
	}

	costUSD := estimateCostUSD(metrics.ResultBypass, "dev", res.TokensOut)

	if shouldWriteReceiptSSE(wantsStream, res.ContentType) {
		writeReceiptSSE(tw, buildReceipt(
			requestID,
			nil,
			"dev",
			metrics.ResultBypass,
			false,
			start,
			res.Body,
			res.TokensOut,
			costUSD,
			res.TTFT,
		))
	}

	s.observe(endpoint, metrics.ResultBypass, "dev", res.StatusCode, start, reason)
}

func (s *Server) serveCacheHit(
	w http.ResponseWriter,
	endpoint string,
	material *key.Material,
	hit lookup.Result,
	start time.Time,
	requestID string,
	wantsStream bool,
) {
	status := hit.Entry.StatusCode
	if status == 0 {
		status = http.StatusOK
	}

	contentType := hit.Entry.ContentType
	if contentType == "" {
		contentType = "application/json"
	}

	costUSD := estimateCostUSD(metrics.ResultHit, hit.Tier, hit.Entry.TokensOut)

	s.observe(endpoint, metrics.ResultHit, hit.Tier, status, start, "")
	writeAeroHeaders(w, metrics.ResultHit, hit.Tier, start, hit.Entry.TokensOut, costUSD)
	writeProofHeaders(w, requestID, true)

	if s.cfg.Debug {
		w.Header().Set("X-Aero-Key", material.KeyHex)
		w.Header().Set("X-Aero-Store-Key", material.StoreKey)
	}

	if hit.Entry.OriginTier != "" {
		w.Header().Set("X-Aero-Origin-Tier", hit.Entry.OriginTier)
	}

	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	_, _ = w.Write(hit.Entry.Response)

	if shouldWriteReceiptSSE(wantsStream, contentType) {
		writeReceiptSSE(w, buildReceipt(
			requestID,
			material,
			hit.Tier,
			metrics.ResultHit,
			true,
			start,
			hit.Entry.Response,
			hit.Entry.TokensOut,
			costUSD,
			0,
		))
	}

	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *Server) handleMiss(
	w http.ResponseWriter,
	r *http.Request,
	endpoint string,
	body []byte,
	material *key.Material,
	gateReason string,
	start time.Time,
	requestID string,
	wantsStream bool,
) {
	leaderRan := false
	leaderWrote := false

	res, _, err := s.coalescer.Do(material.StoreKey, func() (*coalesce.Result, error) {
		leaderRan = true

		tw := &trackedResponseWriter{ResponseWriter: w}

		initialCost := estimateCostUSD(metrics.ResultMiss, "dev", 0)

		writeAeroHeaders(tw, metrics.ResultMiss, "dev", start, 0, initialCost)
		writeProofHeaders(tw, requestID, false)
		tw.Header().Set("X-Aero-Gate", "cacheable")
		tw.Header().Set("X-Aero-Gate-Reason", gateReason)

		if s.cfg.Debug {
			tw.Header().Set("X-Aero-Key", material.KeyHex)
			tw.Header().Set("X-Aero-Store-Key", material.StoreKey)
		}

		s.stats.IncUpstreamCall()

		upRes, err := s.upstream.Do(r.Context(), endpoint, body, tw, func() {
			leaderWrote = true
		})
		if err != nil {
			if tw.WroteHeader() {
				leaderWrote = true
			}
			return nil, err
		}

		costUSD := estimateCostUSD(metrics.ResultMiss, "dev", upRes.TokensOut)

		if upRes.StatusCode >= 200 && upRes.StatusCode < 300 {
			s.writeback.Enqueue(writeback.Job{
				Material:    material,
				Response:    upRes.Body,
				StatusCode:  upRes.StatusCode,
				ContentType: upRes.ContentType,
				TokensOut:   upRes.TokensOut,
				OriginTier:  upRes.OriginTier,
				TTL:         24 * time.Hour,
			})
		}

		if shouldWriteReceiptSSE(wantsStream, upRes.ContentType) {
			writeReceiptSSE(tw, buildReceipt(
				requestID,
				material,
				"dev",
				metrics.ResultMiss,
				false,
				start,
				upRes.Body,
				upRes.TokensOut,
				costUSD,
				upRes.TTFT,
			))
		}

		return &coalesce.Result{
			StatusCode:  upRes.StatusCode,
			Header:      map[string][]string(upRes.Header),
			Body:        upRes.Body,
			ContentType: upRes.ContentType,
			TokensOut:   upRes.TokensOut,
			OriginTier:  upRes.OriginTier,
			TTFT:        upRes.TTFT,
			CostUSD:     costUSD,
		}, nil
	})

	if err != nil {
		s.observe(endpoint, metrics.ResultError, "dev", http.StatusBadGateway, start, "")

		if !leaderWrote {
			writeAeroHeaders(w, metrics.ResultError, "dev", start, 0, 0)
			writeProofHeaders(w, requestID, false)
			writeOpenAIError(w, http.StatusBadGateway, "upstream request failed", "upstream_failed")
		}

		return
	}

	if leaderRan {
		status := http.StatusOK
		if res != nil && res.StatusCode != 0 {
			status = res.StatusCode
		}

		s.observe(endpoint, metrics.ResultMiss, "dev", status, start, "")
		return
	}

	s.serveCoalesced(w, endpoint, material, res, start, requestID, wantsStream)
}

func (s *Server) serveCoalesced(
	w http.ResponseWriter,
	endpoint string,
	material *key.Material,
	res *coalesce.Result,
	start time.Time,
	requestID string,
	wantsStream bool,
) {
	if res == nil {
		s.observe(endpoint, metrics.ResultError, "dev", http.StatusBadGateway, start, "")
		writeAeroHeaders(w, metrics.ResultError, "dev", start, 0, 0)
		writeProofHeaders(w, requestID, false)
		writeOpenAIError(w, http.StatusBadGateway, "coalesced response missing", "coalesced_response_missing")
		return
	}

	status := res.StatusCode
	if status == 0 {
		status = http.StatusOK
	}

	contentType := res.ContentType
	if contentType == "" {
		contentType = "application/json"
	}

	costUSD := estimateCostUSD(metrics.ResultCoalesced, "dev", res.TokensOut)

	s.observe(endpoint, metrics.ResultCoalesced, "dev", status, start, "")
	writeAeroHeaders(w, metrics.ResultCoalesced, "dev", start, res.TokensOut, costUSD)
	writeProofHeaders(w, requestID, false)

	if s.cfg.Debug {
		w.Header().Set("X-Aero-Key", material.KeyHex)
		w.Header().Set("X-Aero-Store-Key", material.StoreKey)
	}

	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	_, _ = w.Write(res.Body)

	if shouldWriteReceiptSSE(wantsStream, contentType) {
		writeReceiptSSE(w, buildReceipt(
			requestID,
			material,
			"dev",
			metrics.ResultCoalesced,
			false,
			start,
			res.Body,
			res.TokensOut,
			costUSD,
			res.TTFT,
		))
	}

	if f, ok := w.(http.Flusher); ok {
		f.Flush()
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
	w.Header().Set("X-Aero-Cost-Usd", fmt.Sprintf("%.8f", costUSD))
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

func getenvLocal(key string, fallback string) string {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}

	return v
}

type trackedResponseWriter struct {
	http.ResponseWriter
	wroteHeader bool
	statusCode  int
}

func (w *trackedResponseWriter) WriteHeader(statusCode int) {
	if w.wroteHeader {
		return
	}

	w.wroteHeader = true
	w.statusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *trackedResponseWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}

	return w.ResponseWriter.Write(b)
}

func (w *trackedResponseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *trackedResponseWriter) WroteHeader() bool {
	return w.wroteHeader
}

func (w *trackedResponseWriter) StatusCode() int {
	if w.statusCode == 0 {
		return http.StatusOK
	}

	return w.statusCode
}
