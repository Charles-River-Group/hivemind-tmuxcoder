package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/opencode/hivemind-tmuxcoder/internal/ui"
)

func handleUIInteractive(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("ui interactive", flag.ExitOnError)
	var (
		socketPath string
		refresh    time.Duration
	)

	fs.StringVar(&socketPath, "socket", "", "Bus server socket path (default: auto-detect)")
	fs.DurationVar(&refresh, "refresh", 2*time.Second, "Refresh interval for workspace list")
	fs.Parse(args)

	config := ui.InteractiveConfig{
		SocketPath:      socketPath,
		RefreshInterval: refresh,
	}

	if err := ui.RunInteractive(ctx, config); err != nil {
		fmt.Printf("Failed to run interactive UI: %v\n", err)
		os.Exit(1)
	}
}
