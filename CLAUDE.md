# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build and Test Commands

```bash
go build ./cmd/...                    # Build all binaries
go build ./cmd/cli                    # Build single binary
go run ./cmd/cli                      # Run directly for development
go test ./...                         # Run all tests
go test -race -cover ./...            # Tests with race detector and coverage
go test -tags=integration ./tests/... # Integration tests (when added)
```

Built binaries go to `dist/`: `tmuxcoder`, `tmuxcoder-bus`, `tmuxcoder-bridge`.

## Architecture Overview

TmuxCoder is a **bus-first orchestrator** for multi-model AI coding CLIs. The architecture follows a Hub-and-Spoke model:

```
┌─────────────────────────────────────┐
│      Bus Server (Central Hub)       │
│  Registry │ Router │ Persistence    │
└─────────────────────────────────────┘
       ▲           ▲           ▲
       │ NDJSON    │ NDJSON    │ NDJSON
       │ Events    │ Events    │ Events
       │           │           │
   ┌───┴───┐   ┌───┴───┐   ┌───┴───┐
   │Bridge │   │  UI   │   │Orch-  │
   │(PTY)  │   │Client │   │estrator│
   └───┬───┘   └───────┘   └───────┘
       │
   ┌───┴───┐
   │Shell/ │
   │REPL   │
   └───────┘
```

**Key principle**: tmux is the UI substrate, not the message bus. All IPC goes through the Bus Server.

## Main Entry Points

| Binary | Path | Purpose |
|--------|------|---------|
| `tmuxcoder` | `cmd/cli/` | CLI tool (logs, send, workspaces commands) |
| `tmuxcoder-bus` | `cmd/bus/` | Central message broker on Unix socket |
| `tmuxcoder-bridge` | `cmd/bridge/` | PTY wrapper connecting shell to bus |
| `mcp-server` | `cmd/mcp-server/` | MCP protocol server (stub) |
| `test-client` | `cmd/test-client/` | Manual verification tool |

## Core Components

**Bus Server** (`internal/bus/server/`):
- `server.go` - Main BusServer lifecycle, accept loop, heartbeats
- `router.go` - Event routing, subscription handling, control messages
- `registry.go` - Connection and workspace tracking
- `connection.go` - Per-connection state and NDJSON encoding

**Protocol** (`internal/protocol/`):
- `envelope.go` - EventEnvelope structure (proto: `tmuxcoder.event.v1`)
- `types.go` - Event type constants and payload structs
- `serializer.go` - NDJSON codec with StreamEncoder/StreamDecoder

**Bridge** (`internal/bridge/`):
- `core/` - Bridge orchestrator connecting PTY to bus
- `pty/` - PTY proxy wrapping child processes (uses `creack/pty`)
- `client/` - Bus client for workspace registration

**Storage** (`internal/store/`):
- `journal/` - Event persistence to NDJSON files with rotation
- `audit/` - Permission-sensitive operation logging

## Event Protocol

Transport: Unix Domain Socket (`$XDG_RUNTIME_DIR/tmuxcoder/bus.sock` or `~/.tmuxcoder/run/bus.sock`)

Message format: NDJSON (Newline-Delimited JSON)

Event envelope required fields:
- `proto`: "tmuxcoder.event.v1"
- `event_id`: ULID
- `ts`: RFC3339 timestamp
- `workspace_uid`: UUID
- `source`: `{kind, id}`
- `type`: event type string
- `payload`: type-specific data

Principal types: `workspace`, `ui`, `orchestrator`, `service`, `bus`, `backend`

Key event types: `bus.register.*`, `bus.subscribe.*`, `bus.heartbeat`, `backend.send`, `backend.stream.delta`, `ui.log.append`, `ui.status.update`

## Code Conventions

- Standard Go formatting (gofmt, tabs)
- `cmd/` packages stay thin; logic goes in `internal/` or `pkg/`
- Tests alongside code in `*_test.go` files
- Commit style: `feat:`, `fix:`, `docs:`, `test:`, etc.

## Dependencies

Minimal external dependencies:
- `github.com/google/uuid` - UUID generation
- `github.com/creack/pty` - PTY wrapper
