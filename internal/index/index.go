// Package index manages the SQLite read index for makestack primitives.
// The index is fully rebuildable from the Git data repository at any time.
package index

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	_ "modernc.org/sqlite" // SQLite driver (pure Go, no CGO)
)

const schema = `
CREATE TABLE IF NOT EXISTS primitives (
    id          TEXT PRIMARY KEY,
    type        TEXT NOT NULL,
    name        TEXT NOT NULL,
    slug        TEXT NOT NULL,
    path        TEXT NOT NULL UNIQUE,
    created     TEXT NOT NULL,
    modified    TEXT NOT NULL,
    description TEXT,
    tags        TEXT,
    cloned_from TEXT,
    properties  TEXT,
    parent_project TEXT,
    manifest    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS relationships (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    source_path       TEXT NOT NULL,
    source_type       TEXT NOT NULL,
    relationship_type TEXT NOT NULL,
    target_path       TEXT NOT NULL,
    target_type       TEXT,
    metadata          TEXT,
    FOREIGN KEY (source_path) REFERENCES primitives(path)
);

CREATE VIRTUAL TABLE IF NOT EXISTS primitives_fts USING fts5(
    name, description, tags, properties,
    content=primitives, content_rowid=rowid
);

CREATE INDEX IF NOT EXISTS idx_primitives_type   ON primitives(type);
CREATE INDEX IF NOT EXISTS idx_primitives_parent ON primitives(parent_project);
CREATE INDEX IF NOT EXISTS idx_relationships_source ON relationships(source_path);
CREATE INDEX IF NOT EXISTS idx_relationships_target ON relationships(target_path);
CREATE INDEX IF NOT EXISTS idx_relationships_type   ON relationships(relationship_type);
`

// Primitive holds the indexed fields for a single makestack primitive.
type Primitive struct {
	ID            string
	Type          string
	Name          string
	Slug          string
	Path          string
	Created       string
	Modified      string
	Description   string
	Tags          string
	ClonedFrom    string
	Properties    string
	ParentProject string
	Manifest      json.RawMessage
}

// Index is a SQLite-backed read index of makestack primitives.
type Index struct {
	db *sql.DB
}

// Open opens (or creates) a SQLite index at the given path.
// Use ":memory:" for an in-memory index.
func Open(path string) (*Index, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}

	return &Index{db: db}, nil
}

// Close closes the underlying database connection.
func (idx *Index) Close() error {
	return idx.db.Close()
}

// Upsert inserts or replaces a primitive in the index.
func (idx *Index) Upsert(ctx context.Context, p Primitive) error {
	manifest, err := p.Manifest.MarshalJSON()
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}

	_, err = idx.db.ExecContext(ctx, `
		INSERT INTO primitives
			(id, type, name, slug, path, created, modified, description,
			 tags, cloned_from, properties, parent_project, manifest)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			id=excluded.id, type=excluded.type, name=excluded.name,
			slug=excluded.slug, created=excluded.created,
			modified=excluded.modified, description=excluded.description,
			tags=excluded.tags, cloned_from=excluded.cloned_from,
			properties=excluded.properties,
			parent_project=excluded.parent_project,
			manifest=excluded.manifest`,
		p.ID, p.Type, p.Name, p.Slug, p.Path, p.Created, p.Modified,
		p.Description, p.Tags, p.ClonedFrom, p.Properties, p.ParentProject,
		string(manifest),
	)
	return err
}

// Delete removes a primitive (and its relationships) by path.
func (idx *Index) Delete(ctx context.Context, path string) error {
	_, err := idx.db.ExecContext(ctx, `DELETE FROM primitives WHERE path = ?`, path)
	return err
}

// RebuildFTS repopulates the FTS index from the primitives table.
func (idx *Index) RebuildFTS(ctx context.Context) error {
	_, err := idx.db.ExecContext(ctx, `INSERT INTO primitives_fts(primitives_fts) VALUES('rebuild')`)
	return err
}
