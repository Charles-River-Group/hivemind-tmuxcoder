package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	appbus "github.com/opencode/hivemind-tmuxcoder/internal/app/bus"
	appingest "github.com/opencode/hivemind-tmuxcoder/internal/app/ingest"
	appsink "github.com/opencode/hivemind-tmuxcoder/internal/app/sink"
	bridgeclient "github.com/opencode/hivemind-tmuxcoder/internal/bridge/client"
	"github.com/opencode/hivemind-tmuxcoder/internal/bus/server"
	"github.com/opencode/hivemind-tmuxcoder/internal/ingest/claim"
	"github.com/opencode/hivemind-tmuxcoder/internal/ui"

	_ "modernc.org/sqlite"
)

// StringSliceFlag is a flag that can be repeated.
type StringSliceFlag []string

func (s *StringSliceFlag) String() string {
	return strings.Join(*s, ",")
}

func (s *StringSliceFlag) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	switch os.Args[1] {
	case "bus":
		handleBus(ctx, os.Args[2:])
	case "context":
		handleContext(ctx, os.Args[2:])
	case "ingest":
		handleIngest(ctx, os.Args[2:])
	case "sink":
		handleSink(ctx, os.Args[2:])
	case "logs":
		handleLogs(ctx, os.Args[2:])
	case "skills":
		handleSkills(ctx, os.Args[2:])
	case "status":
		handleStatus(ctx, os.Args[2:])
	case "ui":
		handleUI(ctx, os.Args[2:])
	case "start":
		handleStart(ctx, os.Args[2:])
	default:
		fmt.Printf("Unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("Usage: tmuxcoder <command> [args]")
	fmt.Println("\nCommands:")
	fmt.Println("  bus                    Run bus server")
	fmt.Println("  context [flags]         Fetch shared context from SQLite")
	fmt.Println("  ingest [flags]          Run ingest")
	fmt.Println("  sink [flags]            Run SQLite sink")
	fmt.Println("  logs tail [flags]       Tail logs")
	fmt.Println("  skills install [flags]  Install skills into Codex/Claude Code")
	fmt.Println("  status [flags]          Show bus/ingest/sink status")
	fmt.Println("  ui [flags]              Launch tmux UI")
	fmt.Println("  start [flags]           Start bus+ingest+sink (optional ui)")
	fmt.Println("\nRun 'tmuxcoder <command> --help' for more information.")
}

func handleBus(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("bus", flag.ExitOnError)
	var socketPath string
	fs.StringVar(&socketPath, "socket", "", "Bus server socket path (default: auto-detect)")
	fs.Parse(args)

	if err := appbus.Run(ctx, appbus.Config{SocketPath: socketPath}); err != nil {
		fmt.Printf("Bus error: %v\n", err)
		os.Exit(1)
	}
}

func handleContext(ctx context.Context, args []string) {
	_ = ctx
	fs := flag.NewFlagSet("context", flag.ExitOnError)
	var (
		dbPath        string
		tmuxSessionID string
		sessionID     string
		source        string
		limit         int
		maxChars      int
		format        string
		debug         bool
	)

	fs.StringVar(&dbPath, "db-path", "", "SQLite DB path (default: ~/.tmuxcoder/model_outputs.db)")
	fs.StringVar(&tmuxSessionID, "tmux-session-id", "", "Tmuxcoder session ID (optional)")
	fs.StringVar(&sessionID, "session-id", "", "Model session ID (optional)")
	fs.StringVar(&source, "source", "", "Source to filter (codex|claude)")
	fs.IntVar(&limit, "limit", 50, "Max rows to keep (0 = all)")
	fs.IntVar(&maxChars, "max-chars", 12000, "Max characters in output (0 = unlimited)")
	fs.StringVar(&format, "format", "text", "Output format: text|json")
	fs.BoolVar(&debug, "debug", false, "Print resolved paths and env to stderr")
	fs.Parse(args)

	if source != "" && source != "codex" && source != "claude" {
		fmt.Println("context error: --source must be codex or claude")
		os.Exit(1)
	}
	if format != "text" && format != "json" {
		fmt.Println("context error: --format must be text or json")
		os.Exit(1)
	}

	resolvedTmuxID, err := resolveTmuxSessionID(tmuxSessionID)
	if err != nil {
		fmt.Printf("context error: %v\n", err)
		os.Exit(1)
	}

	resolvedDBPath, err := resolveDBPath(dbPath)
	if err != nil {
		fmt.Printf("context error: %v\n", err)
		os.Exit(1)
	}

	if debug {
		exe, _ := os.Executable()
		cwd, _ := os.Getwd()
		fmt.Fprintf(os.Stderr, "context debug:\n")
		fmt.Fprintf(os.Stderr, "  executable: %s\n", exe)
		fmt.Fprintf(os.Stderr, "  cwd: %s\n", cwd)
		fmt.Fprintf(os.Stderr, "  HOME: %s\n", os.Getenv("HOME"))
		fmt.Fprintf(os.Stderr, "  TMUXCODER_DB_PATH: %s\n", os.Getenv("TMUXCODER_DB_PATH"))
		fmt.Fprintf(os.Stderr, "  current_db_path: %s\n", readDBPathFile())
		fmt.Fprintf(os.Stderr, "  resolved_db_path: %s\n", resolvedDBPath)
		fmt.Fprintf(os.Stderr, "  resolved_tmux_session_id: %s\n", resolvedTmuxID)
	}

	if _, err := os.Stat(resolvedDBPath); err != nil {
		fmt.Printf("context error: db not found: %s\n", resolvedDBPath)
		os.Exit(1)
	}

	rows, err := readContextRows(resolvedDBPath, resolvedTmuxID, sessionID, source)
	if err != nil {
		readPath, cleanup, snapErr := snapshotDB(resolvedDBPath)
		if snapErr != nil {
			fmt.Printf("context error: %v\n", err)
			os.Exit(1)
		}
		if cleanup != nil {
			defer cleanup()
		}
		rows, err = readContextRows(readPath, resolvedTmuxID, sessionID, source)
		if err != nil {
			fmt.Printf("context error: %v\n", err)
			os.Exit(1)
		}
	}

	rawRows := len(rows)
	if limit > 0 && len(rows) > limit {
		rows = rows[len(rows)-limit:]
	}
	selectedRows := len(rows)
	var firstID, lastID int64
	var firstTS, lastTS string
	if len(rows) > 0 {
		firstID, firstTS = rows[0].id, rows[0].ts
		lastID, lastTS = rows[len(rows)-1].id, rows[len(rows)-1].ts
	}

	merged := mergeRoles(rows)
	merged = trimByChars(merged, maxChars)
	mergedRows := len(merged)

	if err := writeContextReadAudit(resolvedDBPath, resolvedTmuxID, sessionID, source, rawRows, selectedRows, mergedRows, firstID, lastID, firstTS, lastTS, limit, maxChars); err != nil {
		fmt.Fprintf(os.Stderr, "context audit warning: %v\n", err)
	}

	switch format {
	case "json":
		if err := writeContextJSON(os.Stdout, merged); err != nil {
			fmt.Printf("context error: %v\n", err)
			os.Exit(1)
		}
	default:
		writeContextText(os.Stdout, merged)
	}
}

func handleIngest(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("ingest", flag.ExitOnError)
	var (
		socket        string
		workspaceUID  string
		label         string
		watchDirs     StringSliceFlag
		logPath       string
		codexSession  string
		claudeSession string
		claudeProject string
		fromBegin     bool
		projectFilter string
		sessionFilter string
	)

	fs.StringVar(&socket, "socket", "", "Bus socket path (default: auto-detect)")
	fs.StringVar(&workspaceUID, "workspace-uid", "", "Workspace UID for events (default: auto-generate)")
	fs.StringVar(&label, "label", "agent:session", "Client label")
	fs.Var(&watchDirs, "watch", "Directory to watch (repeatable)")
	fs.StringVar(&logPath, "log-path", "", "Specific JSONL file path to watch (overrides --watch)")
	fs.StringVar(&codexSession, "codex-session", "", "Codex session ID to find and watch rollout.jsonl")
	fs.StringVar(&claudeSession, "claude-session", "", "Claude session ID to find and watch")
	fs.StringVar(&claudeProject, "claude-project", "", "Claude project path or name (watches latest session)")
	fs.BoolVar(&fromBegin, "from-begin", false, "Read from file start instead of end")
	fs.StringVar(&projectFilter, "project", "", "Only process files matching this project path")
	fs.StringVar(&sessionFilter, "session-id", "", "Only bind to this specific session ID (in JSONL content)")
	fs.Parse(args)

	cfg := appingest.Config{
		SocketPath:    socket,
		WorkspaceUID:  workspaceUID,
		Label:         label,
		WatchDirs:     watchDirs,
		LogPath:       logPath,
		CodexSession:  codexSession,
		ClaudeSession: claudeSession,
		ClaudeProject: claudeProject,
		FromBegin:     fromBegin,
		ProjectFilter: projectFilter,
		SessionFilter: sessionFilter,
	}

	if err := appingest.Run(ctx, cfg); err != nil {
		fmt.Printf("Ingest error: %v\n", err)
		os.Exit(1)
	}
}

func handleSink(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("sink", flag.ExitOnError)
	var (
		socketPath    string
		dbPath        string
		includeGlobal bool
		sourcesRaw    string
		rebuild       bool
		tmuxSessionID string
	)

	fs.StringVar(&socketPath, "socket", "", "Bus server socket path (default: auto-detect)")
	fs.StringVar(&dbPath, "db-path", "", "SQLite DB path")
	fs.StringVar(&tmuxSessionID, "tmux-session-id", "", "Tmuxcoder session ID for sink writes")
	fs.BoolVar(&includeGlobal, "include-global", false, "Include global bus events")
	fs.StringVar(&sourcesRaw, "sources", "codex,claude", "Comma-separated sources to keep (empty = all)")
	fs.BoolVar(&rebuild, "rebuild", false, "Drop and recreate model_outputs on start")
	fs.Parse(args)

	resolvedDBPath, err := resolveDBPath(dbPath)
	if err != nil {
		fmt.Printf("Sink error: %v\n", err)
		os.Exit(1)
	}
	if err := writeDBPathFile(resolvedDBPath); err != nil {
		fmt.Printf("Failed to write db path file: %v\n", err)
		os.Exit(1)
	}

	cfg := appsink.Config{
		SocketPath:    socketPath,
		DBPath:        resolvedDBPath,
		IncludeGlobal: includeGlobal,
		Sources:       parseCSV(sourcesRaw),
		Rebuild:       rebuild,
		TmuxSessionID: tmuxSessionID,
	}

	if err := appsink.Run(ctx, cfg); err != nil {
		fmt.Printf("Sink error: %v\n", err)
		os.Exit(1)
	}
}

func handleLogs(ctx context.Context, args []string) {
	if len(args) < 1 {
		fmt.Println("Usage: tmuxcoder logs <subcommand>")
		fmt.Println("\nSubcommands:")
		fmt.Println("  tail      Tail logs")
		os.Exit(1)
	}

	switch args[0] {
	case "tail":
		handleLogsTail(ctx, args[1:])
	default:
		fmt.Printf("Unknown logs subcommand: %s\n", args[0])
		os.Exit(1)
	}
}

func handleStatus(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	var (
		socketPath    string
		dbPath        string
		claimDir      string
		tmuxSessionID string
		watchInterval time.Duration
		clearScreen   bool
		uiEnabled     bool
		uiSession     string
		uiWindow      string
	)

	fs.StringVar(&socketPath, "socket", "", "Bus socket path (default: auto-detect)")
	fs.StringVar(&dbPath, "db-path", "", "SQLite DB path (default: ~/.tmuxcoder/model_outputs.db)")
	fs.StringVar(&claimDir, "claim-dir", "", "Ingest claim dir (default: ~/.tmuxcoder/run/ingest-claims)")
	fs.StringVar(&tmuxSessionID, "tmux-session-id", "", "Tmuxcoder session ID (optional)")
	fs.DurationVar(&watchInterval, "watch", 0, "Refresh interval (e.g. 2s)")
	fs.BoolVar(&clearScreen, "clear", false, "Clear screen on each refresh when --watch is set")
	fs.BoolVar(&uiEnabled, "ui", false, "Open tmux UI status window")
	fs.StringVar(&uiSession, "ui-session", "", "tmux session name (default: current or tmuxcoder)")
	fs.StringVar(&uiWindow, "ui-window", "tmuxcoder-status", "tmux window name")
	fs.Parse(args)

	if uiEnabled {
		if watchInterval <= 0 {
			watchInterval = 2 * time.Second
		}
		statusCfg := ui.StatusConfig{
			SessionName:   uiSession,
			WindowName:    uiWindow,
			SocketPath:    socketPath,
			DBPath:        dbPath,
			ClaimDir:      claimDir,
			TmuxSessionID: tmuxSessionID,
			Interval:      watchInterval,
			Clear:         true,
		}
		if err := ui.LaunchTmuxStatus(ctx, statusCfg); err != nil {
			fmt.Printf("Status UI error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if watchInterval > 0 {
		for {
			if ctx.Err() != nil {
				return
			}
			if clearScreen {
				fmt.Print("\033[H\033[2J")
			}
			printBusStatus(socketPath)
			fmt.Println()
			printIngestStatus(claimDir)
			fmt.Println()
			printSinkStatus(dbPath, tmuxSessionID)
			time.Sleep(watchInterval)
		}
	}

	printBusStatus(socketPath)
	fmt.Println()
	printIngestStatus(claimDir)
	fmt.Println()
	printSinkStatus(dbPath, tmuxSessionID)
}

func handleSkills(ctx context.Context, args []string) {
	if len(args) < 1 {
		fmt.Println("Usage: tmuxcoder skills <subcommand>")
		fmt.Println("\nSubcommands:")
		fmt.Println("  install   Install skills")
		os.Exit(1)
	}

	switch args[0] {
	case "install":
		handleSkillsInstall(ctx, args[1:])
	default:
		fmt.Printf("Unknown skills subcommand: %s\n", args[0])
		os.Exit(1)
	}
}

func handleSkillsInstall(ctx context.Context, args []string) {
	_ = ctx
	fs := flag.NewFlagSet("skills install", flag.ExitOnError)
	var (
		skillsDir string
		target    string
		codexDir  string
		claudeDir string
		force     bool
	)

	fs.StringVar(&skillsDir, "skills-dir", "", "Path to skills directory (empty = use embedded)")
	fs.StringVar(&target, "target", "both", "Install target: codex|claude|both")
	fs.StringVar(&codexDir, "codex-dir", "~/.codex/skills", "Codex skills directory")
	fs.StringVar(&claudeDir, "claude-dir", "~/.claude/skills", "Claude Code skills directory")
	fs.BoolVar(&force, "force", false, "Overwrite existing skills")
	fs.Parse(args)

	var targets []string
	switch target {
	case "codex":
		targets = []string{expandHome(codexDir)}
	case "claude":
		targets = []string{expandHome(claudeDir)}
	case "both":
		targets = []string{expandHome(codexDir), expandHome(claudeDir)}
	default:
		fmt.Printf("Invalid --target: %s (expected codex|claude|both)\n", target)
		os.Exit(1)
	}

	if skillsDir == "" {
		skillNames, err := listSkillDirsFS(embeddedSkills, "skills")
		if err != nil {
			fmt.Printf("Failed to read embedded skills: %v\n", err)
			os.Exit(1)
		}
		if len(skillNames) == 0 {
			fmt.Println("No embedded skills found")
			os.Exit(1)
		}
		for _, dst := range targets {
			if err := installSkillsFromFS(embeddedSkills, "skills", dst, skillNames, force); err != nil {
				fmt.Printf("Install failed: %v\n", err)
				os.Exit(1)
			}
		}
	} else {
		srcDir := expandHome(skillsDir)
		if _, err := os.Stat(srcDir); err != nil {
			fmt.Printf("Skills dir not found: %s\n", srcDir)
			os.Exit(1)
		}
		skillNames, err := listSkillDirs(srcDir)
		if err != nil {
			fmt.Printf("Failed to read skills dir: %v\n", err)
			os.Exit(1)
		}
		if len(skillNames) == 0 {
			fmt.Printf("No skills found in: %s\n", srcDir)
			os.Exit(1)
		}
		for _, dst := range targets {
			if err := installSkills(srcDir, dst, skillNames, force); err != nil {
				fmt.Printf("Install failed: %v\n", err)
				os.Exit(1)
			}
		}
	}

	fmt.Println("Skills installed successfully.")
}

func handleLogsTail(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("logs tail", flag.ExitOnError)
	var (
		socketPath    string
		workspaceUID  string
		includeGlobal bool
		format        bool
		eventTypes    string
	)

	fs.StringVar(&socketPath, "socket", "", "Bus server socket path (default: auto-detect)")
	fs.StringVar(&workspaceUID, "workspace", "", "Workspace UID to filter by (optional)")
	fs.BoolVar(&includeGlobal, "include-global", false, "Include global bus events")
	fs.BoolVar(&format, "format", false, "Enable formatted output (adds timestamps/colors)")
	fs.StringVar(&eventTypes, "types", "ui.log.append,ui.status.update", "Comma-separated list of event types")

	fs.Parse(args)

	renderMode := ui.RenderModeRaw
	if format {
		renderMode = ui.RenderModeFormatted
	}

	config := &ui.Config{
		SocketPath:    socketPath,
		IncludeGlobal: includeGlobal,
		Output:        os.Stdout,
		RendererMode:  renderMode,
		EventTypes:    strings.Split(eventTypes, ","),
	}

	if workspaceUID != "" {
		config.WorkspaceUIDs = []string{workspaceUID}
	}

	client := ui.NewClient(config)
	if err := client.Connect(ctx); err != nil {
		fmt.Printf("Failed to connect: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	if format {
		fmt.Println("Connected to bus. Tailing logs...")
	}

	if err := client.Tail(ctx); err != nil {
		if ctx.Err() == nil {
			fmt.Printf("Tail error: %v\n", err)
		}
	}
}

func handleUI(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("ui", flag.ExitOnError)
	var (
		socketPath    string
		sessionName   string
		windowName    string
		workspaceUID  string
		includeGlobal bool
		eventTypes    string
	)

	fs.StringVar(&socketPath, "socket", "", "Bus server socket path (default: auto-detect)")
	fs.StringVar(&sessionName, "session", "", "tmux session name (default: current or tmuxcoder)")
	fs.StringVar(&windowName, "window", "tmuxcoder", "tmux window name")
	fs.StringVar(&workspaceUID, "workspace", "", "Workspace UID to filter logs")
	fs.BoolVar(&includeGlobal, "include-global", false, "Include global bus events in logs")
	fs.StringVar(&eventTypes, "types", "ui.log.append,ui.status.update", "Comma-separated list of event types")

	fs.Parse(args)

	config := ui.TmuxConfig{
		SocketPath:    socketPath,
		SessionName:   sessionName,
		WindowName:    windowName,
		WorkspaceUID:  workspaceUID,
		IncludeGlobal: includeGlobal,
		EventTypes:    strings.Split(eventTypes, ","),
	}

	if err := ui.LaunchTmuxUI(ctx, config); err != nil {
		fmt.Printf("Failed to launch tmux UI: %v\n", err)
		os.Exit(1)
	}
}

func handleStart(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("start", flag.ExitOnError)
	var (
		socket        string
		watchDirs     StringSliceFlag
		logPath       string
		codexSession  string
		claudeSession string
		claudeProject string
		fromBegin     bool
		projectFilter string
		sessionFilter string
		sinkEnabled   bool
		sinkInclude   bool
		sinkDBPath    string
		sinkSources   string
		sinkRebuild   bool
		tmuxSessionID string
		uiEnabled     bool
		writeRules    bool
		rulesFiles    string
	)

	fs.StringVar(&socket, "socket", "", "Bus socket path (default: auto-detect)")
	fs.Var(&watchDirs, "watch", "Directory to watch (repeatable)")
	fs.StringVar(&logPath, "log-path", "", "Specific JSONL file path to watch (overrides --watch)")
	fs.StringVar(&codexSession, "codex-session", "", "Codex session ID to find and watch rollout.jsonl")
	fs.StringVar(&claudeSession, "claude-session", "", "Claude session ID to find and watch")
	fs.StringVar(&claudeProject, "claude-project", "", "Claude project path or name (watches latest session)")
	fs.BoolVar(&fromBegin, "from-begin", false, "Read from file start instead of end")
	fs.StringVar(&projectFilter, "project", "", "Only process files matching this project path")
	fs.StringVar(&sessionFilter, "session-id", "", "Only bind to this specific session ID (in JSONL content)")
	fs.BoolVar(&sinkEnabled, "sink", true, "Start SQLite sink")
	fs.BoolVar(&sinkInclude, "sink-include-global", false, "Include global bus events in sink")
	fs.StringVar(&sinkDBPath, "sink-db-path", "", "SQLite DB path for sink")
	fs.StringVar(&sinkSources, "sink-sources", "codex,claude", "Comma-separated sources to keep")
	fs.BoolVar(&sinkRebuild, "sink-rebuild", false, "Drop and recreate model_outputs on start")
	fs.StringVar(&tmuxSessionID, "tmux-session-id", "", "Tmuxcoder session ID for sink writes")
	fs.BoolVar(&uiEnabled, "ui", false, "Launch tmux UI")
	fs.BoolVar(&writeRules, "write-rules", true, "Write shared-context rules to AGENTS.md and CLAUDE.md")
	fs.StringVar(&rulesFiles, "rules-files", "AGENTS.md,CLAUDE.md", "Comma-separated rules files to write")
	fs.Parse(args)

	if writeRules {
		if err := writeRulesTemplates(parseCSV(rulesFiles)); err != nil {
			fmt.Printf("Failed to write rules: %v\n", err)
			os.Exit(1)
		}
	}

	for _, dir := range []string{"~/.codex/skills", "~/.claude/skills"} {
		if err := ensureSkillInstalled("sqlite-context", expandHome(dir)); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not install sqlite-context skill to %s: %v\n", dir, err)
		}
	}

	if tmuxSessionID != "" {
		_ = os.Setenv("TMUXCODER_SESSION_ID", tmuxSessionID)
		if err := writeSessionFile(tmuxSessionID); err != nil {
			fmt.Printf("Failed to write session file: %v\n", err)
			os.Exit(1)
		}
	}

	if sinkEnabled {
		resolvedDBPath, err := resolveDBPath(sinkDBPath)
		if err != nil {
			fmt.Printf("Failed to resolve db path: %v\n", err)
			os.Exit(1)
		}
		if err := writeDBPathFile(resolvedDBPath); err != nil {
			fmt.Printf("Failed to write db path file: %v\n", err)
			os.Exit(1)
		}
		sinkDBPath = resolvedDBPath
	}

	busCfg := appbus.Config{SocketPath: socket}
	errCh := make(chan error, 4)

	go func() {
		if err := appbus.Run(ctx, busCfg); err != nil {
			errCh <- err
		}
	}()

	socketPath := busSocketPath(socket)
	if err := waitForSocket(socketPath, 3*time.Second); err != nil {
		fmt.Printf("start error: %v\n", err)
		os.Exit(1)
	}

	ingestCfg := appingest.Config{
		SocketPath:    socket,
		WatchDirs:     watchDirs,
		LogPath:       logPath,
		CodexSession:  codexSession,
		ClaudeSession: claudeSession,
		ClaudeProject: claudeProject,
		FromBegin:     fromBegin,
		ProjectFilter: projectFilter,
		SessionFilter: sessionFilter,
	}
	go func() {
		if err := appingest.Run(ctx, ingestCfg); err != nil {
			errCh <- err
		}
	}()

	if sinkEnabled {
		sinkCfg := appsink.Config{
			SocketPath:    socket,
			DBPath:        sinkDBPath,
			IncludeGlobal: sinkInclude,
			Sources:       parseCSV(sinkSources),
			Rebuild:       sinkRebuild,
			TmuxSessionID: tmuxSessionID,
		}
		go func() {
			if err := appsink.Run(ctx, sinkCfg); err != nil {
				errCh <- err
			}
		}()
	}

	if uiEnabled {
		uiCfg := ui.TmuxConfig{}
		if socket != "" {
			uiCfg.SocketPath = socket
		}
		go func() {
			if err := ui.LaunchTmuxUI(ctx, uiCfg); err != nil {
				errCh <- err
			}
		}()
	}

	select {
	case <-ctx.Done():
		return
	case err := <-errCh:
		if err != nil {
			fmt.Printf("start error: %v\n", err)
			os.Exit(1)
		}
	}
}

func parseCSV(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

func busSocketPath(socketFlag string) string {
	if socketFlag != "" {
		return socketFlag
	}
	cfg := server.DefaultConfig()
	return cfg.SocketPath
}

func waitForSocket(path string, timeout time.Duration) error {
	if path == "" {
		return fmt.Errorf("socket path is empty")
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("bus socket not ready: %s", filepath.Clean(path))
}

type contextRow struct {
	id   int64
	ts   string
	role string
	text string
}

type contextBlock struct {
	role string
	text string
}

func snapshotDB(path string) (string, func(), error) {
	tempDir, err := os.MkdirTemp("", "tmuxcoder-context-*")
	if err != nil {
		return "", nil, err
	}

	base := filepath.Base(path)
	dstPath := filepath.Join(tempDir, base)

	if err := copyFile(path, dstPath, 0o600); err != nil {
		_ = os.RemoveAll(tempDir)
		return "", nil, err
	}

	if wal := path + "-wal"; fileExists(wal) {
		if err := copyFile(wal, dstPath+"-wal", 0o600); err != nil {
			_ = os.RemoveAll(tempDir)
			return "", nil, err
		}
	}
	if shm := path + "-shm"; fileExists(shm) {
		if err := copyFile(shm, dstPath+"-shm", 0o600); err != nil {
			_ = os.RemoveAll(tempDir)
			return "", nil, err
		}
	}

	cleanup := func() {
		_ = os.RemoveAll(tempDir)
	}
	return dstPath, cleanup, nil
}

func copyFile(srcPath, dstPath string, mode os.FileMode) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.OpenFile(dstPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		return err
	}
	return dst.Close()
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func readContextRows(path, tmuxSessionID, sessionID, source string) ([]contextRow, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	if _, err := db.Exec("PRAGMA busy_timeout=5000;"); err != nil {
		return nil, err
	}

	query := `SELECT id, ts, role, text FROM model_outputs WHERE tmux_session_id = ?`
	args := []interface{}{tmuxSessionID}
	if sessionID != "" {
		query += ` AND session_id = ?`
		args = append(args, sessionID)
	}
	if source != "" {
		query += ` AND source = ?`
		args = append(args, source)
	}
	query += ` ORDER BY ts ASC, id ASC`

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []contextRow
	for rows.Next() {
		var row contextRow
		if err := rows.Scan(&row.id, &row.ts, &row.role, &row.text); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func mergeRoles(rows []contextRow) []contextBlock {
	var merged []contextBlock
	for _, row := range rows {
		if row.text == "" {
			continue
		}
		if len(merged) > 0 && merged[len(merged)-1].role == row.role {
			merged[len(merged)-1].text = merged[len(merged)-1].text + "\n" + row.text
			continue
		}
		merged = append(merged, contextBlock{role: row.role, text: row.text})
	}
	return merged
}

func trimByChars(rows []contextBlock, maxChars int) []contextBlock {
	if maxChars <= 0 {
		return rows
	}
	var out []contextBlock
	total := 0
	for i := len(rows) - 1; i >= 0; i-- {
		block := fmt.Sprintf("[%s]\n%s\n", rows[i].role, rows[i].text)
		if total+len(block) > maxChars && len(out) > 0 {
			break
		}
		out = append(out, rows[i])
		total += len(block)
	}
	for i := 0; i < len(out)/2; i++ {
		out[i], out[len(out)-1-i] = out[len(out)-1-i], out[i]
	}
	return out
}

func writeContextText(w io.Writer, rows []contextBlock) {
	for i, row := range rows {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "[%s]\n%s\n", row.role, row.text)
	}
}

func writeContextJSON(w io.Writer, rows []contextBlock) error {
	type item struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	out := make([]item, 0, len(rows))
	for _, row := range rows {
		out = append(out, item{Role: row.role, Content: row.text})
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(out)
}

func writeContextReadAudit(dbPath, tmuxSessionID, sessionID, source string, rawRows, selectedRows, mergedRows int, firstID, lastID int64, firstTS, lastTS string, limit, maxChars int) error {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	if _, err := db.Exec("PRAGMA busy_timeout=5000;"); err != nil {
		return err
	}

	if err := ensureContextReadsTable(db); err != nil {
		return err
	}

	var sessionVal interface{}
	if sessionID != "" {
		sessionVal = sessionID
	}
	var firstIDVal interface{}
	var lastIDVal interface{}
	var firstTSVal interface{}
	var lastTSVal interface{}
	if firstID != 0 || lastID != 0 {
		firstIDVal = firstID
		lastIDVal = lastID
	}
	if firstTS != "" || lastTS != "" {
		firstTSVal = firstTS
		lastTSVal = lastTS
	}

	_, err = db.Exec(
		`INSERT INTO context_reads (
			ts, tmux_session_id, session_id, source, rows, max_chars, limit_rows,
			raw_rows, selected_rows, merged_rows, first_id, last_id, first_ts, last_ts
		) VALUES (datetime('now'), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		tmuxSessionID,
		sessionVal,
		source,
		mergedRows,
		maxChars,
		limit,
		rawRows,
		selectedRows,
		mergedRows,
		firstIDVal,
		lastIDVal,
		firstTSVal,
		lastTSVal,
	)
	return err
}

func ensureContextReadsTable(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS context_reads (
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
	);`); err != nil {
		return err
	}
	return ensureContextReadsColumns(db)
}

func resolveTmuxSessionID(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if value := strings.TrimSpace(os.Getenv("TMUXCODER_SESSION_ID")); value != "" {
		return value, nil
	}
	path := filepath.Join(userHomeOrEmpty(), ".tmuxcoder", "current_session_id")
	if path != "" {
		if data, err := os.ReadFile(path); err == nil {
			value := strings.TrimSpace(string(data))
			if value != "" {
				return value, nil
			}
		}
	}
	return "", fmt.Errorf("tmux_session_id is required (use --tmux-session-id, TMUXCODER_SESSION_ID, or ~/.tmuxcoder/current_session_id)")
}

func ensureContextReadsColumns(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(context_reads);`)
	if err != nil {
		return err
	}
	defer rows.Close()

	existing := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, colType string
		var notnull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &colType, &notnull, &dflt, &pk); err != nil {
			return err
		}
		existing[name] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}

	columns := map[string]string{
		"raw_rows":      "INTEGER",
		"selected_rows": "INTEGER",
		"merged_rows":   "INTEGER",
		"first_id":      "INTEGER",
		"last_id":       "INTEGER",
		"first_ts":      "TEXT",
		"last_ts":       "TEXT",
	}

	for name, colType := range columns {
		if existing[name] {
			continue
		}
		if _, err := db.Exec(fmt.Sprintf("ALTER TABLE context_reads ADD COLUMN %s %s;", name, colType)); err != nil {
			return err
		}
	}

	return nil
}

const rulesMarkerStart = "<!-- tmuxcoder:shared-context-rules:start -->"
const rulesMarkerEnd = "<!-- tmuxcoder:shared-context-rules:end -->"

const rulesTemplate = `<!-- tmuxcoder:shared-context-rules:start -->
## Tmuxcoder Shared Context Rules

You MUST fetch shared context by running exactly one command:
tmuxcoder context

--source is optional (codex|claude). Omit it to get context from all sources.
Do NOT run discovery commands (ls/rg/strings/--help). Do NOT search for TMUXCODER_ROOT.
If tmux_session_id is needed, pass --tmux-session-id or ask the user to set TMUXCODER_SESSION_ID.
Then prepend the fetched context to the prompt and answer the user.
<!-- tmuxcoder:shared-context-rules:end -->
`

func writeRulesTemplates(files []string) error {
	for _, name := range files {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if err := writeRulesTemplate(name); err != nil {
			return err
		}
	}
	return nil
}

func writeRulesTemplate(path string) error {
	target := path
	if !filepath.IsAbs(target) {
		if cwd, err := os.Getwd(); err == nil {
			target = filepath.Join(cwd, path)
		}
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}

	existing, err := os.ReadFile(target)
	if err == nil {
		content := string(existing)
		if strings.Contains(content, rulesMarkerStart) && strings.Contains(content, rulesMarkerEnd) {
			updated, ok := replaceRulesBlock(content)
			if !ok {
				return nil
			}
			return os.WriteFile(target, []byte(updated), 0o644)
		}
		sep := "\n"
		if strings.HasSuffix(content, "\n") {
			sep = ""
		}
		return os.WriteFile(target, []byte(content+sep+"\n"+rulesTemplate), 0o644)
	}
	if !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(target, []byte(rulesTemplate), 0o644)
}

func replaceRulesBlock(content string) (string, bool) {
	start := strings.Index(content, rulesMarkerStart)
	end := strings.Index(content, rulesMarkerEnd)
	if start == -1 || end == -1 || end < start {
		return content, false
	}
	end += len(rulesMarkerEnd)
	return content[:start] + rulesTemplate + content[end:], true
}

func printBusStatus(socketFlag string) {
	socketPath := busSocketPath(socketFlag)
	fmt.Println("bus:")
	fmt.Printf("  socket: %s\n", socketPath)
	if socketPath == "" {
		fmt.Println("  status: unknown")
		return
	}
	if _, err := os.Stat(socketPath); err != nil {
		if os.IsNotExist(err) {
			fmt.Println("  status: missing socket")
			return
		}
		fmt.Printf("  status: error (%v)\n", err)
		return
	}
	if err := canRegisterBus(socketPath, 800*time.Millisecond); err != nil {
		fmt.Printf("  status: not responding (%v)\n", err)
		return
	}
	fmt.Println("  status: ok")
}

func printIngestStatus(claimDirFlag string) {
	claimDir := claimDirFlag
	if claimDir == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			claimDir = filepath.Join(home, claim.DefaultClaimDir)
		}
	}
	claimDir = expandHome(claimDir)
	fmt.Println("ingest:")
	fmt.Printf("  claim_dir: %s\n", claimDir)
	entries, err := os.ReadDir(claimDir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("  claims: none")
			return
		}
		fmt.Printf("  claims: error (%v)\n", err)
		return
	}

	var claimsInfo []string
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".lock" {
			continue
		}
		lockPath := filepath.Join(claimDir, entry.Name())
		sessionID := strings.TrimSuffix(entry.Name(), ".lock")
		info, alive, err := readClaim(lockPath)
		if err != nil {
			claimsInfo = append(claimsInfo, fmt.Sprintf("    - %s: error (%v)", sessionID, err))
			continue
		}
		status := "stale"
		if alive {
			status = "alive"
		}
		claimsInfo = append(claimsInfo, fmt.Sprintf("    - %s: pid=%d %s start=%s host=%s", sessionID, info.PID, status, info.StartTS, info.Hostname))
	}

	if len(claimsInfo) == 0 {
		fmt.Println("  claims: none")
		return
	}
	fmt.Println("  claims:")
	for _, line := range claimsInfo {
		fmt.Println(line)
	}
}

func printSinkStatus(dbPathFlag, tmuxSessionID string) {
	dbPath := dbPathFlag
	if dbPath == "" {
		dbPath = defaultStatusDBPath()
	}
	dbPath = expandHome(dbPath)

	fmt.Println("sink:")
	fmt.Printf("  db_path: %s\n", dbPath)

	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			fmt.Println("  status: missing db")
			return
		}
		fmt.Printf("  status: error (%v)\n", err)
		return
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		fmt.Printf("  status: error (%v)\n", err)
		return
	}
	defer db.Close()

	totalRows := queryInt(db, `SELECT COUNT(1) FROM model_outputs`)
	totalSessions := queryInt(db, `SELECT COUNT(1) FROM tmux_sessions`)
	lastTS := queryString(db, `SELECT MAX(ts) FROM model_outputs`)

	fmt.Printf("  model_outputs: %d rows\n", totalRows)
	fmt.Printf("  tmux_sessions: %d\n", totalSessions)
	if lastTS != "" {
		fmt.Printf("  last_ts: %s\n", lastTS)
	}

	currentID, source := resolveStatusTmuxSessionID(tmuxSessionID)
	if currentID != "" {
		fmt.Printf("  current_tmux_session_id: %s (%s)\n", currentID, source)
		sessionRows := queryInt(db, `SELECT COUNT(1) FROM model_outputs WHERE tmux_session_id = ?`, currentID)
		fmt.Printf("  session_rows: %d\n", sessionRows)
		createdAt := queryString(db, `SELECT created_at FROM tmux_sessions WHERE tmux_session_id = ?`, currentID)
		if createdAt != "" {
			fmt.Printf("  session_created_at: %s\n", createdAt)
		}
	}
}

func canRegisterBus(path string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cfg := bridgeclient.DefaultConfig()
	cfg.SocketPath = path
	cfg.Label = "tmuxcoder-status"
	cfg.DriveMode = "status"
	client := bridgeclient.New(cfg)

	oldOutput := log.Writer()
	log.SetOutput(io.Discard)
	defer log.SetOutput(oldOutput)

	if err := client.Connect(ctx); err != nil {
		return err
	}
	return client.Close()
}

func readClaim(lockPath string) (claim.LockInfo, bool, error) {
	var info claim.LockInfo
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return info, false, err
	}
	if err := json.Unmarshal(data, &info); err != nil {
		return info, false, err
	}
	alive := processExists(info.PID)
	return info, alive, nil
}

func processExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

func queryInt(db *sql.DB, query string, args ...interface{}) int {
	var value int
	if err := db.QueryRow(query, args...).Scan(&value); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0
		}
		return 0
	}
	return value
}

func queryString(db *sql.DB, query string, args ...interface{}) string {
	var value sql.NullString
	if err := db.QueryRow(query, args...).Scan(&value); err != nil {
		return ""
	}
	if !value.Valid {
		return ""
	}
	return value.String
}

func resolveStatusTmuxSessionID(explicit string) (string, string) {
	if explicit != "" {
		return explicit, "flag"
	}
	if value := strings.TrimSpace(os.Getenv("TMUXCODER_SESSION_ID")); value != "" {
		return value, "env"
	}
	path := filepath.Join(userHomeOrEmpty(), ".tmuxcoder", "current_session_id")
	if path != "" {
		if data, err := os.ReadFile(path); err == nil {
			value := strings.TrimSpace(string(data))
			if value != "" {
				return value, "file"
			}
		}
	}
	return "", ""
}

func defaultStatusDBPath() string {
	home := userHomeOrEmpty()
	if home == "" {
		return "./tmuxcoder-model-outputs.db"
	}
	return filepath.Join(home, ".tmuxcoder", "model_outputs.db")
}

func resolveDBPath(explicit string) (string, error) {
	if explicit != "" {
		return expandHome(explicit), nil
	}
	if value := strings.TrimSpace(os.Getenv("TMUXCODER_DB_PATH")); value != "" {
		return expandHome(value), nil
	}
	if value := readDBPathFile(); value != "" {
		return expandHome(value), nil
	}
	path := defaultStatusDBPath()
	if path == "" {
		return "", fmt.Errorf("db path is required")
	}
	return expandHome(path), nil
}

func writeDBPathFile(value string) error {
	path := dbPathFilePath()
	if path == "" {
		return fmt.Errorf("db path file is empty")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strings.TrimSpace(value)), 0o600)
}

func readDBPathFile() string {
	path := dbPathFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func dbPathFilePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".tmuxcoder", "current_db_path")
}

func userHomeOrEmpty() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

func listSkillDirs(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var skills []string
	for _, entry := range entries {
		if entry.IsDir() {
			name := entry.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			skills = append(skills, name)
		}
	}
	return skills, nil
}

func installSkills(srcRoot, dstRoot string, skillNames []string, force bool) error {
	if err := os.MkdirAll(dstRoot, 0o755); err != nil {
		return err
	}
	for _, name := range skillNames {
		src := filepath.Join(srcRoot, name)
		dst := filepath.Join(dstRoot, name)
		if _, err := os.Stat(dst); err == nil {
			if !force {
				return fmt.Errorf("skill already exists: %s (use --force to overwrite)", dst)
			}
			if err := os.RemoveAll(dst); err != nil {
				return err
			}
		}
		if err := copyDir(src, dst); err != nil {
			return err
		}
	}
	return nil
}

func copyDir(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("not a directory: %s", src)
	}
	if err := os.MkdirAll(dst, info.Mode().Perm()); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
			continue
		}
		entryInfo, err := entry.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(srcPath)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dstPath, data, entryInfo.Mode().Perm()); err != nil {
			return err
		}
	}
	return nil
}

func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[2:])
	}
	return path
}

func listSkillDirsFS(fsys fs.FS, root string) ([]string, error) {
	entries, err := fs.ReadDir(fsys, root)
	if err != nil {
		return nil, err
	}
	var skills []string
	for _, entry := range entries {
		if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
			skills = append(skills, entry.Name())
		}
	}
	return skills, nil
}

func installSkillsFromFS(fsys fs.FS, srcRoot, dstRoot string, names []string, force bool) error {
	if err := os.MkdirAll(dstRoot, 0o755); err != nil {
		return err
	}
	for _, name := range names {
		src := srcRoot + "/" + name
		dst := filepath.Join(dstRoot, name)
		if _, err := os.Stat(dst); err == nil {
			if !force {
				return fmt.Errorf("skill already exists: %s (use --force to overwrite)", dst)
			}
			if err := os.RemoveAll(dst); err != nil {
				return err
			}
		}
		if err := copyDirFromFS(fsys, src, dst); err != nil {
			return err
		}
	}
	return nil
}

func copyDirFromFS(fsys fs.FS, src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := fs.ReadDir(fsys, src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		srcPath := src + "/" + entry.Name()
		dstPath := filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			if err := copyDirFromFS(fsys, srcPath, dstPath); err != nil {
				return err
			}
			continue
		}
		data, err := fs.ReadFile(fsys, srcPath)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dstPath, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func ensureSkillInstalled(skillName, targetDir string) error {
	dst := filepath.Join(targetDir, skillName)
	if _, err := os.Stat(dst); err == nil {
		return nil // already installed
	}
	return installSkillsFromFS(embeddedSkills, "skills", targetDir, []string{skillName}, false)
}

func writeSessionFile(value string) error {
	path := sessionFilePath()
	if path == "" {
		return fmt.Errorf("session file path is empty")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strings.TrimSpace(value)), 0o600)
}

func sessionFilePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".tmuxcoder", "current_session_id")
}
