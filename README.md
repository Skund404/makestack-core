# makestack-core

Headless data management engine for [Makestack](https://github.com/makestack).

- Manages JSON files in a Git repository
- Maintains a SQLite read index (rebuildable from Git)
- Serves data via REST API on port **8420**
- Watches for file changes and re-indexes automatically

## Usage

```bash
makestack-core -data /path/to/makestack-data
```

## Building

```bash
go build ./cmd/makestack-core
```

## License

MIT
