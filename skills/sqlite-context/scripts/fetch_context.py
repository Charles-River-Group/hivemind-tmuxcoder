#!/usr/bin/env python3
import argparse
import json
import os
import sqlite3
from pathlib import Path


def resolve_tmux_session_id(explicit: str) -> str:
    if explicit:
        return explicit
    env = os.getenv("TMUXCODER_SESSION_ID", "").strip()
    if env:
        return env
    session_file = Path.home() / ".tmuxcoder" / "current_session_id"
    if session_file.exists():
        value = session_file.read_text().strip()
        if value:
            return value
    raise SystemExit("tmux_session_id is required: use --tmux-session-id, TMUXCODER_SESSION_ID, or ~/.tmuxcoder/current_session_id")


def default_db_path() -> str:
    return str(Path.home() / ".tmuxcoder" / "model_outputs.db")


def parse_args():
    p = argparse.ArgumentParser(description="Fetch shared context from SQLite.")
    p.add_argument("--db-path", default=default_db_path())
    p.add_argument("--tmux-session-id", default="")
    p.add_argument("--session-id", required=True)
    p.add_argument("--source", required=True, choices=["codex", "claude"])
    p.add_argument("--limit", type=int, default=50)
    p.add_argument("--max-chars", type=int, default=12000)
    p.add_argument("--format", choices=["text", "json"], default="text")
    return p.parse_args()


def merge_roles(rows):
    merged = []
    for role, text in rows:
        if not text:
            continue
        if merged and merged[-1][0] == role:
            merged[-1] = (role, merged[-1][1] + "\n" + text)
        else:
            merged.append((role, text))
    return merged


def trim_by_chars(rows, max_chars):
    if max_chars <= 0:
        return rows
    out = []
    total = 0
    for role, text in reversed(rows):
        block = f"[{role}]\n{text}\n"
        if total + len(block) > max_chars and out:
            break
        out.append((role, text))
        total += len(block)
    return list(reversed(out))


def main():
    args = parse_args()
    tmux_session_id = resolve_tmux_session_id(args.tmux_session_id)

    conn = sqlite3.connect(args.db_path)
    cur = conn.cursor()

    cur.execute(
        """
        SELECT role, text
        FROM model_outputs
        WHERE tmux_session_id = ? AND session_id = ? AND source = ?
        ORDER BY ts ASC, id ASC
        """,
        (tmux_session_id, args.session_id, args.source),
    )
    rows = cur.fetchall()
    conn.close()

    if args.limit > 0 and len(rows) > args.limit:
        rows = rows[-args.limit :]

    merged = merge_roles(rows)
    merged = trim_by_chars(merged, args.max_chars)

    if args.format == "json":
        print(json.dumps([{"role": r, "content": t} for r, t in merged], ensure_ascii=False))
        return

    out = []
    for role, text in merged:
        out.append(f"[{role}]\n{text}")
    print("\n\n".join(out))


if __name__ == "__main__":
    main()
