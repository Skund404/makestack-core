// Package api provides the REST API server for makestack-core.
// It exposes primitives, relationships, full-text search, and write operations.
package api

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/makestack/makestack-core/internal/auth"
	gitpkg "github.com/makestack/makestack-core/internal/git"
	"github.com/makestack/makestack-core/internal/index"
	"github.com/makestack/makestack-core/internal/schema"
)

// validPrimitiveTypes is the closed set of types the data model recognises.
var validPrimitiveTypes = map[string]bool{
	"tool": true, "material": true, "technique": true,
	"workflow": true, "project": true, "event": true,
}

// slugRe matches any sequence of characters that are not lowercase letters,
// digits, or hyphens, used to sanitise a name into a URL-safe slug.
var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// Server is the makestack-core REST API server.
type Server struct {
	mux         *http.ServeMux
	idx         *index.Index
	writer      *gitpkg.Writer // nil when the data dir is not a git repo
	apiKey      string         // empty = auth disabled
	publicReads bool           // when true, GET endpoints skip auth
}

// NewServer creates a Server wired to the given index, optional writer, and
// auth config, then registers all routes.
//
//   - writer may be nil — write endpoints return 503 when it is.
//   - apiKey empty string disables authentication entirely.
//   - publicReads true allows GET endpoints without a key even when apiKey is set.
func NewServer(idx *index.Index, writer *gitpkg.Writer, apiKey string, publicReads bool) *Server {
	s := &Server{
		mux:         http.NewServeMux(),
		idx:         idx,
		writer:      writer,
		apiKey:      apiKey,
		publicReads: publicReads,
	}
	s.registerRoutes()
	return s
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) registerRoutes() {
	// /health is always public — used by load balancers and health checks.
	s.mux.HandleFunc("GET /health", s.handleHealth)

	// readAuth wraps GET handlers with auth. When --public-reads is set it
	// becomes a no-op so reads are accessible without a key.
	readAuth := s.withAuth
	if s.publicReads {
		readAuth = func(h http.HandlerFunc) http.HandlerFunc { return h }
	}

	// Read — primitives.
	s.mux.HandleFunc("GET /api/primitives", readAuth(s.handleListPrimitives))
	s.mux.HandleFunc("GET /api/primitives/{path...}", readAuth(s.handleGetPrimitive))

	// Write — primitives (always protected regardless of --public-reads).
	s.mux.HandleFunc("POST /api/primitives", s.withAuth(s.handleCreatePrimitive))
	s.mux.HandleFunc("PUT /api/primitives/{path...}", s.withAuth(s.handleUpdatePrimitive))
	s.mux.HandleFunc("DELETE /api/primitives/{path...}", s.withAuth(s.handleDeletePrimitive))

	// Search and relationships.
	s.mux.HandleFunc("GET /api/search", readAuth(s.handleSearch))
	s.mux.HandleFunc("GET /api/relationships/{path...}", readAuth(s.handleRelationships))
}

// withAuth wraps a handler so it requires a valid API key before proceeding.
// If the Server has no key configured, all requests pass through.
func (s *Server) withAuth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := auth.ValidateRequest(r, s.apiKey); err != nil {
			writeError(w, http.StatusUnauthorized, err)
			return
		}
		h(w, r)
	}
}

// — read handlers —————————————————————————————————————————————————————————————

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleListPrimitives handles GET /api/primitives[?type=<type>][&workshop=<slug>].
func (s *Server) handleListPrimitives(w http.ResponseWriter, r *http.Request) {
	typeFilter   := r.URL.Query().Get("type")
	workshopSlug := r.URL.Query().Get("workshop")

	primitives, err := s.idx.List(r.Context(), typeFilter, workshopSlug)
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

// handleGetPrimitive handles GET /api/primitives/{path...}.
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

// handleSearch handles GET /api/search?q=<query>.
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

// handleRelationships handles GET /api/relationships/{path...}.
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

// — write handlers ————————————————————————————————————————————————————————————

// handleCreatePrimitive handles POST /api/primitives.
//
// Accepts a JSON body with the manifest content. Required: type, name.
// Optional auto-generation: id (UUID v4 if absent), slug (derived from name
// if absent), created and modified (set to current UTC time).
//
// The file is written to {type}s/{slug}/manifest.json relative to the data
// directory and immediately committed to Git. The watcher will pick up the
// change and update the index asynchronously.
func (s *Server) handleCreatePrimitive(w http.ResponseWriter, r *http.Request) {
	if !s.writerReady(w) {
		return
	}

	var body map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body: %w", err))
		return
	}

	// — validate required fields ——————————————————————————————————————————
	primType := jsonString(body["type"])
	if primType == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("missing required field: type"))
		return
	}
	if !validPrimitiveTypes[primType] {
		writeError(w, http.StatusBadRequest, fmt.Errorf(
			"invalid type %q: must be one of tool, material, technique, workflow, project, event", primType))
		return
	}

	name := jsonString(body["name"])
	if name == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("missing required field: name"))
		return
	}

	// — auto-generate missing optional fields —————————————————————————————
	if jsonString(body["id"]) == "" {
		body["id"] = jsonRaw(generateID())
	}
	if jsonString(body["slug"]) == "" {
		body["slug"] = jsonRaw(slugify(name))
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if _, ok := body["created"]; !ok {
		body["created"] = jsonRaw(now)
	}
	body["modified"] = jsonRaw(now)

	slug := jsonString(body["slug"])

	// — validate structure ————————————————————————————————————————————————
	if errs := schema.Validate(primType, body); len(errs) > 0 {
		writeError(w, http.StatusBadRequest,
			fmt.Errorf("validation failed: %s", strings.Join(errs, "; ")))
		return
	}

	// — write to disk and commit ——————————————————————————————————————————
	data, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("marshal manifest: %w", err))
		return
	}

	// Canonical path: {type}s/{slug}/manifest.json
	relPath := primType + "s/" + slug + "/manifest.json"
	commitMsg := fmt.Sprintf("create %s: %s", primType, name)

	if err := s.writer.WriteManifest(relPath, data, commitMsg); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("write manifest: %w", err))
		return
	}

	// Parse and return the new manifest directly — the index will be updated
	// asynchronously by the file watcher.
	m := gitpkg.Manifest{Path: relPath, Raw: json.RawMessage(data)}
	pm, err := m.Parse()
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("parse created manifest: %w", err))
		return
	}
	writeJSON(w, http.StatusCreated, parsedToAPI(pm))
}

// handleUpdatePrimitive handles PUT /api/primitives/{path...}.
//
// Overwrites the manifest at path with the request body. All four required
// fields (id, type, name, slug) must be present in the body. modified is
// always updated to the current time. The change is committed to Git.
func (s *Server) handleUpdatePrimitive(w http.ResponseWriter, r *http.Request) {
	if !s.writerReady(w) {
		return
	}

	relPath := r.PathValue("path")
	if relPath == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("path is required"))
		return
	}

	// Confirm the primitive exists before overwriting.
	existing, err := s.idx.Get(r.Context(), relPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if existing == nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("not found: %s", relPath))
		return
	}

	var body map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body: %w", err))
		return
	}

	// Validate all required fields are present in the supplied body.
	for _, field := range []string{"id", "type", "name", "slug"} {
		if jsonString(body[field]) == "" {
			writeError(w, http.StatusBadRequest, fmt.Errorf("missing required field: %s", field))
			return
		}
	}

	primType := jsonString(body["type"])
	if !validPrimitiveTypes[primType] {
		writeError(w, http.StatusBadRequest, fmt.Errorf(
			"invalid type %q: must be one of tool, material, technique, workflow, project, event", primType))
		return
	}

	// Always stamp modified with the current time.
	body["modified"] = jsonRaw(time.Now().UTC().Format(time.RFC3339))

	// — validate structure ————————————————————————————————————————————————
	if errs := schema.Validate(primType, body); len(errs) > 0 {
		writeError(w, http.StatusBadRequest,
			fmt.Errorf("validation failed: %s", strings.Join(errs, "; ")))
		return
	}

	data, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("marshal manifest: %w", err))
		return
	}

	name := jsonString(body["name"])
	commitMsg := fmt.Sprintf("update %s: %s", primType, name)

	if err := s.writer.WriteManifest(relPath, data, commitMsg); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("write manifest: %w", err))
		return
	}

	m := gitpkg.Manifest{Path: relPath, Raw: json.RawMessage(data)}
	pm, err := m.Parse()
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("parse updated manifest: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, parsedToAPI(pm))
}

// handleDeletePrimitive handles DELETE /api/primitives/{path...}.
//
// Removes the manifest file and its parent directory from disk and commits
// the deletion. Returns 204 No Content on success.
func (s *Server) handleDeletePrimitive(w http.ResponseWriter, r *http.Request) {
	if !s.writerReady(w) {
		return
	}

	relPath := r.PathValue("path")
	if relPath == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("path is required"))
		return
	}

	existing, err := s.idx.Get(r.Context(), relPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if existing == nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("not found: %s", relPath))
		return
	}

	commitMsg := fmt.Sprintf("delete %s: %s", existing.Type, existing.Name)

	if err := s.writer.DeleteManifest(relPath, commitMsg); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("delete manifest: %w", err))
		return
	}

	w.WriteHeader(http.StatusNoContent)
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

// toAPIPrimitive converts an index.Primitive (from the SQLite index) to the
// API response shape.
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

// parsedToAPI converts a git.ParsedManifest directly to the API response
// shape. Used by write handlers to return data before the watcher has had
// time to update the index.
func parsedToAPI(pm *gitpkg.ParsedManifest) apiPrimitive {
	tags := json.RawMessage("[]")
	if len(pm.Tags) > 0 {
		if b, err := json.Marshal(pm.Tags); err == nil {
			tags = b
		}
	}
	return apiPrimitive{
		ID:            pm.ID,
		Type:          pm.Type,
		Name:          pm.Name,
		Slug:          pm.Slug,
		Path:          pm.Path,
		Created:       pm.Created,
		Modified:      pm.Modified,
		Description:   pm.Description,
		Tags:          tags,
		Properties:    pm.Properties,
		ParentProject: pm.ParentProject,
		Manifest:      pm.Raw,
	}
}

// — helpers ——————————————————————————————————————————————————————————————————

// writerReady returns true if the writer is available, otherwise writes a
// 503 and returns false. Write handlers call this at the top.
func (s *Server) writerReady(w http.ResponseWriter) bool {
	if s.writer == nil {
		writeError(w, http.StatusServiceUnavailable,
			fmt.Errorf("write operations unavailable: data directory is not a git repository"))
		return false
	}
	return true
}

// jsonString safely extracts a string value from a raw JSON token.
// Returns "" for nil, non-string, or malformed input.
func jsonString(raw json.RawMessage) string {
	if raw == nil {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

// jsonRaw wraps a plain string value as a quoted JSON string RawMessage.
func jsonRaw(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return json.RawMessage(b)
}

// slugify converts a human-readable name into a URL-safe slug.
// "Stitching Chisel (4-prong)" → "stitching-chisel-4-prong"
func slugify(name string) string {
	s := strings.ToLower(name)
	s = slugRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}

// generateID returns a random UUID v4 string using crypto/rand.
func generateID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failure is catastrophic and unrecoverable.
		panic(fmt.Sprintf("crypto/rand: %v", err))
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant bits
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

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
