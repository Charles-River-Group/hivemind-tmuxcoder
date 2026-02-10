# TmuxCoder Usage (Single Binary)

This repository ships a single binary: `tmuxcoder`. All functionality is exposed via subcommands and one‑shot convenience commands.

## Build and Package

```sh
# Build (outputs to dist/)
make build

# Package (tar.gz)
make package
```

Or build directly with Go:

```sh
go build -o dist/tmuxcoder ./cmd/tmuxcoder
```

## One‑Shot Start (Recommended)

```sh
# Start bus + ingest + sink (Codex example)
dist/tmuxcoder start --codex-session <session-id>

# Start bus + ingest + sink + ui
dist/tmuxcoder start --codex-session <session-id> --ui

# Rebuild sink tables (drop and recreate)
dist/tmuxcoder start --codex-session <session-id> --sink-rebuild=true
```

Notes:
- `start` runs **bus + ingest + sink** by default; `--ui` is optional.
- `--codex-session / --claude-session / --claude-project / --log-path / --watch` and similar flags behave the same as in the `ingest` subcommand.

## Start Components Separately (More Control)

```sh
# bus
dist/tmuxcoder bus

# ingest (Codex / Claude / explicit file)
dist/tmuxcoder ingest --codex-session <session-id>
dist/tmuxcoder ingest --claude-session <session-id>
dist/tmuxcoder ingest --claude-project /Users/you/work/project
dist/tmuxcoder ingest --log-path /path/to/log.jsonl

# sink (SQLite persistence)
dist/tmuxcoder sink --db-path ~/.tmuxcoder/model_outputs.db

# logs
dist/tmuxcoder logs tail --include-global

# ui
dist/tmuxcoder ui
```

## bus

```sh
# Default socket:  $XDG_RUNTIME_DIR/tmuxcoder/bus.sock
# Fallback socket: ~/.tmuxcoder/run/bus.sock

dist/tmuxcoder bus

# Use a specific socket
dist/tmuxcoder bus --socket /tmp/tmuxcoder/bus.sock
```

If the socket is already accepting connections, the process exits immediately instead of starting a second bus instance.

## ingest

Common flags:
- `--codex-session <id>`
- `--claude-session <id>`
- `--claude-project <path|name>`
- `--log-path <file>` / `--watch <dir>`
- `--from-begin`
- `--project` / `--session-id`
- `--socket` / `--workspace-uid` / `--label`

For the same session, only one ingest process is allowed. If another process already owns the claim, a new one will exit immediately.

## sink (SQLite Persistence)

Default path: `~/.tmuxcoder/model_outputs.db`

```sh
# Use the default path
dist/tmuxcoder sink

# Use a custom path
dist/tmuxcoder sink --db-path /Users/user/.tmuxcoder/model_outputs.db

# Only store Codex / Claude data
dist/tmuxcoder sink --sources codex,claude

# Rebuild tables (drop and recreate)
dist/tmuxcoder sink --rebuild=true
```

### Schema (model_outputs)

```sql
CREATE TABLE model_outputs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ts TEXT NOT NULL,
  source TEXT NOT NULL,      -- codex / claude
  role TEXT NOT NULL,        -- assistant / user
  session_id TEXT,
  workspace_uid TEXT,
  text TEXT NOT NULL
);

CREATE INDEX idx_model_outputs_ts ON model_outputs(ts);
CREATE INDEX idx_model_outputs_source ON model_outputs(source);
CREATE INDEX idx_model_outputs_session ON model_outputs(session_id);
```

## logs tail

```sh
# By default subscribes to ui.log.append / ui.status.update
dist/tmuxcoder logs tail --include-global

# Pretty-printed output
dist/tmuxcoder logs tail --format
```

## ui

```sh
# tmux log UI window
dist/tmuxcoder ui
```

## Help

```sh
dist/tmuxcoder --help
dist/tmuxcoder ingest --help
dist/tmuxcoder sink --help
dist/tmuxcoder start --help
```
