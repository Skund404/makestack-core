package federation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// writeFedConfig writes a federation.json under dir/.makestack/ and returns
// the dir path. Creates the .makestack subdirectory automatically.
func writeFedConfig(t *testing.T, dir string, v any) {
	t.Helper()
	msDir := filepath.Join(dir, ".makestack")
	if err := os.MkdirAll(msDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(msDir, "federation.json"), data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func TestLoadConfig_NoFile_ReturnsSingleRootDefault(t *testing.T) {
	dir := t.TempDir()

	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.Roots) != 1 {
		t.Fatalf("expected 1 root, got %d", len(cfg.Roots))
	}
	r := cfg.Roots[0]
	if r.Slug != "primary" {
		t.Errorf("slug: got %q, want %q", r.Slug, "primary")
	}
	if r.Path != dir {
		t.Errorf("path: got %q, want %q", r.Path, dir)
	}
	if r.Trust != TrustPersonal {
		t.Errorf("trust: got %q, want %q", r.Trust, TrustPersonal)
	}
	if !r.Primary {
		t.Error("single-root default must be primary")
	}
}

func TestLoadConfig_ValidMultiRoot(t *testing.T) {
	primaryDir := t.TempDir()
	supplierDir := t.TempDir()

	raw := map[string]any{
		"version": "1",
		"roots": []map[string]any{
			{"slug": "primary", "path": primaryDir, "trust": "personal", "primary": true},
			{"slug": "wickett", "path": supplierDir, "trust": "supplier", "primary": false},
		},
	}
	writeFedConfig(t, primaryDir, raw)

	cfg, err := LoadConfig(primaryDir)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.Roots) != 2 {
		t.Fatalf("expected 2 roots, got %d", len(cfg.Roots))
	}

	p := cfg.Primary()
	if p.Slug != "primary" {
		t.Errorf("Primary().Slug = %q, want %q", p.Slug, "primary")
	}
}

func TestLoadConfig_NoPrimary_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	raw := map[string]any{
		"version": "1",
		"roots": []map[string]any{
			{"slug": "a", "path": dir, "trust": "supplier", "primary": false},
		},
	}
	writeFedConfig(t, dir, raw)

	_, err := LoadConfig(dir)
	if err == nil {
		t.Error("expected error for missing primary root, got nil")
	}
}

func TestLoadConfig_MultiplePrimaries_ReturnsError(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	raw := map[string]any{
		"version": "1",
		"roots": []map[string]any{
			{"slug": "a", "path": dir1, "trust": "personal", "primary": true},
			{"slug": "b", "path": dir2, "trust": "personal", "primary": true},
		},
	}
	writeFedConfig(t, dir1, raw)

	_, err := LoadConfig(dir1)
	if err == nil {
		t.Error("expected error for multiple primaries, got nil")
	}
}

func TestLoadConfig_DuplicateSlugs_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	raw := map[string]any{
		"version": "1",
		"roots": []map[string]any{
			{"slug": "same", "path": dir, "trust": "personal", "primary": true},
			{"slug": "same", "path": dir, "trust": "supplier", "primary": false},
		},
	}
	writeFedConfig(t, dir, raw)

	_, err := LoadConfig(dir)
	if err == nil {
		t.Error("expected error for duplicate slug, got nil")
	}
}

func TestLoadConfig_PathDoesNotExist_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	raw := map[string]any{
		"version": "1",
		"roots": []map[string]any{
			{"slug": "primary", "path": dir, "trust": "personal", "primary": true},
			{"slug": "ghost", "path": "/nonexistent/path/xyz", "trust": "supplier", "primary": false},
		},
	}
	writeFedConfig(t, dir, raw)

	_, err := LoadConfig(dir)
	if err == nil {
		t.Error("expected error for nonexistent path, got nil")
	}
}

func TestRoot_ParserConfigFile_DefaultsToMakestackParser(t *testing.T) {
	r := Root{Slug: "primary"}
	if got := r.ParserConfigFile(); got != "makestack-parser.json" {
		t.Errorf("ParserConfigFile() = %q, want %q", got, "makestack-parser.json")
	}
}

func TestRoot_ParserConfigFile_CustomValue(t *testing.T) {
	r := Root{Slug: "supplier", ParserConfig: "custom-parser.json"}
	if got := r.ParserConfigFile(); got != "custom-parser.json" {
		t.Errorf("ParserConfigFile() = %q, want %q", got, "custom-parser.json")
	}
}

func TestLoadConfig_BadJSON_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	msDir := filepath.Join(dir, ".makestack")
	if err := os.MkdirAll(msDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(msDir, "federation.json"), []byte(`{bad`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := LoadConfig(dir); err == nil {
		t.Error("expected error for bad JSON, got nil")
	}
}
