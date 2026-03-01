package git

import (
	"context"
	"encoding/json"
	"testing"
)

// fixturesDir points at the shared test fixtures relative to this file.
// Go test runs with the package directory as CWD, so we walk up two levels.
const fixturesDir = "../../test/fixtures"

// — ReadAll tests —————————————————————————————————————————————————————————————

func TestReadAll_FindsManifests(t *testing.T) {
	r, err := NewReader(fixturesDir)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}

	manifests, err := r.ReadAll(context.Background())
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	// test/fixtures contains: tools/stitching-chisel, materials/veg-tan-leather,
	// techniques/saddle-stitching, workflows/bifold-wallet,
	// projects/zombie-wallet-v1, projects/zombie-wallet-v1-card-pocket,
	// events/leather-class-march-2026 — that is 7 manifest.json files.
	if len(manifests) < 1 {
		t.Fatalf("expected at least 1 manifest, got 0")
	}

	// Every entry must have a non-empty path and non-nil Raw.
	for _, m := range manifests {
		if m.Path == "" {
			t.Error("manifest has empty Path")
		}
		if len(m.Raw) == 0 {
			t.Errorf("manifest %s has empty Raw", m.Path)
		}
	}
}

func TestNewReader_RejectsNonExistentDir(t *testing.T) {
	_, err := NewReader("/this/does/not/exist")
	if err == nil {
		t.Fatal("expected error for non-existent directory, got nil")
	}
}

// — Parse tests ———————————————————————————————————————————————————————————————

func TestParse_ValidManifest(t *testing.T) {
	raw := json.RawMessage(`{
		"id":       "test-001",
		"type":     "tool",
		"name":     "Test Tool",
		"slug":     "test-tool",
		"created":  "2026-01-01T00:00:00Z",
		"modified": "2026-01-01T00:00:00Z",
		"description": "A test tool",
		"tags": ["a", "b"],
		"relationships": [
			{"type":"uses_material","target":"materials/foo/manifest.json"}
		]
	}`)

	m := Manifest{Path: "tools/test-tool/manifest.json", Raw: raw}
	pm, err := m.Parse()
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if pm.ID != "test-001" {
		t.Errorf("ID: got %q, want %q", pm.ID, "test-001")
	}
	if pm.Type != "tool" {
		t.Errorf("Type: got %q, want %q", pm.Type, "tool")
	}
	if pm.Name != "Test Tool" {
		t.Errorf("Name: got %q, want %q", pm.Name, "Test Tool")
	}
	if pm.Slug != "test-tool" {
		t.Errorf("Slug: got %q, want %q", pm.Slug, "test-tool")
	}
	if pm.Description != "A test tool" {
		t.Errorf("Description: got %q", pm.Description)
	}
	if len(pm.Tags) != 2 {
		t.Errorf("Tags: got %d, want 2", len(pm.Tags))
	}
	if len(pm.Relationships) != 1 {
		t.Errorf("Relationships: got %d, want 1", len(pm.Relationships))
	} else {
		rel := pm.Relationships[0]
		if rel.Type != "uses_material" {
			t.Errorf("Rel.Type: got %q", rel.Type)
		}
		if rel.Target != "materials/foo/manifest.json" {
			t.Errorf("Rel.Target: got %q", rel.Target)
		}
	}
}

func TestParse_MissingRequiredFields(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"missing id",   `{"type":"tool","name":"X","slug":"x"}`},
		{"missing type", `{"id":"1","name":"X","slug":"x"}`},
		{"missing name", `{"id":"1","type":"tool","slug":"x"}`},
		{"missing slug", `{"id":"1","type":"tool","name":"X"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := Manifest{Path: "tools/x/manifest.json", Raw: json.RawMessage(tc.raw)}
			_, err := m.Parse()
			if err == nil {
				t.Errorf("expected error for %s, got nil", tc.name)
			}
		})
	}
}

func TestParse_InvalidJSON(t *testing.T) {
	m := Manifest{Path: "tools/x/manifest.json", Raw: json.RawMessage(`not json`)}
	_, err := m.Parse()
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

// TestParse_AllFixtures ensures every fixture parses without error.
func TestParse_AllFixtures(t *testing.T) {
	r, err := NewReader(fixturesDir)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}

	manifests, err := r.ReadAll(context.Background())
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	for _, m := range manifests {
		t.Run(m.Path, func(t *testing.T) {
			pm, err := m.Parse()
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			// Sanity: path echoed back correctly.
			if pm.Path != m.Path {
				t.Errorf("Path: got %q, want %q", pm.Path, m.Path)
			}
		})
	}
}
