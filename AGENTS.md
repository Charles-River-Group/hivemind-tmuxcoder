# Repository Guidelines

## Project Structure & Module Organization
- `cmd/`: entrypoints for binaries (e.g., `cli`, `bus`, `bridge`, `mcp-server`, `test-client`). Each subfolder is a standalone `main`.
- `internal/`: private application packages (bus server, bridge, protocol, store). Keep non-exported APIs here.
- `pkg/`: shared utilities intended for reuse (e.g., `pkg/util/ulid`).
- `configs/`: configuration examples or defaults.
- `docs/`: design notes and implementation references.
- `tests/`: placeholder for higher-level tests (currently empty).

## Build, Test, and Development Commands
- `go build ./cmd/...`: build all binaries into the default Go build cache.
- `go build ./cmd/cli`: build a single binary.
- `go run ./cmd/cli`: run a binary directly for local development.
- `go test ./...`: run all Go tests.
- `go test -race -cover ./...`: run tests with the race detector and coverage.
- `go test -tags=integration ./tests/...`: run integration tests if/when added under `tests/`.

## Coding Style & Naming Conventions
- Use standard Go formatting (`gofmt`); tabs for indentation, no manual alignment.
- Package names are short, lowercase, and single-purpose.
- Exported identifiers use `CamelCase`; unexported use `lowerCamel`.
- Keep `cmd/` packages thin; move shared logic into `internal/` or `pkg/`.

## Testing Guidelines
- Place unit tests alongside code using `*_test.go`.
- Test names follow `TestXxx`, `BenchmarkXxx`, and `FuzzXxx`.
- Prefer deterministic tests; use `t.TempDir()` for filesystem work.

## Commit & Pull Request Guidelines
- Commit messages follow a conventional style (e.g., `feat: ...`, `fix: ...`, `docs: ...`).
- PRs should include a short summary, test commands run, and links to relevant issues or docs.
- Add screenshots or logs when behavior changes or new CLI output is introduced.

## Security & Configuration Tips
- Keep secrets out of the repo; prefer environment variables or local-only config files.
- Document any required config in `configs/` or `docs/`.
