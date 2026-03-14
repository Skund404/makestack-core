// Package api provides the REST API server for makestack-core.
// It exposes primitives, relationships, full-text search, and write operations.
package api

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/makestack/makestack-core/internal/auth"
	"github.com/makestack/makestack-core/internal/federation"
	gitpkg "github.com/makestack/makestack-core/internal/git"
	"github.com/makestack/makestack-core/internal/index"
	"github.com/makestack/makestack-core/internal/parser"
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

// BuildPrimitivePath constructs the canonical path for a primitive.
// origin is "" for v0.1 (single-repo). In v0.2 federation it becomes the
// repo's declared origin name, prefixing the path so that the same bare slug
// can coexist across multiple roots without collision.
func BuildPrimitivePath(origin, primType, slug string) string {
	if origin == "" {
		return primType + "s/" + slug + "/manifest.json"
	}
	return origin + "/" + primType + "s/" + slug + "/manifest.json"
}

// Server is the makestack-core REST API server.
type Server struct {
	mux         *http.ServeMux
	idx         *index.Index
	writer      *gitpkg.Writer // nil when the data dir is not a git repo
	apiKey      string         // empty = auth disabled
	publicReads bool           // when true, GET endpoints skip auth

	// Federation — both nil in single-root mode.
	fedConfig  *federation.Config          // loaded from .makestack/federation.json
	parserCfgs map[string]*parser.Config   // root_slug → parser config
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

// WithFederation attaches a federation config and per-root parser configs to
// the server, enabling /api/roots, GET /api/parser-config/{slug}, root
// filtering on /api/primitives, and write guards on federated roots.
// Call this after NewServer before the first request is served.
func (s *Server) WithFederation(fedConfig *federation.Config, parserCfgs map[string]*parser.Config) *Server {
	s.fedConfig = fedConfig
	s.parserCfgs = parserCfgs
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

	// Federation — roots and parser configs.
	s.mux.HandleFunc("GET /api/roots", readAuth(s.handleListRoots))
	s.mux.HandleFunc("GET /api/parser-config/{slug}", readAuth(s.handleGetParserConfig))
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

// handleListPrimitives handles GET /api/primitives[?type=<type>][?root=<slug>][?domain=<domain>][?status=<status>].
func (s *Server) handleListPrimitives(w http.ResponseWriter, r *http.Request) {
	typeFilter   := r.URL.Query().Get("type")
	rootFilter   := r.URL.Query().Get("root")
	domainFilter := r.URL.Query().Get("domain")
	statusFilter := r.URL.Query().Get("status")

	primitives, err := s.idx.List(r.Context(), typeFilter, rootFilter, domainFilter, statusFilter)
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
//
// Two optional behaviours are layered on top of the normal index lookup:
//
//   - If the path ends with "/hash", strip the suffix and return the current
//     HEAD commit hash for that primitive (see handleGetPrimitiveHash).
//     All valid primitive paths end with "/manifest.json", so "/hash" is
//     unambiguous as a sub-resource indicator.
//
//   - If the query parameter "at" is present, read the manifest from that
//     specific Git commit instead of the live SQLite index (see
//     handleGetPrimitiveAtCommit).
func (s *Server) handleGetPrimitive(w http.ResponseWriter, r *http.Request) {
	path := r.PathValue("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("path is required"))
		return
	}

	// Sub-resources: /history, /diff, /hash
	// Go 1.22 mux does not support {wildcard}/literal patterns, so we detect
	// suffixes here and delegate.
	if strings.HasSuffix(path, "/history") {
		s.handleGetPrimitiveHistory(w, r, strings.TrimSuffix(path, "/history"))
		return
	}
	if strings.HasSuffix(path, "/diff") {
		s.handleGetPrimitiveDiff(w, r, strings.TrimSuffix(path, "/diff"))
		return
	}
	if strings.HasSuffix(path, "/hash") {
		s.handleGetPrimitiveHash(w, r, strings.TrimSuffix(path, "/hash"))
		return
	}

	// Historical read: GET /api/primitives/{path...}?at={commitHash}
	if at := r.URL.Query().Get("at"); at != "" {
		s.handleGetPrimitiveAtCommit(w, r, path, at)
		return
	}

	// Normal path: current state from SQLite index.
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

// handleGetPrimitiveAtCommit serves GET /api/primitives/{path...}?at={hash}.
// It reads the manifest directly from the Git object store at the given commit,
// bypassing the SQLite index (which only holds current state). The response
// has the same shape as a normal primitive response, with the additional
// commit_hash field set to the requested hash.
func (s *Server) handleGetPrimitiveAtCommit(w http.ResponseWriter, r *http.Request, path, commitHash string) {
	if s.writer == nil {
		writeError(w, http.StatusServiceUnavailable,
			fmt.Errorf("historical reads unavailable: data directory is not a git repository"))
		return
	}

	pm, err := s.writer.ReadManifestAtCommit(path, commitHash)
	if err != nil {
		if errors.Is(err, gitpkg.ErrNotFound) {
			writeError(w, http.StatusNotFound,
				fmt.Errorf("not found: %s at commit %s", path, commitHash))
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	resp := parsedToAPI(pm)
	resp.CommitHash = commitHash
	writeJSON(w, http.StatusOK, resp)
}

// handleGetPrimitiveHash serves GET /api/primitives/{path...}/hash.
// It validates that the primitive exists in the current index, then returns
// the HEAD commit hash. The Shell stores this hash when adding a catalogue
// item to inventory, so it can later retrieve the exact version via ?at=.
func (s *Server) handleGetPrimitiveHash(w http.ResponseWriter, r *http.Request, path string) {
	if s.writer == nil {
		writeError(w, http.StatusServiceUnavailable,
			fmt.Errorf("git operations unavailable: data directory is not a git repository"))
		return
	}

	// Confirm the primitive exists before returning a hash.
	p, err := s.idx.Get(r.Context(), path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if p == nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("not found: %s", path))
		return
	}

	hash, err := s.writer.LastCommitHashForPath(path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("resolve last commit for %s: %w", path, err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"commit_hash": hash})
}

// handleGetPrimitiveHistory serves GET /api/primitives/{path...}/history.
// Returns the list of commits that touched this primitive, newest first.
// Pagination via ?limit= (default 50, max 200) and ?offset= (default 0).
func (s *Server) handleGetPrimitiveHistory(w http.ResponseWriter, r *http.Request, path string) {
	if s.writer == nil {
		writeError(w, http.StatusServiceUnavailable,
			fmt.Errorf("git operations unavailable: data directory is not a git repository"))
		return
	}

	limit := 50
	offset := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := parsePositiveInt(v); err == nil {
			limit = n
		}
	}
	if limit > 200 {
		limit = 200
	}
	if limit < 1 {
		limit = 1
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := parsePositiveInt(v); err == nil {
			offset = n
		}
	}

	commits, total, err := s.writer.CommitHistoryForPath(path, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"path":    path,
		"total":   total,
		"commits": commits,
	})
}

// handleGetPrimitiveDiff serves GET /api/primitives/{path...}/diff.
// Returns a structured field-level diff between two versions of a primitive.
//
//   - ?from= and ?to= are full 40-char commit hashes.
//   - If ?to= is omitted, HEAD is used.
//   - If ?from= is omitted, the parent of ?to= is used; if the commit has no
//     parent (initial commit), a 400 is returned.
func (s *Server) handleGetPrimitiveDiff(w http.ResponseWriter, r *http.Request, path string) {
	if s.writer == nil {
		writeError(w, http.StatusServiceUnavailable,
			fmt.Errorf("git operations unavailable: data directory is not a git repository"))
		return
	}

	fromHash := r.URL.Query().Get("from")
	toHash := r.URL.Query().Get("to")

	// Default ?to= to HEAD.
	if toHash == "" {
		var err error
		toHash, err = s.writer.HeadHash()
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Errorf("resolve HEAD: %w", err))
			return
		}
	}

	// Default ?from= to parent of ?to=.
	if fromHash == "" {
		var err error
		fromHash, err = s.writer.ParentHash(toHash)
		if err != nil {
			if errors.Is(err, gitpkg.ErrNotFound) {
				writeError(w, http.StatusBadRequest,
					fmt.Errorf("commit %s has no parent; specify ?from= explicitly", toHash))
				return
			}
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	}

	fromPM, err := s.writer.ReadManifestAtCommit(path, fromHash)
	if err != nil {
		if errors.Is(err, gitpkg.ErrNotFound) {
			writeError(w, http.StatusNotFound,
				fmt.Errorf("not found: %s at commit %s", path, fromHash))
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	toPM, err := s.writer.ReadManifestAtCommit(path, toHash)
	if err != nil {
		if errors.Is(err, gitpkg.ErrNotFound) {
			writeError(w, http.StatusNotFound,
				fmt.Errorf("not found: %s at commit %s", path, toHash))
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	fromTS, err := s.writer.CommitTimestamp(fromHash)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	toTS, err := s.writer.CommitTimestamp(toHash)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	changes := gitpkg.DiffManifests(fromPM.Raw, toPM.Raw)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"path":           path,
		"from_hash":      fromHash,
		"to_hash":        toHash,
		"from_timestamp": fromTS,
		"to_timestamp":   toTS,
		"changes":        changes,
	})
}

// parsePositiveInt parses s as a non-negative integer.
func parsePositiveInt(s string) (int, error) {
	var n int
	_, err := fmt.Sscanf(s, "%d", &n)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("not a non-negative integer: %q", s)
	}
	return n, nil
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
// Slug collision handling:
//   - If the caller did not supply "slug", the slug is derived from "name".
//     If the resulting path already exists in the index, a numeric suffix is
//     appended (-2, -3, … up to -100) until a free path is found.
//   - If the caller supplied an explicit "slug" and it already exists, the
//     request is rejected with 409 Conflict so the caller retains control.
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

	// Capture whether the caller explicitly supplied a slug before we
	// auto-generate one — the collision policy differs between the two cases.
	explicitSlug := jsonString(body["slug"]) != ""

	if jsonString(body["id"]) == "" {
		body["id"] = jsonRaw(generateID())
	}
	if !explicitSlug {
		body["slug"] = jsonRaw(slugify(name))
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if _, ok := body["created"]; !ok {
		body["created"] = jsonRaw(now)
	}
	body["modified"] = jsonRaw(now)

	// — validate structure ————————————————————————————————————————————————
	if errs := schema.Validate(primType, body); len(errs) > 0 {
		writeError(w, http.StatusBadRequest,
			fmt.Errorf("validation failed: %s", strings.Join(errs, "; ")))
		return
	}

	// — resolve slug, checking for path collisions in the index ——————————
	baseSlug := jsonString(body["slug"])
	slug := baseSlug
	relPath := BuildPrimitivePath("", primType, slug)

	if explicitSlug {
		// Caller controls the slug — check once and reject if already taken.
		exists, err := s.idx.Exists(r.Context(), relPath)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if exists {
			writeError(w, http.StatusConflict,
				fmt.Errorf("slug %q already taken; choose a different slug", slug))
			return
		}
	} else {
		// Auto-generated slug — walk -2, -3, … until a free path is found.
		exists, err := s.idx.Exists(r.Context(), relPath)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if exists {
			found := false
			for n := 2; n <= 100; n++ {
				slug = fmt.Sprintf("%s-%d", baseSlug, n)
				relPath = BuildPrimitivePath("", primType, slug)
				ex, err := s.idx.Exists(r.Context(), relPath)
				if err != nil {
					writeError(w, http.StatusInternalServerError, err)
					return
				}
				if !ex {
					found = true
					break
				}
			}
			if !found {
				writeError(w, http.StatusConflict,
					fmt.Errorf("slug %q: all suffixes up to -100 are taken", baseSlug))
				return
			}
			// Record the de-duplicated slug in the manifest body before marshal.
			body["slug"] = jsonRaw(slug)
		}
	}

	// — write to disk and commit ——————————————————————————————————————————
	data, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("marshal manifest: %w", err))
		return
	}

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

	// Guard: writes are restricted to the primary root.
	if s.fedConfig != nil && existing.RootSlug != "primary" {
		writeError(w, http.StatusBadRequest, fmt.Errorf(
			"cannot write to federated root %q: writes are restricted to the primary root",
			existing.RootSlug))
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

	// Guard: writes are restricted to the primary root.
	if s.fedConfig != nil && existing.RootSlug != "primary" {
		writeError(w, http.StatusBadRequest, fmt.Errorf(
			"cannot write to federated root %q: writes are restricted to the primary root",
			existing.RootSlug))
		return
	}

	commitMsg := fmt.Sprintf("delete %s: %s", existing.Type, existing.Name)

	if err := s.writer.DeleteManifest(relPath, commitMsg); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("delete manifest: %w", err))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// — federation handlers ———————————————————————————————————————————————————————

// apiRoot is the JSON shape returned for a single root in /api/roots.
type apiRoot struct {
	Slug           string `json:"slug"`
	Trust          string `json:"trust"`
	Primary        bool   `json:"primary"`
	PrimitiveCount int    `json:"primitive_count"`
}

// handleListRoots handles GET /api/roots.
// Returns all configured roots with slug, trust level, primary flag, and
// primitive count. Works in both single-root and multi-root modes.
func (s *Server) handleListRoots(w http.ResponseWriter, r *http.Request) {
	counts, err := s.idx.CountByRoot(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	var roots []apiRoot
	if s.fedConfig == nil {
		// Single-root mode: synthesise a single "primary" root entry.
		roots = []apiRoot{{
			Slug:           "primary",
			Trust:          string(federation.TrustPersonal),
			Primary:        true,
			PrimitiveCount: counts["primary"],
		}}
	} else {
		for _, root := range s.fedConfig.Roots {
			roots = append(roots, apiRoot{
				Slug:           root.Slug,
				Trust:          string(root.Trust),
				Primary:        root.Primary,
				PrimitiveCount: counts[root.Slug],
			})
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"roots": roots})
}

// handleGetParserConfig handles GET /api/parser-config/{slug}.
// Returns the parser config for the given root slug. When no parser config
// file was present for the root, the default Makestack conventions are
// returned. Returns 404 for unknown slugs.
func (s *Server) handleGetParserConfig(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if slug == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("slug is required"))
		return
	}

	// If parser configs are loaded, look up by slug.
	if s.parserCfgs != nil {
		cfg, ok := s.parserCfgs[slug]
		if !ok {
			writeError(w, http.StatusNotFound, fmt.Errorf("no root with slug %q", slug))
			return
		}
		writeJSON(w, http.StatusOK, cfg)
		return
	}

	// Single-root mode: only "primary" is valid.
	if slug != "primary" {
		writeError(w, http.StatusNotFound, fmt.Errorf("no root with slug %q", slug))
		return
	}
	writeJSON(w, http.StatusOK, parser.DefaultConfig())
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
	// Primitives Evolution fields (Core-1, additive).
	Domain     string `json:"domain,omitempty"`
	Unit       string `json:"unit,omitempty"`
	Subtype    string `json:"subtype,omitempty"`
	OccurredAt string `json:"occurred_at,omitempty"`
	Status     string `json:"status,omitempty"`
	Manifest      json.RawMessage `json:"manifest"`
	// CommitHash is the Git commit hash this primitive was read from.
	// Only set when the request used the ?at= query parameter.
	CommitHash string `json:"commit_hash,omitempty"`
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
		Domain:        p.Domain,
		Unit:          p.Unit,
		Subtype:       p.Subtype,
		OccurredAt:    p.OccurredAt,
		Status:        p.Status,
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
		Domain:        pm.Domain,
		Unit:          pm.Unit,
		Subtype:       pm.Subtype,
		OccurredAt:    pm.OccurredAt,
		Status:        pm.Status,
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
