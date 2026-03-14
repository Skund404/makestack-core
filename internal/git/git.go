// Package git provides operations for reading manifest files from a
// makestack data repository.
package git

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Relationship represents a directed link declared inside a manifest,
// pointing from the owning primitive to another primitive.
type Relationship struct {
	// Type describes the nature of the link (e.g. "uses_tool", "uses_material").
	Type string `json:"type"`
	// Target is the relative path of the target manifest within the data repo
	// (e.g. "tools/stitching-chisel/manifest.json").
	Target string `json:"target"`
	// Metadata holds optional extra data about the link (e.g. {"quantity":"2 sq ft"}).
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

// ParsedManifest holds all typed fields extracted from a manifest.json file.
// It is the primary type passed to the index for storage.
type ParsedManifest struct {
	// Required identity fields.
	ID   string
	Type string
	Name string
	Slug string

	// Path is the manifest file path relative to the data repository root
	// (e.g. "tools/stitching-chisel/manifest.json").
	Path string

	// Optional descriptive fields.
	Created       string
	Modified      string
	Description   string
	Tags          []string
	Properties    json.RawMessage
	ClonedFrom    string
	ParentProject string

	// Optional Primitives Evolution fields (Core-1, additive).
	Domain     string // domain pack affiliation (any string, not enforced)
	Unit       string // unit of measure for material primitives (any string)
	Subtype    string // material subtype: consumable, component, product, organism
	OccurredAt string // ISO8601 timestamp for event primitives
	Status     string // lifecycle status for project primitives

	// Relationships declared in this manifest.
	Relationships []Relationship

	// Raw is the complete, unmodified JSON content of the manifest file.
	Raw json.RawMessage
}

// rawManifest mirrors the JSON structure of a manifest file for unmarshaling.
// It is unexported — callers use ParsedManifest.
type rawManifest struct {
	ID            string          `json:"id"`
	Type          string          `json:"type"`
	Name          string          `json:"name"`
	Slug          string          `json:"slug"`
	Created       string          `json:"created"`
	Modified      string          `json:"modified"`
	Description   string          `json:"description"`
	Tags          []string        `json:"tags"`
	Properties    json.RawMessage `json:"properties"`
	ClonedFrom    string          `json:"cloned_from"`
	ParentProject string          `json:"parent_project"`
	Relationships []Relationship  `json:"relationships"`
	// Primitives Evolution fields (Core-1, additive).
	Domain     string `json:"domain,omitempty"`
	Unit       string `json:"unit,omitempty"`
	Subtype    string `json:"subtype,omitempty"`
	OccurredAt string `json:"occurred_at,omitempty"`
	Status     string `json:"status,omitempty"`
}

// Manifest is a raw (unparsed) manifest read from disk.
type Manifest struct {
	// Path is the file path relative to the data repository root.
	Path string
	// Raw is the raw JSON content of the manifest.
	Raw json.RawMessage
}

// Parse extracts and validates the typed fields from the raw manifest JSON.
// Returns an error if any required field (id, type, name, slug) is missing
// or if the JSON cannot be decoded.
func (m Manifest) Parse() (*ParsedManifest, error) {
	var raw rawManifest
	if err := json.Unmarshal(m.Raw, &raw); err != nil {
		return nil, fmt.Errorf("parse manifest at %s: %w", m.Path, err)
	}

	// Validate required fields.
	if raw.ID == "" {
		return nil, fmt.Errorf("manifest at %s: missing required field 'id'", m.Path)
	}
	if raw.Type == "" {
		return nil, fmt.Errorf("manifest at %s: missing required field 'type'", m.Path)
	}
	if raw.Name == "" {
		return nil, fmt.Errorf("manifest at %s: missing required field 'name'", m.Path)
	}
	if raw.Slug == "" {
		return nil, fmt.Errorf("manifest at %s: missing required field 'slug'", m.Path)
	}

	return &ParsedManifest{
		ID:            raw.ID,
		Type:          raw.Type,
		Name:          raw.Name,
		Slug:          raw.Slug,
		Path:          m.Path,
		Created:       raw.Created,
		Modified:      raw.Modified,
		Description:   raw.Description,
		Tags:          raw.Tags,
		Properties:    raw.Properties,
		ClonedFrom:    raw.ClonedFrom,
		ParentProject: raw.ParentProject,
		Relationships: raw.Relationships,
		Domain:        raw.Domain,
		Unit:          raw.Unit,
		Subtype:       raw.Subtype,
		OccurredAt:    raw.OccurredAt,
		Status:        raw.Status,
		Raw:           m.Raw,
	}, nil
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
// Files that cannot be read are returned as errors immediately (not skipped).
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
