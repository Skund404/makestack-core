# makestack-core

Headless data management engine for [Makestack](https://github.com/makestack) — an open-source, modular project management toolkit for makers.

`makestack-core` manages a catalogue of shared knowledge (tools, materials, techniques, workflows, projects, and events) stored as JSON files in a Git repository. It maintains a SQLite read index for fast queries, exposes a REST API, and watches the data directory for live changes.

It is intentionally impersonal: no users, no ownership, no personal state. That belongs in the Shell (makestack-app).

---

## Architecture

```
makestack-core  (this repo)
  └─ Git data repo  ←  reads / writes JSON manifests, auto-commits
  └─ SQLite index   ←  rebuilt from Git on startup, kept live by watcher
  └─ REST API       ←  serves the Shell (makestack-app)

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
| `-data` | *(required)* | Path to the Git-managed data repository |
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

On startup, Core bulk-loads all manifests from the data repo into the index, rebuilds the FTS index, then starts the file watcher and HTTP server. Graceful shutdown on `SIGINT`/`SIGTERM`.

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

The data repo is a plain Git repository. Each primitive lives in its own directory with a `manifest.json`:

```
makestack-data/
├── .makestack/
│   ├── config.json              # Shell configuration (served via Core API)
│   └── modules/                 # Per-module configuration files
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

The `.makestack/` directory holds Shell configuration and is served by Core like any other file, but is not indexed as primitives.

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

## REST API

Base URL: `http://localhost:8420`

### Health

```
GET /health
```

Always returns `200 OK`. No auth required.

---

### List Primitives

```
GET /api/primitives
GET /api/primitives?type=tool
```

Returns all indexed primitives, optionally filtered by type.

---

### Get Primitive

```
GET /api/primitives/{type}/{slug}/manifest.json
```

Returns the primitive from the SQLite index (current HEAD).

**Version-pinned read** — read a primitive as it existed at a specific commit:

```
GET /api/primitives/{type}/{slug}/manifest.json?at={commit_hash}
```

Reads directly from the Git object store; response includes `commit_hash`. Returns `404` for unknown commit or path, `503` if the data directory is not a Git repo.

---

### Get Last-Modified Hash

```
GET /api/primitives/{type}/{slug}/manifest.json/hash
```

Returns `{"commit_hash": "abc123..."}` — the hash of the most recent commit that modified this primitive, not the repository HEAD. The Shell stores this when adding a catalogue entry to a user's inventory, so it can retrieve the exact version later via `?at=`.

---

### Commit History

```
GET /api/primitives/{type}/{slug}/manifest.json/history
GET /api/primitives/{type}/{slug}/manifest.json/history?limit=50&offset=0
```

Returns a paginated list of commits that touched this file:

```json
[
  {
    "hash": "abc123...",
    "message": "feat: update swivel knife description",
    "author": "Alice",
    "timestamp": "2026-03-01T12:00:00Z"
  }
]
```

Returns an empty list (not `404`) for paths with no history.

---

### Diff Between Commits

```
GET /api/primitives/{type}/{slug}/manifest.json/diff?from={hash}&to={hash}
```

Returns a structured, field-level JSON diff between two commits. `to` defaults to HEAD; `from` defaults to the parent of `to`. Returns `400` if the target commit has no parent.

```json
[
  { "field": "description", "old": "Old text.", "new": "New text." }
]
```

---

### Full-Text Search

```
GET /api/search?q=carving
```

Searches across `name`, `description`, `tags`, and `properties` using SQLite FTS5.

---

### Relationships

```
GET /api/relationships/{type}/{slug}/manifest.json
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

`id`, `slug`, `created`, and `modified` are set automatically. Returns `201` with the full manifest. Validates input before writing; returns `400` with all validation errors if invalid.

---

### Update Primitive

```
PUT /api/primitives/{type}/{slug}/manifest.json
Content-Type: application/json

{ "name": "Wing Divider", "description": "Updated description." }
```

Updates `modified` automatically. Returns `200` with the updated manifest. Returns `400` on validation errors.

---

### Delete Primitive

```
DELETE /api/primitives/{type}/{slug}/manifest.json
```

Removes the manifest from the data repo and Git history. Returns `204 No Content`.

---

## How Writes Work

1. `POST`/`PUT`/`DELETE` writes the manifest to the Git data repo and auto-commits.
2. The file watcher detects the change (~200 ms debounce) and updates the SQLite index.
3. Subsequent `GET` requests reflect the new state.

Write endpoints return `503` if the data directory is not a valid Git repository.

---

## License

MIT
