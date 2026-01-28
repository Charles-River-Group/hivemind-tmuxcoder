// Bridge command - Workspace Bridge for TmuxCoder.
// Wraps a child process (shell/REPL) with a PTY and connects it to the Bus Server.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/google/uuid"
	"github.com/opencode/hivemind-tmuxcoder/internal/bridge/core"
)

func main() {
	// CLI flags
	socketPath := flag.String("socket", "", "Bus server socket path (default: auto-detect)")
	workspaceUID := flag.String("workspace-uid", "", "Workspace UUID (default: auto-generate)")
	workspaceID := flag.String("workspace-id", "", "Display workspace ID (e.g., tmux:$1:@2)")
	label := flag.String("label", "", "Workspace label for display")
	shell := flag.String("shell", "", "Shell to spawn (default: $SHELL or /bin/sh)")
	shellArgs := stringSlice{}
	workDir := flag.String("dir", "", "Working directory (default: current directory)")
	rows := flag.Uint("rows", 24, "Terminal rows")
	cols := flag.Uint("cols", 80, "Terminal columns")
	flag.Var(&shellArgs, "arg", "Shell argument (repeatable)")
	flag.Parse()

	// Resolve shell
	shellCmd := *shell
	if shellCmd == "" {
		shellCmd = os.Getenv("SHELL")
		if shellCmd == "" {
			shellCmd = "/bin/sh"
		}
	}

	// Resolve workspace UID
	wsUID := *workspaceUID
	if wsUID == "" {
		wsUID = uuid.New().String()
	}

	// Resolve workspace ID
	wsID := *workspaceID
	if wsID == "" {
		wsID = fmt.Sprintf("bridge:%s", shortID(wsUID))
	}

	// Resolve working directory
	dir := *workDir
	if dir == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			dir = os.Getenv("HOME")
		}
	}

	// Build config
	config := &core.Config{
		SocketPath:   *socketPath,
		WorkspaceUID: wsUID,
		WorkspaceID:  wsID,
		Label:        *label,
		Command:      shellCmd,
		Args:         shellArgs,
		WorkDir:      dir,
		Rows:         uint16(*rows),
		Cols:         uint16(*cols),
	}

	// Compute project UID from working directory
	if absDir, err := filepath.Abs(dir); err == nil {
		if realDir, err := filepath.EvalSymlinks(absDir); err == nil {
			config.ProjectUID = fmt.Sprintf("sha256:%x", realDir) // Simplified; should use crypto/sha256
		}
	}

	log.Printf("Starting bridge:")
	log.Printf("  Workspace UID: %s", config.WorkspaceUID)
	log.Printf("  Workspace ID:  %s", config.WorkspaceID)
	log.Printf("  Command:       %s", config.Command)
	log.Printf("  Work Dir:      %s", config.WorkDir)

	// Create bridge
	bridge, err := core.New(config)
	if err != nil {
		log.Fatalf("Failed to create bridge: %v", err)
	}

	// Handle signals
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Run bridge
	if err := bridge.Run(ctx); err != nil {
		log.Fatalf("Bridge error: %v", err)
	}

	log.Println("Bridge stopped")
}

func shortID(value string) string {
	if len(value) <= 8 {
		return value
	}
	return value[:8]
}

type stringSlice []string

func (s *stringSlice) String() string {
	return strings.Join(*s, ",")
}

func (s *stringSlice) Set(value string) error {
	*s = append(*s, value)
	return nil
}
