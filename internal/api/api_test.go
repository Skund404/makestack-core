package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	gitpkg "github.com/makestack/makestack-core/internal/git"
	"github.com/makestack/makestack-core/internal/index"
)

// fixturesDir points at the shared test fixtures relative to this file.
const fixturesDir = "../../test/fixtures"

// newTestServer builds a Server backed by a real in-memory index loaded from
// the test/fixtures directory. writer is nil (write endpoints return 503).
// Use apiKey="" for open access and publicReads=false unless testing auth.
func newTestServer(t *testing.T, apiKey string, publicReads bool) *httptest.Server {
	t.Helper()

	idx, err := index.Open(":memory:")
	if err != nil {
		t.Fatalf("index.Open: %v", err)
	}
	t.Cleanup(func() { idx.Close() })

	ctx := context.Background()

	r, err := gitpkg.NewReader(fixturesDir)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	manifests, err := r.ReadAll(ctx)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	for _, m := range manifests {
		pm, err := m.Parse()
		if err != nil {
			continue // skip invalid fixtures
		}
		if err := idx.IndexManifest(ctx, pm); err != nil {
			t.Fatalf("IndexManifest %s: %v", pm.Path, err)
		}
	}
	if err := idx.RebuildFTS(ctx); err != nil {
		t.Fatalf("RebuildFTS: %v", err)
	}

	srv := NewServer(idx, nil /* no writer */, apiKey, publicReads)
	return httptest.NewServer(srv)
}

// get is a test helper for unauthenticated GET requests.
func get(t *testing.T, srv *httptest.Server, path string) *http.Response {
	t.Helper()
	resp, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

// getWithKey performs a GET request with X-API-Key header.
func getWithKey(t *testing.T, srv *httptest.Server, path, key string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	req.Header.Set("X-API-Key", key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

// decodeJSON is a test helper that decodes the response body into v.
func decodeJSON(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
}

// readBody drains and returns the response body as a string.
func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

// — /health ——————————————————————————————————————————————————————————————————

func TestHealth_Returns200(t *testing.T) {
	srv := newTestServer(t, "", false)
	defer srv.Close()

	resp := get(t, srv, "/health")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestHealth_AlwaysPublic_EvenWithAPIKey(t *testing.T) {
	// Auth enabled, but /health must still be reachable without a key.
	srv := newTestServer(t, "secret", false)
	defer srv.Close()

	resp := get(t, srv, "/health")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for /health without key, got %d", resp.StatusCode)
	}
}

// — GET /api/primitives —————————————————————————————————————————————————————

func TestListPrimitives_ReturnsAll(t *testing.T) {
	srv := newTestServer(t, "", false)
	defer srv.Close()

	resp := get(t, srv, "/api/primitives")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d\n%s", resp.StatusCode, readBody(t, resp))
	}

	var result []apiPrimitive
	decodeJSON(t, resp, &result)

	if len(result) < 1 {
		t.Errorf("expected at least 1 primitive, got %d", len(result))
	}
}

func TestListPrimitives_TypeFilter(t *testing.T) {
	srv := newTestServer(t, "", false)
	defer srv.Close()

	resp := get(t, srv, "/api/primitives?type=tool")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result []apiPrimitive
	decodeJSON(t, resp, &result)

	for _, p := range result {
		if p.Type != "tool" {
			t.Errorf("unexpected type %q in tool list", p.Type)
		}
	}
	if len(result) == 0 {
		t.Error("expected at least one tool in fixtures")
	}
}

// — GET /api/primitives/{path} ————————————————————————————————————————————————

func TestGetPrimitive_KnownPath(t *testing.T) {
	srv := newTestServer(t, "", false)
	defer srv.Close()

	// This fixture is guaranteed to exist.
	resp := get(t, srv, "/api/primitives/tools/stitching-chisel/manifest.json")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d\n%s", resp.StatusCode, readBody(t, resp))
	}

	var p apiPrimitive
	decodeJSON(t, resp, &p)

	if p.Type != "tool" {
		t.Errorf("Type: got %q, want %q", p.Type, "tool")
	}
	if p.Name == "" {
		t.Error("Name is empty")
	}
}

func TestGetPrimitive_UnknownPath_Returns404(t *testing.T) {
	srv := newTestServer(t, "", false)
	defer srv.Close()

	resp := get(t, srv, "/api/primitives/tools/does-not-exist/manifest.json")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

// — GET /api/search —————————————————————————————————————————————————————————

func TestSearch_FindsResults(t *testing.T) {
	srv := newTestServer(t, "", false)
	defer srv.Close()

	// "leather" appears in tags and descriptions of several fixtures.
	resp := get(t, srv, "/api/search?q=leather")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d\n%s", resp.StatusCode, readBody(t, resp))
	}

	var result []apiPrimitive
	decodeJSON(t, resp, &result)

	if len(result) == 0 {
		t.Error("expected at least one match for 'leather'")
	}
}

func TestSearch_MissingQuery_Returns400(t *testing.T) {
	srv := newTestServer(t, "", false)
	defer srv.Close()

	resp := get(t, srv, "/api/search")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

// — GET /api/relationships/{path} ————————————————————————————————————————————

func TestRelationships_KnownPath(t *testing.T) {
	srv := newTestServer(t, "", false)
	defer srv.Close()

	// saddle-stitching declares a uses_tool relationship to stitching-chisel.
	resp := get(t, srv, "/api/relationships/techniques/saddle-stitching/manifest.json")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d\n%s", resp.StatusCode, readBody(t, resp))
	}

	var rels []apiRelationship
	decodeJSON(t, resp, &rels)

	if len(rels) == 0 {
		t.Error("expected at least one relationship for saddle-stitching")
	}
}

func TestRelationships_UnknownPath_ReturnsEmpty(t *testing.T) {
	srv := newTestServer(t, "", false)
	defer srv.Close()

	resp := get(t, srv, "/api/relationships/tools/nonexistent/manifest.json")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 (empty list), got %d", resp.StatusCode)
	}

	var rels []apiRelationship
	decodeJSON(t, resp, &rels)

	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

// — Authentication ————————————————————————————————————————————————————————————

func TestAuth_WithKeySet_UnauthenticatedReturns401(t *testing.T) {
	srv := newTestServer(t, "secret-key", false)
	defer srv.Close()

	resp := get(t, srv, "/api/primitives")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

func TestAuth_WithKeySet_AuthenticatedReturns200(t *testing.T) {
	srv := newTestServer(t, "secret-key", false)
	defer srv.Close()

	resp := getWithKey(t, srv, "/api/primitives", "secret-key")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 with correct key, got %d", resp.StatusCode)
	}
}

func TestAuth_WrongKey_Returns401(t *testing.T) {
	srv := newTestServer(t, "secret-key", false)
	defer srv.Close()

	resp := getWithKey(t, srv, "/api/primitives", "wrong-key")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 for wrong key, got %d", resp.StatusCode)
	}
}

func TestAuth_PublicReads_GetWithoutKey_Returns200(t *testing.T) {
	srv := newTestServer(t, "secret-key", true /* publicReads */)
	defer srv.Close()

	resp := get(t, srv, "/api/primitives")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for public read without key, got %d", resp.StatusCode)
	}
}

func TestAuth_PublicReads_PostWithoutKey_Returns401(t *testing.T) {
	srv := newTestServer(t, "secret-key", true /* publicReads */)
	defer srv.Close()

	// POST without key must still be rejected even with --public-reads.
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/primitives", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 for POST without key (public-reads), got %d", resp.StatusCode)
	}
}

// newGitTestServer creates a Server backed by a temp git repository with one
// committed manifest. Returns the test server, the HEAD commit hash, and the
// relative manifest path. The index is populated directly; no watcher is started.
func newGitTestServer(t *testing.T) (*httptest.Server, string, string) {
	t.Helper()

	tmpDir := t.TempDir()

	w, err := gitpkg.NewWriter(tmpDir)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	const manifestPath = "tools/wing-divider/manifest.json"
	const manifest = `{
		"id":          "tool-wd-001",
		"type":        "tool",
		"name":        "Wing Divider",
		"slug":        "wing-divider",
		"created":     "2026-01-01T00:00:00Z",
		"modified":    "2026-01-01T00:00:00Z",
		"description": "Marks parallel stitch lines on leather",
		"tags":        ["leather", "marking"]
	}`

	if err := w.WriteManifest(manifestPath, []byte(manifest), "add wing divider"); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}

	hash, err := w.HeadHash()
	if err != nil {
		t.Fatalf("HeadHash: %v", err)
	}

	idx, err := index.Open(":memory:")
	if err != nil {
		t.Fatalf("index.Open: %v", err)
	}
	t.Cleanup(func() { idx.Close() })

	ctx := context.Background()
	m := gitpkg.Manifest{Path: manifestPath, Raw: json.RawMessage(manifest)}
	pm, err := m.Parse()
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := idx.IndexManifest(ctx, pm); err != nil {
		t.Fatalf("IndexManifest: %v", err)
	}
	if err := idx.RebuildFTS(ctx); err != nil {
		t.Fatalf("RebuildFTS: %v", err)
	}

	srv := NewServer(idx, w, "" /* no auth */, false)
	return httptest.NewServer(srv), hash, manifestPath
}

// — GET /api/primitives/{path}?at= ———————————————————————————————————————————

func TestGetPrimitiveAtCommit_ReturnsCorrectData(t *testing.T) {
	srv, commitHash, path := newGitTestServer(t)
	defer srv.Close()

	resp := get(t, srv, "/api/primitives/"+path+"?at="+commitHash)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d\n%s", resp.StatusCode, readBody(t, resp))
	}

	var p apiPrimitive
	decodeJSON(t, resp, &p)

	if p.Name != "Wing Divider" {
		t.Errorf("Name: got %q, want %q", p.Name, "Wing Divider")
	}
	if p.CommitHash != commitHash {
		t.Errorf("CommitHash: got %q, want %q", p.CommitHash, commitHash)
	}
}

func TestGetPrimitiveAtCommit_BadCommitHash_Returns404(t *testing.T) {
	srv, _, path := newGitTestServer(t)
	defer srv.Close()

	const badHash = "0000000000000000000000000000000000000000"
	resp := get(t, srv, "/api/primitives/"+path+"?at="+badHash)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestGetPrimitiveAtCommit_UnknownPath_Returns404(t *testing.T) {
	srv, commitHash, _ := newGitTestServer(t)
	defer srv.Close()

	resp := get(t, srv, "/api/primitives/tools/nonexistent/manifest.json?at="+commitHash)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestGetPrimitiveAtCommit_NoWriter_Returns503(t *testing.T) {
	// newTestServer wires writer=nil.
	srv := newTestServer(t, "", false)
	defer srv.Close()

	const hash = "0000000000000000000000000000000000000000"
	resp := get(t, srv, "/api/primitives/tools/stitching-chisel/manifest.json?at="+hash)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", resp.StatusCode)
	}
}

// — GET /api/primitives/{path}/hash ——————————————————————————————————————————

func TestGetPrimitiveHash_ReturnsCommitHash(t *testing.T) {
	srv, _, path := newGitTestServer(t)
	defer srv.Close()

	resp := get(t, srv, "/api/primitives/"+path+"/hash")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d\n%s", resp.StatusCode, readBody(t, resp))
	}

	var result map[string]string
	decodeJSON(t, resp, &result)

	h, ok := result["commit_hash"]
	if !ok {
		t.Fatal("response missing commit_hash field")
	}
	if len(h) != 40 {
		t.Errorf("expected 40-char hash, got %d chars: %q", len(h), h)
	}
}

func TestGetPrimitiveHash_UnknownPath_Returns404(t *testing.T) {
	srv, _, _ := newGitTestServer(t)
	defer srv.Close()

	resp := get(t, srv, "/api/primitives/tools/nonexistent/manifest.json/hash")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestGetPrimitiveHash_NoWriter_Returns503(t *testing.T) {
	// newTestServer wires writer=nil.
	srv := newTestServer(t, "", false)
	defer srv.Close()

	resp := get(t, srv, "/api/primitives/tools/stitching-chisel/manifest.json/hash")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", resp.StatusCode)
	}
}
