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

Three named layers:

- **Catalogue** = This repo (makestack-core). Impersonal, canonical knowledge. No user state, no ownership.
- **Shell** = makestack-app (separate repo, not yet built). Host application: React frontend + Python/FastAPI backend. Owns UserDB, module registry, auth, routing, keyword renderers, theme system. The Shell is the **only** client of Core. Modules never talk to Core directly.
- **Inventory** = A concept in the Shell's UserDB, extended by modules. A user's personal relationship to the catalogue (what they own, how much, what they paid). References catalogue entries, never copies them.

```
┌─────────────────────────────────┐
│   THIS REPO: CORE (Go)         │
│   = THE CATALOGUE               │
│                                 │
│   Impersonal documented knowledge│
│   No user state, no ownership   │
│   • Git read/write (go-git)     │
│   • SQLite read index (modernc) │
│   • JSON schema validation      │
│   • REST API                    │
│   • File watcher                │
└───────────────┬─────────────────┘
                │ REST API (JSON over HTTP)
                │ (Shell is the ONLY client)
┌───────────────▼─────────────────┐
│   SEPARATE REPO: SHELL          │
│   (Python + React)              │
│                                 │
│   • Proxy to Core               │
│   • UserDB (personal state)     │
│   • Module registry             │
│   • Authentication              │
│   • Routing & navigation        │
│   • Keyword renderer registry   │
│   • Theme system                │
│   • Settings                    │
│                                 │
│   Modules extend the Shell:     │
│   • Inventory (what you own)    │
│   • Cost tracking               │
│   • Suppliers, CNC, etc.        │
└─────────────────────────────────┘
```

**Rules:**
- The catalogue never knows about the user
- The inventory never stores what the catalogue already knows
- Uninstall every module → the catalogue still works fully
- Shell is the only client of Core; modules never talk to Core directly

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

Workshops (personal organizational lenses) belong in the Shell's UserDB, not in the catalogue. The catalogue serves a flat, unscoped list of primitives.

### Directory Structure (Data Repo)

```
makestack-data/
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
└── events/
    └── {slug}/manifest.json
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

Core: **FEATURE COMPLETE FOR v0**

- [x] Go module initialized (`github.com/makestack/makestack-core`)
- [x] Project structure created
- [x] Git operations: read — `internal/git`: walks data dir, reads all `manifest.json` files, parses typed fields, validates required fields, extracts relationships
- [x] Git operations: write — `internal/git/writer.go`: WriteManifest + DeleteManifest, auto-commits via go-git
- [x] SQLite indexer — `internal/index`: full schema (primitives, relationships, FTS5), `UpsertFull` (atomic tx), `Delete`, `List`, `Get`, `Search`, `RelationshipsFor`, `RebuildFTS`
- [x] REST API read — `GET /health`, `GET /api/primitives[?type=]`, `GET /api/primitives/{path...}[?at={hash}]`, `GET /api/primitives/{path...}/hash`, `GET /api/search?q=`, `GET /api/relationships/{path...}`
- [x] REST API write — `POST /api/primitives` (create, auto id/slug/timestamps), `PUT /api/primitives/{path...}` (update), `DELETE /api/primitives/{path...}` (delete)
- [x] Full-text search (FTS5) — indexes name, description, tags, properties
- [x] Relationship indexing + reverse lookups — `RelationshipsFor` returns both directions
- [x] File watcher — `internal/watcher`: fsnotify v1.9.0, recursive dir watching, 200 ms debounce, handles create/edit/delete live; recursively processes new dirs to avoid race with write API
- [x] Test fixtures — one of each primitive type
- [x] Authentication — API key via `--api-key` flag or `MAKESTACK_API_KEY` env var; `--public-reads` makes GET endpoints open; constant-time comparison; `/health` always public
- [x] Workshop support — REMOVED; workshops are personal lenses, belong in Shell's UserDB
- [x] JSON schema validation — `internal/schema`: structural type checks on POST/PUT; common (description/tags/relationships) + type-specific (steps, parent_project); all errors returned at once; 400 on failure
- [x] Tests — `internal/git`, `internal/index`, `internal/schema`, `internal/api` all covered; 100% pass

---

## What's In Progress

Nothing currently in progress.

---

## What's Blocked / Known Issues

- **DB is in-memory by default:** Index is rebuilt from disk on every startup. Use `-db /path/to/index.db` for persistence across restarts.

---

## Next Steps (Priority Order)

1. Update Dockerfile (Go 1.24 already done) and verify Docker build
2. Core is feature-complete for v0 — next work moves to makestack-app (Shell)

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
- Auth: single static API key; `Authorization: Bearer <key>` or `X-API-Key: <key>`; constant-time compare; `/health` always public; `--public-reads` opens GET endpoints
- Workshops moved to Shell — Core serves flat, unscoped catalogue only; no workshop tables in SQLite
- Core has no concept of users, ownership, or personal state
- Shell is the only client of Core; modules never talk to Core directly
- Version-specific reads: `?at={hash}` reads directly from Git object store (bypasses SQLite); `/hash` sub-resource returns HEAD hash for Shell to pin inventory records; both return 503 when writer is nil
- Go 1.22 mux `{path...}/suffix` limitation: detected via `strings.HasSuffix` inside the wildcard handler — safe because all primitive paths end with `/manifest.json`

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

### 2026-03-01 — JSON Schema Validation
- Implemented `internal/schema/schema.go`: `Validate(primType, body)` — no external libraries, pure Go
- Common checks (all types): `description` string, `tags` array-of-strings, `relationships` array of objects with non-empty `type` + `target`; element-level errors e.g. `relationships[1]: missing required field "target"`
- Type-specific: `technique`/`workflow` → `steps` must be array; `project` → `parent_project` must be string; `tool`/`material`/`event` → no extra checks
- All errors collected and returned together (not fail-fast), so callers see everything wrong at once
- Called in `handleCreatePrimitive` and `handleUpdatePrimitive` after timestamps are stamped, before `WriteManifest` — nothing touches disk on invalid input
- All 5 validation scenarios verified; PUT path verified separately

### 2026-03-01 — Catalogue Refinement + Tests
- Architectural clarification: Core = Catalogue (impersonal knowledge), Shell = host app (UserDB, auth, modules), Inventory = module-provided personal layer
- Removed workshops from Core — workshops are personal lenses, belong in Shell's UserDB
- Removed `workshops`/`workshop_members` SQLite tables, `IndexWorkshop`, `ReadWorkshops`, `?workshop=` query param, workshop fixture
- `index.List` simplified: `List(ctx, typeFilter string)` — no workshopSlug param
- Core now serves flat, unscoped catalogue only
- Fixed Dockerfile: `golang:1.22-alpine` → `golang:1.24-alpine`
- Added comprehensive test suite: `internal/git/git_test.go`, `internal/index/index_test.go`, `internal/schema/schema_test.go`, `internal/api/api_test.go`; all pass (`go test ./...`)
- Core is feature-complete for v0

### 2026-03-01 — Version-Specific Primitive Retrieval
- Added `internal/git/history.go`: `ReadManifestAtCommit(path, commitHash)` reads a manifest from the Git object store at any past commit; `HeadHash()` returns the current HEAD hash as a 40-char hex string; `ErrNotFound` sentinel (wraps `plumbing.ErrObjectNotFound` / `object.ErrFileNotFound`) so callers don't import go-git plumbing packages
- Added `GET /api/primitives/{path...}?at={commit_hash}`: reads primitive from Git at a specific commit, bypasses SQLite index; response includes `commit_hash` field; 404 for unknown commit or path, 503 if data dir is not a git repo
- Added `GET /api/primitives/{path...}/hash`: validates primitive exists in index, returns current HEAD hash as `{"commit_hash":"..."}` — Shell stores this when adding catalogue entry to inventory; 503 if not a git repo, 404 if unknown path
- `/hash` suffix routing: Go 1.22 mux does not support `{path...}/suffix` patterns; detected via `strings.HasSuffix` inside `handleGetPrimitive` — safe because all valid paths end with `/manifest.json`
- `internal/git/history_test.go`: 7 unit tests covering HeadHash and ReadManifestAtCommit (round-trip, versioned reads, not-found cases)
- `internal/api/api_test.go`: 7 integration tests via `newGitTestServer` helper (temp git repo + committed fixture + in-memory index + real Writer); covers all success and error paths
- `go test -race ./...` — all packages pass
