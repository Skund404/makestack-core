// Package federation manages the multi-root configuration for makestack-core.
// It defines the Root and Config types, handles loading from disk, and
// enforces federation invariants (unique slugs, exactly one primary root,
// all paths must exist on disk).
package federation

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// TrustLevel describes the trust relationship between Core and a root.
type TrustLevel string

const (
	// TrustPersonal is the primary read-write root owned by the user.
	TrustPersonal TrustLevel = "personal"
	// TrustCommunity is a trusted commons node (read-only).
	TrustCommunity TrustLevel = "community"
	// TrustSupplier is a supplier catalogue (read-only).
	TrustSupplier TrustLevel = "supplier"
	// TrustPeer is another user's personal repo (read-only).
	TrustPeer TrustLevel = "peer"
)

// Root describes a single Git data repository managed by Core.
type Root struct {
	// Slug is the unique, lowercase identifier for this root.
	// Primary root conventionally uses "primary".
	Slug string `json:"slug"`
	// Path is the absolute path to the root's data directory on disk.
	Path string `json:"path"`
	// Trust describes the relationship between Core and this root.
	Trust TrustLevel `json:"trust"`
	// Primary marks the one read-write root. Exactly one root must be true.
	Primary bool `json:"primary"`
	// ParserConfig is the filename (relative to Path) of the parser config.
	// Defaults to "makestack-parser.json" when absent.
	ParserConfig string `json:"parser_config,omitempty"`
}

// ParserConfigFile returns the parser config filename relative to this root's
// Path. Falls back to "makestack-parser.json" when not set.
func (r *Root) ParserConfigFile() string {
	if r.ParserConfig != "" {
		return r.ParserConfig
	}
	return "makestack-parser.json"
}

// Config holds the complete federation configuration for a Core instance.
type Config struct {
	Version string `json:"version,omitempty"`
	Roots   []Root `json:"roots"`
}

// Primary returns a pointer to the primary root.
// Panics if no primary root exists — LoadConfig validates this invariant.
func (c *Config) Primary() *Root {
	for i := range c.Roots {
		if c.Roots[i].Primary {
			return &c.Roots[i]
		}
	}
	panic("federation: no primary root (invariant violation — LoadConfig should have caught this)")
}

// configFile is the location of the federation config within the primary data
// directory, relative to that directory's root.
const configFile = ".makestack/federation.json"

// LoadConfig reads the federation config from .makestack/federation.json
// inside dataDir. If that file does not exist, a single-root default config
// is returned (backwards-compatible single-root mode). Any other read or
// parse error, or a validation failure, is returned as an error.
func LoadConfig(dataDir string) (*Config, error) {
	path := filepath.Join(dataDir, configFile)

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return singleRootDefault(dataDir), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read federation config %s: %w", path, err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse federation config %s: %w", path, err)
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("federation config %s: %w", path, err)
	}

	return &cfg, nil
}

// singleRootDefault returns a Config with one personal root at dataDir.
// This is the v0.1 backwards-compatible default when no federation.json exists.
func singleRootDefault(dataDir string) *Config {
	return &Config{
		Version: "1",
		Roots: []Root{
			{
				Slug:    "primary",
				Path:    dataDir,
				Trust:   TrustPersonal,
				Primary: true,
			},
		},
	}
}

// validate checks all federation invariants and returns the first error found.
func (c *Config) validate() error {
	if len(c.Roots) == 0 {
		return fmt.Errorf("roots list is empty; at least one root is required")
	}

	slugs := make(map[string]bool, len(c.Roots))
	primaryCount := 0

	for _, r := range c.Roots {
		if r.Slug == "" {
			return fmt.Errorf("root has empty slug")
		}
		if slugs[r.Slug] {
			return fmt.Errorf("duplicate root slug: %q", r.Slug)
		}
		slugs[r.Slug] = true

		if r.Primary {
			primaryCount++
		}

		if _, err := os.Stat(r.Path); err != nil {
			return fmt.Errorf("root %q: path %q: %w", r.Slug, r.Path, err)
		}
	}

	switch primaryCount {
	case 0:
		return fmt.Errorf(`no primary root defined; exactly one root must have "primary": true`)
	case 1:
		// valid
	default:
		return fmt.Errorf(`%d roots marked primary; exactly one root must have "primary": true`, primaryCount)
	}

	return nil
}
