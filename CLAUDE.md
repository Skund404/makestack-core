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

- **Language:** Go 1.24 (go-git v5.17.0 requires 1.24)
- **Git operations:** go-git v5.17.0 (github.com/go-git/go-git/v5)
- **SQLite:** modernc.org/sqlite v1.29.6 (pure Go, no CGO)
- **HTTP router:** stdlib net/http (Go 1.22 method+path patterns)
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

Core: MVP COMPLETE — WRITE API DONE
- [x] Go module initialized
- [x] Project structure created
- [x] Git operations: read manifests from repo (internal/git/git.go)
- [x] Git operations: write + commit manifests (internal/git/writer.go — go-git)
- [x] SQLite indexer: build index from Git (internal/index/index.go)
- [x] REST API: GET /primitives, GET /primitives/{path}, GET /search?q= (internal/api/api.go)
- [x] REST API: POST /primitives (create), PUT /primitives/{path} (update), DELETE /primitives/{path} (delete)
- [x] Full-text search (FTS5)
- [x] Relationship indexing + reverse lookups
- [x] File watcher: re-index on change, debounced 200ms (internal/watcher/watcher.go)
- [ ] Authentication
- [ ] Workshop support (scope queries by workshop)
- [ ] JSON schema validation

---

## What's In Progress

Nothing currently in progress.

---

## What's Blocked / Known Issues

- No blocking issues

---

## Next Steps (Priority Order)

1. Authentication (API key for v0 — simplest that protects the write API)
2. Workshop-scoped queries (`GET /api/primitives?workshop=leatherwork`)
3. JSON schema validation on POST/PUT (validate against per-type schema)
4. Unit + integration tests
5. Dockerize

---

## Decisions Made

- Language: Go 1.24 (go-git forced upgrade from 1.22)
- Git library: go-git v5.17.0
- SQLite library: modernc.org/sqlite (pure Go, no CGO)
- HTTP router: stdlib net/http (Go 1.22 method+path routing is sufficient)
- File watcher: fsnotify v1.9.0
- Binary name: makestack-core
- Default port: 8420
- License: MIT
- Versioning: Semver from 0.1.0
- Write path: POST/PUT/DELETE write to Git and commit; watcher picks up change and updates index async
- Workshop fixture format: `workshops/{slug}/workshop.json` (not manifest.json, so reader/watcher ignore it — read-only reference for now)

## Decisions Deferred

- REST vs GraphQL (REST default, reconsider if relationship queries are awkward)
- Auth mechanism (JWT, API key, or session — decide when building auth; API key is likely v0)

---

## Session Log

### 2026-02-27 — Specification Phase
- Created nine spec documents defining the full architecture
- Created this CLAUDE.md
- Ready to start implementation

### 2026-02-28 — MVP + Write API
- Initialized Go module, project structure, Dockerfile, README, CONTRIBUTING
- Implemented internal/git/git.go: manifest reader, parser, ParsedManifest, Relationship
- Implemented internal/index/index.go: full SQLite schema, FTS5, UpsertFull, Delete, List, Get, Search, RelationshipsFor, RebuildFTS, IndexManifest
- Implemented internal/api/api.go: REST endpoints (read + write)
- Implemented internal/watcher/watcher.go: fsnotify watcher, 200ms debounce, incremental index updates
- Implemented internal/git/writer.go: go-git write/commit (WriteManifest, DeleteManifest)
- Wired everything together in cmd/makestack-core/main.go
- Added test fixtures for all 6 primitive types + workshop fixture
- Fixed watcher to recursively watch/process new directories (race with write API)
- All write endpoints verified: POST 201, PUT 200, DELETE 204; all commit to Git
