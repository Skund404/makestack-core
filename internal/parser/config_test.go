package parser

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig_HasAllSixTypes(t *testing.T) {
	cfg := DefaultConfig()
	want := map[string]string{
		"tools":      "tool",
		"materials":  "material",
		"techniques": "technique",
		"workflows":  "workflow",
		"projects":   "project",
		"events":     "event",
	}
	for dir, typ := range want {
		if got := cfg.Index.Directories[dir]; got != typ {
			t.Errorf("Directories[%q] = %q, want %q", dir, got, typ)
		}
	}
	if cfg.ManifestFile() != "manifest.json" {
		t.Errorf("ManifestFile() = %q, want %q", cfg.ManifestFile(), "manifest.json")
	}
}

func TestLoadConfig_MissingFile_ReturnsDefault(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	def := DefaultConfig()
	if len(cfg.Index.Directories) != len(def.Index.Directories) {
		t.Errorf("directories count: got %d, want %d",
			len(cfg.Index.Directories), len(def.Index.Directories))
	}
}

func TestLoadConfig_ValidFile_ParsedCorrectly(t *testing.T) {
	raw := `{
		"version": "1",
		"index": {
			"directories": {"products": "material", "tools": "tool"},
			"manifest_filename": "item.json"
		}
	}`
	path := filepath.Join(t.TempDir(), "makestack-parser.json")
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Index.Directories["products"] != "material" {
		t.Errorf("expected products→material, got %q", cfg.Index.Directories["products"])
	}
	if cfg.ManifestFile() != "item.json" {
		t.Errorf("ManifestFile() = %q, want %q", cfg.ManifestFile(), "item.json")
	}
}

func TestLoadConfig_EmptyDirectories_FallsBackToDefault(t *testing.T) {
	// A config with an empty directories map should get the default directories.
	raw := `{"version":"1","index":{}}`
	path := filepath.Join(t.TempDir(), "makestack-parser.json")
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	def := DefaultConfig()
	if len(cfg.Index.Directories) != len(def.Index.Directories) {
		t.Errorf("expected default directories, got %v", cfg.Index.Directories)
	}
	if cfg.ManifestFile() != "manifest.json" {
		t.Errorf("manifest file should default to manifest.json, got %q", cfg.ManifestFile())
	}
}

func TestLoadConfig_RenderSectionPreservedVerbatim(t *testing.T) {
	raw := `{
		"version": "1",
		"index": {"directories": {"tools": "tool"}},
		"render": {"labels": {"tool": "Tool"}}
	}`
	path := filepath.Join(t.TempDir(), "makestack-parser.json")
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	// Render must be present and parseable as a JSON object.
	var render map[string]json.RawMessage
	if err := json.Unmarshal(cfg.Render, &render); err != nil {
		t.Fatalf("Render is not valid JSON object: %v", err)
	}
	if _, ok := render["labels"]; !ok {
		t.Error("Render missing 'labels' key")
	}
}

func TestLoadConfig_BadJSON_ReturnsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "makestack-parser.json")
	if err := os.WriteFile(path, []byte(`{bad json`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Error("expected error for bad JSON, got nil")
	}
}
