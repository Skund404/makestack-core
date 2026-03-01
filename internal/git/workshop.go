// Package git — this file provides workshop reading.
// Workshops live at workshops/{slug}/workshop.json and are read-only
// at startup; they are not tracked by the file watcher.
package git

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
)

// Workshop holds the parsed content of a workshop.json file.
// A workshop is an organisational lens over existing primitives — it
// references them by path without owning them.
type Workshop struct {
	ID          string
	Slug        string
	Name        string
	Description string
	Created     string
	Modified    string
	// Primitives is the list of primitive paths that belong to this workshop,
	// e.g. "tools/stitching-chisel/manifest.json".
	Primitives []string
	// Path is the workshop.json path relative to the data directory root.
	Path string
}

// workshopJSON mirrors the on-disk JSON structure of a workshop.json file.
type workshopJSON struct {
	ID          string   `json:"id"`
	Slug        string   `json:"slug"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Created     string   `json:"created"`
	Modified    string   `json:"modified"`
	Primitives  []string `json:"primitives"`
}

// ReadWorkshops walks dataDir and parses every workshops/*/workshop.json it
// finds. Malformed or unreadable files are skipped with a warning so a single
// bad workshop never blocks the rest.
func ReadWorkshops(dataDir string) ([]Workshop, error) {
	var workshops []Workshop

	workshopsDir := filepath.Join(dataDir, "workshops")
	if _, err := os.Stat(workshopsDir); os.IsNotExist(err) {
		return nil, nil // no workshops directory — that's fine
	}

	err := filepath.WalkDir(workshopsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if d.IsDir() || d.Name() != "workshop.json" {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			log.Printf("git: read workshop %s: %v", path, err)
			return nil
		}

		var raw workshopJSON
		if err := json.Unmarshal(data, &raw); err != nil {
			log.Printf("git: parse workshop %s: %v", path, err)
			return nil
		}

		if raw.Slug == "" {
			log.Printf("git: skipping workshop %s: missing slug", path)
			return nil
		}

		rel, err := filepath.Rel(dataDir, path)
		if err != nil {
			return fmt.Errorf("rel path for %s: %w", path, err)
		}

		workshops = append(workshops, Workshop{
			ID:          raw.ID,
			Slug:        raw.Slug,
			Name:        raw.Name,
			Description: raw.Description,
			Created:     raw.Created,
			Modified:    raw.Modified,
			Primitives:  raw.Primitives,
			Path:        filepath.ToSlash(rel),
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk workshops dir: %w", err)
	}

	return workshops, nil
}
