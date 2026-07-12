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
