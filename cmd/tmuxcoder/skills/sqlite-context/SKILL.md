---
name: sqlite-context
description: Read shared context from ~/.tmuxcoder/model_outputs.db for a specific tmuxcoder session and model session. Use this skill to build consistent prompts for Codex/Claude Code.
---

# SQLite Context Skill

## Purpose
Build shared context from SQLite so Codex and Claude Code use the same conversation history.

## Required Inputs
- `source` (optional; `codex` or `claude` — omit to get all sources)
- `tmux_session_id` (optional; resolved by `tmuxcoder context` automatically)
- `session_id` (optional; filter to a single model session)

## Execution Rule (Important)
Do **not** run discovery commands (`ls`, `rg`, `strings`, `tmuxcoder context --help`, etc.).
Run **exactly one command** to fetch context:
```
tmuxcoder context
```

If needed, add optional flags in the same command:
```
tmuxcoder context --source codex --tmux-session-id <tmux-id> --session-id <model-session-id> --limit 50 --max-chars 12000 --format text
```

## Default DB Path
Handled internally by `tmuxcoder context`.

## SQL Standard
Implemented inside `tmuxcoder context` (no direct SQL needed here).

## Audit Log (context_reads)
Each skill call records an audit row in SQLite:
```sql
CREATE TABLE IF NOT EXISTS context_reads (
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

## Output Format (default text)
```
[user]
...
[assistant]
...
```

## Minimal Skill Call (Recommended)
```
tmuxcoder context
```
