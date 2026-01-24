# TmuxCoder

**Subtitle**: bus-first orchestrator for coding CLIs

**Status**: working draft (intended to be implementation-oriented, not a final spec).

This document merges:
- **Landscape learnings** (Managers vs Boosters) from `architecture3.md`
- **System-shape sketching** from `architecture4.md`
into a cohesive, buildable architecture for multi-model orchestration in a tmux-native environment.

---

## 0. Executive Summary

We want a control layer that can coordinate multiple AI coding CLIs/models to achieve:
- task decomposition (plan → execute → evaluate),
- multi-model strategies (role specialization, racing, consensus),
- safe cross-agent collaboration, and
- HITL (Human-in-the-Loop) approvals at the right choke points.

This control layer is designed to fit TmuxCoder’s baseline architecture:
- **TmuxCoder is a bus-first orchestrator for existing coding tools** (Claude Code, OpenCode, Codex CLI, Gemini, etc.).
- A separate **bus server** provides discovery, routing, policy, and auditing for connected coding sessions (“workspaces”).
- Each coding session connects via a small **bridge/adapter** (PTY proxy wrapper, tmux pane I/O harness, or native adapter) that emits user input + model output events onto the bus and can receive gated “drive” requests back.
- **TmuxCoder can be launched inside or outside tmux**; the tmux UI client attaches to a tmux session (prompting when ambiguous) or creates a new session when none exists.
- Single-session monitoring/control is valid; multi-session orchestration is enabled by the bus (not required for basic use).
- **Controllers are just endpoints**: a human UI client or one workspace can manage another workspace end-to-end (dispatch tasks, issue approvals, send interrupts), via permissioned bus events.
- **Policies are task/session directives**, not hardcoded rules: constraints and preferences come from the operator/controller and are carried as messages + gates.
- **Approvals can be preapproved per control session** (allowlist or “all gates”), so gated actions can be satisfied automatically when you explicitly choose to run hands-off.

The core decision that resolves the biggest ambiguity in `architecture4.md`:

> **tmux is the UI substrate, not the message bus.**

Coding sessions communicate through **structured events on the bus** (via bridges/agents). tmux provides:
- operator UI surfaces (logs/status/inbox/approvals), and
- for terminal-only tools, a practical **I/O harness** (`pipe-pane`/`capture-pane` for output and `send-keys`/`paste-buffer` for input) that the bridge uses to drive the tool under policy + audit.

---

## 0.1 Staged delivery (derisking)

This plan is easiest to execute as a staged product where each stage is useful on its own.

**MVP (prove the spine)**:
- bus server: registry + routing + journal/audit + policy chokepoint
- tmux UI: status + inbox + approvals + log tail
- one bridge mode (prefer PTY wrapper for deterministic automation)
- one backend integration (even a “dumb worker” initially)

**Then**: add multi-backend orchestration loops, richer automation, and additional adapter modes once the spine is stable.

## 1. Goals and Non-Goals

### 1.1 Goals

1. **Multi-backend / multi-model orchestration**
   - Treat “worker” targets as heterogeneous endpoints (Claude Code, OpenCode, Codex, etc.), each with its own capabilities and failure modes.
2. **Composable orchestration**
   - Support both:
     - **Autonomous manager loops** (plan/execute/evaluate), and
     - **Workflow-driven orchestration** (deterministic playbooks / state machines).
3. **First-class HITL**
   - Explicit, inspectable approval points (spec approval, cross-workspace send/admin, patch/merge gates).
4. **Safe defaults**
   - Cross-workspace messaging off by default; strong policy + audit logs when enabled.
5. **Graceful degradation**
   - Capability negotiation and “feature disappears” behavior instead of brittle assumptions.
6. **Operational visibility**
   - A tmux-first “command center” that shows: what’s running, who is talking to whom, what requires approval, and what changed.
7. **Hot retargeting**
   - Switch active backend and backend session **without creating a new window** (workspaces are reusable views).

### 1.2 Non-goals (for the first working architecture)

- Replace the underlying vendor tools’ **canonical transcript/state stores** (we can cache metadata/excerpts, but don’t try to become the source of truth).
- Build a full cloud sandbox or remote executor (local-first architecture; remote can be an adapter later).
- Make “agent-to-agent free chat” a default (we want routable, policy-enforced interactions).

---

## 2. What We Learned (Landscape → Patterns)

`architecture3.md` usefully divides the ecosystem into two integration pathways:

### 2.1 Managers (Autonomous orchestration frameworks)
Common patterns across projects like CodeMachine / Pied-Piper / Every Code / Auto-Claude / Maestro / Claude-Flow:

- **Role separation**: planner vs coder vs reviewer (or more specialized roles).
- **Plan → Execute → Evaluate** loops: explicit lifecycle improves reliability and auditability.
- **Stateful orchestration**: task graphs or state machines (“beads”, workflows, worktrees).
- **HITL at gates**: spec approval, review/merge, manual “route approval”.
- **Isolation for parallelism**: worktrees or multi-process workers to avoid destructive interference.
- **Policy & routing**: a hub decides which worker gets what, under explicit policy.

### 2.2 Boosters (Plugins/config that inject orchestration)
Common patterns across plugins/config efforts:

- **Workflow DSLs** (JSON nodes / YAML playbooks) to express deterministic sequences with approval nodes.
- **Pseudo multi-agent via parallel workers** (swarm patterns).
- **MCP/tool ecosystem leverage**: use a shared tool interface rather than bespoke integrations.

### 2.3 Implications

To unify “Managers” and “Boosters” without building two systems:
- Treat autonomous orchestration as **one workflow type** (a reusable workflow node: “LLM planner + scheduler policy + router”).
- Treat deterministic orchestration as **another workflow type** (a state machine / DAG executor).
- Standardize everything around **typed events**, **capability negotiation**, and **explicit gates**.

---

## 3. Architectural Planes (Separation of Concerns)

We model the system as four planes. Keeping these boundaries clear prevents the “tmux as message bus” trap.

1. **UI Plane (tmux)**
   - Presents state, logs, approvals, and controls.
   - Never becomes the cross-workspace transport/router (tmux may still be used as a backend I/O harness for terminal tools).
2. **Control Plane (Orchestration)**
   - Parses user intent, plans work, schedules tasks, routes messages, enforces policy.
3. **Execution Plane (Workers/Backends)**
   - Runs actual coding sessions, tool calls, diffs, and streams output.
4. **State Plane (Persistence)**
   - Stores only what we must: configs, orchestration state, event journal + audit trails, and optionally retrieval indexes.

---

## 4. System Overview (Corrected “Spine”)

### 4.1 High-level spine (control flow)

```text
Existing coding tool session (native UI)
  │  (tmux pane I/O harness, PTY proxy, or native API adapter)
  ▼
[G] Workspace bridge/agent (per coding session)
  │  publishes events / receives gated “drive” requests
  ▼
[BUS] Workspace bus server (shared)
  │
  ├──► tmux UI clients (log/status/inbox/approvals/controls)
  ├──► [O] Orchestrator (Planner/Scheduler/Router) (optional; can be headless)
  └──► Other workspaces (other bridges/agents)
```

### 4.2 Side dependencies

```text
[BUS] reads:
  - config (socket path, permissions, limits)

[BUS] writes:
  - event journal (append-only NDJSON, rotatable)
  - audit sink (append-only, rotatable)
  - operational logs (debugging)
  - optional query index (SQLite; rebuildable)

[G]/[O] read/use:
  - config (integration modes, prompts/commands/workflows, worker registry)
  - state store (workspace meta + task graphs + gates)
  - artifact store (content-addressed blobs; referenced by hash)

tmux UI clients use:
  - [T] tmux driver (UI control)

[G] MAY also use:
  - [T] tmux driver (pane I/O harness) when the backend adapter is “tmux pane” rather than a native API/PTY proxy
```

### 4.3 tmux subsystem (UI + pane I/O harness)

```text
[T] tmux driver responsibilities:
  - session/window/pane management
  - layouts and popups (pickers, approvals, inspectors)
  - capture/telemetry (read-only)
  - pane I/O harness for terminal tools (bridge-facing):
    - output: prefer `pipe-pane`; fallback to polling `capture-pane` when needed
    - input: `send-keys` / `paste-buffer` for text; keycodes for control keys (`Enter`, `C-c`, etc.)
  - tmux integration via CLI now; control-mode later (optimization)

Hard rule:
  tmux is not the cross-workspace router/policy plane.
```

Notes:
- Cross-workspace routing/policy/audit remain on the bus, even if the destination bridge ultimately uses tmux I/O to deliver the request into its tool UI.
- For terminal-only tools, tmux I/O is a useful **compatibility adapter**, but it is inherently brittle (TUIs, spinners, prompt boundaries, timing/races). If deterministic automation matters, prefer a PTY proxy or native adapter.
- Bridges SHOULD advertise a `drive_mode` (`tmux_io|pty|native`) and the orchestrator/UI MUST degrade features based on it (readiness detection, cancellation reliability, structured capture).
- Any cross-workspace action that results in tool-driving keystrokes MUST still follow gates (sender confirm, receiver accept, preapprovals) and MUST be auditable as bus events (don’t “just send keys” outside the event stream).

#### Startup and tmux session attachment (launcher)

TmuxCoder’s launcher can run from inside tmux or from a normal shell outside tmux:
- **Inside tmux** (`$TMUX` set): default to the current tmux session; if policy allows, prompt whether to “merge” into the current session or run in a separate/new session.
- **Outside tmux**: if a tmux server already has sessions, prompt whether to merge into an existing session (and which one) or create a new session; if no tmux sessions exist, create a new one.
- **Non-interactive**: allow the operator to specify the target session via CLI args/config so the launcher can skip prompts.

### 4.4 Process and IPC model (Bus-first)

```text
coding tool (native UI) ⇄ bridge/agent ⇄ bus server ⇄ (tmux UI clients + orchestrator + other bridges)
```

Key properties:
- UI clients and orchestrators do not talk to coding tools directly; they talk to the bus.
- Controllers can be human-driven or model-driven: one workspace may manage another (tasks, interrupts, approvals) via permissioned bus events.
- The bridge/agent is the **receiver-side policy choke point** for any request that would inject/drive the tool session.
- The bus is the **shared discovery + routing + policy** plane for cross-session actions (with receiver re-checks).
- Transport should be streaming-friendly, event-driven, and debuggable (Unix socket + NDJSON default).

### 4.5 Workspace bus (registry + router + policy enforcement)

A shared local bus server mediates communication between principals (workspaces, UI clients, orchestrators) and enables multi-session orchestration:
- registry (discover active workspaces),
- router (deliver cross-workspace messages),
- policy enforcement (centralize cross-workspace permissions).

Recommended bus behavior:
- transport: well-known Unix socket (prefer `$XDG_RUNTIME_DIR`, fall back to `~/.tmuxcoder/run/`)
- autostart: if enabled and no bus is running, the first client (bridge/agent or UI) starts it
- lifecycle: can run as a persistent user service; if not, it may exit when the last client disconnects

Workspace identity needs two layers:
- `workspace_uid` (stable): a persisted UUID that identifies a workspace/coding session across restarts/recreates (independent of tmux).
- `workspace_id` (alias/address): an operator-friendly alias used for display and UI pickers.
  - it is **not stable** across tmux restarts and **must not** be used as a routing identifier (routing uses `workspace_uid`)
  - it SHOULD be treated as an opaque string (avoid parsing logic that assumes a specific delimiter)
  - recommended formats (pick one and be consistent):
    - **stable-for-tmux-server-lifetime**: `workspace_id := tmux:<session_id>:<window_id>:<pane_id>` (e.g., `tmux:$1:@3:%7`)
    - **more human-friendly**: `workspace_id := <session_name>:<window_name>` (e.g., `dev:claude`) (breaks if names change)

Bus registry guidance:
- the bus SHOULD treat `workspace_uid` as the canonical registry key (unique, stable) and treat `workspace_id` as mutable display/address metadata (can change if tmux sessions are renamed or windows moved)
- the bus SHOULD enforce at-most-one active connection per `workspace_uid` (reject duplicate `bus.register.request` unless an explicit “takeover” mode is implemented)

Recommended persistence/attachment flow (bridge-first):
- on bridge/agent start, determine the `workspace_uid` (stable):
  - if an explicit `workspace_uid` is provided (CLI arg/env), use it
  - else (best-effort cache) if running inside tmux and a tmux pane/window option (e.g., `@tmuxcoder_workspace_uid`) exists and parses, use it
  - else attempt to reattach by scanning recent workspace metadata where `last_seen_project_root` (and optionally `last_backend_id`) match; if ambiguous, show a picker
  - else generate a new UUID and create state at `~/.tmuxcoder/state/workspaces/<workspace_uid>/`
- update `meta.json` on each start/stop with `last_seen_*` fields (project root, backend/tool, tmux ids if present) to make reattach predictable after tmux restarts/recreates
- if the bridge is running inside tmux, it MAY set a tmux option with the current `workspace_uid` for operator convenience, but that option must be treated as a cache (not canonical)
- UI clients attach to the bus, list active workspaces, and optionally provide “bind this workspace to this tmux window/pane” helpers for navigation

#### Workspace registry metadata (recommended)

To make reattach predictable (especially after a tmux server restart where window options are lost), persist a small, human-readable metadata file alongside each workspace.

Recommended file:
- `~/.tmuxcoder/state/workspaces/<workspace_uid>/meta.json`

Recommended minimum fields:
```json
{
  "workspace_uid": "5f8a2c0a-0f07-4c9c-9f8a-0e2a6f2e2d12",
  "label": "optional display name",
  "created_ts": "2026-01-15T18:10:00Z",
  "last_seen_ts": "2026-01-15T18:17:00Z",
  "last_seen_workspace_id": "tmux:$1:@1:%7",
  "last_seen_tmux_session_name": "dev",
  "last_seen_tmux_session_id": "$1",
  "last_seen_tmux_window_id": "@1",
  "last_seen_tmux_pane_id": "%7",
  "last_seen_project_uid": "sha256:9b3c3a0c0a7c5d7e7c9a8b7c6d5e4f3a2b1c0d9e8f7a6b5c4d3e2f1a0b9c8d7",
  "last_seen_project_root": "/home/arch/TmuxCoder",
  "last_backend_id": "codex",
  "drive_mode": "pty",
  "last_backend_session_id": "optional backend session ref"
}
```

Deterministic attach/reattach rules (recommended):
1. If an explicit `workspace_uid` is provided (CLI arg/env) and parses as UUID → attach that `workspace_uid`.
2. Else if running inside tmux and a tmux pane/window option `@tmuxcoder_workspace_uid` exists and parses as UUID → attach that `workspace_uid` (cache).
   - if the bus reports that `workspace_uid` is already active elsewhere, treat this as a conflict and prompt (reuse existing vs takeover vs choose different vs create new)
3. Else compute `project_root` (prefer git root; else current working directory) and `project_uid := sha256(realpath(project_root))`.
4. Load workspace metadata candidates where `last_seen_project_uid == project_uid` (preferred; fall back to `last_seen_project_root == project_root` for older metadata) and optionally `last_backend_id == <this backend/tool>`.
5. If exactly one candidate exists → attach it (and if inside tmux, set `@tmuxcoder_workspace_uid` as a cache).
6. If multiple candidates exist → show a picker sorted by `last_seen_ts` (descending), showing `label`, `last_seen_workspace_id`, and `last_backend_id`.
7. If none exist (or user chooses “new workspace”) → generate new UUID, create directory, write `meta.json` (and if inside tmux, set `@tmuxcoder_workspace_uid` as a cache).

Bus integration (recommended):
- `bus.list_workspaces.request` SHOULD return `{workspace_uid, workspace_id, label?}` for UI pickers, **filtered** to only those workspaces the caller has `read` permission for (or higher).
- `bus.register.request` MAY include `label` for display in the registry; the canonical key remains `workspace_uid`.

#### Bus security and threat model (local-first)

Threat model goals:
- protect against **accidental** cross-workspace actions, misconfiguration, and buggy clients
- do **not** claim to defend against a fully malicious same-UID local process (that requires OS sandboxing and stricter OS-level isolation)

Defense in depth:
- the bus enforces routing and cross-workspace policy
- **destination bridges/agents must still re-check inbound policy** and reject/queue actions if not allowed (bus is not a single point of failure)

Authn/authz primitives (recommended defaults):
- socket directory permissions: `0700`
- socket file permissions: `0600` (prevents cross-user access)
- bus validates peer credentials on connect (e.g., Linux `SO_PEERCRED` / BSD `LOCAL_PEERCRED`) and rejects unexpected UID
- principal model (important):
  - **principal**: who is making the request (workspace bridge, tmux UI client, headless orchestrator, service)
  - **workspace context**: which workspace the event should attach to for storage/rendering (`workspace_uid`), which is not necessarily “who sent it”
  - the bus MUST treat the principal as authenticated and connection-bound; `workspace_uid` is an attributed context that must be permission-checked
  - recommended id convention: `source.id` is an opaque string; use stable UUID for workspaces and prefixed ids for others (e.g., `ui:<ulid>`, `orchestrator:<ulid>`)
- registration handshake:
  1. client connects → bus validates peer UID
  2. client sends `bus.register.request` with `{source, pid, start_ts, nonce, label?}` plus:
     - if `source.kind=="workspace"`: `{workspace_uid, workspace_id, project_uid, drive_mode}` (and `source.id` MUST equal `workspace_uid`)
     - else: `{client_label?}` (optional; for display/debugging)
  3. bus replies with `bus.register.response` including `{connection_id, registered_source, registered_workspace_uid?}` (and optionally a `session_token` for debugging/telemetry)
  4. the bus MUST bind the connection to `registered_source` and MUST reject events whose envelope `source` claims a different principal (prevents spoofing/misconfigured clients)
  5. if `registered_source.kind=="workspace"`, the bus MUST also bind the connection to `registered_workspace_uid` and MUST reject events whose envelope `workspace_uid` is not that workspace (prevents confused/misconfigured bridges)
  6. v1 messages do **not** carry `connection_id`/`session_token` in the event envelope; authn/authz is per-connection and via peer creds + registry binding

Operational hardening (bus and daemons):
- maximum message size (e.g., 64KB); larger payloads must be sent as **artifact references** (sha256-id; see “Artifact references (v1)”) or rejected
- rate limit per principal (`source.kind/source.id`) (and/or per destination workspace) to avoid one client spamming others
- replay/dedupe window keyed by `event_id` (idempotency for retries)

### 4.6 UI surfaces (panes, popups, views, layouts)

Core panes (in a “workspace view” window, optional):
1. **Log**: append-only transcript + streams (stdout-first)
2. **Control**: interactive REPL (commands + gated sends/admin)
3. **Status**: active backend/session + operation state + monitoring (stdout-first)

Optional panes/popups:
- sessions browser, backend picker, inbox (cross-workspace requests), operation stack/notifications, config editor/inspectors

**Layouts vs Views**:
- Layouts define **geometry** (where panes go).
- Views define **presentation** (how a pane renders), and should be built-in profiles (selected by name).

Implementation note: tmux does not expose “place pane by rectangle (x/y/w/h)”. Layouts should be expressed as docking-style constraints (top/bottom bars, left/right side panes, one “fill” pane), and compiled into tmux splits/resizes (or an applied tmux layout string).

### 4.7 Monitoring and status (default: cost/usage)

The status surface is a first-class operator tool:
- default: cost/usage + session duration (from the backend when possible)
- optional: backend health/status, response stats
- position: persistent / popup / toggleable

### 4.8 Errors and notifications (don’t break the transcript)

Recommended UX pattern:
- backend connectivity errors: popup with retry/dismiss
- user/action errors: notifications stack (toggleable pane)
- system health: persistent status indicators (connected/disconnected, ops running)

---

## 5. Core Runtime Components

### 5.1 Workspace bridge/agent (Gateway)

The workspace bridge/agent is the *local control hub* for one coding session (one workspace):
- connects a running tool session to the bus (PTY proxy wrapper, tmux pane I/O harness, or native API adapter),
- emits events for user input + model output + errors/status (as available),
- receives bus requests (send/admin) and enforces receiver-side gates before any injection/drive action,
- optionally runs planner/scheduler/router locally, but can also be driven by a separate orchestrator client.

Key property: **the bridge/agent is the receiver-side place where policy gates and audit hooks live** (the bus centralizes routing/policy, but receivers must re-check).

#### How workspaces talk to the bus (practical)

In most integrations, the *coding tool* does not speak the bus protocol. The bridge does:
- Bridge ↔ bus: structured IPC (Unix socket + NDJSON envelopes) using `bus.register.*`, `bus.subscribe.*`, `bus.send.*`, `bus.admin.*`, `bus.lock.*`, etc.
- Bridge ↔ tool: one of:
  - **tmux pane I/O** (`pipe-pane`/`capture-pane` + `send-keys`/`paste-buffer`) for terminal-only tools,
  - **PTY proxy** (wrap the tool process and read/write the PTY directly),
  - **native API/plugin** (best fidelity when the tool supports it).

If you want the model inside a coding tool to act as a controller (manage other workspaces, issue approvals, query logs), expose a bus client surface it can call:
- **MCP**: run a local MCP server that exposes “bus actions” as tools (preferred when the host tool supports MCP).
- **CLI**: provide `tmuxcoder` subcommands that call the bus (works when the host tool can run shell commands/tools).

Either way, these controller calls still produce bus events, still go through permissions + gates, and are still audited.

### 5.2 Orchestrator engine

The orchestrator engine is a set of composable modules:
- **Planner**: converts an objective into a plan (steps, dependencies, expected artifacts).
- **Scheduler**: chooses ordering, concurrency, timeouts, retries, and “race vs consensus” strategy.
- **Router**: dispatches work to workers/backends and manages input/output envelopes.

Implementation note: Planner/Scheduler/Router can run:
- inside a workspace bridge/agent (common), or
- in a dedicated headless process (advanced, optional).

Control does not have to be “human at the keyboard”. Any workspace can act as a **controller workspace** for other workspaces by emitting `bus.send.*` / `bus.admin.*` requests, handling gates, and issuing interrupts/cancel. The controller’s “policy” is simply the current task’s directives (constraints, stop conditions, preferences) carried in those messages; TmuxCoder itself should avoid hardcoding policy beyond enforcing permissions and mandatory gates.

### 5.3 Worker endpoints

A “worker” is anything that can accept tasks and return results:
- a backend session (e.g., Claude Code session, OpenCode session),
- a tool runner (MCP tool host),
- a specialized internal worker (diff reviewer, test runner, lint runner),
- a remote endpoint (future).

Workers must advertise:
- **capabilities** (streaming, patch proposals, tool use, context size),
- **constraints** (cost, latency, max concurrency),
- **identity** (stable name + tags/role labels),
- **policy surface** (what actions require approval).

#### MCP fit (where it belongs)

MCP (Model Context Protocol) fits in two places:

1. **Tool execution worker** (MCP tool host)
   - Treat an MCP tool host as a worker endpoint that can run tools (filesystem, git, issue tracker, etc.) on behalf of an orchestrator task.
   - Tool use is privileged and should integrate with the same gate/audit model (who called what tool, with what scope, and why).

2. **Bus control gateway** (bus actions exposed as MCP tools)
   - Expose a curated set of bus operations as MCP tools so LLM-driven sessions can *explicitly* call into the bus without text-parsing hacks.
   - Example tool families:
     - discovery: `list_workspaces`, `get_workspace_status`
     - messaging: `send_to_inbox`, `admin_request`
     - gates: `list_pending_approvals`, `approve`, `deny`
     - safety: `locks_acquire`, `locks_release`, `cancel_operation`, `interrupt_workspace`
     - observability: `logs_tail`, `logs_query` (permission/redaction-aware)
   - Each MCP call should translate into one or more bus events (request + result) with stable `correlation_id`/`operation_id` so everything remains debuggable and auditable.

### 5.4 Backend adapters

Adapters are *translation layers* that normalize heterogeneous CLIs/servers into a shared event protocol:
- request/response envelopes,
- streaming chunk formats,
- cancellation semantics,
- capability negotiation (feature flags),
- error taxonomy.

To mitigate the “adapter treadmill”:
- enforce **strict version gating** (declare supported backend versions; fail early with a clear error),
- define a minimal set of **stable primitives** to rely on (stream text, list sessions, send prompt, cancel),
- degrade gracefully when unsupported (feature disappears instead of breaking the workspace).

Adapters should be treated as “unstable edge” code and heavily contract-tested.

#### Drive modes and capabilities (v1)

Each workspace bridge SHOULD advertise a `drive_mode` so the orchestrator/UI don’t assume features that aren’t real:

- `tmux_io`: attach to an already-running pane via `pipe-pane`/`capture-pane` + `send-keys` (best-effort; weak readiness + cancel)
- `pty`: proxy the tool’s PTY (better streaming/cancel; still heuristic readiness)
- `native`: tool-specific API/plugin (best fidelity when available)

Capability guidance:
- `tmux_io`: `can_stream=true` (noisy), `can_cancel=weak`, `can_detect_readiness=weak`, `can_capture_structured=false`
- `pty`: `can_stream=true`, `can_cancel=medium`, `can_detect_readiness=medium`, `can_capture_structured=limited`
- `native`: `can_stream=true`, `can_cancel=strong`, `can_detect_readiness=strong`, `can_capture_structured=true` (when supported)

### 5.5 Event protocol v1 (MVP)

To keep multi-model orchestration reliable under concurrency, retries, and streaming, we need a **normative event contract** shared across:
- tmux UI clients ↔ bus (views, approvals, operator controls),
- workspace bridges/agents ↔ bus (workspace registration + cross-workspace messaging),
- bridges/agents ↔ backend adapters (PTY proxy or native APIs).

This does not force all transports to be identical, but it does force **stable semantics**.

#### Transport and framing

MVP recommendation:
- Unix sockets + **NDJSON** (one JSON object per line) for streaming-friendly, debuggable framing.
- One common envelope schema across all surfaces; the `payload` differs by event type.

#### Schema, fixtures, and versioning (v1)

To avoid drift between bus/bridge/ui implementations:
- define JSON Schema for the envelope + each `type` payload (and generate code where feasible)
- keep golden fixtures for each event type (request/response/deliver variants)
- define `payload_hash` canonicalization once (e.g., RFC 8785) and add conformance fixtures across languages
- versioning rule of thumb: additive fields are allowed within `tmuxcoder.event.v1`; breaking changes require a new `proto`

#### Required envelope fields (v1)

Every event MUST include:
- `proto`: `"tmuxcoder.event.v1"`
- `event_id`: ULID (recommended) or UUID (globally unique)
- `ts`: RFC3339 timestamp
- `workspace_uid`: UUID workspace context (stable across restarts; see workspace identity). For bus-global events, use `00000000-0000-0000-0000-000000000000`.
- `source`: `{ kind: "ui"|"workspace"|"orchestrator"|"service"|"bus"|"backend", id: string }` (principal / emitter)
- `type`: event type string (enum)
- `payload`: type-specific object (see below)

And SHOULD include when applicable:
- `workspace_id`: operator-friendly alias string (may change; see workspace identity)
- `dest`: `{ kind, id }`
- `operation_id`: ties into the operation stack (`queued → running → done|failed|canceled|gated`)
- `task_id`: ties into the task graph (DAG)
- `correlation_id`: end-to-end trace id for one logical workflow/intent
- `parent_event_id`: immediate causal parent (the event that directly triggered this event)
- `in_reply_to`: request event id this event is responding to (when applicable)
- `payload_hash`: `sha256` of canonical payload bytes (enables audit-by-hash + dedupe)

Notes:
- If you use JSON for payloads, define “canonical bytes” explicitly (e.g., RFC 8785 JSON canonicalization) so hashes are stable.
- `event_id` enables idempotent retry handling and bus dedupe windows.
- `workspace_uid` is the stable join key for persistence/audit and indicates *attachment context* (it is not necessarily “who sent it”).
- If you want sortability everywhere, standardize on ULID for `event_id`, `gate_id`, and `lockset_token` and use UUID specifically for long-lived identities like `workspace_uid`.

#### Correlation and causation (v1)

`event_id` is the identity of a single event. To stitch multiple events into an explainable end-to-end story (across panes, daemons, bus, and backends), we use three additional correlation fields:

- `correlation_id` (trace): identifies one logical workflow/intent across components (often “one user action”, “one orchestration run”, or “one cross-workspace request”). It MUST be preserved end-to-end and is the primary key for joining logs across processes.
- `parent_event_id` (causation): identifies the *direct* trigger for this event. Use it to reconstruct a causal tree (or chain) of what happened and why.
- `in_reply_to` (request/response): identifies the request event being answered by this event (used for explicit replies/acks/results).

Propagation rules (recommended defaults):
- **Ingress**: if a UI/control event arrives without `correlation_id`, the first handling component (UI client, bridge/agent, or orchestrator) SHOULD set `correlation_id := event_id` and then propagate it unchanged.
- **Derivation**: if an event is emitted as a direct result of another event, set `parent_event_id := <triggering event_id>` and copy `correlation_id`.
- **Requests**: request-like events SHOULD set `correlation_id` and omit `in_reply_to`.
- **Responses**: response-like events SHOULD set `in_reply_to := <request event_id>` and also copy `correlation_id`.
- **Cross-workspace**: receiver workspaces MUST preserve the original `correlation_id` even when they allocate new local `operation_id`/`task_id` for their own execution and UI rendering.

Relationship to other ids:
- `operation_id` is a *workspace-local* UI/concurrency handle (“what the operator sees running”).
- `task_id` is a *plan-local* DAG node identifier (“which step in the plan”).
- `correlation_id` is *end-to-end* across components/workspaces (“which overall flow this belongs to”).

Concrete correlation examples (field intent, not full schemas):
- Backend streaming: `ui.run_command(E1,C1)` → `backend.send(E2,C1,parent=E1)` → `backend.stream.delta(E3,C1,parent=E2,reply=E2,seq=1)` → `backend.stream.end(E4,C1,parent=E2,reply=E2)`
- Cross-workspace inbox: `bus.send.request(EA,CA)` → `bus.send.deliver(EB,CA,parent=EA)` → receiver `ui.approval.request(EC,CA,parent=EB)` → `ui.approval.respond(ED,CA,parent=EC,reply=EC)`

#### Addressing model (v1)

The event envelope supports optional addressing via:
- `dest`: `{ kind, id }`

In v1, we treat addressing as a *routing* concern (how something gets delivered) and keep it strictly tmux-independent:

Allowed `dest.kind` (recommended MVP set):
- `workspace`: another workspace (cross-workspace routing)
- `principal`: a bus-registered client (UI, orchestrator, or workspace bridge) for replies/results
- `backend`: a backend adapter/endpoint (usually implied by operation state; include only when needed)

Canonical workspace addressing rule (MUST for cross-workspace):
- when `dest.kind == "workspace"`, `dest.id` MUST be the destination `workspace_uid` (UUID)
- tmux-derived `workspace_id` (alias) MUST NOT be used as the routing identifier in `dest.id`

Principal addressing rule (MUST for replies/results):
- when `dest.kind == "principal"`, `dest.id` MUST be the bus-registered principal id (opaque string; typically `source.id`)

Operator-friendly aliases:
- users may refer to destinations by `workspace_id` (alias) in UI commands/pickers
- senders MUST resolve aliases to `workspace_uid` (via bus registry or local workspace metadata) before emitting any cross-workspace request

Bus events MUST be unambiguous:
- `bus.send.request` and `bus.admin.request` MUST set `dest.kind="workspace"` and `dest.id=<workspace_uid>`
- the corresponding delivery events SHOULD include human-friendly aliases in the payload for display (e.g., `payload.from_workspace_id` and optionally `payload.to_workspace_id`) and MUST treat `workspace_uid`/`dest.id` as authoritative

Bus envelope `workspace_uid` guidance (recommended):
- for `*.request` events emitted by a `source.kind=="workspace"`, `workspace_uid` SHOULD be the sender’s `workspace_uid` (i.e., `source.id`)
- for `*.request` events emitted by non-workspace principals (UI/orchestrator/service), `workspace_uid` SHOULD usually be the global context (`00000000-0000-0000-0000-000000000000`) unless the event is explicitly “about” a specific workspace
- for `*.deliver` events emitted by the bus into a destination workspace, `workspace_uid` SHOULD be the destination `workspace_uid` (so persistence/UI attach the event to the receiver workspace)
  - interpretation: `workspace_uid` indicates **which workspace context should attach/store the event**, not “who sent it”; use `payload.from` / `payload.reply_to` / `dest` for sender/receiver identity

Minimal cross-workspace payload conventions (v1) (field intent, not full schemas):
- `bus.send.request.payload`: `{ to_workspace_uid, to_workspace_id?, body, summary? }`
- `bus.send.deliver.payload`: `{ from: { kind, id }, from_workspace_id?, reply_to: { kind: "principal", id }, body, summary? }`
- `bus.admin.request.payload`: `{ to_workspace_uid, to_workspace_id?, action, params, summary? }`
- `bus.admin.deliver.payload`: `{ from: { kind, id }, from_workspace_id?, reply_to: { kind: "principal", id }, action, params, summary? }`
- `bus.send.result.deliver.payload`: `{ from_workspace_uid, from_workspace_id?, in_reply_to_request_event_id, decision, scope?, decided_ts, note? }`
- `bus.admin.result.deliver.payload`: `{ from_workspace_uid, from_workspace_id?, in_reply_to_request_event_id, action, state, result_ts, error?, note? }`

#### Cross-workspace message body schema (v1)

Cross-workspace messaging should be explicit about what is being sent so we can enforce size limits, audit by hash, and render safely in the receiver inbox.

`body` is a tagged union with a stable `kind`:

```json
{ "kind": "text", "text": "short UTF-8 text" }
```

```json
{ "kind": "artifact_ref", "artifact_ref": { "store": "shared", "sha256": "<hex>", "size_bytes": 123, "mime": "text/plain", "created_ts": "<rfc3339>" } }
```

```json
{ "kind": "task_ref", "task_ref": { "workspace_uid": "<uuid>", "task_id": "<id>", "label": "optional" } }
```

Norms (recommended):
- `body.kind` MUST be present.
- `text` bodies SHOULD be small (prefer ≤ 8KB); if larger, write an artifact and send `artifact_ref`.
- `summary` (when present) SHOULD be short (prefer ≤ 200 chars) and SHOULD avoid sensitive content; it is intended for inbox list rendering.
- the receiver MUST treat all cross-workspace `body` content as untrusted input; nothing executes automatically.

#### Artifact references (v1)

Artifacts are the escape hatch for payloads that exceed message size limits or should be stored/audited as immutable blobs (patches, logs, summaries, images, etc.).

Artifact reference payload (minimum viable):

```json
{
  "store": "shared|workspace",
  "store_workspace_uid": "uuid (required when store==workspace)",
  "sha256": "hex",
  "size_bytes": 12345,
  "mime": "text/plain",
  "created_ts": "2026-01-15T18:20:00Z",
  "label": "optional display name"
}
```

Storage rules (recommended):
- artifact refs MUST NOT carry arbitrary filesystem paths; the path is derived from `{store, sha256}` to prevent path traversal and accidental disclosure.
- shared store root (recommended): `~/.tmuxcoder/state/artifacts/sha256/<aa>/<sha256>`
- workspace store root (recommended): `~/.tmuxcoder/state/workspaces/<workspace_uid>/artifacts/sha256/<aa>/<sha256>`
- when `store == "workspace"`, `store_workspace_uid` MUST be included and indicates which workspace directory to resolve against.
- receivers SHOULD verify `sha256` when reading an artifact (integrity check) and treat missing artifacts as a recoverable error.

Lifecycle rules (recommended):
- artifacts SHOULD be append-only and content-addressed (sha256 as the canonical id).
- artifacts SHOULD have a retention policy (time/size based) and MAY be garbage-collected if not referenced by any persisted orchestration state/audit entry.

#### Bus request/response and delivery semantics (v1)

MVP intent: a local, in-memory router that delivers only to **connected** destinations (no durable queueing by default).

Request/response:
- every bus request event SHOULD receive a corresponding response event back to the caller:
  - `bus.register.response` to `bus.register.request`
  - `bus.unregister.response` to `bus.unregister.request`
  - `bus.list_workspaces.response` to `bus.list_workspaces.request`
  - `bus.send.response` to `bus.send.request`
  - `bus.admin.response` to `bus.admin.request`
  - `bus.send.result.response` to `bus.send.result.request` (receiver disposition/result notifications)
  - `bus.admin.result.response` to `bus.admin.result.request` (receiver disposition/result notifications)
- responses MUST set `in_reply_to := <request event_id>` and SHOULD copy `correlation_id`.
- on failure, responses SHOULD include an `error` object following the error taxonomy (`kind`, `code`, `retryable`, `message`).

Response payloads (minimum viable):
```json
{ "status": "ok" }
```

```json
{ "status": "error", "error": { "kind": "policy|transport|protocol", "code": "<code>", "retryable": false, "message": "<message>" } }
```

Recommended response shapes:
- `bus.register.response.payload`: `{ status, connection_id, registered_source, registered_workspace_uid?, bus_version?, session_token? }`
- `bus.unregister.response.payload`: `{ status }`
- `bus.list_workspaces.response.payload`: `{ status, workspaces: [{ workspace_uid, workspace_id, label?, status_summary? }] }` (MUST respect `read` permissions)
- `bus.send.response.payload`: `{ status, delivered, deliver_event_id?, error? }`
- `bus.admin.response.payload`: `{ status, delivered, deliver_event_id?, error? }`
- `bus.send.result.response.payload`: `{ status, delivered, deliver_event_id?, error? }`
- `bus.admin.result.response.payload`: `{ status, delivered, deliver_event_id?, error? }`

Delivery behavior (recommended):
- the bus MUST only deliver to destinations currently registered/connected (workspaces and principals); if the destination is not connected, reply with an error (do not queue).
- on successful delivery attempt, the bus SHOULD reply to the sender with `bus.*.response` including `delivered: true` and `deliver_event_id`.
- the bus MUST emit `bus.*.deliver` to the destination and SHOULD set:
  - `correlation_id` copied from the request
  - `parent_event_id := <request event_id>` (lets receivers dedupe and explain causality)

UX semantics when `DEST_NOT_CONNECTED` (recommended):
- show an explicit “destination offline” state (not a silent failure)
- offer “wait for connect and retry” (subscribe to registry/heartbeat and retry once)
- optionally offer “save as draft” (persist `body` as an `artifact_ref` locally) or “choose a different workspace”

Idempotency and dedupe (recommended):
- the bus SHOULD maintain a short in-memory dedupe window keyed by `(source.kind, source.id, request.event_id)`:
  - duplicates SHOULD return the same response (same `deliver_event_id`)
  - duplicates SHOULD NOT cause multiple deliveries
- receiver inboxes SHOULD dedupe deliveries by `(payload.from.kind, payload.from.id, parent_event_id)` to avoid duplicate inbox items on retries.

Ordering:
- the bus SHOULD preserve delivery order for events sent over the same sender connection.
- ordering between different senders is not defined and must not be relied on.

#### Subscription model and backpressure (v1)

The bus is a pub/sub event stream; clients SHOULD explicitly subscribe with filters (rather than implicitly receiving everything).

Recommended bus APIs:
- `bus.subscribe.request` / `bus.subscribe.response` (establish or update a subscription)
- `bus.unsubscribe.request` / `bus.unsubscribe.response` (drop a subscription)

Recommended subscription knobs:
- filters: `workspace_uids?`, `types?`, `include_global?` (where global is `workspace_uid=00000000-0000-0000-0000-000000000000`)
- replay: `live|replay_then_live` (cursor-based when available)
- redaction: `metadata|full` (bus enforces permissions and may downgrade to metadata)
- streaming: `include_stream_deltas` (default off)

Backpressure stance (recommended):
- the bus MUST enforce per-connection limits (no unbounded buffering)
- if a client can’t keep up, the bus MAY drop `backend.stream.delta` while still delivering `backend.stream.end` and operation state
- journaling streaming deltas SHOULD be opt-in; default to end summaries + hashes

#### Sender-visible disposition/results (v1)

Transport-level delivery acks (`bus.*.response`) are not the same as **receiver decisions** (accept/deny) in the destination inbox. To make inbox outcomes visible to the original sender, destination bridges/agents SHOULD emit result notifications back to the sender via the bus.

Event families:
- `bus.send.result.request` / `bus.send.result.response` / `bus.send.result.deliver`
- `bus.admin.result.request` / `bus.admin.result.response` / `bus.admin.result.deliver`

Norms (recommended):
- result requests MUST set `dest.kind="principal"` and `dest.id=<reply_to principal id>` (from `bus.*.deliver.payload.reply_to.id`).
- result requests MUST include `in_reply_to_request_event_id := <original request event_id>` (the `event_id` of `bus.send.request` or `bus.admin.request`).
- result requests SHOULD copy `correlation_id` from the original request.
- result delivery events (`bus.*.result.deliver`) SHOULD also set `in_reply_to := <original request event_id>` in the **envelope** (in addition to including `in_reply_to_request_event_id` in the payload).
- best-effort delivery: if the original sender principal is not connected, the bus MUST return `DEST_NOT_CONNECTED` and MUST NOT queue.

`bus.send.result.request.payload` (minimum viable). `bus.send.result.deliver.payload` SHOULD include the same fields plus `from_workspace_uid` and optional `from_workspace_id` for display:
```json
{
  "in_reply_to_request_event_id": "01J2F3N4Q5R6S7T8U9V0W1X2Y3",
  "decision": "accepted|denied|expired",
  "scope": "once|session",
  "decided_ts": "2026-01-15T18:26:10Z",
  "note": "optional short operator note"
}
```

`bus.admin.result.request.payload` (minimum viable). `bus.admin.result.deliver.payload` SHOULD include the same fields plus `from_workspace_uid` and optional `from_workspace_id` for display:
```json
{
  "in_reply_to_request_event_id": "01J2F3N4Q5R6S7T8U9V0W1X2Y3",
  "action": "orchestrator.request_review",
  "state": "accepted|denied|expired|executed|failed",
  "result_ts": "2026-01-15T18:27:00Z",
  "error": { "kind": "policy|transport|backend|timeout|protocol|canceled", "code": "POLICY_DENY", "retryable": false, "message": "optional" },
  "note": "optional short operator note"
}
```

Semantics (recommended):
- `state=accepted|denied|expired` communicates the receiver inbox decision.
- if `state=accepted`, the destination MAY later emit a second `bus.admin.result.*` with `state=executed|failed` when the action finishes.
- result notifications MUST NOT cause automatic execution in the sender; they are for visibility/audit/UI state.

#### Admin action namespace (v1)

`bus.admin.*` exists to request **structured orchestration actions** in another workspace. It is intentionally constrained.

Recommended v1 allowlist (unknown actions MUST be rejected):
- `orchestrator.request_plan` → `params: { objective, constraints?, deliverables? }`
- `orchestrator.request_review` → `params: { artifact_ref, question?, rubric? }`
- `orchestrator.interrupt` → `params: { kind: "cancel_operation|sigint", operation_id?, reason? }`

Deferred (recommended to add later, after the spine is stable):
- `orchestrator.enqueue_task` → `params: { title, role?, body, expected_outputs? }`
- `orchestrator.gate_decide` → `params: { gate_id, decision: "approve|deny", scope: "once|session", reason? }`
- `orchestrator.set_session_settings` → `params: { directives?, approval_policy? }`

Norms (recommended):
- `admin` is always two-phase (sender confirm + receiver accept). Receiver acceptance must happen before any action executes.
- even after acceptance, destination bridges/agents SHOULD apply local policy checks and MAY insert additional gates (e.g., “run tests”, “apply patch”).

#### Minimal event types (v1)

UI client → bridge/agent (direct or via bus):
- `ui.submit_text` (free text to active backend/session)
- `ui.run_command` (slash command invocation + args)
- `ui.cancel_operation` (cancel `operation_id`)
- `ui.approval.respond` (approve/deny with scope: once/session; may be issued by a human UI client *or* an authorized controller workspace)
- `ui.select_backend` (switch active backend/session)

Bridge/agent or orchestrator → UI client:
- `ui.log.append` (append-only log text/structured chunks)
- `ui.status.update` (backend/session + cost/usage + health)
- `ui.operation.update` (operation lifecycle updates)
- `ui.approval.request` (render gate with context + options; used for plan/apply gates and cross-workspace inbox items)
- `ui.notify` / `ui.error`

Bridge/agent ↔ backend adapter (normalized):
- `backend.capabilities` (version + feature flags)
- `backend.send` (send prompt/task to backend)
- `backend.stream.delta` (streaming chunk)
- `backend.stream.end` (explicit stream termination + final stats)
- `backend.cancel`
- `backend.error`

Bridge/agent ↔ bus:
- `bus.register.request` / `bus.register.response`
- `bus.unregister.request` / `bus.unregister.response`
- `bus.list_workspaces.request` / `bus.list_workspaces.response`
- `bus.subscribe.request` / `bus.subscribe.response`
- `bus.unsubscribe.request` / `bus.unsubscribe.response`
- `bus.lock.acquire.request` / `bus.lock.acquire.response`
- `bus.lock.release.request` / `bus.lock.release.response`
- `bus.lock.heartbeat.request` / `bus.lock.heartbeat.response`
- `bus.lock.list.request` / `bus.lock.list.response`
- `bus.lock.changed` (broadcast informational)
- `bus.send.request` / `bus.send.response` / `bus.send.deliver` (cross-workspace send; delivered to receiver inbox by default)
- `bus.admin.request` / `bus.admin.response` / `bus.admin.deliver` (cross-workspace admin request; receiver acceptance required)
- `bus.send.result.request` / `bus.send.result.response` / `bus.send.result.deliver` (receiver disposition/result notification back to sender)
- `bus.admin.result.request` / `bus.admin.result.response` / `bus.admin.result.deliver` (receiver disposition/result notification back to sender)
- `bus.error`

#### Approval and gate semantics (v1)

A gate is a first-class pause point that blocks an `operation_id` until an authorized approver (human UI or authorized controller principal) makes a decision.

`ui.approval.request` (bridge/agent or orchestrator → UI client) payload (minimum viable):
```json
{
  "gate_id": "01J2F3N4Q5R6S7T8U9V0W1X2Y3",
  "gate_kind": "plan_approve|cross_workspace_send_confirm|cross_workspace_send_accept|cross_workspace_admin_confirm|cross_workspace_admin_accept|file_lock|patch_apply|merge_commit|escalation",
  "prompt": "human-readable question",
  "summary": "short context for the operator",
  "scope_options": ["once", "session"],
  "default_scope": "once",
  "expires_ts": "2026-01-15T18:25:00Z",
  "approval_cache_key": "optional stable key used for session caching"
}
```

Normative behavior (recommended):
- `ui.approval.request` SHOULD include `operation_id` in the envelope and transition that operation to state `gated`.
- the request event SHOULD carry `correlation_id` and set `parent_event_id` to the event that triggered the gate.

`ui.approval.respond` (UI client → bridge/agent or orchestrator) payload (minimum viable):
```json
{
  "gate_id": "01J2F3N4Q5R6S7T8U9V0W1X2Y3",
  "decision": "approve|deny",
  "scope": "once|session",
  "reason": "optional operator note"
}
```

Response requirements:
- `ui.approval.respond` MUST set `in_reply_to := <ui.approval.request event_id>`
- `ui.approval.respond` SHOULD copy `correlation_id` and set `parent_event_id := <ui.approval.request event_id>`

Session caching (when `scope == "session"`):
- bridges/agents MAY cache approvals by `approval_cache_key` for the lifetime of the process
- caching MUST NOT bypass receiver acceptance for cross-workspace `admin` actions (two-phase stays two-phase)

Session preapproval (explicit opt-in):
- here, “session” means the lifetime of the controlling client (UI or controller principal) attached to the bus; it is distinct from the backend’s canonical conversation/session id
- a control session MAY be configured to auto-approve selected `gate_kind` values (or all gates) and respond immediately to `ui.approval.request`
- this is a convenience feature to enable “hands-off” orchestration runs; it must be visible in the UI/status and must still be audited (the decision should be attributable via the event `source`)
- preapproval should be configurable per session (runtime) and may have a config default/preset; it should be easy to turn off mid-run
- suggested shape (one of many valid designs): `approval_policy := { mode: "manual|auto_allowlist|auto_all", auto_approve_gate_kinds?: [gate_kind...] }`

Recommended `approval_cache_key` construction:
- the gating component (bridge/agent or orchestrator) SHOULD compute this key (not the UI) and include it in the request payload
- keys SHOULD be stable across restarts only if you intentionally persist them (default: in-memory session cache only)
- example shapes:
  - sender confirm: `xws.send.confirm:src=<source.kind>:<source.id>:dst=<dest_workspace_uid>`
  - receiver accept: `xws.send.accept:src=<source.kind>:<source.id>:dst=<workspace_uid>`
  - admin confirm: `xws.admin.confirm:src=<source.kind>:<source.id>:dst=<dest_workspace_uid>`
  - admin accept: `xws.admin.accept:src=<source.kind>:<source.id>:dst=<workspace_uid>`

#### Streaming semantics (v1)

For streaming responses:
- include `stream_id` and a monotonic `seq` per `(operation_id, stream_id)`
- emit `backend.stream.delta` events for chunks and exactly one `backend.stream.end`
- `backend.stream.end` SHOULD include final stats when available (tokens, cost, latency)
- streaming events SHOULD also include `in_reply_to := <backend.send event_id>` (or the equivalent request event) and propagate `correlation_id`
- streaming deltas SHOULD NOT be journaled or broadcast by default; make them opt-in via subscription and an explicit capture policy

#### Cancellation semantics (v1)

Cancellation MUST be explicit and observable:
- `ui.cancel_operation` targets an `operation_id`
- adapters SHOULD map that to backend-native cancellation when possible
- every canceled operation must transition to a terminal state: `canceled` or `cancel_failed` (never “silent cancel”)

#### Error taxonomy (v1)

All errors should map to:
- `kind`: `policy|transport|backend|timeout|protocol|canceled`
- `code`: stable string (for UI + tests)
- `retryable`: boolean
- `message`: human-readable summary
- errors SHOULD set `in_reply_to` when they are a response to a specific request event

Recommended `code` values (minimal v1 enum; expand only when needed):
- `DEST_NOT_CONNECTED` (`kind=transport`, `retryable=true`): destination not currently registered/connected.
- `POLICY_DENY` (`kind=policy`, `retryable=false`): denied by permissions or a mandatory gate.
- `PAYLOAD_TOO_LARGE` (`kind=protocol`, `retryable=false`): payload exceeds max bytes (use `artifact_ref`).
- `RATE_LIMITED` (`kind=policy`, `retryable=true`): sender exceeded bus/daemon rate limits.
- `LOCK_BUSY` (`kind=policy`, `retryable=true`): requested lock(s) are currently held by another workspace/operation.
- `LOCK_TOKEN_INVALID` (`kind=protocol`, `retryable=false`): invalid or unknown lock token.
- `LOCK_EXPIRED` (`kind=timeout`, `retryable=true`): lock lease expired before action completed.
- `BAD_SCHEMA` (`kind=protocol`, `retryable=false`): required fields missing/invalid types.
- `UNKNOWN_ACTION` (`kind=protocol`, `retryable=false`): `bus.admin.*` action not in the allowlist.
- `VERSION_UNSUPPORTED` (`kind=backend|protocol`, `retryable=false`): adapter/backend version not supported.

### 5.6 Backend independence (no direct backend↔backend)

Backends should never talk directly to each other. All cross-backend collaboration is mediated by bridges/agents + the bus (and is disabled by default unless explicitly allowed).

---

## 6. The Orchestration Lifecycle (Plan → Execute → Evaluate)

### 6.1 Lifecycle states

1. **Intent**: user describes objective (free text or command).
2. **Plan**: produce a plan (task graph) with explicit steps + dependencies.
3. **Gate (HITL)**: user approves plan (optional but strongly recommended for autonomous mode).
4. **Execute**: dispatch tasks to workers; collect streams/results.
5. **Evaluate**: review results (automatic checks + reviewer worker + HITL).
6. **Apply/Commit**: apply patches / merge work (always gated in practice).
7. **Postmortem**: store audit metadata, decisions, and summaries.

### 6.2 Task graph model (minimum viable)

Represent a plan as a DAG of tasks:
- `id`, `title`, `role`, `inputs`, `expected_outputs`
- `deps` (task ids)
- `constraints` (timeout, max retries, worker tags)
- `gates` (approval required, review required)

This aligns with:
- spec-driven pipelines (CodeMachine),
- state machines/playbooks (Pied-Piper),
- racing/consensus (Every Code) by allowing multiple tasks to compete for the same expected output.

### 6.3 Operation stack (interactive concurrency + cancellation)

Interactive CLIs hit a common failure case: the user (or orchestrator) issues new work while a backend is still streaming.

Recommended model:
- every user/orchestrator action creates an `operation_id`
- operations transition through `queued → running → done|failed|canceled|gated`
- the UI exposes an “operation stack” (what’s running, what’s blocked on approval, what’s pending)
- cancellation is explicit and consistent across adapters (best-effort cancel semantics)

### 6.4 Concurrency control (file locking for shared working trees)

When two workspaces write the same file concurrently, you can get corruption, lost updates, or “heisenbugs” that are hard to attribute. TmuxCoder needs a cooperative file locking system so orchestration stays deterministic under parallelism.

Core rules (recommended v1):
- any component that mutates files under a `project_root` MUST acquire a write lockset before writing (apply patch, edit file, rename, delete)
- locks are scoped to a `project_uid := sha256(realpath(project_root))` and keyed by normalized relative paths
- lock acquisition MUST be atomic across multiple paths (all-or-nothing) to avoid partial locks and deadlocks
- locks MUST be leases with TTL; the bus MUST release on disconnect or expiry
- long-running refactors SHOULD use per-workspace git worktrees instead of holding locks for minutes

Important limitations (don’t oversell):
- locks are cooperative; external editors, git hooks, and background formatters will ignore them
- within TmuxCoder, make locks hard to bypass accidentally (wrap all mutating executors) and obvious when bypassed
- consider adding directory/glob/repo-wide locks later (for refactors touching many files, `go fmt ./...`, `git checkout`, etc.)

#### Lock keys and normalization

- `project_root` SHOULD be the git root if available; else the current working directory.
- `project_uid` MUST be computed from `realpath(project_root)` to avoid symlink aliasing (and to keep absolute paths out of routine audit logs).
- all `paths` MUST be relative to `project_root`, must be `filepath.Clean`’d, and MUST NOT contain `..` after cleaning.
- the bus MUST canonicalize and sort `paths` before acquisition so ordering does not depend on the caller.

#### Lock modes

Minimal v1 can be write-only (exclusive). If we add read locks later, they should behave like standard shared/exclusive locks:
- `mode=write`: exclusive; blocks any other lock on the same path
- `mode=read` (optional): shared; blocks `write`

#### Bus lock protocol (v1)

New bus API surface (minimum viable):
- `bus.lock.acquire.request` / `bus.lock.acquire.response`
- `bus.lock.release.request` / `bus.lock.release.response`
- `bus.lock.heartbeat.request` / `bus.lock.heartbeat.response`
- `bus.lock.list.request` / `bus.lock.list.response`
- `bus.lock.changed` (broadcast informational for UI)

`bus.lock.acquire.request.payload` (minimum viable):
```json
{
  "project_uid": "sha256:9b3c3a0c0a7c5d7e7c9a8b7c6d5e4f3a2b1c0d9e8f7a6b5c4d3e2f1a0b9c8d7",
  "mode": "write",
  "paths": ["src/foo.go", "README.md"],
  "lease_ms": 60000,
  "reason": "apply_patch"
}
```

`bus.lock.acquire.response.payload` when granted:
```json
{
  "granted": true,
  "lockset_token": "01J2F3N4Q5R6S7T8U9V0W1X2Y3",
  "lease_expires_ts": "2026-01-15T18:30:00Z",
  "normalized_paths": ["README.md", "src/foo.go"]
}
```

When denied (`LOCK_BUSY`), the response SHOULD include enough conflict info for a human to make a decision:
```json
{
  "granted": false,
  "error": { "kind": "policy", "code": "LOCK_BUSY", "retryable": true, "message": "one or more paths are locked" },
  "conflicts": [
    {
      "path": "src/foo.go",
      "holder": { "workspace_uid": "5f8a2c0a-0f07-4c9c-9f8a-0e2a6f2e2d12", "operation_id": "op_123", "lease_expires_ts": "2026-01-15T18:30:00Z" }
    }
  ]
}
```

Lock release (payload):
```json
{ "lockset_token": "01J2F3N4Q5R6S7T8U9V0W1X2Y3" }
```

Heartbeat/extend (payload):
```json
{ "lockset_token": "01J2F3N4Q5R6S7T8U9V0W1X2Y3", "extend_ms": 60000 }
```

Norms (recommended):
- the bus MUST bind `lockset_token` to the requesting `workspace_uid` and reject release/heartbeat from other workspaces (`POLICY_DENY` or `LOCK_TOKEN_INVALID`).
- `lease_ms` MUST be clamped to a configured maximum (e.g., 5 minutes); longer holds should use worktree isolation.
- the bus SHOULD broadcast `bus.lock.changed` for acquire/release/expiry so UIs can render “who is blocking what”.

#### Apply/commit integration (minimum viable)

Locks prevent concurrent writes, but they do not prevent *stale patches*. Patch/apply should be treated as:
1. compute affected file list,
2. acquire lockset,
3. re-check preconditions (hashes/mtimes) for affected files,
4. apply change,
5. release lockset.

If preconditions fail after locks are acquired, the operation SHOULD fail fast and request a patch regen or manual merge (don’t “best-effort apply” into a changed file).

#### UI behavior on lock contention

If `bus.lock.acquire` returns `LOCK_BUSY`, the bridge/orchestrator SHOULD transition the operation to `gated` and emit a `ui.approval.request` with `gate_kind=file_lock` offering:
- wait/retry (with backoff),
- cancel,
- (optional, privileged) force release (always explicit + audited).

#### Optional OS-level lock files (defense in depth)

To tolerate bus restarts or accidental multiple bus instances, an apply executor MAY also take an OS-level `flock` on per-path lock files while it holds the bus lockset:
- lock file root: `~/.tmuxcoder/locks/<project_uid>/`
- lock file name: stable hash of the normalized relative path (e.g., `sha256(<path>).lock`)

This is still advisory (external editors won’t respect it), but it makes “two TmuxCoder writers without a shared bus” much harder to do by accident.

---

## 7. Multi-Model Strategies (How We Actually Use Multiple Models)

### 7.1 Role-based specialization (default)

The baseline “3-role loop” is:
- **Planner**: writes plan/spec and acceptance criteria.
- **Coder**: implements changes.
- **Reviewer**: critiques and proposes fixes.

This maps cleanly to both:
- a manager framework (autonomous), and
- a workflow DSL (deterministic).

### 7.2 Racing (parallel proposals)

Use when:
- problem is ambiguous,
- solution space is large,
- we want alternatives quickly.

Mechanism:
- schedule N parallel “proposal” tasks (possibly different models/backends),
- collect outputs into an evaluation stage,
- pick one (HITL or scored heuristics).

### 7.3 Consensus (parallel + merge)

Use when:
- correctness matters more than speed,
- we can define a clear scoring rubric (tests, static analysis, reviewers).

Mechanism:
- multiple workers implement/check,
- a consensus stage merges or selects based on passing checks + reviewer approval.

### 7.4 Deterministic playbooks (repeatable pipelines)

Use when:
- you want the same sequence repeatedly (e.g., “new microservice scaffold”),
- you want audit/replay and predictable HITL points.

Mechanism:
- a workflow file defines steps, dependencies, and approval nodes.

---

## 8. HITL (Human-in-the-Loop) Gates

We explicitly model “stopping points” where automation must wait for operator approval.

### 8.1 Common gates

- **Plan approval**: “Do you agree with this plan and scope?”
- **Cross-workspace send**: “Do you allow Workspace A to send a message/task to Workspace B’s inbox?”
- **Cross-workspace admin accept**: “Do you allow Workspace A to request an orchestration/admin action in Workspace B?” (two-phase; receiver must accept)
- **Patch apply**: “Do you apply these diffs?”
- **Merge/commit**: “Do you commit / merge?”
- **Escalated actions**: any action that expands capability surface (e.g., enabling tool use, enabling cross-comm, enabling shell access).

### 8.2 Gate implementation guidelines

- Gates must be:
  - **visible** (UI shows pending gate + what it blocks),
  - **scoped** (approve once vs approve for session),
  - **audited** (who approved what, when).
- Gate decisions may be manual or automatic (session preapproval); either way, gated actions should only proceed after an explicit approval decision exists in the event stream.
- Default bias:
  - `read` can be config-only,
  - `send` should prompt by default (in the sending workspace), and deliver to a receiver **inbox** (not direct injection),
  - `admin` should require a two-phase gate (sender confirmation + receiver acceptance),
  - `apply/merge` should always gate in practice.

---

## 9. Policy, Safety, and Auditability

### 9.1 Trust boundaries

- Pane output is **untrusted data** (prompt injection risk).
- Cross-worker content is **untrusted by default** unless explicitly allowed.
- Tool execution is **privileged** and must be gated.
- Hard invariant: untrusted text (pane output or model output) MUST NOT directly trigger cross-workspace sends/admin, gate decisions, or privileged tool execution; only explicit operator actions or explicit tool calls can do that (and they still go through gates + audit).

### 9.2 Permission levels (recommended)

Hierarchical permission levels for cross-workspace/backends (evaluated as `source principal → destination workspace`):
- `none`: no visibility, no sending.
- `read`: presence + minimal metadata only (workspace label/id + high-level status), **not** transcripts or file/artifact contents.
- `send`: can send a message/task (implies `read`).
- `admin`: can request orchestration actions (implies `send`).

Note: if you want to share transcript excerpts, diffs, or other rich content, treat it as `send` (deliver to receiver inbox) and prefer `artifact_ref` for anything large or sensitive.

Permissions should support wildcard matching for principals and destination workspaces (`source → destination`):
- source principal matchers SHOULD support:
  - exact: `<source.kind>:<source.id>` (for a workspace principal, `source.kind=="workspace"` and `source.id == workspace_uid`)
  - kind-wide: `<source.kind>:*` (e.g., `ui:*`, `orchestrator:*`)
- destination workspace matchers SHOULD support:
  - `*` matches all workspaces
  - and (optionally) `workspace_id` wildcards, depending on your chosen `workspace_id` format:
  - if `workspace_id := tmux:<session_id>:<window_id>`: `tmux:$<session_id>:*` matches a tmux session; `tmux:$<session_id>:@<window_id>` matches a single workspace
  - if `workspace_id := <session_name>:<window_id>`: `<session_name>:*` matches a tmux session; `<session_name>:@<window_id>` matches a single workspace

For tmux-independent, long-lived rules, permissions SHOULD also support exact matching by `workspace_uid` (stable UUID). `workspace_uid` matches should be considered more specific than any `workspace_id` wildcard.

When multiple rules match, the most specific rule should win (`workspace_uid` exact > `workspace_id` exact > session wildcard > global wildcard). Ties should be treated as invalid configuration.

Confirmation behavior:
- `read` should be controlled purely by config (no runtime prompts).
- `send` should be configurable to require sender confirmation (default: prompt in the sending principal’s UI surface).
- `send` delivery should default to **receiver inbox**, where the receiver can decide to forward into its own input/backend.
- `admin` must be **two-phase**: sender confirms the request, and the **receiving** workspace must accept before any admin action executes.
- approvals should support “approve once” and “approve for this session”, scoped to `source → destination + action` (applies to both sender-side and receiver-side prompts).

### 9.2.1 Policy directives (typed, with merge rules)

“Policy as directives carried as messages” works best if the directives have a small typed core with explicit defaults.

Recommended minimal policy object (v1):
- `approval_policy` (manual vs session auto-approvals)
- `cross_workspace_policy` (defaults + allow/deny rules for `source → destination`)
- `artifact_capture_policy` (what gets journaled inline vs by hash vs as artifacts; whether to capture stream deltas)
- `locks_policy` (lease defaults, max TTL, whether force-release exists)

Directive sources and merge rules (recommended):
- sources: global config → project config → control-session runtime settings → per-operation/per-task directives
- merge: later/more-specific sources override earlier ones; unknown keys are ignored (but should be logged)
- if two equally-specific rules conflict, treat as invalid configuration (fail fast)
- only authorized principals (typically those with `admin`) should be able to set runtime directives for another workspace

#### Receiver inbox (cross-workspace)

Default behavior (safe, debuggable):
- all cross-workspace deliveries land in a **receiver inbox** (pane or popup), not as direct input injection
- inbox items show: `source principal`, `action type`, a short summary, and `payload_hash`
- receiver can `accept` / `deny` / `accept for session`; the destination daemon SHOULD notify the sender via `bus.send.result.*` / `bus.admin.result.*` (best-effort if the sender is still connected)
- accepting/denying is audited (request + decision + payload hash)

Semantics:
- `send`: receiver may forward accepted content into its own input/backend, or keep it as a note (implementation choice); nothing runs automatically.
- `admin`: always a **request**, never an immediate effect; receiver acceptance is required before any orchestration/admin action executes.

Optional convenience knobs (explicit opt-in, off by default):
- sender-side “approve for session” caching is allowed for repeated `send/admin` requests, but **never removes receiver acceptance for `admin`**
- `send` auto-forwarding into the receiver’s backend is allowed only if the receiver explicitly enables it for a specific source principal (still audited)

Policy scope should be intentionally coarse-grained (no per-pane/per-command ACLs initially): either a destination is allowed at `read|send|admin` or it isn’t.

### 9.3 Logging, journaling, and audit model

We want logs that are:
- useful for operators (debug + forensics),
- queryable by `correlation_id` / `operation_id` / `task_id`,
- safe by default (don’t accidentally become a transcript database),
- compatible with the permission model (metadata visibility ≠ content sharing).

#### 9.3.1 Log types (v1)

1. **Operational logs** (component-internal)
   - writers: bus, bridge/agent, orchestrator
   - purpose: debugging crashes, adapter failures, perf issues
   - format: structured logs (`logfmt` or JSON); not relied on for policy decisions

2. **Event journal** (canonical orchestration record)
   - writer: bus server (centralized)
   - format: NDJSON of `tmuxcoder.event.v1` envelopes
   - contents: registrations, routing decisions, lock lifecycle, approvals, task graph + operation state transitions, backend send/cancel, and (optionally) stream end summaries
   - payload policy:
     - keep `payload` small (prefer ≤ 8KB; see artifact references)
     - for large/sensitive content, persist `payload_hash` + `artifact_ref` (capture is explicit opt-in)
     - default: do **not** journal `backend.stream.delta`; treat full streams as opt-in capture

3. **Audit log** (privileged action ledger)
   - writer: bus server (and/or the receiver bridge as a defense-in-depth mirror)
   - format: `logfmt` (default) or `jsonl`
   - default: metadata + `payload_hash` only (no full payload unless explicitly enabled)
   - audited events include:
     - cross-workspace sends/admin requests,
     - approvals and denials,
     - task graph creation/modification,
     - patch apply/merge actions,
     - file-lock force release (if implemented).

4. **Shadow I/O capture** (optional; “what the human saw/sent”)
   - writer: bridge/agent (PTY proxy) or bus-derived from events
   - purpose: local search/export/replay without claiming canonical history
   - storage: excerpts and/or artifacts referenced from the journal

#### 9.3.2 Persistence roots (recommended defaults)

- `RUN_DIR`: `$XDG_RUNTIME_DIR/tmuxcoder` (preferred) or `~/.tmuxcoder/run/`
- `STATE_DIR`: `~/.tmuxcoder/state/` (future: `$XDG_STATE_HOME/tmuxcoder/`)

#### 9.3.3 On-disk layout (recommended)

```text
~/.tmuxcoder/state/
  journal/
    manifest.json
    events.20260117T120000Z.ndjson
    events.20260117T130000Z.ndjson
  audit/
    audit.logfmt
    audit.logfmt.20260117T140500Z
  logs/
    bus.log
    bus.log.20260117T140500Z
    bridge.<workspace_uid>.log
  indexes/              # optional, rebuildable
    journal.sqlite
  artifacts/
    sha256/<aa>/<sha256>
  workspaces/<workspace_uid>/
    meta.json
    ... (task graphs, gate cache, etc)
```

Notes:
- journal/audit/logs should be rotatable (size or time); filenames should be timestamped (no renumbering).
- artifacts are content-addressed; retention/GC is policy-driven.

#### 9.3.4 Journal segmentation and rotation

- The journal should be written as append-only segments to avoid unbounded files.
- Rotation triggers (pick one or both):
  - size-based (`max_bytes`)
  - time-based (`max_duration`, e.g. hourly)
- `manifest.json` SHOULD record for each segment:
  - filename
  - start/end timestamps (best-effort)
  - first/last `event_id` (for quick seek)
  - optional query hints (best-effort, for fast segment skipping):
    - `workspaces_present: [workspace_uid...]` (or a bloom filter) when feasible
    - `types_present: [type...]` when feasible
    - `approx_event_count`
  - sha256 of the segment file (optional, for integrity checks)

#### 9.3.5 Querying logs (live + historical)

Live:
- any client can subscribe to the bus event stream (with filters + redaction enforced by the bus); this powers tmux panes like “Log”, “Inbox”, “Approvals”, and “Locks”.
- live tails SHOULD be served from the bus’s in-memory stream and only fall back to disk for replay/backfill.

Historical:
- the bus should expose query endpoints that read from persisted segments (and optionally an index).
- clients SHOULD NOT scrape files directly in normal operation; querying through the bus keeps permission/redaction rules consistent.

Efficient query strategy (without an index):
1. Use `manifest.json` to pick only segments that overlap `[since_ts, until_ts]` (and optionally match `workspace_uid` / `types` via hints).
2. Scan the selected NDJSON segment files linearly, filter in-process, and stop once `limit` matches are found.
3. Return an opaque cursor so the client can request the next page without restarting the scan.

Minimum viable bus API (names illustrative):
- `bus.journal.query.request` / `bus.journal.query.response`
- `bus.journal.tail.request` / `bus.journal.tail.response` (replay from cursor, then live)
- `bus.audit.query.request` / `bus.audit.query.response`

`bus.journal.query.request.payload` (example):
```json
{
  "workspace_uid": "5f8a2c0a-0f07-4c9c-9f8a-0e2a6f2e2d12",
  "since_ts": "2026-01-17T00:00:00Z",
  "until_ts": "2026-01-17T23:59:59Z",
  "types": ["ui.approval.request", "bus.lock.acquire.request"],
  "correlation_id": "01J2F3N4Q5R6S7T8U9V0W1X2Y3",
  "operation_id": "op_123",
  "limit": 500,
  "cursor": "opaque_cursor",
  "redaction": "metadata"
}
```

Response SHOULD include:
- `events: [...]` (envelopes, possibly redacted)
- `next_cursor` (for pagination)
- `truncated` (true if more matches exist)

Cursor semantics (recommended):
- `next_cursor` is opaque, but should encode enough to resume efficiently (e.g., `{segment, byte_offset, last_event_id}`).
- `bus.journal.tail` should accept a cursor for “replay from here” and then seamlessly switch to live stream.

CLI ergonomics:
- `tmuxcoder logs tail ...` should call `bus.journal.tail`.
- `tmuxcoder logs query ...` should call `bus.journal.query` and print NDJSON for `jq`/`rg`.

#### 9.3.6 Redaction and permission-aware queries

The bus must not turn `read` permission into “free transcript access”:
- `read` queries should return **metadata only** by default (envelope fields + `payload_hash`), omitting large/secret-bearing payloads.
- sharing rich content (diffs, excerpts, artifacts) should be treated as `send` and delivered via inbox with explicit acceptance.
- `admin` tooling may allow richer queries, but should still support `redaction=metadata` for safe dashboards.

#### 9.3.7 Indexing (optional, rebuildable)

For MVP, scanning NDJSON segments is acceptable. If journal size grows:
- maintain an optional SQLite index keyed by common joins: `workspace_uid`, `type`, `ts`, `correlation_id`, `operation_id`, `task_id`
- store enough location info to fetch the full event from the segment (`segment`, `byte_offset`) without duplicating payloads
- queries become:
  1. indexed lookup returning a small list of matching `(segment, byte_offset)` rows (ordered by `ts`, then `event_id`)
  2. `seek` into the segment file, read one NDJSON line, parse the envelope, and apply redaction
- the index must be rebuildable from `journal/` segments (delete index → reindex)

### 9.4 “No tmux as bus” rationale (why this matters)

Using tmux as the **cross-workspace communication fabric** (“send keys”, “paste buffer”, “copy-mode”) breaks:
- policy enforcement (hard to intercept),
- auditing (hard to prove what happened),
- correctness (racey / timing dependent),
- portability (pane layouts/ids change),
- safety (accidental “command injection” paths).

Therefore:
- orchestration uses structured IPC and typed messages,
- tmux is allowed (and often preferred) as the bridge’s I/O harness for terminal-only backends, but it is not the router/policy/audit source of truth.

---

## 10. Context, Memory, and State

### 10.1 “Source of truth” rule

If a backend owns the canonical transcript (most CLIs do), the orchestrator should not try to duplicate it wholesale.

Recommended approach:
- store **references** to backend session IDs,
- store **summaries** and **hashes** of important artifacts,
- optionally store **small excerpts** needed for retrieval/debugging.

### 10.2 Context manager responsibilities

A context manager can exist without becoming a transcript store:
- maintain “working set” context per task (what files, diffs, decisions),
- enforce per-worker context budgets,
- generate concise handoffs (“task brief” → worker),
- keep a retrieval index for summaries/excerpts (optional vector store).

### 10.3 Restart and resumability

To make orchestration robust:
- task graphs and their states should be persisted (queued/running/succeeded/failed/gated),
- worker assignments and `correlation_id`/`operation_id`/`task_id` should be stored,
- the UI should be able to reattach and show pending gates.

### 10.4 Shadow log (optional, not canonical)

This is the optional “shadow I/O capture” sink described in §9.3:
- record what the operator saw/sent as lightweight events (plus hashes), not a backend transcript clone
- persist per-workspace under `~/.tmuxcoder/state/workspaces/<workspace_uid>/` and reference large bodies via `artifact_ref`

---

## 11. Configuration Model (Practical)

### 11.1 Core config (YAML + overrides)

Baseline conventions:
- global: `~/.tmuxcoder/config.yaml`
- project override: `.tmuxcoder/config.yaml` (overrides global)
- keys: `snake_case` (config ergonomics)
- backend IDs: `kebab-case` (map keys under `backends:`)

Core config should cover:
- backends (enablement + endpoints)
- supported versions (version gating)
- monitoring (status surface)
- prompts + commands directories (and override rules)
- layout preset + view selection
- workspace bus (enable + socket selection)
- cross-backend / cross-workspace permissions + confirmations
- gate/approval defaults (preapproval presets, required gates)
- logging + audit configuration

Optional: a first-run setup wizard can auto-discover likely local servers, validate connectivity/version compatibility, and write a baseline `~/.tmuxcoder/config.yaml`.

### 11.2 Worker registry (roles + routing) (optional)

Workers may be declared inline under `orchestration.workers` or in a dedicated file (e.g. `.tmuxcoder/workers.yaml`). Example shape:

```yaml
orchestration:
  workers:
    planner:
      role: planner
      tags: [spec, decomposition]
      backend: gemini
      caps: [plan, summarize]

    coder_a:
      role: coder
      tags: [go, refactor]
      backend: claude-code
      caps: [implement, patch_propose, stream]

    reviewer:
      role: reviewer
      tags: [review, critique]
      backend: opencode
      caps: [review, diff_summarize]
```

### 11.3 Prompts (static, layered)

Prompts should be plain text files (no templating), layered by concatenation:
- global: `~/.tmuxcoder/prompts/`
- project override: `.tmuxcoder/prompts/`
- typical: `system.txt`, `<backend>/system.txt`, optional `sessions/<session-id>/system.txt`

### 11.4 Commands (templated, reusable)

User-defined commands should be YAML files whose `template` is rendered with Handlebars:
- global: `~/.tmuxcoder/commands/`
- project override: `.tmuxcoder/commands/`
- partials: `commands/partials/` (project overrides global)

Commands act as orchestration “macros” (repeatable objectives that compile into prompts/tasks).

### 11.5 workflows/ (optional deterministic orchestration)

Workflow files define:
- steps/tasks,
- dependency edges,
- gates (`approval` nodes),
- routing constraints.

This lets “Boosters” patterns exist without hardcoding logic.

### 11.6 CLI + env binding (how processes attach)

We need a consistent way to bind:
- a tmux pane (where the tool is running),
- a stable `workspace_uid` (identity),
- and a bus socket (IPC).

Recommended conventions (names illustrative):

- Global flags/env:
  - `--config <path>` (default: `~/.tmuxcoder/config.yaml`)
  - `--bus-socket <path>` (default: `$XDG_RUNTIME_DIR/tmuxcoder/bus.sock` or `~/.tmuxcoder/run/bus.sock`)
  - `--state-dir <path>` (default: `~/.tmuxcoder/state/`)
  - env fallbacks: `TMUXCODER_BUS_SOCKET`, `TMUXCODER_STATE_DIR`, `TMUXCODER_WORKSPACE_UID`, `TMUXCODER_PROJECT_ROOT`

- Bus lifecycle:
  - `tmuxcoder bus start` (or autostart on first connect)
  - `tmuxcoder bus status`

- UI client:
  - `tmuxcoder ui` with tmux attachment args (session/window selection) and a non-interactive mode to skip prompts.

- Bridge/agent (per workspace):
  - `tmuxcoder bridge attach --adapter tmux-pane`:
    - binds to a tmux pane (`--tmux-pane %7` or `$TMUX_PANE`)
    - derives/loads `workspace_uid` (or accepts `--workspace-uid <uuid>`)
    - registers on the bus and begins emitting events / accepting gated drive actions
  - `tmuxcoder bridge wrap --adapter pty`:
    - launches the tool under a PTY proxy so the bridge can read/write without tmux

- MCP gateway (optional):
  - `tmuxcoder mcp serve --transport stdio|unix`:
    - exposes selected bus actions as MCP tools so LLM-driven sessions can call `send/approve/locks/logs` explicitly.

The key requirement is: whatever the transport, every controller/bridge process must be able to unambiguously identify itself (`workspace_uid`) and its target (a tmux pane, backend session, or other handle) so events are attributable and gates/audit are enforceable.

---

## 12. Testing Strategy (Hybrid)

Testing should not require real third-party servers to be running:
- **contract tests** per backend adapter (shapes + streaming parsing + cancel semantics)
- **recorded fixtures** to replay streaming output deterministically in CI
- **small live integration tests** (optional) against real local servers to detect drift

High-risk areas worth targeted tests early:
- adapter version gating + capability negotiation
- operation stack and cancellation consistency
- cross-workspace permission resolution + approval caching
- lock manager atomicity + expiry/reattach behavior
- audit log output (format + rotation + payload hashing)

---

## 13. Suggested PoC Blueprint (Smallest End-to-End Demo)

A minimal proof-of-concept that exercises the architecture:

1. **Manager workspace** with orchestrator enabled.
2. **Three workers** (planner/coder/reviewer) as separate sessions or separate backends.
3. **Plan gate**: show plan in popup; user approves.
4. **Execute**: coder implements, reviewer critiques.
5. **Apply gate + locks**: present patch summary; on approval, acquire file lockset and apply.
6. **Audit**: record approvals + payload hashes.

This validates:
- routing,
- HITL,
- policy enforcement,
- audit logs,
- parallelism boundaries (even if only 2-way parallel).

---

## 14. Appendix A: Mapping Research Projects → Reusable Patterns

This is not a feature checklist; it’s “what to steal”.

- **CodeMachine** → spec-to-task-graph + explicit plan/implement/review loop.
- **Pied-Piper** → deterministic playbooks/state machines; editable nodes as HITL.
- **Every Code** → racing/consensus as a first-class orchestration strategy.
- **Auto-Claude** → worktree-based isolation for parallel changes; manual merge gate.
- **Maestro** → multi-process supervision; unified monitoring UI.
- **Claude-Flow** → skill registry + policy routing + explicit manual gates.
- **Oh-my-opencode / opencode-config** → lightweight “swarm worker” configs to simulate multi-agent without a heavy manager.

---

## 15. Appendix B: Capability Matrix (Target State)

| Dimension | Target behavior |
|---|---|
| Division template | Roles (planner/coder/reviewer) + optional workflow steps |
| Parallelism | Racing + consensus + bounded concurrency |
| Isolation | Workspaces/sessions as isolation; optional worktrees for destructive ops |
| HITL | First-class approval nodes; scoped approvals; visible pending gates |
| Policy | Default deny; hierarchical permissions; centrally enforced |
| Auditability | Metadata + payload hashes by default; append-only rotation |
| Extensibility | Adapters for backends; workflow DSL; tool/plugin registry |
| Degradation | Capability negotiation; missing features disappear cleanly |
