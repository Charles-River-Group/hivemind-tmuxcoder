package bus

import (
	"context"
	"fmt"
	"log"
	"net"
	"time"

	"github.com/opencode/hivemind-tmuxcoder/internal/bus/server"
)

type Config struct {
	SocketPath string
}

func Run(ctx context.Context, cfg Config) error {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)
	log.SetPrefix("[BUS] ")

	busCfg := server.DefaultConfig()
	if cfg.SocketPath != "" {
		busCfg.SocketPath = cfg.SocketPath
	}
	if isBusRunning(busCfg.SocketPath) {
		log.Printf("Bus already running on: %s", busCfg.SocketPath)
		return nil
	}

	srv := server.New(busCfg)
	if err := srv.Start(); err != nil {
		return fmt.Errorf("failed to start server: %w", err)
	}

	log.Printf("Bus Server started")
	log.Printf("Listening on: %s", busCfg.SocketPath)

	<-ctx.Done()

	if err := srv.Stop(); err != nil {
		log.Printf("Error during shutdown: %v", err)
	}

	return nil
}

func isBusRunning(socketPath string) bool {
	if socketPath == "" {
		return false
	}
	conn, err := net.DialTimeout("unix", socketPath, 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
