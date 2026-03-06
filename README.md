# makestack-core

Headless data management engine for [Makestack](https://github.com/makestack) — an open-source, modular project management toolkit for makers.

`makestack-core` manages a catalogue of shared knowledge (tools, materials, techniques, workflows, projects, and events) stored as JSON files in one or more Git repositories. It maintains a SQLite read index for fast queries, exposes a REST API, and watches the primary data directory for live changes.

It is intentionally impersonal: no users, no ownership, no personal state. That belongs in the Shell (makestack-app).

---

## Architecture

```
makestack-core  (this repo)
  └─ Primary Git repo    ←  reads / writes JSON manifests, auto-commits
  └─ Federated repos     ←  read-only, indexed at startup (optional)
  └─ SQLite index        ←  rebuilt from Git on startup, kept live by watcher
  └─ REST API            ←  serves the Shell (makestack-app)

makestack-app  (separate repo)
  └─ proxies Core
  └─ owns UserDB, auth, modules, inventory, theme
```

The Shell is the **only** client of Core. Modules never talk to Core directly.

---

## Prerequisites

- Go 1.24+
- A Git-initialized data directory (see [Data Repository Layout](#data-repository-layout))

---

## Building

```bash
go build -o makestack-core ./cmd/makestack-core
```

Or with Docker:

```bash
docker build -t makestack-core .
```

---

## Running

```
makestack-core -data <path-to-data-repo> [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `-data` | *(required)* | Path to the primary Git-managed data repository |
| `-addr` | `:8420` | Address and port to listen on |
| `-db` | `:memory:` | SQLite index path; use a file path to persist across restarts |
| `-api-key` | *(env: `MAKESTACK_API_KEY`)* | API key for authentication |
| `--public-reads` | `false` | Allow unauthenticated access to read endpoints; writes still require the key |

**Example — persistent index, protected writes, open reads:**

```bash
makestack-core \
  -data /srv/makestack-data \
  -db /var/lib/makestack/index.db \
  -api-key supersecret \
  --public-reads
```

On startup, Core loads the federation config (if present), bulk-loads all manifests from all roots into the index, rebuilds the FTS index, then starts the file watcher on the primary root and the HTTP server. Graceful shutdown on `SIGINT`/`SIGTERM`.

---

## Authentication

All endpoints require the API key by default. Pass it as:

```
Authorization: Bearer <key>
X-API-Key: <key>        # alternative header
```

`GET /health` is always public.

With `--public-reads`, all `GET` endpoints are open; `POST`, `PUT`, and `DELETE` still require the key.

If no key is configured, a warning is logged and all endpoints are unauthenticated.

---

## Data Repository Layout

The primary data repo is a plain Git repository. Each primitive lives in its own directory with a `manifest.json`:

```
makestack-data/
├── .makestack/
│   ├── federation.json          # Multi-root config (optional — see Federation)
│   └── config.json              # Shell configuration (served via Core API)
├── tools/
│   └── {slug}/manifest.json
├── materials/
│   └── {slug}/manifest.json
├── techniques/
│   └── {slug}/manifest.json
├── workflows/
│   └── {slug}/manifest.json
├── projects/
│   └── {slug}/manifest.json
└── events/
    └── {slug}/manifest.json
```

The `.makestack/` directory holds configuration files and is not indexed as primitives.

Every write through the API auto-commits to Git, giving you a full audit trail.

---

## Primitives

Six primitive types form the catalogue:

| Type | Description |
|------|-------------|
| `tool` | Instruments used to perform work |
| `material` | Consumable inputs |
| `technique` | Methods and skills |
| `workflow` | Ordered sequences of techniques |
| `project` | Concrete instances of making (can nest via `parent_project`) |
| `event` | Time-bound occurrences within projects |

### Manifest Fields

All primitives share these top-level fields:

```json
{
  "id": "uuid",
  "type": "tool",
  "name": "Swivel Knife",
  "slug": "swivel-knife",
  "description": "Used for carving leather.",
  "tags": ["leather", "carving"],
  "created": "2026-03-01T00:00:00Z",
  "modified": "2026-03-01T00:00:00Z",
  "relationships": [
    { "type": "used_by", "target": "techniques/carving/manifest.json" }
  ]
}
```

Type-specific fields: `technique` and `workflow` support a `steps` array; `project` supports `parent_project` (path to parent manifest).

---

## Federation

Core can index **multiple Git repositories simultaneously**. Each additional repository is a *federated root* — read-only, independently versioned, and identified by a slug.

### Use cases

- A supplier publishes their product catalogue as a Git repo. You add it as a read-only root; their materials appear alongside your own in search and list results.
- A community template pack (techniques, workflows) shared as a Git repo.
- Your own personal catalogue as the primary read-write root.

### Configuration

Create `.makestack/federation.json` in the primary data directory:

```json
{
  "version": "1",
  "roots": [
    {
      "slug": "primary",
      "path": "/path/to/my-repo",
      "trust": "personal",
      "primary": true
    },
    {
      "slug": "wickett-craig",
      "path": "/path/to/wickett-craig-catalogue",
      "trust": "supplier",
      "primary": false
    },
    {
      "slug": "community-lw",
      "path": "/path/to/leatherwork-commons",
      "trust": "community",
      "primary": false
    }
  ]
}
```

If this file is absent, Core runs in single-root mode — identical to pre-federation behaviour.

**Rules:**
- Exactly one root must have `"primary": true`. Only that root accepts writes.
- Slugs must be unique and lowercase.
- All paths must exist on disk. Core does not clone repositories.
- Federated roots are loaded once at startup. Scheduled sync is not yet implemented.

**Trust levels:** `personal` · `community` · `supplier` · `peer`

### Path namespacing

Primitives from the primary root retain their existing paths (`tools/chisel/manifest.json`). Primitives from federated roots are prefixed with the root slug:

```
primary:        tools/stitching-chisel/manifest.json
wickett-craig:  wickett-craig/materials/hide-splitter/manifest.json
```

This means every path in the index is globally unique, and the Shell can address any primitive directly.

### Parser Config

Foreign repositories may use different directory names. Place a `makestack-parser.json` at the root of any repo to tell Core how to index it:

```json
{
  "version": "1",
  "index": {
    "directories": {
      "products": "material",
      "tools": "tool"
    },
    "manifest_filename": "manifest.json"
  },
  "render": {
    "labels": { "material": "Product" }
  }
}
```

- `index.directories` — maps directory names to primitive type strings. Defaults to the standard six-directory layout when absent.
- `index.manifest_filename` — defaults to `manifest.json`.
- `render` — stored verbatim by Core and served to the Shell via `GET /api/parser-config/{slug}`. Core does not interpret it.

---

## REST API

Base URL: `http://localhost:8420`

### Health

```
GET /health
```

Always returns `200 OK`. No auth required.

---

### List Roots

```
GET /api/roots
```

Returns all configured roots with their slug, trust level, primary flag, and primitive count.

```json
{
  "roots": [
    { "slug": "primary",       "trust": "personal",  "primary": true,  "primitive_count": 12 },
    { "slug": "wickett-craig", "trust": "supplier",  "primary": false, "primitive_count": 847 }
  ]
}
```

---

### Get Parser Config

```
GET /api/parser-config/{slug}
```

Returns the parser config for the given root. If no `makestack-parser.json` exists for that root, the default Makestack conventions are returned. Used by the Shell to make rendering decisions.

Returns `404` for unknown slugs.

---

### List Primitives

```
GET /api/primitives
GET /api/primitives?type=tool
GET /api/primitives?root=wickett-craig
GET /api/primitives?type=material&root=wickett-craig
```

Returns all indexed primitives, optionally filtered by type and/or root. An unknown root slug returns an empty list (not `404`).

---

### Get Primitive

```
GET /api/primitives/{path}/manifest.json
```

Returns the primitive from the SQLite index (current HEAD). Federated primitives are addressed with their full namespaced path:

```
GET /api/primitives/tools/stitching-chisel/manifest.json
GET /api/primitives/wickett-craig/materials/hide-splitter/manifest.json
```

**Version-pinned read** — read a primitive as it existed at a specific commit:

```
GET /api/primitives/{path}/manifest.json?at={commit_hash}
```

Reads directly from the Git object store; response includes `commit_hash`. Returns `404` for unknown commit or path, `503` if the data directory is not a Git repo.

---

### Get Last-Modified Hash

```
GET /api/primitives/{path}/manifest.json/hash
```

Returns `{"commit_hash": "abc123..."}` — the hash of the most recent commit that modified this primitive, not the repository HEAD. The Shell stores this when adding a catalogue entry to a user's inventory, so it can retrieve the exact version later via `?at=`.

---

### Commit History

```
GET /api/primitives/{path}/manifest.json/history
GET /api/primitives/{path}/manifest.json/history?limit=50&offset=0
```

Returns a paginated list of commits that touched this file. Returns an empty list (not `404`) for paths with no history.

---

### Diff Between Commits

```
GET /api/primitives/{path}/manifest.json/diff?from={hash}&to={hash}
```

Returns a structured, field-level JSON diff between two commits. `to` defaults to HEAD; `from` defaults to the parent of `to`. Returns `400` if the target commit has no parent.

```json
[
  { "field": "description", "type": "modified", "old": "Old text.", "new": "New text." }
]
```

---

### Full-Text Search

```
GET /api/search?q=carving
```

Searches across `name`, `description`, `tags`, `properties`, and `root_slug` using SQLite FTS5.

---

### Relationships

```
GET /api/relationships/{path}/manifest.json
```

Returns all relationships where this primitive is the source or target (bidirectional).

---

### Create Primitive

```
POST /api/primitives
Content-Type: application/json

{
  "type": "tool",
  "name": "Wing Divider",
  "description": "Marks stitch lines at a consistent distance from the edge."
}
```

`id`, `slug`, `created`, and `modified` are set automatically. If the auto-generated slug is already taken, a numeric suffix is appended (`-2`, `-3`, …). Supplying an explicit `slug` that is already taken returns `409 Conflict`. Returns `201` with the full manifest. All writes target the primary root only.

---

### Update Primitive

```
PUT /api/primitives/{path}/manifest.json
Content-Type: application/json

{ "id": "...", "type": "tool", "name": "Wing Divider", "slug": "wing-divider", ... }
```

Updates `modified` automatically. Returns `200` with the updated manifest. Returns `400` if the path belongs to a federated (non-primary) root.

---

### Delete Primitive

```
DELETE /api/primitives/{path}/manifest.json
```

Removes the manifest from the data repo and commits the deletion. Returns `204 No Content`. Returns `400` if the path belongs to a federated (non-primary) root.

---

## How Writes Work

1. `POST`/`PUT`/`DELETE` writes the manifest to the primary Git data repo and auto-commits.
2. The file watcher detects the change (~200 ms debounce) and updates the SQLite index.
3. Subsequent `GET` requests reflect the new state.

Write endpoints return `503` if the primary data directory is not a valid Git repository. Write endpoints return `400` if the target path belongs to a federated read-only root.

---

## License

MIT
