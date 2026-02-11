package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	appbus "github.com/opencode/hivemind-tmuxcoder/internal/app/bus"
	appingest "github.com/opencode/hivemind-tmuxcoder/internal/app/ingest"
	appsink "github.com/opencode/hivemind-tmuxcoder/internal/app/sink"
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

	cfg := appsink.Config{
		SocketPath:    socketPath,
		DBPath:        dbPath,
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
	_ = ctx
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	var (
		socketPath    string
		dbPath        string
		claimDir      string
		tmuxSessionID string
	)

	fs.StringVar(&socketPath, "socket", "", "Bus socket path (default: auto-detect)")
	fs.StringVar(&dbPath, "db-path", "", "SQLite DB path (default: ~/.tmuxcoder/model_outputs.db)")
	fs.StringVar(&claimDir, "claim-dir", "", "Ingest claim dir (default: ~/.tmuxcoder/run/ingest-claims)")
	fs.StringVar(&tmuxSessionID, "tmux-session-id", "", "Tmuxcoder session ID (optional)")
	fs.Parse(args)

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

	fs.StringVar(&skillsDir, "skills-dir", "skills", "Path to skills directory (default: ./skills)")
	fs.StringVar(&target, "target", "both", "Install target: codex|claude|both")
	fs.StringVar(&codexDir, "codex-dir", "~/.codex/skills", "Codex skills directory")
	fs.StringVar(&claudeDir, "claude-dir", "~/.claude/skills", "Claude Code skills directory")
	fs.BoolVar(&force, "force", false, "Overwrite existing skills")
	fs.Parse(args)

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

	switch target {
	case "codex":
		if err := installSkills(srcDir, expandHome(codexDir), skillNames, force); err != nil {
			fmt.Printf("Install failed: %v\n", err)
			os.Exit(1)
		}
	case "claude":
		if err := installSkills(srcDir, expandHome(claudeDir), skillNames, force); err != nil {
			fmt.Printf("Install failed: %v\n", err)
			os.Exit(1)
		}
	case "both":
		if err := installSkills(srcDir, expandHome(codexDir), skillNames, force); err != nil {
			fmt.Printf("Install failed: %v\n", err)
			os.Exit(1)
		}
		if err := installSkills(srcDir, expandHome(claudeDir), skillNames, force); err != nil {
			fmt.Printf("Install failed: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Printf("Invalid --target: %s (expected codex|claude|both)\n", target)
		os.Exit(1)
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
	fs.Parse(args)

	if tmuxSessionID != "" {
		_ = os.Setenv("TMUXCODER_SESSION_ID", tmuxSessionID)
		if err := writeSessionFile(tmuxSessionID); err != nil {
			fmt.Printf("Failed to write session file: %v\n", err)
			os.Exit(1)
		}
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
	if err := canDialUnix(socketPath, 400*time.Millisecond); err != nil {
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

func canDialUnix(path string, timeout time.Duration) error {
	conn, err := net.DialTimeout("unix", path, timeout)
	if err != nil {
		return err
	}
	return conn.Close()
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
