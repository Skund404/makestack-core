// Package parser defines the parser configuration that tells Core how to
// index a repository and tells the Shell (opaquely) how to render its
// primitives. Core reads the index section; the render section is stored
// verbatim and served to the Shell without interpretation.
package parser

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// Config is the full parser configuration for a repository.
type Config struct {
	Version string          `json:"version,omitempty"`
	Index   IndexConfig     `json:"index"`
	// Render is stored verbatim by Core and served to the Shell via
	// GET /api/parser-config/{slug}. Core does not interpret its contents.
	Render json.RawMessage `json:"render,omitempty"`
}

// IndexConfig controls which directories Core walks and what manifest
// filename it looks for within each subdirectory.
type IndexConfig struct {
	// Directories maps directory names to primitive type strings.
	// e.g. {"tools": "tool", "products": "material"}
	Directories map[string]string `json:"directories"`
	// ManifestFilename is the filename Core looks for in each leaf directory.
	// Defaults to "manifest.json" when absent or empty.
	ManifestFilename string `json:"manifest_filename,omitempty"`
}

// ManifestFile returns the configured manifest filename, defaulting to
// "manifest.json" when the field is absent or empty.
func (c *Config) ManifestFile() string {
	if c.Index.ManifestFilename != "" {
		return c.Index.ManifestFilename
	}
	return "manifest.json"
}

// DefaultConfig returns the standard Makestack conventions expressed as a
// Config value. Used for any repository that does not supply a
// makestack-parser.json, and as the merge base when a partial config is loaded.
func DefaultConfig() *Config {
	return &Config{
		Version: "1",
		Index: IndexConfig{
			Directories: map[string]string{
				"tools":      "tool",
				"materials":  "material",
				"techniques": "technique",
				"workflows":  "workflow",
				"projects":   "project",
				"events":     "event",
			},
			ManifestFilename: "manifest.json",
		},
	}
}

// LoadConfig reads a parser config from path. If the file does not exist,
// DefaultConfig is returned with no error. Any other read or parse failure
// is returned as an error. Missing fields are filled with default values.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return DefaultConfig(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read parser config %s: %w", path, err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse parser config %s: %w", path, err)
	}

	// Apply defaults for missing fields so callers never see a zero Config.
	if len(cfg.Index.Directories) == 0 {
		cfg.Index.Directories = DefaultConfig().Index.Directories
	}
	if cfg.Index.ManifestFilename == "" {
		cfg.Index.ManifestFilename = "manifest.json"
	}

	return &cfg, nil
}
