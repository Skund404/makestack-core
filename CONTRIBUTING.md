# Contributing to makestack-core

## Requirements

- Go 1.24+

## Getting Started

```bash
git clone https://github.com/makestack/makestack-core
cd makestack-core
go mod tidy
go build ./cmd/makestack-core
go test ./...
```

## Code Standards

- `gofmt` and `go vet` before committing
- Exported functions must have doc comments
- Table-driven tests
- Commits follow [Conventional Commits](https://www.conventionalcommits.org/):
  `feat:`, `fix:`, `docs:`, `refactor:`, `test:`, `chore:`

## Branching

GitHub Flow: `main` + short-lived feature branches. Open a PR against `main`.
