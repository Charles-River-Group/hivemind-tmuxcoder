---
name: sqlite-context
description: Read shared context from ~/.tmuxcoder/model_outputs.db for a specific tmuxcoder session and model session. Use this skill to build consistent prompts for Codex/Claude Code.
---

# SQLite Context Skill

## Purpose
Build shared context from SQLite so Codex and Claude Code use the same conversation history.

## Required Inputs
- `tmux_session_id` (must be resolved via the standard order below)
- `session_id` (model session id)
- `source` (`codex` or `claude`)

## tmux_session_id Resolution Order (must follow)
1) CLI flag `--tmux-session-id`
2) Environment variable `TMUXCODER_SESSION_ID`
3) File `~/.tmuxcoder/current_session_id`

If none is present, the script must fail with a clear error.

## Default DB Path
- `~/.tmuxcoder/model_outputs.db`

## SQL Standard
```sql
SELECT ts, role, text
FROM model_outputs
WHERE tmux_session_id = ? AND session_id = ? AND source = ?
ORDER BY ts ASC, id ASC;
```

## Output Format (default text)
```
[user]
...
[assistant]
...
```

## Scripts
Use `scripts/fetch_context.py` to fetch and format context.
```
python scripts/fetch_context.py \
  --session-id <id> \
  --source codex \
  --tmux-session-id <tmux-id> \
  --limit 50 \
  --max-chars 12000 \
  --format text
```

## Minimal Skill Call (Recommended)
If `TMUXCODER_SESSION_ID` is set or `~/.tmuxcoder/current_session_id` exists, you can omit `--tmux-session-id`.
```
python scripts/fetch_context.py \
  --session-id <id> \
  --source codex
```
