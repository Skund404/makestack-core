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
