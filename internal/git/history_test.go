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

// — CommitHistoryForPath ——————————————————————————————————————————————————————

func TestCommitHistoryForPath_SingleCommit(t *testing.T) {
	w, hash, path := newWriterWithCommit(t)

	commits, total, err := w.CommitHistoryForPath(path, 50, 0)
	if err != nil {
		t.Fatalf("CommitHistoryForPath: %v", err)
	}
	if total != 1 {
		t.Errorf("total: got %d, want 1", total)
	}
	if len(commits) != 1 {
		t.Fatalf("len(commits): got %d, want 1", len(commits))
	}
	if commits[0].Hash != hash {
		t.Errorf("Hash: got %q, want %q", commits[0].Hash, hash)
	}
	if commits[0].Author == "" {
		t.Error("Author is empty")
	}
	if commits[0].Timestamp == "" {
		t.Error("Timestamp is empty")
	}
	if commits[0].Message == "" {
		t.Error("Message is empty")
	}
}

func TestCommitHistoryForPath_MultipleCommits_NewestFirst(t *testing.T) {
	w, firstHash, path := newWriterWithCommit(t)

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

	commits, total, err := w.CommitHistoryForPath(path, 50, 0)
	if err != nil {
		t.Fatalf("CommitHistoryForPath: %v", err)
	}
	if total != 2 {
		t.Errorf("total: got %d, want 2", total)
	}
	if len(commits) != 2 {
		t.Fatalf("len(commits): got %d, want 2", len(commits))
	}
	// Newest first: HEAD should be first, initial commit second.
	if commits[1].Hash != firstHash {
		t.Errorf("expected first commit at index 1, got %q", commits[1].Hash)
	}
}

func TestCommitHistoryForPath_UnknownPath_ReturnsEmpty(t *testing.T) {
	w, _, _ := newWriterWithCommit(t)

	commits, total, err := w.CommitHistoryForPath("tools/nonexistent/manifest.json", 50, 0)
	if err != nil {
		t.Fatalf("CommitHistoryForPath: %v", err)
	}
	if total != 0 {
		t.Errorf("total: got %d, want 0", total)
	}
	if len(commits) != 0 {
		t.Errorf("len(commits): got %d, want 0", len(commits))
	}
}

func TestCommitHistoryForPath_Pagination(t *testing.T) {
	w, _, path := newWriterWithCommit(t)

	// Write two more versions — three commits total on the same path.
	for i, desc := range []string{"v2 description", "v3 description"} {
		body := `{"id":"tool-wd-001","type":"tool","name":"Wing Divider","slug":"wing-divider","created":"2026-01-01T00:00:00Z","modified":"2026-01-01T00:00:00Z","description":"` + desc + `"}`
		if err := w.WriteManifest(path, []byte(body), "update "+string(rune('a'+i))); err != nil {
			t.Fatalf("WriteManifest: %v", err)
		}
	}

	// total=3; offset=1, limit=1 → exactly one middle commit.
	commits, total, err := w.CommitHistoryForPath(path, 1, 1)
	if err != nil {
		t.Fatalf("CommitHistoryForPath: %v", err)
	}
	if total != 3 {
		t.Errorf("total: got %d, want 3", total)
	}
	if len(commits) != 1 {
		t.Errorf("len(commits): got %d, want 1", len(commits))
	}
}

func TestCommitHistoryForPath_UnrelatedPath_NotIncluded(t *testing.T) {
	w, _, pathA := newWriterWithCommit(t)

	// Commit a different file.
	const pathB = "tools/awl/manifest.json"
	const mB = `{"id":"tool-awl-001","type":"tool","name":"Awl","slug":"awl","created":"2026-01-01T00:00:00Z","modified":"2026-01-01T00:00:00Z"}`
	if err := w.WriteManifest(pathB, []byte(mB), "add awl"); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}

	// History of pathA should still be 1 — the awl commit must not appear.
	_, total, err := w.CommitHistoryForPath(pathA, 50, 0)
	if err != nil {
		t.Fatalf("CommitHistoryForPath: %v", err)
	}
	if total != 1 {
		t.Errorf("total for pathA: got %d, want 1", total)
	}
}

// — LastCommitHashForPath —————————————————————————————————————————————————————

func TestLastCommitHashForPath_ReturnsPathSpecificHash(t *testing.T) {
	w, hashA, pathA := newWriterWithCommit(t)

	// Commit a second, unrelated file. HEAD advances, but pathA's last-touched
	// commit must not change.
	const pathB = "tools/awl/manifest.json"
	const mB = `{"id":"tool-awl-001","type":"tool","name":"Awl","slug":"awl","created":"2026-01-01T00:00:00Z","modified":"2026-01-01T00:00:00Z"}`
	if err := w.WriteManifest(pathB, []byte(mB), "add awl"); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	hashB, _ := w.HeadHash()

	if hashA == hashB {
		t.Fatal("test setup: expected two distinct commits")
	}

	// pathA was last modified at hashA, not HEAD.
	got, err := w.LastCommitHashForPath(pathA)
	if err != nil {
		t.Fatalf("LastCommitHashForPath(pathA): %v", err)
	}
	if got != hashA {
		t.Errorf("pathA: got %q, want %q (not HEAD %q)", got, hashA, hashB)
	}

	// pathB was last modified at HEAD (hashB).
	got2, err := w.LastCommitHashForPath(pathB)
	if err != nil {
		t.Fatalf("LastCommitHashForPath(pathB): %v", err)
	}
	if got2 != hashB {
		t.Errorf("pathB: got %q, want %q", got2, hashB)
	}
}

func TestLastCommitHashForPath_UnknownPath_ReturnsErrNotFound(t *testing.T) {
	w, _, _ := newWriterWithCommit(t)

	_, err := w.LastCommitHashForPath("tools/nonexistent/manifest.json")
	if err == nil {
		t.Fatal("expected error for unknown path, got nil")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

// — ParentHash ————————————————————————————————————————————————————————————————

func TestParentHash_ReturnsParentOfSecondCommit(t *testing.T) {
	w, firstHash, path := newWriterWithCommit(t)

	const m2 = `{"id":"tool-wd-001","type":"tool","name":"Wing Divider","slug":"wing-divider","created":"2026-01-01T00:00:00Z","modified":"2026-06-01T00:00:00Z"}`
	if err := w.WriteManifest(path, []byte(m2), "update wing divider"); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	headHash, _ := w.HeadHash()

	parent, err := w.ParentHash(headHash)
	if err != nil {
		t.Fatalf("ParentHash: %v", err)
	}
	if parent != firstHash {
		t.Errorf("ParentHash: got %q, want %q", parent, firstHash)
	}
}

func TestParentHash_InitialCommit_ReturnsErrNotFound(t *testing.T) {
	w, hash, _ := newWriterWithCommit(t)

	_, err := w.ParentHash(hash)
	if err == nil {
		t.Fatal("expected error for initial commit, got nil")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

// — DiffManifests —————————————————————————————————————————————————————————————

func TestDiffManifests(t *testing.T) {
	cases := []struct {
		name    string
		from    string
		to      string
		wantNil bool // expect nil (not empty slice)
		check   func(t *testing.T, changes []FieldChange)
	}{
		{
			name: "identical documents produce no changes",
			from: `{"name":"X","description":"d"}`,
			to:   `{"name":"X","description":"d"}`,
			check: func(t *testing.T, changes []FieldChange) {
				if len(changes) != 0 {
					t.Errorf("expected 0 changes, got %d: %v", len(changes), changes)
				}
			},
		},
		{
			name: "scalar modified",
			from: `{"description":"old"}`,
			to:   `{"description":"new"}`,
			check: func(t *testing.T, changes []FieldChange) {
				if len(changes) != 1 {
					t.Fatalf("expected 1 change, got %d", len(changes))
				}
				if changes[0].Field != "description" || changes[0].Type != "modified" {
					t.Errorf("unexpected change: %+v", changes[0])
				}
				if changes[0].OldValue != "old" {
					t.Errorf("OldValue: got %v", changes[0].OldValue)
				}
				if changes[0].NewValue != "new" {
					t.Errorf("NewValue: got %v", changes[0].NewValue)
				}
			},
		},
		{
			name: "field added",
			from: `{"name":"X"}`,
			to:   `{"name":"X","description":"added"}`,
			check: func(t *testing.T, changes []FieldChange) {
				if len(changes) != 1 {
					t.Fatalf("expected 1 change, got %d", len(changes))
				}
				if changes[0].Field != "description" || changes[0].Type != "added" {
					t.Errorf("unexpected change: %+v", changes[0])
				}
			},
		},
		{
			name: "field removed",
			from: `{"name":"X","description":"old"}`,
			to:   `{"name":"X"}`,
			check: func(t *testing.T, changes []FieldChange) {
				if len(changes) != 1 {
					t.Fatalf("expected 1 change, got %d", len(changes))
				}
				if changes[0].Field != "description" || changes[0].Type != "removed" {
					t.Errorf("unexpected change: %+v", changes[0])
				}
			},
		},
		{
			name: "array element added",
			from: `{"tags":["a"]}`,
			to:   `{"tags":["a","b"]}`,
			check: func(t *testing.T, changes []FieldChange) {
				if len(changes) != 1 {
					t.Fatalf("expected 1 change, got %d", len(changes))
				}
				if changes[0].Field != "tags[1]" || changes[0].Type != "added" {
					t.Errorf("unexpected change: %+v", changes[0])
				}
			},
		},
		{
			name: "array element removed",
			from: `{"tags":["a","b"]}`,
			to:   `{"tags":["a"]}`,
			check: func(t *testing.T, changes []FieldChange) {
				if len(changes) != 1 {
					t.Fatalf("expected 1 change, got %d", len(changes))
				}
				if changes[0].Field != "tags[1]" || changes[0].Type != "removed" {
					t.Errorf("unexpected change: %+v", changes[0])
				}
			},
		},
		{
			name: "array element modified",
			from: `{"tags":["a","b"]}`,
			to:   `{"tags":["a","c"]}`,
			check: func(t *testing.T, changes []FieldChange) {
				if len(changes) != 1 {
					t.Fatalf("expected 1 change, got %d", len(changes))
				}
				if changes[0].Field != "tags[1]" || changes[0].Type != "modified" {
					t.Errorf("unexpected change: %+v", changes[0])
				}
			},
		},
		{
			name: "nested object field changed",
			from: `{"properties":{"tension":"low"}}`,
			to:   `{"properties":{"tension":"high"}}`,
			check: func(t *testing.T, changes []FieldChange) {
				if len(changes) != 1 {
					t.Fatalf("expected 1 change, got %d", len(changes))
				}
				if changes[0].Field != "properties.tension" || changes[0].Type != "modified" {
					t.Errorf("unexpected change: %+v", changes[0])
				}
			},
		},
		{
			name:    "invalid from JSON returns nil",
			from:    `not json`,
			to:      `{"name":"X"}`,
			wantNil: true,
		},
		{
			name:    "invalid to JSON returns nil",
			from:    `{"name":"X"}`,
			to:      `not json`,
			wantNil: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			changes := DiffManifests([]byte(tc.from), []byte(tc.to))
			if tc.wantNil {
				if changes != nil {
					t.Errorf("expected nil, got %v", changes)
				}
				return
			}
			tc.check(t, changes)
		})
	}
}
