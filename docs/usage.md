# TmuxCoder 纯日志采集模式

本文仅说明“纯日志采集模式”的使用：通过 `tmuxcoder ingest` 把 Codex / Claude 的会话日志写入总线，再用 `tmuxcoder logs tail` 或 `tmuxcoder ui` 查看。

## 构建与打包

推荐用 Makefile：

```sh
# 构建单一二进制（输出到 dist/）
make build

# 打包为 tar.gz（包含 tmuxcoder）
make package
```

也可直接用 Go：

```sh
# 构建主二进制
go build ./cmd/...

# 单个构建示例
go build -o dist/tmuxcoder ./cmd/tmuxcoder
```

## 组件（纯日志采集）

交付只有一个二进制：`tmuxcoder`。功能通过子命令区分：

- `tmuxcoder bus`：消息总线服务
- `tmuxcoder ingest`：日志采集（Codex / Claude JSONL）
- `tmuxcoder sink`：SQLite 落盘
- `tmuxcoder logs tail`：查看日志
- `tmuxcoder ui`：tmux 日志窗口
- `tmuxcoder start`：一键启动（bus + ingest + sink，可选 ui）

## 启动顺序（纯日志采集）

```sh
# 1) 启动 bus
dist/tmuxcoder bus

# 2) 启动 ingest（示例：Codex session）
dist/tmuxcoder ingest --codex-session <session-id>

# 3) tail 日志（命令行）
dist/tmuxcoder logs tail --include-global

# 4) 或启动 tmux UI（只显示日志）
dist/tmuxcoder ui

# 5) 启动 SQLite sink（把日志落盘）
dist/tmuxcoder sink --db-path ~/.tmuxcoder/model_outputs.db

# 需要重建表时
dist/tmuxcoder sink --rebuild=true
```

## bus（总线）

```sh
# 默认 socket：$XDG_RUNTIME_DIR/tmuxcoder/bus.sock
# fallback：~/.tmuxcoder/run/bus.sock

dist/tmuxcoder bus

# 指定 socket
dist/tmuxcoder bus --socket /tmp/tmuxcoder/bus.sock
```

## ingest（采集 Codex / Claude 会话）

```sh
# 通过 Codex session id 查找 rollout.jsonl
dist/tmuxcoder ingest --codex-session <session-id>

# 通过 Claude session id 查找 jsonl
dist/tmuxcoder ingest --claude-session <session-id>

# 通过 Claude project（路径或名称）查找最新 session
dist/tmuxcoder ingest --claude-project /Users/you/work/project

# 直接指定 log 文件或目录
dist/tmuxcoder ingest --log-path /path/to/log.jsonl
dist/tmuxcoder ingest --watch /path/to/dir --watch /another/dir
```

常用过滤选项：

- `--from-begin`：从文件头开始读取
- `--project`：仅处理匹配项目路径的文件
- `--session-id`：仅处理指定 session ID 的内容
- `--socket` / `--workspace-uid` / `--label`：设置 bus 连接与事件标识

## logs tail（查看日志）

```sh
# tail 日志（默认订阅 ui.log.append 和 ui.status.update）
dist/tmuxcoder logs tail --socket /tmp/tmuxcoder/bus.sock --include-global

# 格式化输出（时间戳/级别）
dist/tmuxcoder logs tail --format

# 自定义事件类型
dist/tmuxcoder logs tail --types ui.log.append,ui.status.update
```

## ui（tmux 日志窗口）

```sh
# 启动 tmux UI（仅日志面板）
dist/tmuxcoder ui
```

## sink（SQLite 落盘）

```sh
# 默认路径：~/.tmuxcoder/model_outputs.db
dist/tmuxcoder sink

# 指定路径
dist/tmuxcoder sink --db-path /Users/user/.tmuxcoder/model_outputs.db

# 仅存 codex / claude
dist/tmuxcoder sink --sources codex,claude

# 重建表（清空旧数据）
dist/tmuxcoder sink --rebuild=true
```

## start（一键启动）

```sh
# 启动 bus + ingest + sink
dist/tmuxcoder start --codex-session <session-id>

# 启动 bus + ingest + sink + ui
dist/tmuxcoder start --codex-session <session-id> --ui

# 启动时重建 sink 表
dist/tmuxcoder start --codex-session <session-id> --sink-rebuild=true
```

## 参考

更多参数说明：

```sh
dist/tmuxcoder --help
dist/tmuxcoder logs tail --help
dist/tmuxcoder ingest --help
dist/tmuxcoder sink --help
dist/tmuxcoder start --help
```
