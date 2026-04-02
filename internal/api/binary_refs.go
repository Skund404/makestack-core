// Package api — this file implements the /api/binary-refs endpoints.
//
// Binary refs are git-backed JSON pointer files that track the location and
// metadata of binary assets (photos, videos, 3D models, documents, etc.)
// without using Git LFS. Each ref is stored at binary-refs/{slug}/ref.json
// in the data repository and indexed in SQLite for fast querying.
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/makestack/makestack-core/internal/index"
)

// — request / response shapes —————————————————————————————————————————————————

// apiBinaryRef is the JSON shape returned for a single binary ref.
type apiBinaryRef struct {
	ID             string   `json:"id"`
	Slug           string   `json:"slug"`
	Filename       string   `json:"filename"`
	MimeType       string   `json:"mime_type,omitempty"`
	SizeBytes      int64    `json:"size_bytes,omitempty"`
	SHA256         string   `json:"sha256,omitempty"`
	LocalPath      string   `json:"local_path,omitempty"`
	BackupLocation string   `json:"backup_location,omitempty"`
	AssetType      string   `json:"asset_type,omitempty"`
	Description    string   `json:"description,omitempty"`
	Tags           []string `json:"tags"`
	PrimitiveRef   string   `json:"primitive_ref,omitempty"`
	Created        string   `json:"created,omitempty"`
	Modified       string   `json:"modified,omitempty"`
}

// buildBinaryRefPath returns the canonical relative path for a binary ref.
func buildBinaryRefPath(slug string) string {
	return "binary-refs/" + slug + "/ref.json"
}

// — handlers ——————————————————————————————————————————————————————————————————

// handleListBinaryRefs handles GET /api/binary-refs.
// Optional query params: ?asset_type=<type>&primitive_ref=<path>
func (s *Server) handleListBinaryRefs(w http.ResponseWriter, r *http.Request) {
	assetType := r.URL.Query().Get("asset_type")
	primitiveRef := r.URL.Query().Get("primitive_ref")

	refs, err := s.idx.ListBinaryRefs(r.Context(), assetType, primitiveRef)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	result := make([]apiBinaryRef, len(refs))
	for i, ref := range refs {
		result[i] = toBinaryRefAPI(ref)
	}
	writeJSON(w, http.StatusOK, result)
}

// handleGetBinaryRef handles GET /api/binary-refs/{slug}.
func (s *Server) handleGetBinaryRef(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if slug == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("slug is required"))
		return
	}

	ref, err := s.idx.GetBinaryRef(r.Context(), slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if ref == nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("not found: %s", slug))
		return
	}
	writeJSON(w, http.StatusOK, toBinaryRefAPI(*ref))
}

// handleCreateBinaryRef handles POST /api/binary-refs.
// Required field: filename. Optional: all others.
func (s *Server) handleCreateBinaryRef(w http.ResponseWriter, r *http.Request) {
	if !s.writerReady(w) {
		return
	}

	var body map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body: %w", err))
		return
	}

	filename := jsonString(body["filename"])
	if filename == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("missing required field: filename"))
		return
	}

	// Auto-generate id and slug.
	if jsonString(body["id"]) == "" {
		body["id"] = jsonRaw(generateID())
	}
	if jsonString(body["slug"]) == "" {
		body["slug"] = jsonRaw(slugify(filename))
	}

	// De-duplicate slug if needed.
	baseSlug := jsonString(body["slug"])
	slug := baseSlug
	refPath := buildBinaryRefPath(slug)
	for n := 2; n <= 100; n++ {
		exists, err := s.idx.BinaryRefExists(r.Context(), slug)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if !exists {
			break
		}
		slug = fmt.Sprintf("%s-%d", baseSlug, n)
		refPath = buildBinaryRefPath(slug)
	}
	body["slug"] = jsonRaw(slug)

	// Stamp timestamps.
	now := time.Now().UTC().Format(time.RFC3339)
	if _, ok := body["created"]; !ok {
		body["created"] = jsonRaw(now)
	}
	body["modified"] = jsonRaw(now)

	data, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("marshal ref: %w", err))
		return
	}

	commitMsg := fmt.Sprintf("add binary-ref: %s", filename)
	if err := s.writer.WriteManifest(refPath, data, commitMsg); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("write ref: %w", err))
		return
	}

	// Index synchronously (watcher will also pick it up, but callers expect
	// the created ref to be immediately queryable).
	ref := parseBinaryRefBody(body, refPath)
	if err := s.idx.UpsertBinaryRef(r.Context(), ref); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("index ref: %w", err))
		return
	}

	writeJSON(w, http.StatusCreated, toBinaryRefAPI(ref))
}

// handleUpdateBinaryRef handles PUT /api/binary-refs/{slug}.
func (s *Server) handleUpdateBinaryRef(w http.ResponseWriter, r *http.Request) {
	if !s.writerReady(w) {
		return
	}

	slug := r.PathValue("slug")
	if slug == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("slug is required"))
		return
	}

	existing, err := s.idx.GetBinaryRef(r.Context(), slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if existing == nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("not found: %s", slug))
		return
	}

	var body map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body: %w", err))
		return
	}

	// Lock id and slug to the existing values.
	body["id"] = jsonRaw(existing.ID)
	body["slug"] = jsonRaw(existing.Slug)

	// Preserve original created timestamp; stamp new modified.
	body["created"] = jsonRaw(existing.Created)
	body["modified"] = jsonRaw(time.Now().UTC().Format(time.RFC3339))

	data, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("marshal ref: %w", err))
		return
	}

	refPath := buildBinaryRefPath(slug)
	filename := jsonString(body["filename"])
	if filename == "" {
		filename = existing.Filename
	}
	commitMsg := fmt.Sprintf("update binary-ref: %s", filename)
	if err := s.writer.WriteManifest(refPath, data, commitMsg); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("write ref: %w", err))
		return
	}

	ref := parseBinaryRefBody(body, refPath)
	if err := s.idx.UpsertBinaryRef(r.Context(), ref); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("index ref: %w", err))
		return
	}

	writeJSON(w, http.StatusOK, toBinaryRefAPI(ref))
}

// handleDeleteBinaryRef handles DELETE /api/binary-refs/{slug}.
func (s *Server) handleDeleteBinaryRef(w http.ResponseWriter, r *http.Request) {
	if !s.writerReady(w) {
		return
	}

	slug := r.PathValue("slug")
	if slug == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("slug is required"))
		return
	}

	existing, err := s.idx.GetBinaryRef(r.Context(), slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if existing == nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("not found: %s", slug))
		return
	}

	refPath := buildBinaryRefPath(slug)
	commitMsg := fmt.Sprintf("delete binary-ref: %s", existing.Filename)
	if err := s.writer.DeleteManifest(refPath, commitMsg); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("delete ref: %w", err))
		return
	}

	if err := s.idx.DeleteBinaryRef(r.Context(), slug); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("remove ref from index: %w", err))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// — helpers ———————————————————————————————————————————————————————————————————

// parseBinaryRefBody converts a raw JSON body map to an index.BinaryRef.
func parseBinaryRefBody(body map[string]json.RawMessage, refPath string) index.BinaryRef {
	ref := index.BinaryRef{
		ID:             jsonString(body["id"]),
		Slug:           jsonString(body["slug"]),
		Filename:       jsonString(body["filename"]),
		MimeType:       jsonString(body["mime_type"]),
		LocalPath:      jsonString(body["local_path"]),
		BackupLocation: jsonString(body["backup_location"]),
		AssetType:      jsonString(body["asset_type"]),
		Description:    jsonString(body["description"]),
		SHA256:         jsonString(body["sha256"]),
		PrimitiveRef:   jsonString(body["primitive_ref"]),
		Created:        jsonString(body["created"]),
		Modified:       jsonString(body["modified"]),
	}

	// Parse size_bytes as int64.
	if raw, ok := body["size_bytes"]; ok {
		var n int64
		if err := json.Unmarshal(raw, &n); err == nil {
			ref.SizeBytes = n
		}
	}

	// Parse tags as []string.
	if raw, ok := body["tags"]; ok {
		var tags []string
		if err := json.Unmarshal(raw, &tags); err == nil {
			ref.Tags = tags
		}
	}

	ref.Raw = buildRefRaw(body)
	return ref
}

// buildRefRaw re-serialises the body map to a canonical JSON blob for storage.
func buildRefRaw(body map[string]json.RawMessage) json.RawMessage {
	b, _ := json.MarshalIndent(body, "", "  ")
	return json.RawMessage(b)
}

// toBinaryRefAPI converts an index.BinaryRef to the API response shape.
func toBinaryRefAPI(r index.BinaryRef) apiBinaryRef {
	tags := r.Tags
	if tags == nil {
		tags = []string{}
	}
	return apiBinaryRef{
		ID:             r.ID,
		Slug:           r.Slug,
		Filename:       r.Filename,
		MimeType:       r.MimeType,
		SizeBytes:      r.SizeBytes,
		SHA256:         r.SHA256,
		LocalPath:      r.LocalPath,
		BackupLocation: r.BackupLocation,
		AssetType:      r.AssetType,
		Description:    r.Description,
		Tags:           tags,
		PrimitiveRef:   r.PrimitiveRef,
		Created:        r.Created,
		Modified:       r.Modified,
	}
}
