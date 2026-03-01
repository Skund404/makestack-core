package git

import (
	"errors"
	"strings"
	"testing"
)

// newWriterWithCommit is a test helper that creates a Writer on a fresh
// temporary git repository, writes one manifest.json, commits it, and returns
// the writer together with the commit hash and the relative manifest path.
func newWriterWithCommit(t *testing.T) (*Writer, string, string) {
	t.Helper()

	tmpDir := t.TempDir()

	w, err := NewWriter(tmpDir)
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

	return w, hash, manifestPath
}

// — HeadHash —————————————————————————————————————————————————————————————————

func TestHeadHash_ReturnsHexString(t *testing.T) {
	w, hash, _ := newWriterWithCommit(t)
	_ = w

	// A SHA-1 hash is exactly 40 lowercase hex characters.
	if len(hash) != 40 {
		t.Errorf("expected 40-char hash, got %d chars: %q", len(hash), hash)
	}
	for _, c := range hash {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Errorf("non-hex character %q in hash %q", c, hash)
			break
		}
	}
}

func TestHeadHash_ChangesAfterNewCommit(t *testing.T) {
	w, firstHash, _ := newWriterWithCommit(t)

	// Write another file and commit — HEAD should advance.
	const p2 = "tools/awl/manifest.json"
	const m2 = `{"id":"tool-awl-001","type":"tool","name":"Awl","slug":"awl","created":"2026-01-01T00:00:00Z","modified":"2026-01-01T00:00:00Z"}`
	if err := w.WriteManifest(p2, []byte(m2), "add awl"); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}

	secondHash, err := w.HeadHash()
	if err != nil {
		t.Fatalf("HeadHash: %v", err)
	}
	if firstHash == secondHash {
		t.Error("expected HEAD hash to change after a new commit, but it did not")
	}
}

// — ReadManifestAtCommit ——————————————————————————————————————————————————————

func TestReadManifestAtCommit_ReturnsCorrectData(t *testing.T) {
	w, hash, path := newWriterWithCommit(t)

	pm, err := w.ReadManifestAtCommit(path, hash)
	if err != nil {
		t.Fatalf("ReadManifestAtCommit: %v", err)
	}

	if pm.ID != "tool-wd-001" {
		t.Errorf("ID: got %q, want %q", pm.ID, "tool-wd-001")
	}
	if pm.Type != "tool" {
		t.Errorf("Type: got %q, want %q", pm.Type, "tool")
	}
	if pm.Name != "Wing Divider" {
		t.Errorf("Name: got %q, want %q", pm.Name, "Wing Divider")
	}
	if pm.Description != "Marks parallel stitch lines on leather" {
		t.Errorf("Description: got %q", pm.Description)
	}
	if pm.Path != path {
		t.Errorf("Path: got %q, want %q", pm.Path, path)
	}
}

func TestReadManifestAtCommit_ReturnsOldVersionAfterUpdate(t *testing.T) {
	w, firstHash, path := newWriterWithCommit(t)

	// Update the manifest — a new version with a different description.
	const updated = `{
		"id":          "tool-wd-001",
		"type":        "tool",
		"name":        "Wing Divider",
		"slug":        "wing-divider",
		"created":     "2026-01-01T00:00:00Z",
		"modified":    "2026-06-01T00:00:00Z",
		"description": "Updated description"
	}`
	if err := w.WriteManifest(path, []byte(updated), "update wing divider"); err != nil {
		t.Fatalf("WriteManifest (update): %v", err)
	}

	// Reading at the first commit should return the original description.
	pm, err := w.ReadManifestAtCommit(path, firstHash)
	if err != nil {
		t.Fatalf("ReadManifestAtCommit at first hash: %v", err)
	}
	if pm.Description != "Marks parallel stitch lines on leather" {
		t.Errorf("expected original description at first commit, got %q", pm.Description)
	}

	// Reading at HEAD should return the updated description.
	headHash, _ := w.HeadHash()
	pm2, err := w.ReadManifestAtCommit(path, headHash)
	if err != nil {
		t.Fatalf("ReadManifestAtCommit at HEAD: %v", err)
	}
	if pm2.Description != "Updated description" {
		t.Errorf("expected updated description at HEAD, got %q", pm2.Description)
	}
}

func TestReadManifestAtCommit_UnknownCommitHash_ReturnsErrNotFound(t *testing.T) {
	w, _, path := newWriterWithCommit(t)

	const badHash = "0000000000000000000000000000000000000000"
	_, err := w.ReadManifestAtCommit(path, badHash)
	if err == nil {
		t.Fatal("expected error for unknown commit hash, got nil")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

func TestReadManifestAtCommit_UnknownPath_ReturnsErrNotFound(t *testing.T) {
	w, hash, _ := newWriterWithCommit(t)

	_, err := w.ReadManifestAtCommit("tools/nonexistent/manifest.json", hash)
	if err == nil {
		t.Fatal("expected error for unknown path, got nil")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

func TestReadManifestAtCommit_PathExistsAtLaterCommit_NotFoundAtEarlierCommit(t *testing.T) {
	w, firstHash, _ := newWriterWithCommit(t)

	// Add a second primitive in a new commit.
	const path2 = "tools/awl/manifest.json"
	const m2 = `{"id":"tool-awl-001","type":"tool","name":"Awl","slug":"awl","created":"2026-01-01T00:00:00Z","modified":"2026-01-01T00:00:00Z"}`
	if err := w.WriteManifest(path2, []byte(m2), "add awl"); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}

	// The awl did not exist at the first commit — should return ErrNotFound.
	_, err := w.ReadManifestAtCommit(path2, firstHash)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for path not yet in repo, got: %v", err)
	}

	// But it exists at HEAD.
	headHash, _ := w.HeadHash()
	pm, err := w.ReadManifestAtCommit(path2, headHash)
	if err != nil {
		t.Fatalf("ReadManifestAtCommit at HEAD: %v", err)
	}
	if pm.Name != "Awl" {
		t.Errorf("Name: got %q", pm.Name)
	}
}
