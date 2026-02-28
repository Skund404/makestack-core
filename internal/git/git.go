// Package git provides operations for reading manifest files from a
// makestack data repository managed by Git.
package git

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Manifest represents a parsed makestack primitive manifest.json file.
type Manifest struct {
	// Path is the file path relative to the data repository root.
	Path string
	// Raw is the raw JSON content of the manifest.
	Raw json.RawMessage
}

// Reader walks a makestack data directory and reads manifest files.
type Reader struct {
	dataDir string
}

// NewReader creates a Reader for the given data directory.
func NewReader(dataDir string) (*Reader, error) {
	info, err := os.Stat(dataDir)
	if err != nil {
		return nil, fmt.Errorf("data dir: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("data dir %q is not a directory", dataDir)
	}
	return &Reader{dataDir: dataDir}, nil
}

// ReadAll walks the data directory and returns all manifest.json files found.
func (r *Reader) ReadAll(ctx context.Context) ([]Manifest, error) {
	var manifests []Manifest

	err := filepath.WalkDir(r.dataDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() || d.Name() != "manifest.json" {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}

		rel, err := filepath.Rel(r.dataDir, path)
		if err != nil {
			return fmt.Errorf("rel path for %s: %w", path, err)
		}

		manifests = append(manifests, Manifest{
			Path: rel,
			Raw:  json.RawMessage(data),
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk data dir: %w", err)
	}

	return manifests, nil
}
