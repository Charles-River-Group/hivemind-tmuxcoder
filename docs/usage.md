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

最简调用（会自动读取 `~/.tmuxcoder/current_session_id`）：
```sh
python skills/sqlite-context/scripts/fetch_context.py \
  --session-id <model-session-id> \
  --source codex
```

如果需要指定 tmux_session_id：
```sh
python skills/sqlite-context/scripts/fetch_context.py \
  --session-id <model-session-id> \
  --source claude \
  --tmux-session-id <tmux-session-id>
```

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
