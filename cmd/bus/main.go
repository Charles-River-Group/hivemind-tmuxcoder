// TmuxCoder Bus Server
//
// The Bus Server is the central message bus for TmuxCoder workspace communication.
// It provides:
//   - Workspace registration and discovery
//   - Event routing and pub/sub
//   - Cross-workspace messaging
//   - Event journaling and audit logging
//
// Usage:
//
//	tmuxcoder-bus [flags]
//
// Flags:
//
//	-socket string    Socket path (default: $XDG_RUNTIME_DIR/tmuxcoder/bus.sock)
//	-config string    Config file path (default: ~/.tmuxcoder/config.yaml)
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/opencode/hivemind-tmuxcoder/internal/bus/server"
)

var (
	socketPath = flag.String("socket", "", "Socket path")
	configPath = flag.String("config", "", "Config file path")
	version    = flag.Bool("version", false, "Print version")
)

const Version = "0.1.0"

func main() {
	flag.Parse()

	if *version {
		fmt.Printf("tmuxcoder-bus version %s\n", Version)
		os.Exit(0)
	}

	// Configure logging
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)
	log.SetPrefix("[BUS] ")

	// Build config
	config := server.DefaultConfig()
	if *socketPath != "" {
		config.SocketPath = *socketPath
	}

	// Create and start server
	srv := server.New(config)

	if err := srv.Start(); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}

	log.Printf("Bus Server started (version %s)", Version)
	log.Printf("Listening on: %s", config.SocketPath)

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	sig := <-sigChan
	log.Printf("Received signal %v, shutting down...", sig)

	if err := srv.Stop(); err != nil {
		log.Printf("Error during shutdown: %v", err)
	}

	log.Printf("Bus Server stopped")
}
