package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

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
	case "ui":
		handleUI(ctx, os.Args[2:])
	default:
		fmt.Printf("Unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("Usage: tmuxcoder <command> [args]")
	fmt.Println("\nCommands:")
	fmt.Println("  logs tail [flags]      Tail logs")
	fmt.Println("  ui [flags]             Launch tmux UI")
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

	// Configure UI client
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
		if ctx.Err() == nil { // Don't print error on normal cancellation
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
