package main

import (
	"context"
	"flag"
	"fmt"
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
	"github.com/opencode/hivemind-tmuxcoder/internal/ui"
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
	)

	fs.StringVar(&socketPath, "socket", "", "Bus server socket path (default: auto-detect)")
	fs.StringVar(&dbPath, "db-path", "", "SQLite DB path")
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
	fs.BoolVar(&uiEnabled, "ui", false, "Launch tmux UI")
	fs.Parse(args)

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
