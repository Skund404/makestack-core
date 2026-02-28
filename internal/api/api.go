// Package api provides the REST API server for makestack-core.
// It exposes primitives, relationships, and full-text search over HTTP.
package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/makestack/makestack-core/internal/index"
)

// Server is the makestack-core REST API server.
type Server struct {
	mux *http.ServeMux
	idx *index.Index
}

// NewServer creates a Server wired to the given index and registers all routes.
func NewServer(idx *index.Index) *Server {
	s := &Server{
		mux: http.NewServeMux(),
		idx: idx,
	}
	s.registerRoutes()
	return s
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("GET /health", s.handleHealth)

	// Primitives — list and single-item lookup.
	// The catch-all {path...} must be a separate pattern from the bare list endpoint.
	s.mux.HandleFunc("GET /api/primitives", s.handleListPrimitives)
	s.mux.HandleFunc("GET /api/primitives/{path...}", s.handleGetPrimitive)

	// Full-text search.
	s.mux.HandleFunc("GET /api/search", s.handleSearch)

	// Relationships for a given primitive path.
	s.mux.HandleFunc("GET /api/relationships/{path...}", s.handleRelationships)
}

// — handlers —————————————————————————————————————————————————————————————————

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleListPrimitives handles GET /api/primitives[?type=<type>]
// Returns all indexed primitives, optionally filtered by primitive type.
func (s *Server) handleListPrimitives(w http.ResponseWriter, r *http.Request) {
	typeFilter := r.URL.Query().Get("type")

	primitives, err := s.idx.List(r.Context(), typeFilter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	// Always return an array, never null.
	result := make([]apiPrimitive, len(primitives))
	for i, p := range primitives {
		result[i] = toAPIPrimitive(p)
	}
	writeJSON(w, http.StatusOK, result)
}

// handleGetPrimitive handles GET /api/primitives/{path...}
// The path is the relative path of the manifest within the data repository
// (e.g. "tools/stitching-chisel/manifest.json").
func (s *Server) handleGetPrimitive(w http.ResponseWriter, r *http.Request) {
	path := r.PathValue("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("path is required"))
		return
	}

	p, err := s.idx.Get(r.Context(), path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if p == nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("not found: %s", path))
		return
	}
	writeJSON(w, http.StatusOK, toAPIPrimitive(*p))
}

// handleSearch handles GET /api/search?q=<query>
// Runs a full-text search across name, description, tags, and properties.
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("missing required query parameter: q"))
		return
	}

	primitives, err := s.idx.Search(r.Context(), q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	result := make([]apiPrimitive, len(primitives))
	for i, p := range primitives {
		result[i] = toAPIPrimitive(p)
	}
	writeJSON(w, http.StatusOK, result)
}

// handleRelationships handles GET /api/relationships/{path...}
// Returns all relationships where the primitive at path appears as source or target.
func (s *Server) handleRelationships(w http.ResponseWriter, r *http.Request) {
	path := r.PathValue("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("path is required"))
		return
	}

	rels, err := s.idx.RelationshipsFor(r.Context(), path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	result := make([]apiRelationship, len(rels))
	for i, rel := range rels {
		result[i] = apiRelationship{
			SourcePath: rel.SourcePath,
			SourceType: rel.SourceType,
			RelType:    rel.RelType,
			TargetPath: rel.TargetPath,
			TargetType: rel.TargetType,
			Metadata:   rel.Metadata,
		}
	}
	writeJSON(w, http.StatusOK, result)
}

// — response types ————————————————————————————————————————————————————————————

// apiPrimitive is the JSON shape returned for a single primitive.
type apiPrimitive struct {
	ID            string          `json:"id"`
	Type          string          `json:"type"`
	Name          string          `json:"name"`
	Slug          string          `json:"slug"`
	Path          string          `json:"path"`
	Created       string          `json:"created,omitempty"`
	Modified      string          `json:"modified,omitempty"`
	Description   string          `json:"description,omitempty"`
	Tags          json.RawMessage `json:"tags"`
	Properties    json.RawMessage `json:"properties,omitempty"`
	ParentProject string          `json:"parent_project,omitempty"`
	Manifest      json.RawMessage `json:"manifest"`
}

// apiRelationship is the JSON shape returned for a single relationship row.
type apiRelationship struct {
	SourcePath string          `json:"source_path"`
	SourceType string          `json:"source_type"`
	RelType    string          `json:"relationship_type"`
	TargetPath string          `json:"target_path"`
	TargetType string          `json:"target_type,omitempty"`
	Metadata   json.RawMessage `json:"metadata,omitempty"`
}

// toAPIPrimitive converts an index.Primitive to the API response shape.
func toAPIPrimitive(p index.Primitive) apiPrimitive {
	return apiPrimitive{
		ID:            p.ID,
		Type:          p.Type,
		Name:          p.Name,
		Slug:          p.Slug,
		Path:          p.Path,
		Created:       p.Created,
		Modified:      p.Modified,
		Description:   p.Description,
		Tags:          p.Tags,
		Properties:    p.Properties,
		ParentProject: p.ParentProject,
		Manifest:      p.Manifest,
	}
}

// — helpers ——————————————————————————————————————————————————————————————————

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("api: encode response: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
