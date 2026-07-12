package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/swaroop/aero/aerocore/internal/placement"
	"github.com/swaroop/aero/aerocore/internal/registry"
)

type Config struct {
	DefaultUpstreamURL string
}

type Server struct {
	reg      *registry.MemoryRegistry
	resolver *placement.Resolver
	mux      *http.ServeMux
	config   Config
}

type healthPatchRequest struct {
	Healthy bool `json:"healthy"`
}

type readyResponse struct {
	Ready                     bool   `json:"ready"`
	Reason                    string `json:"reason"`
	BackendCount              int    `json:"backend_count"`
	HealthyBackendCount       int    `json:"healthy_backend_count"`
	DefaultUpstreamConfigured bool   `json:"default_upstream_configured"`
}

type configResponse struct {
	DefaultUpstreamConfigured bool `json:"default_upstream_configured"`
	BackendCount              int  `json:"backend_count"`
	HealthyBackendCount       int  `json:"healthy_backend_count"`
	StaleBackendCount         int  `json:"stale_backend_count"`
}

func New(reg *registry.MemoryRegistry) *Server {
	return NewWithConfig(reg, Config{})
}

func NewWithConfig(reg *registry.MemoryRegistry, config Config) *Server {
	s := &Server{
		reg: reg,
		resolver: placement.NewResolver(
			reg,
			placement.WithDefaultUpstreamURL(config.DefaultUpstreamURL),
		),
		mux:    http.NewServeMux(),
		config: config,
	}

	s.mux.HandleFunc("/healthz", s.handleHealthz)
	s.mux.HandleFunc("/readyz", s.handleReadyz)
	s.mux.HandleFunc("/config", s.handleConfig)
	s.mux.HandleFunc("/backends", s.handleBackends)
	s.mux.HandleFunc("/backends/", s.handleBackendByID)
	s.mux.HandleFunc("/resolve", s.handleResolve)

	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
	})
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}

	resp := s.buildReadyResponse()
	if !resp.Ready {
		writeJSON(w, http.StatusServiceUnavailable, resp)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}

	writeJSON(w, http.StatusOK, s.buildConfigResponse())
}

func (s *Server) handleBackends(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}

	writeJSON(w, http.StatusOK, s.reg.ListBackends())
}

func (s *Server) handleBackendByID(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/backends/")

	if strings.HasSuffix(path, "/health") {
		s.handleBackendHealth(w, r)
		return
	}

	switch r.Method {
	case http.MethodPut:
		s.handlePutBackend(w, r)
	case http.MethodDelete:
		s.handleDeleteBackend(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
	}
}

func (s *Server) handlePutBackend(w http.ResponseWriter, r *http.Request) {
	id, err := backendIDFromPath(r.URL.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var b placement.Backend
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}

	if b.ID != "" && b.ID != id {
		writeError(w, http.StatusBadRequest, "backend_id_path_body_mismatch")
		return
	}

	b.ID = id
	s.reg.UpsertBackend(b)

	writeJSON(w, http.StatusOK, b)
}

func (s *Server) handleDeleteBackend(w http.ResponseWriter, r *http.Request) {
	id, err := backendIDFromPath(r.URL.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if !s.reg.DeleteBackend(id) {
		writeError(w, http.StatusNotFound, "backend_not_found")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleBackendHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}

	id, err := backendIDFromHealthPath(r.URL.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var req healthPatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}

	b, ok := s.reg.SetHealth(id, req.Healthy)
	if !ok {
		writeError(w, http.StatusNotFound, "backend_not_found")
		return
	}

	writeJSON(w, http.StatusOK, b)
}

func (s *Server) handleResolve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}

	var req placement.PlacementRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}

	resp := s.resolver.Resolve(req)
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) buildReadyResponse() readyResponse {
	backends := s.reg.ListBackends()
	healthy := countHealthy(backends)
	defaultUpstreamConfigured := s.config.DefaultUpstreamURL != ""

	resp := readyResponse{
		Ready:                     healthy > 0 || defaultUpstreamConfigured,
		BackendCount:              len(backends),
		HealthyBackendCount:       healthy,
		DefaultUpstreamConfigured: defaultUpstreamConfigured,
	}

	switch {
	case healthy > 0:
		resp.Reason = "healthy_backend_available"
	case defaultUpstreamConfigured:
		resp.Reason = "default_upstream_available"
	default:
		resp.Reason = "no_healthy_backend_or_default_upstream"
	}

	return resp
}

func (s *Server) buildConfigResponse() configResponse {
	backends := s.reg.ListBackends()

	return configResponse{
		DefaultUpstreamConfigured: s.config.DefaultUpstreamURL != "",
		BackendCount:              len(backends),
		HealthyBackendCount:       countHealthy(backends),
		StaleBackendCount:         countStale(backends),
	}
}

func countHealthy(backends []placement.Backend) int {
	count := 0
	for _, b := range backends {
		if b.Healthy {
			count++
		}
	}
	return count
}

func countStale(backends []placement.Backend) int {
	count := 0
	for _, b := range backends {
		if !b.Healthy {
			count++
		}
	}
	return count
}

func backendIDFromPath(path string) (string, error) {
	id := strings.TrimPrefix(path, "/backends/")
	id = strings.TrimSpace(id)

	if id == "" || strings.Contains(id, "/") {
		return "", errors.New("invalid_backend_id")
	}

	return id, nil
}

func backendIDFromHealthPath(path string) (string, error) {
	id := strings.TrimPrefix(path, "/backends/")
	id = strings.TrimSuffix(id, "/health")
	id = strings.TrimSpace(id)

	if id == "" || strings.Contains(id, "/") {
		return "", errors.New("invalid_backend_id")
	}

	return id, nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{
		"error": code,
	})
}
