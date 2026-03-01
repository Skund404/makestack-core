# CLAUDE.md — Makestack Core

> This file is read by Claude Code at the start of every session.
> It contains project context, current state, and coding instructions.
> Update this file at the end of each session.

---

## Instructions

1. Read this ENTIRE file before doing any work.
2. Check "Current State" and "What's In Progress" before starting.
3. Ask the user what to work on — don't assume.
4. At the END of each session, suggest updates to this file.
5. Never contradict the spec documents without discussing first.
6. If something isn't covered by the specs, ask — don't guess.
7. Write clear, well-commented code. The user relies heavily on AI for development.

---

## Project Overview

Makestack is an open-source, modular project management and ERP toolkit for makers (leatherworkers, cosplayers, woodworkers, 3D printers, cooks, etc.).

**This repo (makestack-core)** is the headless data management engine. It:
- Manages JSON files in a Git repository
- Maintains a SQLite read index (rebuildable from Git)
- Serves data via REST API
- Handles authentication
- Watches for file changes and re-indexes

It does NOT contain business logic, UI, modules, or rendering opinions. Those belong in makestack-app.

**Previous project:** HideSync (leatherwork-specific ERP). Abandoned due to deep technical debt from an encryption layer in SQL that permeated the backend. Makestack is the architecturally clean successor.

**Relationship to Rillmark:** Makestack is the open-source ERP/PM layer without the behavioral intelligence or dual-entity structure. Architecture supports adding those later without rework.

---

## Architecture

Two independent systems communicating via REST:

```
┌─────────────────────────────┐
│   THIS REPO: CORE (Go)     │
│                             │
│   • Git read/write (go-git) │
│   • SQLite index (modernc)  │
│   • JSON schema validation  │
│   • REST API                │
│   • Auth                    │
│   • File watcher            │
└──────────────┬──────────────┘
               │ REST API (JSON over HTTP)
┌──────────────▼──────────────┐
│   SEPARATE REPO: APP LAYER  │
│   (Python + React)          │
│                             │
│   • Business logic          │
│   • UI rendering            │
│   • Module system           │
│   • Keyword processing      │
└─────────────────────────────┘
```

The App Layer CANNOT touch Git or SQLite. Only REST. This boundary is enforced by architecture.

---

## Tech Stack (This Repo)

- **Language:** Go 1.24 (go-git v5.17.0 requires 1.24; installed at `~/go`)
- **Git operations:** go-git v5.17.0 (github.com/go-git/go-git/v5)
- **SQLite:** modernc.org/sqlite v1.29.6 (pure Go, no CGO)
- **HTTP router:** stdlib `net/http` (Go 1.22 method+path patterns) — no chi needed
- **File watcher:** fsnotify v1.9.0
- **JSON validation:** TBD
- **Testing:** Go stdlib + testify if helpful
- **Build:** Single binary, no runtime dependencies

---

## Data Model

Six primitives stored as JSON files in Git:

| Primitive | What It Captures |
|-----------|-----------------|
| Tool | Instruments used to perform work |
| Material | Consumable inputs |
| Technique | Methods and skills |
| Workflow | Ordered sequences of techniques |
| Project | Concrete instances of making (recursive — can contain child projects) |
| Event | Time-bound occurrences within projects |

**Workshops** are organizational scopes (lenses) above primitives. They reference global primitives — they don't contain copies. Removing from a workshop ≠ deleting from Git.

### Directory Structure (Data Repo)

```
makestack-data/
├── .makestack/
│   ├── config.json
│   ├── themes/
│   └── modules/
├── workshops/
│   ├── leatherwork/workshop.json
│   └── cosplay/workshop.json
├── projects/
│   └── {slug}/manifest.json
├── techniques/
│   └── {slug}/manifest.json
├── materials/
│   └── {slug}/manifest.json
├── tools/
│   └── {slug}/manifest.json
├── workflows/
│   └── {slug}/manifest.json
├── events/
│   └── {slug}/manifest.json
└── templates/
```

### SQLite Index Schema

```sql
CREATE TABLE primitives (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL,
    name TEXT NOT NULL,
    slug TEXT NOT NULL,
    path TEXT NOT NULL UNIQUE,
    created TEXT NOT NULL,
    modified TEXT NOT NULL,
    description TEXT,
    tags TEXT,
    cloned_from TEXT,
    properties TEXT,
    parent_project TEXT,
    manifest TEXT NOT NULL
);

CREATE TABLE relationships (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source_path TEXT NOT NULL,
    source_type TEXT NOT NULL,
    relationship_type TEXT NOT NULL,
    target_path TEXT NOT NULL,
    target_type TEXT,
    metadata TEXT,
    FOREIGN KEY (source_path) REFERENCES primitives(path)
);

CREATE VIRTUAL TABLE primitives_fts USING fts5(
    name, description, tags, properties,
    content=primitives, content_rowid=rowid
);

CREATE INDEX idx_primitives_type ON primitives(type);
CREATE INDEX idx_primitives_parent ON primitives(parent_project);
CREATE INDEX idx_relationships_source ON relationships(source_path);
CREATE INDEX idx_relationships_target ON relationships(target_path);
CREATE INDEX idx_relationships_type ON relationships(relationship_type);
```

---

## Code Structure (This Repo)

```
makestack-core/
├── cmd/
│   └── makestack-core/
│       └── main.go              # Entry point
├── internal/
│   ├── git/                     # Git operations (go-git)
│   ├── index/                   # SQLite indexer
│   ├── api/                     # REST API handlers
│   ├── auth/                    # Authentication
│   ├── schema/                  # JSON schema validation
│   └── watcher/                 # File system watcher
├── pkg/                         # Public Go packages (if any)
├── test/
│   └── fixtures/                # Test JSON files (manifests)
├── Dockerfile
├── go.mod
├── go.sum
├── CLAUDE.md                    # This file
├── LICENSE                      # MIT
├── README.md
└── CONTRIBUTING.md
```

---

## Code Standards

- Follow standard Go conventions (`gofmt`, `go vet`)
- Use `internal/` for packages that shouldn't be imported externally
- Error handling: always return errors, never panic in library code
- Comments: exported functions must have doc comments
- Testing: table-driven tests
- Commits: Conventional Commits (`feat:`, `fix:`, `docs:`, etc.)
- Branch: GitHub Flow (main + feature branches)

```go
// Good
func (s *IndexService) RebuildIndex(ctx context.Context) error {
    // ...
}

// Bad
func rebuildIdx() {
    // unexported, no context, no error return
}
```

---

## Specification Documents

The full specs are in the makestack-docs repo. Key documents for Core development:

- **01-ARCHITECTURE.md** — Two-system design, testing strategy
- **02-DATA-MODEL.md** — Six primitives, schemas, relationships, workshops
- **03-JSON-KEYWORD-CONVENTION.md** — Keywords in JSON (Core doesn't process these, just stores/serves them)
- **06-TECH-STACK-DECISIONS.md** — Why Go, why SQLite, why Git
- **07-LICENSING.md** — MIT
- **08-REPOSITORY-STRUCTURE.md** — Multi-repo layout, versioning

---

## Current State

Core: MVP COMPLETE — WORKSHOPS DONE

- [x] Go module initialized (`github.com/makestack/makestack-core`)
- [x] Project structure created
- [x] Git operations: read — `internal/git`: walks data dir, reads all `manifest.json` files, parses typed fields, validates required fields, extracts relationships
- [x] Git operations: write — `internal/git/writer.go`: WriteManifest + DeleteManifest, auto-commits via go-git
- [x] SQLite indexer — `internal/index`: full schema (primitives, relationships, FTS5), `UpsertFull` (atomic tx), `Delete`, `List`, `Get`, `Search`, `RelationshipsFor`, `RebuildFTS`
- [x] REST API read — `GET /health`, `GET /api/primitives[?type=]`, `GET /api/primitives/{path...}`, `GET /api/search?q=`, `GET /api/relationships/{path...}`
- [x] REST API write — `POST /api/primitives` (create, auto id/slug/timestamps), `PUT /api/primitives/{path...}` (update), `DELETE /api/primitives/{path...}` (delete)
- [x] Full-text search (FTS5) — indexes name, description, tags, properties
- [x] Relationship indexing + reverse lookups — `RelationshipsFor` returns both directions
- [x] File watcher — `internal/watcher`: fsnotify v1.9.0, recursive dir watching, 200 ms debounce, handles create/edit/delete live; recursively processes new dirs to avoid race with write API
- [x] Test fixtures — one of each primitive type + workshop fixture
- [x] Authentication — API key via `--api-key` flag or `MAKESTACK_API_KEY` env var; `--public-reads` makes GET endpoints open; constant-time comparison; `/health` always public
- [x] Workshop support — `GET /api/primitives?workshop=<slug>[&type=<t>]`; workshops indexed from `workshops/*/workshop.json` at startup into SQLite `workshops` + `workshop_members` tables
- [ ] JSON schema validation
- [ ] Tests (unit + integration)

---

## What's In Progress

Nothing currently in progress.

---

## What's Blocked / Known Issues

- **DB is in-memory by default:** Index is rebuilt from disk on every startup. Use `-db /path/to/index.db` for persistence across restarts.

---

## Next Steps (Priority Order)

1. JSON schema validation on POST/PUT (validate against per-type schema)
2. Unit + integration tests
3. Dockerize

---

## Decisions Made

- Language: Go 1.24 (go-git v5.17.0 forced upgrade from 1.22)
- Git library: go-git v5.17.0
- SQLite library: modernc.org/sqlite v1.29.6 (pure Go, no CGO)
- HTTP router: stdlib `net/http` with Go 1.22 method+path patterns — no external router needed
- File watcher: fsnotify v1.9.0; watcher failure is non-fatal — server continues without live reload
- Binary name: makestack-core
- Default port: 8420
- Default DB: `:memory:` (flag: `-db`)
- License: MIT
- Versioning: Semver from 0.1.0
- Relationship direction: `RelationshipsFor` returns both source and target matches (bidirectional)
- Manifest JSON stored verbatim in `primitives.manifest` column — no data loss
- Watcher debounce: 200 ms (handles editor atomic-rename save patterns)
- `index.IndexManifest` is the single conversion point from `git.ParsedManifest` to index rows (bulk loader and watcher both call it)
- Write path: POST/PUT/DELETE write to Git and commit; watcher picks up change and updates index async
- Workshop fixture format: `workshops/{slug}/workshop.json` (not manifest.json, so watcher ignores it — read at startup only)
- Workshop storage: `workshops` + `workshop_members` tables in SQLite; membership filter uses subquery `WHERE path IN (SELECT primitive_path FROM workshop_members WHERE workshop_slug = ?)`
- Auth: single static API key; `Authorization: Bearer <key>` or `X-API-Key: <key>`; constant-time compare; `/health` always public; `--public-reads` opens GET endpoints

## Decisions Deferred

- REST vs GraphQL (REST default, reconsider if relationship queries become awkward)
- Multi-user auth (JWT or session tokens — API key is sufficient for v0 single-owner use)

---

## Session Log

### 2026-02-27 — Specification Phase
- Created nine spec documents defining the full architecture
- Created this CLAUDE.md
- Ready to start implementation

### 2026-02-28 — MVP Implementation
- Initialized Go module (`github.com/makestack/makestack-core`) and full project structure
- Implemented `internal/git`: manifest reader, typed `ParsedManifest`, `Relationship`, `Parse()` with validation
- Implemented `internal/index`: full SQLite schema, `UpsertFull` (atomic tx), `Delete`, `List`, `Get`, `Search` (FTS5), `RelationshipsFor`, `RebuildFTS`
- Implemented `internal/api`: all REST read endpoints, typed response structs, `writeJSON`/`writeError` helpers
- Wired `cmd/makestack-core/main.go`: reads → parses → indexes → FTS rebuild → HTTP server with graceful shutdown
- Added test fixtures for all six primitive types with realistic cross-references
- Installed Go, ran `go mod tidy`, confirmed clean build
- Smoke-tested all read endpoints against `test/fixtures/` — 8 primitives indexed, all endpoints return correct JSON
- Decided on stdlib `net/http` (Go 1.22 patterns) — no external router needed

### 2026-02-28 — File Watcher
- Implemented `internal/watcher` using fsnotify v1.9.0
- Recursive directory watching: walks data dir on start, adds new dirs as they appear
- 200 ms debounce to handle editor multi-event save patterns
- After debounce: reads file — exists → parse + upsert; missing → delete from index
- `RebuildFTS` called after each incremental change
- Added `index.IndexManifest(ctx, *git.ParsedManifest)` as the shared conversion

### 2026-02-28 — Write API + go-git
- Added go-git v5.17.0 (required go 1.24 upgrade in go.mod)
- Implemented `internal/git/writer.go`: `Writer`, `WriteManifest`, `DeleteManifest` — auto-commits to git on every write
- Rewrote `internal/api/api.go`: added POST/PUT/DELETE handlers, `writerReady()` guard (503 if nil), auto id/slug/timestamps on POST
- Wired `git.Writer` into `main.go`; write endpoints return 503 if data dir is not a git repo
- Fixed watcher `handleEvent` to recursively walk + process new directories (fixes race condition where write API creates dirs too fast for fsnotify)
- Added `test/fixtures/workshops/leatherwork/workshop.json`
- All write endpoints verified: POST 201 + auto fields, PUT 200, DELETE 204; all commit to Git; index updated via watcher ~200ms later

### 2026-03-01 — Workshop-scoped queries
- Added `internal/git/workshop.go`: `ReadWorkshops` walks `workshops/*/workshop.json`, parses slug/name/primitives list
- Added `workshops` + `workshop_members` SQLite tables; `IndexWorkshop` upserts atomically (delete-then-reinsert members)
- Updated `index.List` to accept `workshopSlug` param; uses `WHERE path IN (subquery)` — four SQL branches for all filter combinations
- Updated `handleListPrimitives` to extract `?workshop=` query param
- Updated `main.go` to read and index workshops after manifest bulk load
- Verified: 7/8 primitives in leatherwork workshop; type+workshop combined filter works; unknown slug returns `[]`

### 2026-03-01 — Authentication
- Implemented `internal/auth/auth.go`: `ValidateRequest` checks `Authorization: Bearer` or `X-API-Key` header with `subtle.ConstantTimeCompare`
- Added `withAuth` middleware to `internal/api/api.go`; all routes protected; `/health` always public
- `--api-key` flag overrides `MAKESTACK_API_KEY` env var; no key → warning + open access
- `--public-reads` flag makes GET endpoints public while keeping writes protected
- Logs auth mode at startup; all scenarios smoke-tested
