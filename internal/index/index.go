// Package index manages the SQLite read index for makestack primitives.
// The index is fully rebuildable from the Git data repository at any time.
package index

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	gitpkg "github.com/makestack/makestack-core/internal/git"
	_ "modernc.org/sqlite" // register the sqlite driver (pure Go, no CGO)
)

// schemaStmts are executed in order on Open to create tables and indexes.
// Splitting into individual statements avoids driver multi-statement quirks.
var schemaStmts = []string{
	`CREATE TABLE IF NOT EXISTS primitives (
		id             TEXT PRIMARY KEY,
		type           TEXT NOT NULL,
		name           TEXT NOT NULL,
		slug           TEXT NOT NULL,
		path           TEXT NOT NULL UNIQUE,
		created        TEXT NOT NULL,
		modified       TEXT NOT NULL,
		description    TEXT,
		tags           TEXT,
		cloned_from    TEXT,
		properties     TEXT,
		parent_project TEXT,
		manifest       TEXT NOT NULL
	)`,

	`CREATE TABLE IF NOT EXISTS relationships (
		id                INTEGER PRIMARY KEY AUTOINCREMENT,
		source_path       TEXT NOT NULL,
		source_type       TEXT NOT NULL,
		relationship_type TEXT NOT NULL,
		target_path       TEXT NOT NULL,
		target_type       TEXT,
		metadata          TEXT,
		FOREIGN KEY (source_path) REFERENCES primitives(path)
	)`,

	// Content table FTS — actual content lives in primitives; the FTS index
	// stores only the search tokens. Rebuild with the 'rebuild' command.
	`CREATE VIRTUAL TABLE IF NOT EXISTS primitives_fts USING fts5(
		name, description, tags, properties,
		content=primitives, content_rowid=rowid
	)`,

	`CREATE INDEX IF NOT EXISTS idx_primitives_type      ON primitives(type)`,
	`CREATE INDEX IF NOT EXISTS idx_primitives_parent    ON primitives(parent_project)`,
	`CREATE INDEX IF NOT EXISTS idx_relationships_source ON relationships(source_path)`,
	`CREATE INDEX IF NOT EXISTS idx_relationships_target ON relationships(target_path)`,
	`CREATE INDEX IF NOT EXISTS idx_relationships_type   ON relationships(relationship_type)`,
}

// Primitive holds all indexed fields for a single makestack primitive.
// Tags and Properties are stored as raw JSON so callers receive them
// ready to re-encode into API responses without an extra round-trip.
type Primitive struct {
	ID            string
	Type          string
	Name          string
	Slug          string
	Path          string
	Created       string
	Modified      string
	Description   string
	Tags          json.RawMessage // JSON array, e.g. ["leather","hand-tool"]
	ClonedFrom    string
	Properties    json.RawMessage // JSON object or null
	ParentProject string
	Manifest      json.RawMessage // complete original manifest
}

// Relationship holds a single row from the relationships table.
type Relationship struct {
	SourcePath string
	SourceType string
	RelType    string // e.g. "uses_tool", "uses_material"
	TargetPath string
	TargetType string          // may be empty if not yet resolved
	Metadata   json.RawMessage // optional extra data
}

// Index is a SQLite-backed read index of makestack primitives.
type Index struct {
	db *sql.DB
}

// Open opens (or creates) a SQLite index at the given path.
// Use ":memory:" for an ephemeral in-memory index.
func Open(path string) (*Index, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", path, err)
	}

	for _, stmt := range schemaStmts {
		if _, err := db.Exec(stmt); err != nil {
			db.Close()
			return nil, fmt.Errorf("apply schema: %w\nstatement: %s", err, stmt)
		}
	}

	return &Index{db: db}, nil
}

// Close closes the underlying database connection.
func (idx *Index) Close() error {
	return idx.db.Close()
}

// UpsertFull inserts or updates a primitive together with its relationships
// inside a single transaction. Any existing relationships for source_path are
// deleted first so the stored set always reflects the current manifest exactly.
func (idx *Index) UpsertFull(ctx context.Context, p Primitive, rels []Relationship) error {
	tx, err := idx.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// Normalise JSON fields so we never store empty []byte as SQL NULL.
	tagsStr := "[]"
	if len(p.Tags) > 0 {
		tagsStr = string(p.Tags)
	}
	propsStr := "null"
	if len(p.Properties) > 0 {
		propsStr = string(p.Properties)
	}

	_, err = tx.ExecContext(ctx, `
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
		p.Description, tagsStr, p.ClonedFrom, propsStr, p.ParentProject,
		string(p.Manifest),
	)
	if err != nil {
		return fmt.Errorf("upsert primitive %s: %w", p.Path, err)
	}

	// Replace all relationships declared by this source.
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM relationships WHERE source_path = ?`, p.Path,
	); err != nil {
		return fmt.Errorf("delete old relationships for %s: %w", p.Path, err)
	}

	for _, rel := range rels {
		metaStr := "null"
		if len(rel.Metadata) > 0 {
			metaStr = string(rel.Metadata)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO relationships
				(source_path, source_type, relationship_type, target_path, target_type, metadata)
			VALUES (?, ?, ?, ?, ?, ?)`,
			p.Path, p.Type, rel.RelType, rel.TargetPath, rel.TargetType, metaStr,
		); err != nil {
			return fmt.Errorf("insert relationship %s->%s: %w", p.Path, rel.TargetPath, err)
		}
	}

	return tx.Commit()
}

// Delete removes a primitive and all relationships where it is source or target.
func (idx *Index) Delete(ctx context.Context, path string) error {
	tx, err := idx.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM relationships WHERE source_path = ? OR target_path = ?`, path, path,
	); err != nil {
		return fmt.Errorf("delete relationships for %s: %w", path, err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM primitives WHERE path = ?`, path,
	); err != nil {
		return fmt.Errorf("delete primitive %s: %w", path, err)
	}

	return tx.Commit()
}

// RebuildFTS repopulates the full-text search index from the primitives table.
// Call this after bulk inserts for best performance.
func (idx *Index) RebuildFTS(ctx context.Context) error {
	_, err := idx.db.ExecContext(ctx,
		`INSERT INTO primitives_fts(primitives_fts) VALUES('rebuild')`)
	return err
}

// List returns primitives ordered by type then name.
// typeFilter is optional — pass "" to return all types.
func (idx *Index) List(ctx context.Context, typeFilter string) ([]Primitive, error) {
	const cols = `id, type, name, slug, path, created, modified,
	              description, tags, cloned_from, properties, parent_project, manifest`

	var (
		rows *sql.Rows
		err  error
	)

	if typeFilter == "" {
		rows, err = idx.db.QueryContext(ctx,
			`SELECT `+cols+` FROM primitives ORDER BY type, name`)
	} else {
		rows, err = idx.db.QueryContext(ctx,
			`SELECT `+cols+` FROM primitives WHERE type = ? ORDER BY name`,
			typeFilter)
	}

	if err != nil {
		return nil, fmt.Errorf("list primitives: %w", err)
	}
	defer rows.Close()

	return scanPrimitives(rows)
}

// Get returns the primitive with the given path, or nil if it does not exist.
func (idx *Index) Get(ctx context.Context, path string) (*Primitive, error) {
	row := idx.db.QueryRowContext(ctx, `
		SELECT id, type, name, slug, path, created, modified,
		       description, tags, cloned_from, properties, parent_project, manifest
		FROM primitives
		WHERE path = ?`,
		path)

	p, err := scanPrimitive(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return p, err
}

// Search performs a full-text search across name, description, tags, and
// properties using FTS5. Returns matching primitives ordered by name.
func (idx *Index) Search(ctx context.Context, query string) ([]Primitive, error) {
	rows, err := idx.db.QueryContext(ctx, `
		SELECT p.id, p.type, p.name, p.slug, p.path, p.created, p.modified,
		       p.description, p.tags, p.cloned_from, p.properties, p.parent_project, p.manifest
		FROM primitives p
		WHERE p.rowid IN (
			SELECT rowid FROM primitives_fts WHERE primitives_fts MATCH ?
		)
		ORDER BY p.name`,
		query)
	if err != nil {
		return nil, fmt.Errorf("fts search %q: %w", query, err)
	}
	defer rows.Close()

	return scanPrimitives(rows)
}

// RelationshipsFor returns all relationships where path appears as either
// source or target, giving a full picture of what a primitive connects to and
// what connects to it.
func (idx *Index) RelationshipsFor(ctx context.Context, path string) ([]Relationship, error) {
	rows, err := idx.db.QueryContext(ctx, `
		SELECT source_path, source_type, relationship_type, target_path, target_type, metadata
		FROM relationships
		WHERE source_path = ? OR target_path = ?
		ORDER BY relationship_type, target_path`,
		path, path)
	if err != nil {
		return nil, fmt.Errorf("query relationships for %s: %w", path, err)
	}
	defer rows.Close()

	return scanRelationships(rows)
}

// — manifest conversion ———————————————————————————————————————————————————————

// IndexManifest converts a git.ParsedManifest to a Primitive and its
// Relationships and calls UpsertFull atomically. It is the single shared
// conversion used by both the initial bulk load and the file watcher.
func (idx *Index) IndexManifest(ctx context.Context, pm *gitpkg.ParsedManifest) error {
	return idx.UpsertFull(ctx, primitiveFrom(pm), relationshipsFrom(pm))
}

// primitiveFrom maps the typed fields of a ParsedManifest to an index Primitive.
func primitiveFrom(pm *gitpkg.ParsedManifest) Primitive {
	p := Primitive{
		ID:            pm.ID,
		Type:          pm.Type,
		Name:          pm.Name,
		Slug:          pm.Slug,
		Path:          pm.Path,
		Created:       pm.Created,
		Modified:      pm.Modified,
		Description:   pm.Description,
		ClonedFrom:    pm.ClonedFrom,
		ParentProject: pm.ParentProject,
		Properties:    pm.Properties,
		Manifest:      pm.Raw,
	}
	if len(pm.Tags) > 0 {
		if b, err := json.Marshal(pm.Tags); err == nil {
			p.Tags = json.RawMessage(b)
		}
	} else {
		p.Tags = json.RawMessage("[]")
	}
	return p
}

// relationshipsFrom flattens the relationships embedded in a ParsedManifest
// into the index.Relationship rows the indexer stores.
func relationshipsFrom(pm *gitpkg.ParsedManifest) []Relationship {
	if len(pm.Relationships) == 0 {
		return nil
	}
	rels := make([]Relationship, len(pm.Relationships))
	for i, r := range pm.Relationships {
		rels[i] = Relationship{
			SourcePath: pm.Path,
			SourceType: pm.Type,
			RelType:    r.Type,
			TargetPath: r.Target,
			Metadata:   r.Metadata,
		}
	}
	return rels
}

// — scan helpers ——————————————————————————————————————————————————————————————

func scanPrimitives(rows *sql.Rows) ([]Primitive, error) {
	var result []Primitive
	for rows.Next() {
		p, err := scanPrimitive(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *p)
	}
	return result, rows.Err()
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows so the same scan
// logic can serve QueryRow and Query results.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanPrimitive(s rowScanner) (*Primitive, error) {
	var p Primitive
	var tags, props, manifest string
	err := s.Scan(
		&p.ID, &p.Type, &p.Name, &p.Slug, &p.Path,
		&p.Created, &p.Modified, &p.Description,
		&tags, &p.ClonedFrom, &props, &p.ParentProject, &manifest,
	)
	if err != nil {
		return nil, err
	}
	p.Tags = json.RawMessage(tags)
	p.Properties = json.RawMessage(props)
	p.Manifest = json.RawMessage(manifest)
	return &p, nil
}

func scanRelationships(rows *sql.Rows) ([]Relationship, error) {
	var result []Relationship
	for rows.Next() {
		var rel Relationship
		var meta string
		if err := rows.Scan(
			&rel.SourcePath, &rel.SourceType, &rel.RelType,
			&rel.TargetPath, &rel.TargetType, &meta,
		); err != nil {
			return nil, err
		}
		rel.Metadata = json.RawMessage(meta)
		result = append(result, rel)
	}
	return result, rows.Err()
}

// CountAll returns the total number of indexed primitives. Useful for startup logging.
func (idx *Index) CountAll(ctx context.Context) (int, error) {
	var n int
	err := idx.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM primitives`).Scan(&n)
	return n, err
}
