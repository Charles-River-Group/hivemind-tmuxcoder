package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/opencode/hivemind-tmuxcoder/internal/ui"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	// Create a cancellable context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	switch os.Args[1] {
	case "logs":
		handleLogs(ctx, os.Args[2:])
	case "send":
		handleSend(ctx, os.Args[2:])
	case "message":
		handleMessage(ctx, os.Args[2:])
	case "ui":
		handleUI(ctx, os.Args[2:])
	case "workspaces":
		handleWorkspaces(ctx, os.Args[2:])
	default:
		fmt.Printf("Unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("Usage: tmuxcoder <command> [args]")
	fmt.Println("\nCommands:")
	fmt.Println("  logs tail [flags]      Tail logs from workspaces")
	fmt.Println("  send [flags] <text>    Send input to a workspace PTY (backend.send)")
	fmt.Println("  message [flags] <text> Send cross-workspace message (bus.send.request)")
	fmt.Println("  ui [flags]             Launch tmux UI")
	fmt.Println("  workspaces list        List active workspaces")
	fmt.Println("\nRun 'tmuxcoder <command> --help' for more information.")
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
		workspaceUID  string
		includeGlobal bool
		format        bool
		eventTypes    string
	)

	fs.StringVar(&workspaceUID, "workspace", "", "Workspace UID to filter by (optional)")
	fs.BoolVar(&includeGlobal, "include-global", false, "Include global bus events")
	fs.BoolVar(&format, "format", false, "Enable formatted output (adds timestamps/colors)")
	fs.StringVar(&eventTypes, "types", "ui.log.append,ui.status.update", "Comma-separated list of event types")

	fs.Parse(args)

	// Configure UI client
	renderMode := ui.RenderModeRaw
	if format {
		renderMode = ui.RenderModeFormatted
	}

	config := &ui.Config{
		SocketPath:    "", // Use default
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
		if ctx.Err() == nil { // Don't print error on normal cancellation
			fmt.Printf("Tail error: %v\n", err)
		}
	}
}

func handleSend(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("send", flag.ExitOnError)
	var (
		workspaceUID string
		noNewline    bool
	)

	fs.StringVar(&workspaceUID, "workspace", "", "Target Workspace UID (required)")
	fs.BoolVar(&noNewline, "no-newline", false, "Do not append newline to text")

	fs.Parse(args)

	if workspaceUID == "" {
		fmt.Println("Error: --workspace is required")
		fs.Usage()
		os.Exit(1)
	}

	text := strings.Join(fs.Args(), " ")
	if text == "" {
		fmt.Println("Error: text required")
		fs.Usage()
		os.Exit(1)
	}

	config := &ui.Config{Output: os.Stdout}
	client := ui.NewClient(config)

	if err := client.Connect(ctx); err != nil {
		fmt.Printf("Failed to connect: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	if err := client.SendInput(ctx, workspaceUID, text, noNewline); err != nil {
		fmt.Printf("Failed to send: %v\n", err)
		os.Exit(1)
	}

	// Wait a brief moment for any immediate errors (optional, since it's async)
	// But CLI usually exits immediately.
	fmt.Println("Sent.")
}

func handleMessage(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("message", flag.ExitOnError)
	var (
		toWorkspaceUID string
	)

	fs.StringVar(&toWorkspaceUID, "to", "", "Target Workspace UID (required)")

	fs.Parse(args)

	if toWorkspaceUID == "" {
		fmt.Println("Error: --to is required")
		fs.Usage()
		os.Exit(1)
	}

	text := strings.Join(fs.Args(), " ")
	if text == "" {
		fmt.Println("Error: message text required")
		fs.Usage()
		os.Exit(1)
	}

	config := &ui.Config{Output: os.Stdout}
	client := ui.NewClient(config)

	if err := client.Connect(ctx); err != nil {
		fmt.Printf("Failed to connect: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	if err := client.SendMessage(ctx, toWorkspaceUID, text); err != nil {
		fmt.Printf("Failed to send message: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Message delivered.")
}

func handleWorkspaces(ctx context.Context, args []string) {
	if len(args) < 1 {
		fmt.Println("Usage: tmuxcoder workspaces <subcommand>")
		fmt.Println("\nSubcommands:")
		fmt.Println("  list      List workspaces")
		os.Exit(1)
	}

	switch args[0] {
	case "list":
		handleWorkspacesList(ctx, args[1:])
	default:
		fmt.Printf("Unknown workspaces subcommand: %s\n", args[0])
		os.Exit(1)
	}
}

func handleWorkspacesList(ctx context.Context, args []string) {
	config := &ui.Config{Output: os.Stdout}
	client := ui.NewClient(config)

	if err := client.Connect(ctx); err != nil {
		fmt.Printf("Failed to connect: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	workspaces, err := client.ListWorkspaces(ctx)
	if err != nil {
		fmt.Printf("Failed to list workspaces: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("%-36s  %-20s  %-10s\n", "WORKSPACE UID", "WORKSPACE ID", "LABEL")
	fmt.Println(strings.Repeat("-", 80))
	for _, w := range workspaces {
		fmt.Printf("%-36s  %-20s  %-10s\n", w.WorkspaceUID, w.WorkspaceID, w.Label)
	}
}

func handleUI(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("ui", flag.ExitOnError)
	var (
		sessionName   string
		windowName    string
		workspaceUID  string
		includeGlobal bool
		eventTypes    string
		refresh       time.Duration
	)

	fs.StringVar(&sessionName, "session", "", "tmux session name (default: current or tmuxcoder)")
	fs.StringVar(&windowName, "window", "tmuxcoder", "tmux window name")
	fs.StringVar(&workspaceUID, "workspace", "", "Workspace UID to filter logs")
	fs.BoolVar(&includeGlobal, "include-global", false, "Include global bus events in logs")
	fs.StringVar(&eventTypes, "types", "ui.log.append,ui.status.update", "Comma-separated list of event types")
	fs.DurationVar(&refresh, "refresh", 2*time.Second, "Refresh interval for workspace list")

	fs.Parse(args)

	config := ui.TmuxConfig{
		SessionName:     sessionName,
		WindowName:      windowName,
		WorkspaceUID:    workspaceUID,
		IncludeGlobal:   includeGlobal,
		EventTypes:      strings.Split(eventTypes, ","),
		RefreshInterval: refresh,
	}

	if err := ui.LaunchTmuxUI(ctx, config); err != nil {
		fmt.Printf("Failed to launch tmux UI: %v\n", err)
		os.Exit(1)
	}
}
