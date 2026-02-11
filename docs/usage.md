# TmuxCoder User Guide (Single Binary)

This project ships a single binary: `tmuxcoder`.

---

## Quick Start (3 steps)

```sh
make build
make install
tmuxcoder start --codex-session <session-id> --tmux-session-id <tmux-id>
```

Then, in any directory:
```sh
tmuxcoder context --source codex
```

---

## 0) Install

### Build from source
```sh
make build
# or
GOOS=$(go env GOOS) GOARCH=$(go env GOARCH) go build -o dist/tmuxcoder ./cmd/tmuxcoder
```

### Install (user path, no sudo)
```sh
make install
# installs to ~/.local/bin by default
```

### Install (global, optional)
```sh
sudo make install PREFIX=/usr/local/bin
```

Verify:
```sh
which tmuxcoder
tmuxcoder --help
```

---

## 1) Two IDs you should know

1) **codex/claude session id**
- The model session you want to ingest.

2) **tmux_session_id**
- A human label for a shared context scope (e.g., `hivemind`).
- All sessions under the same `tmux_session_id` share context.

---

## 2) One‑shot start (recommended)

```sh
tmuxcoder start \
  --codex-session <codex-session-id> \
  --tmux-session-id <tmux-session-id>
```

Notes:
- `start` runs **bus + ingest + sink** by default.
- Writes `~/.tmuxcoder/current_session_id` for context tools.
- Writes `~/.tmuxcoder/current_db_path` so `tmuxcoder context` uses the correct DB.
- Add `--ui` to launch tmux UI.
- Writes shared-context rules to `AGENTS.md` / `CLAUDE.md` unless disabled.

Useful flags:
```sh
# Do not write rules templates
TMUXCODER=1 tmuxcoder start --write-rules=false

# Write rules to specific files
TMUXCODER=1 tmuxcoder start --rules-files AGENTS.md,CLAUDE.md
```

---

## 3) Check status

Tail logs:
```sh
tmuxcoder logs tail --include-global
```

Status snapshot:
```sh
tmuxcoder status
```

Status in tmux (auto-refresh):
```sh
tmuxcoder status --ui
```

---

## 4) Install skills (required once)

Install repo skills into Codex / Claude Code:
```sh
tmuxcoder skills install
```

Common options:
```sh
# Only Codex
TMUXCODER=1 tmuxcoder skills install --target codex

# Only Claude Code
TMUXCODER=1 tmuxcoder skills install --target claude

# Custom skills source directory
TMUXCODER=1 tmuxcoder skills install --skills-dir /path/to/skills

# Overwrite existing skills
TMUXCODER=1 tmuxcoder skills install --force
```

Restart Codex / Claude Code after installing skills.

---

## 5) Shared context (single command)

Default: **aggregate across the tmux session**
```sh
tmuxcoder context --source codex
```

Specify `tmux_session_id` explicitly:
```sh
tmuxcoder context --source claude --tmux-session-id <tmux-session-id>
```

Filter a single model session:
```sh
tmuxcoder context --source codex --session-id <model-session-id>
```

Optional flags:
- `--limit 50` (0 = all)
- `--max-chars 12000` (0 = unlimited)
- `--format text|json`
- `--db-path /path/to/db` (override DB)
- `--debug` (print resolved paths and env to stderr)

Audit log:
- Each call writes a row to `context_reads` in SQLite.

---

## 6) Rule template (for Codex / Claude Code)

If your CLI supports rules, the template enforces **one command only**:
```
You MUST fetch shared context by running exactly one command:
tmuxcoder context --source <codex|claude>

Do NOT run discovery commands (ls/rg/strings/--help). Do NOT search for TMUXCODER_ROOT.
If tmux_session_id is needed, pass --tmux-session-id or ask the user to set TMUXCODER_SESSION_ID.
Then prepend the fetched context to the prompt and answer the user.
```

This is written automatically by `start` unless `--write-rules=false`.

---

## 7) Advanced: run components separately

```sh
# bus
TMUXCODER=1 tmuxcoder bus

# ingest
TMUXCODER=1 tmuxcoder ingest --codex-session <session-id>
TMUXCODER=1 tmuxcoder ingest --claude-session <session-id>
TMUXCODER=1 tmuxcoder ingest --claude-project /Users/you/work/project
TMUXCODER=1 tmuxcoder ingest --log-path /path/to/log.jsonl

# sink
TMUXCODER=1 tmuxcoder sink --tmux-session-id <tmux-session-id>

# UI
TMUXCODER=1 tmuxcoder ui
```

---

## 8) SQLite schema (model_outputs)

Default DB: `~/.tmuxcoder/model_outputs.db`

```sql
CREATE TABLE model_outputs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ts TEXT NOT NULL,
  source TEXT NOT NULL,      -- codex / claude
  role TEXT NOT NULL,        -- assistant / user
  session_id TEXT,
  tmux_session_id TEXT,
  workspace_uid TEXT,
  text TEXT NOT NULL
);

CREATE INDEX idx_model_outputs_ts ON model_outputs(ts);
CREATE INDEX idx_model_outputs_source ON model_outputs(source);
CREATE INDEX idx_model_outputs_session ON model_outputs(session_id);
CREATE INDEX idx_model_outputs_tmux_session ON model_outputs(tmux_session_id);
```

```sql
CREATE TABLE tmux_sessions (
  tmux_session_id TEXT PRIMARY KEY,
  created_at TEXT NOT NULL
);
```

```sql
CREATE TABLE context_reads (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ts TEXT NOT NULL,
  tmux_session_id TEXT,
  session_id TEXT,
  source TEXT,
  rows INTEGER,
  max_chars INTEGER,
  limit_rows INTEGER,
  raw_rows INTEGER,
  selected_rows INTEGER,
  merged_rows INTEGER,
  first_id INTEGER,
  last_id INTEGER,
  first_ts TEXT,
  last_ts TEXT
);
```

---

## 9) Troubleshooting

**Q: `context` says `no such table: model_outputs`**
- `tmuxcoder context` is reading the wrong DB.
- Fix by using `--db-path` or ensuring `~/.tmuxcoder/current_db_path` exists.
- `tmuxcoder start` and `tmuxcoder sink` write this automatically.

**Q: `context` returns empty**
- `session_id` might not be written. Re-ingest with `--from-begin` or `--sink-rebuild`.

**Q: `tmux_session_id` missing**
- Provide `--tmux-session-id`, or set `TMUXCODER_SESSION_ID`, or ensure `~/.tmuxcoder/current_session_id` exists.

**Q: AGENTS.md / CLAUDE.md changed**
- `start` writes the shared-context rule block by default.
- Disable with `--write-rules=false`.

---

## 10) Help

```sh
tmuxcoder --help
tmuxcoder ingest --help
tmuxcoder sink --help
tmuxcoder start --help
tmuxcoder skills install --help
tmuxcoder status --help
tmuxcoder context --help
```
