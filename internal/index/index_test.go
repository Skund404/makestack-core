package index

import (
	"context"
	"encoding/json"
	"testing"

	gitpkg "github.com/makestack/makestack-core/internal/git"
)

// openMemory is a test helper that opens an in-memory index or fatals.
func openMemory(t *testing.T) *Index {
	t.Helper()
	idx, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { idx.Close() })
	return idx
}

// testPrimitive returns a minimal Primitive suitable for insertion.
func testPrimitive(id, typ, name, slug, path string) Primitive {
	return Primitive{
		ID:       id,
		Type:     typ,
		Name:     name,
		Slug:     slug,
		Path:     path,
		Created:  "2026-01-01T00:00:00Z",
		Modified: "2026-01-01T00:00:00Z",
		Tags:     json.RawMessage(`["test"]`),
		Manifest: json.RawMessage(`{"id":"` + id + `","type":"` + typ + `","name":"` + name + `","slug":"` + slug + `"}`),
	}
}

// — Open —————————————————————————————————————————————————————————————————————

func TestOpen_CreatesSchema(t *testing.T) {
	idx := openMemory(t)

	// Verify the primitives table exists by querying it.
	var n int
	if err := idx.db.QueryRow(`SELECT COUNT(*) FROM primitives`).Scan(&n); err != nil {
		t.Fatalf("primitives table missing or unreadable: %v", err)
	}
	if err := idx.db.QueryRow(`SELECT COUNT(*) FROM relationships`).Scan(&n); err != nil {
		t.Fatalf("relationships table missing or unreadable: %v", err)
	}
}

// — UpsertFull + Get —————————————————————————————————————————————————————————

func TestUpsertFull_Get_RoundTrip(t *testing.T) {
	idx := openMemory(t)
	ctx := context.Background()

	p := testPrimitive("tool-001", "tool", "Wing Divider", "wing-divider", "tools/wing-divider/manifest.json")
	p.Description = "Marks stitch lines"
	p.Tags = json.RawMessage(`["leather","marking"]`)

	if err := idx.UpsertFull(ctx, p, nil); err != nil {
		t.Fatalf("UpsertFull: %v", err)
	}

	got, err := idx.Get(ctx, p.Path)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil for existing path")
	}

	if got.ID != p.ID {
		t.Errorf("ID: got %q, want %q", got.ID, p.ID)
	}
	if got.Type != p.Type {
		t.Errorf("Type: got %q, want %q", got.Type, p.Type)
	}
	if got.Name != p.Name {
		t.Errorf("Name: got %q, want %q", got.Name, p.Name)
	}
	if got.Description != p.Description {
		t.Errorf("Description: got %q, want %q", got.Description, p.Description)
	}
}

func TestUpsertFull_Idempotent(t *testing.T) {
	idx := openMemory(t)
	ctx := context.Background()

	p := testPrimitive("tool-001", "tool", "Wing Divider", "wing-divider", "tools/wing-divider/manifest.json")

	// Insert twice — second call should update, not duplicate.
	if err := idx.UpsertFull(ctx, p, nil); err != nil {
		t.Fatalf("first UpsertFull: %v", err)
	}
	p.Name = "Wing Divider (Updated)"
	if err := idx.UpsertFull(ctx, p, nil); err != nil {
		t.Fatalf("second UpsertFull: %v", err)
	}

	n, err := idx.CountAll(ctx)
	if err != nil {
		t.Fatalf("CountAll: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 primitive after upsert, got %d", n)
	}

	got, _ := idx.Get(ctx, p.Path)
	if got.Name != "Wing Divider (Updated)" {
		t.Errorf("Name after update: got %q", got.Name)
	}
}

func TestGet_ReturnsNilForMissingPath(t *testing.T) {
	idx := openMemory(t)
	ctx := context.Background()

	got, err := idx.Get(ctx, "tools/nonexistent/manifest.json")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for missing path, got %+v", got)
	}
}

// — Relationships ————————————————————————————————————————————————————————————

func TestRelationshipsFor_BidirectionalLookup(t *testing.T) {
	idx := openMemory(t)
	ctx := context.Background()

	toolPath := "tools/stitching-chisel/manifest.json"
	techPath := "techniques/saddle-stitching/manifest.json"

	tool := testPrimitive("tool-001", "tool", "Stitching Chisel", "stitching-chisel", toolPath)
	tech := testPrimitive("tec-001", "technique", "Saddle Stitching", "saddle-stitching", techPath)

	rel := Relationship{
		SourcePath: techPath,
		SourceType: "technique",
		RelType:    "uses_tool",
		TargetPath: toolPath,
	}

	if err := idx.UpsertFull(ctx, tool, nil); err != nil {
		t.Fatalf("UpsertFull tool: %v", err)
	}
	if err := idx.UpsertFull(ctx, tech, []Relationship{rel}); err != nil {
		t.Fatalf("UpsertFull technique: %v", err)
	}

	// Query from the target's perspective — should still find the relationship.
	rels, err := idx.RelationshipsFor(ctx, toolPath)
	if err != nil {
		t.Fatalf("RelationshipsFor: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship via tool path, got %d", len(rels))
	}
	if rels[0].RelType != "uses_tool" {
		t.Errorf("RelType: got %q, want %q", rels[0].RelType, "uses_tool")
	}

	// Query from the source's perspective.
	rels, err = idx.RelationshipsFor(ctx, techPath)
	if err != nil {
		t.Fatalf("RelationshipsFor (source): %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship via technique path, got %d", len(rels))
	}
}

// — Delete ————————————————————————————————————————————————————————————————————

func TestDelete_RemovesPrimitiveAndRelationships(t *testing.T) {
	idx := openMemory(t)
	ctx := context.Background()

	toolPath := "tools/chisel/manifest.json"
	techPath := "techniques/stitch/manifest.json"

	tool := testPrimitive("tool-001", "tool", "Chisel", "chisel", toolPath)
	tech := testPrimitive("tec-001", "technique", "Stitch", "stitch", techPath)
	rel := Relationship{SourcePath: techPath, SourceType: "technique", RelType: "uses_tool", TargetPath: toolPath}

	_ = idx.UpsertFull(ctx, tool, nil)
	_ = idx.UpsertFull(ctx, tech, []Relationship{rel})

	if err := idx.Delete(ctx, techPath); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Primitive gone.
	got, _ := idx.Get(ctx, techPath)
	if got != nil {
		t.Error("expected nil after Delete, got non-nil")
	}

	// Relationship gone.
	rels, _ := idx.RelationshipsFor(ctx, toolPath)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships after Delete, got %d", len(rels))
	}
}

// — List ——————————————————————————————————————————————————————————————————————

func TestList_NoFilter_ReturnsAll(t *testing.T) {
	idx := openMemory(t)
	ctx := context.Background()

	_ = idx.UpsertFull(ctx, testPrimitive("1", "tool",     "T", "t", "tools/t/manifest.json"),     nil)
	_ = idx.UpsertFull(ctx, testPrimitive("2", "material", "M", "m", "materials/m/manifest.json"), nil)
	_ = idx.UpsertFull(ctx, testPrimitive("3", "technique","X", "x", "techniques/x/manifest.json"),nil)

	all, err := idx.List(ctx, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("expected 3, got %d", len(all))
	}
}

func TestList_TypeFilter_ReturnsSubset(t *testing.T) {
	idx := openMemory(t)
	ctx := context.Background()

	_ = idx.UpsertFull(ctx, testPrimitive("1", "tool",     "T1", "t1", "tools/t1/manifest.json"),   nil)
	_ = idx.UpsertFull(ctx, testPrimitive("2", "tool",     "T2", "t2", "tools/t2/manifest.json"),   nil)
	_ = idx.UpsertFull(ctx, testPrimitive("3", "material", "M",  "m",  "materials/m/manifest.json"),nil)

	tools, err := idx.List(ctx, "tool")
	if err != nil {
		t.Fatalf("List(tool): %v", err)
	}
	if len(tools) != 2 {
		t.Errorf("expected 2 tools, got %d", len(tools))
	}
	for _, p := range tools {
		if p.Type != "tool" {
			t.Errorf("unexpected type %q in tool list", p.Type)
		}
	}
}

// — Search (FTS5) ————————————————————————————————————————————————————————————

func TestSearch_FindsByName(t *testing.T) {
	idx := openMemory(t)
	ctx := context.Background()

	p := testPrimitive("1", "tool", "Wing Divider", "wing-divider", "tools/wing-divider/manifest.json")
	p.Manifest = json.RawMessage(`{"id":"1","type":"tool","name":"Wing Divider","slug":"wing-divider"}`)
	_ = idx.UpsertFull(ctx, p, nil)
	_ = idx.UpsertFull(ctx, testPrimitive("2", "material", "Leather", "leather", "materials/leather/manifest.json"), nil)

	if err := idx.RebuildFTS(ctx); err != nil {
		t.Fatalf("RebuildFTS: %v", err)
	}

	results, err := idx.Search(ctx, "Wing")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result for 'Wing', got %d", len(results))
	}
	if results[0].ID != "1" {
		t.Errorf("unexpected result ID %q", results[0].ID)
	}
}

func TestSearch_FindsByDescription(t *testing.T) {
	idx := openMemory(t)
	ctx := context.Background()

	p := testPrimitive("1", "tool", "Awl", "awl", "tools/awl/manifest.json")
	p.Description = "Pierces holes in vegetable-tanned leather"
	_ = idx.UpsertFull(ctx, p, nil)

	if err := idx.RebuildFTS(ctx); err != nil {
		t.Fatalf("RebuildFTS: %v", err)
	}

	results, err := idx.Search(ctx, "vegetable")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].ID != "1" {
		t.Errorf("expected to find awl by description, got %+v", results)
	}
}

func TestSearch_FindsByTags(t *testing.T) {
	idx := openMemory(t)
	ctx := context.Background()

	p := testPrimitive("1", "tool", "Awl", "awl", "tools/awl/manifest.json")
	p.Tags = json.RawMessage(`["leather","piercing","hand-tool"]`)
	_ = idx.UpsertFull(ctx, p, nil)

	if err := idx.RebuildFTS(ctx); err != nil {
		t.Fatalf("RebuildFTS: %v", err)
	}

	results, err := idx.Search(ctx, "piercing")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result for 'piercing', got %d", len(results))
	}
}

// — IndexManifest ————————————————————————————————————————————————————————————

func TestIndexManifest_FullPipeline(t *testing.T) {
	idx := openMemory(t)
	ctx := context.Background()

	pm := &gitpkg.ParsedManifest{
		ID:          "tec-001",
		Type:        "technique",
		Name:        "Saddle Stitching",
		Slug:        "saddle-stitching",
		Path:        "techniques/saddle-stitching/manifest.json",
		Created:     "2026-01-01T00:00:00Z",
		Modified:    "2026-01-01T00:00:00Z",
		Description: "Hand stitching with two needles",
		Tags:        []string{"leather", "stitching"},
		Relationships: []gitpkg.Relationship{
			{Type: "uses_tool", Target: "tools/stitching-chisel/manifest.json"},
		},
		Raw: json.RawMessage(`{"id":"tec-001","type":"technique","name":"Saddle Stitching","slug":"saddle-stitching"}`),
	}

	if err := idx.IndexManifest(ctx, pm); err != nil {
		t.Fatalf("IndexManifest: %v", err)
	}

	got, err := idx.Get(ctx, pm.Path)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.Description != pm.Description {
		t.Errorf("Description: got %q", got.Description)
	}

	rels, err := idx.RelationshipsFor(ctx, pm.Path)
	if err != nil {
		t.Fatalf("RelationshipsFor: %v", err)
	}
	if len(rels) != 1 || rels[0].RelType != "uses_tool" {
		t.Errorf("expected 1 uses_tool relationship, got %+v", rels)
	}
}
