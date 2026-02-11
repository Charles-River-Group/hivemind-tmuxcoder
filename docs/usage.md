# TmuxCoder 用户操作手册（单一二进制）

本项目交付一个二进制文件：`tmuxcoder`。以下是从“用户视角”的完整使用流程。

---

## 0. 准备与安装

你只需要一个可执行文件 `tmuxcoder`。

如果你从源码构建：
```sh
# 构建（输出到 dist/）
make build

# 或直接用 Go 构建
go build -o dist/tmuxcoder ./cmd/tmuxcoder
```

---

## 1. 你需要准备的两个 ID

1) `codex/claude session id`  
你要采集哪一个模型会话，就填它的 session id。

2) `tmux_session_id`  
这是你给“这次采集任务”起的名字，用于把同一批数据聚合在一起。  
建议每次开新的采集任务都换一个新的 id。

---

## 2. 一键启动（推荐）

这是最省心的方式：自动启动 bus + ingest + sink。

```sh
dist/tmuxcoder start \
  --codex-session <codex-session-id> \
  --tmux-session-id <tmux-session-id>
```

说明：
- `start` 默认启动 **bus + ingest + sink**。
- 会自动写入 `~/.tmuxcoder/current_session_id`，供 skills 自动读取。
- 如需 UI：加 `--ui`。
- 启动时会自动把 shared-context 规则写入 `AGENTS.md` / `CLAUDE.md`（可用 `--write-rules=false` 关闭）。

常用参数补充：
```sh
# 不写规则模板
dist/tmuxcoder start --write-rules=false

# 指定写入的规则文件
dist/tmuxcoder start --rules-files AGENTS.md,CLAUDE.md
```

---

## 3. 检查是否正常运行

查看实时日志：
```sh
dist/tmuxcoder logs tail --include-global
```

如果能看到 `[codex:user] ...` / `[codex:assistant] ...` 的输出，说明采集已生效。

你也可以使用状态命令快速检查：
```sh
dist/tmuxcoder status
```

如果希望在 tmux 中实时刷新（类似仪表盘）：
```sh
dist/tmuxcoder status --ui
```

---

## 4. 首次使用：一键安装 skill（必要）

当前 repo 下的 `skills/` 不会自动被 Codex / Claude Code 识别。  
推荐直接用内置命令安装：

```sh
dist/tmuxcoder skills install
```

常用选项：
```sh
# 只装到 Codex
dist/tmuxcoder skills install --target codex

# 只装到 Claude Code
dist/tmuxcoder skills install --target claude

# 指定 skills 源目录（比如二进制不在 repo 内时）
dist/tmuxcoder skills install --skills-dir /path/to/skills

# 覆盖已存在的 skills
dist/tmuxcoder skills install --force
```

如果已有 Codex / Claude Code 实例在运行，请重启以加载新 skill。

---

## 5. 读取 SQLite 上下文（给技能 / 模型用）

最简调用（默认按 tmux_session_id 聚合）：
```sh
python skills/sqlite-context/scripts/fetch_context.py \
  --source codex
```

如果需要指定 tmux_session_id：
```sh
python skills/sqlite-context/scripts/fetch_context.py \
  --source claude \
  --tmux-session-id <tmux-session-id>
```

如果需要只读取单个 model session：
```sh
python skills/sqlite-context/scripts/fetch_context.py \
  --source codex \
  --session-id <model-session-id>
```

---

## 5.1 方案B：强制模型自动调用 skill（推荐）

目标：让 Codex / Claude Code **每次回复前**都自动调用 `sqlite-context` skill，保证共享上下文生效。

做法：把下面模板加入你的模型“系统/规则指令”（具体文件取决于你使用的 CLI）。

**模板（通用）：**
```
Before answering any user message, you MUST call the sqlite-context skill to fetch shared context from SQLite.
Use tmux_session_id resolution order: flag -> TMUXCODER_SESSION_ID -> ~/.tmuxcoder/current_session_id.
If a session_id is not provided, fetch context across the entire tmux_session_id (shared context).
Then prepend the fetched context to the prompt and answer the user.
```

**放置位置（常见）：**
- Codex：项目级 `AGENTS.md` 或用户级指令配置
- Claude Code：项目级 `CLAUDE.md` 或用户级指令配置

> 若你的环境使用了不同的规则文件，请以实际为准，但指令内容保持一致即可。

可选参数：
- `--limit 50`：最近 N 条  
- `--max-chars 12000`：总字符上限  
- `--format json|text`：输出格式  

---

## 6. 高级用法：单独启动组件

当你想手动控制时：

```sh
# bus
dist/tmuxcoder bus

# ingest（Codex / Claude / 指定文件）
dist/tmuxcoder ingest --codex-session <session-id>
dist/tmuxcoder ingest --claude-session <session-id>
dist/tmuxcoder ingest --claude-project /Users/you/work/project
dist/tmuxcoder ingest --log-path /path/to/log.jsonl

# sink（SQLite）
dist/tmuxcoder sink --tmux-session-id <tmux-session-id>

# UI
dist/tmuxcoder ui
```

---

## 7. SQLite 数据结构（model_outputs）

默认库文件：`~/.tmuxcoder/model_outputs.db`

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

---

## 8. 常见问题

1) **重复启动 bus 会怎样？**  
如果 socket 已经存在，新的 bus 会直接退出，不会重复启动。

2) **重复启动 ingest 会怎样？**  
同一个 session 只能有一个 ingest 实例，重复启动会直接退出。

3) **为什么 skills 报 tmux_session_id 缺失？**  
你需要满足以下任一条件：  
- 启动时传了 `--tmux-session-id`  
- 或设置了 `TMUXCODER_SESSION_ID`  
- 或文件 `~/.tmuxcoder/current_session_id` 存在

4) **fetch_context 输出为空？**  
通常是 `session_id` 没写入（Codex 的 JSONL 可能不带 session_id）。  
解决：用 `--from-begin` 重新跑 ingest 或 `--sink-rebuild` 重建数据库，让新版本写入 session_id。

5) **如何确认 skills 读取过 SQLite？**  
每次调用 `fetch_context.py` 会写入审计表 `context_reads`：  
```sh
sqlite3 ~/.tmuxcoder/model_outputs.db "select * from context_reads order by id desc limit 5;"
```
字段说明（便于定位范围）：
- `raw_rows`：匹配条件的总行数  
- `selected_rows`：应用 `--limit` 后的行数  
- `merged_rows`：合并相邻角色后的行数  
- `first_id/last_id`：本次读取的行范围  
- `first_ts/last_ts`：本次读取的时间范围

6) **为什么 AGENTS.md / CLAUDE.md 被改动？**  
`start` 默认会自动写入 shared-context 规则模板，确保模型每次回复前调用 sqlite-context。  
如果不需要，可以用 `--write-rules=false` 关闭，或用 `--rules-files` 指定文件列表。

---

## 9. 帮助命令

```sh
dist/tmuxcoder --help
dist/tmuxcoder ingest --help
dist/tmuxcoder sink --help
dist/tmuxcoder start --help
dist/tmuxcoder skills install --help
dist/tmuxcoder status --help
```
