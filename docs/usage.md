# TmuxCoder Usage Guide

This document summarizes the available build, packaging, and run commands in this repository to help you get started quickly and use TmuxCoder in day‑to‑day work.

## Build and Package

Recommended: use the Makefile:

```sh
# Build all binaries (outputs to dist/)
make build

# Package as tar.gz (includes bus/bridge/ingest/cli)
make package
```

You can also use Go directly:

```sh
# Build all cmd/*
go build ./cmd/...

# Example: build a single binary
go build -o dist/tmuxcoder ./cmd/cli
```

## Core Components and How to Run Them

The main components are:

- `tmuxcoder-bus`: message bus service
- `tmuxcoder-bridge`: workspace bridge (PTY / shell)
- `tmuxcoder-ingest`: log ingestion (Codex / Claude JSONL)
- `tmuxcoder`: unified CLI (logs / ui / send / message / workspaces / controller)

### bus (message bus)

```sh
# Default socket: $XDG_RUNTIME_DIR/tmuxcoder/bus.sock
# Fallback:       ~/.tmuxcoder/run/bus.sock
dist/tmuxcoder-bus

# Specify socket explicitly
dist/tmuxcoder-bus -socket /tmp/tmuxcoder/bus.sock
```

### bridge (single workspace bridge)

```sh
# Use default shell and current directory
dist/tmuxcoder-bridge

# Specify socket, working directory, and shell
dist/tmuxcoder-bridge -socket /tmp/tmuxcoder/bus.sock -dir /path/to/project -shell /bin/zsh
```

### ingest (ingest Codex / Claude sessions)

```sh
# Locate rollout.jsonl via Codex session id
dist/tmuxcoder-ingest --codex-session <session-id>

# Locate jsonl via Claude session id
dist/tmuxcoder-ingest --claude-session <session-id>

# Find latest session via Claude project (path or name)
dist/tmuxcoder-ingest --claude-project /Users/you/work/project

# Directly specify log file or directory
dist/tmuxcoder-ingest --log-path /path/to/log.jsonl
dist/tmuxcoder-ingest --watch /path/to/dir --watch /another/dir
```

Common filtering options:

- `--from-begin`: read from the beginning of the file
- `--project`: only process files whose project path matches
- `--session-id`: only process content for the given session ID
- `--socket` / `--workspace-uid` / `--label`: configure bus connection and event labels

## CLI (tmuxcoder) Common Commands

### logs tail

```sh
# Tail logs (subscribes to ui.log.append and ui.status.update by default)
dist/tmuxcoder logs tail --socket /tmp/tmuxcoder/bus.sock --include-global

# Pretty-print output (timestamps / levels)
dist/tmuxcoder logs tail --format

# Custom event types
dist/tmuxcoder logs tail --types ui.log.append,ui.status.update
```

### ui (tmux UI)

```sh
# Start the tmux UI (current or new session)
dist/tmuxcoder ui

# Start the interactive control panel
dist/tmuxcoder ui interactive
```

### workspaces

```sh
dist/tmuxcoder workspaces list
dist/tmuxcoder workspaces watch --refresh 2s
```

### send / message

```sh
# Send input to a specific workspace
dist/tmuxcoder send --workspace <uid> "ls -la"

# Send a message across workspaces
dist/tmuxcoder message --to <uid> "hello"
```

## Recommended Local Quickstart Flow

```sh
# 1) Build binaries
make build

# 2) Start the bus
make bus

# 3) Start the controller
make controller

# 4) Start the UI
make ui

# 5) Start a bridge (or another entrypoint)
dist/tmuxcoder-bridge

# 6) Tail logs
dist/tmuxcoder logs tail --include-global
```

## Reference

For more command options, use `--help`:

```sh
dist/tmuxcoder --help
dist/tmuxcoder logs tail --help
dist/tmuxcoder-ingest --help
```
