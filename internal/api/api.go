// Package api provides the REST API server for makestack-core.
// It exposes primitives, relationships, and search over HTTP.
package api

import (
	"net/http"
)

// Server is the makestack-core REST API server.
type Server struct {
	mux *http.ServeMux
}

// NewServer creates a new API server with all routes registered.
func NewServer() *Server {
	s := &Server{mux: http.NewServeMux()}
	s.registerRoutes()
	return s
}

// ServeHTTP implements http.Handler so Server can be used directly
// with http.ListenAndServe.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) registerRoutes() {
	// TODO: register handlers as they are implemented
	s.mux.HandleFunc("GET /health", s.handleHealth)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`)) //nolint:errcheck
}
