package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	bridgeclient "github.com/opencode/hivemind-tmuxcoder/internal/bridge/client"
	"github.com/opencode/hivemind-tmuxcoder/internal/bus/server"
)

func TestWaitForWorkspaceConnected(t *testing.T) {
	socketPath := testSocketPath(t)

	cfg := server.DefaultConfig()
	cfg.SocketPath = socketPath
	cfg.IdleTimeout = 2 * time.Second
	srv := server.New(cfg)
	if err := srv.Start(); err != nil {
		t.Fatalf("start bus: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop() })

	wsUID := "ws-test"

	go func() {
		time.Sleep(150 * time.Millisecond)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		c := bridgeclient.New(&bridgeclient.Config{
			SocketPath:   socketPath,
			WorkspaceUID: wsUID,
			WorkspaceID:  wsUID,
			Label:        wsUID,
			DriveMode:    "test",
		})
		_ = c.Connect(ctx)
		defer c.Close()
		<-ctx.Done()
	}()

	if err := waitForWorkspaceConnected(socketPath, wsUID, 2*time.Second); err != nil {
		t.Fatalf("wait: %v", err)
	}
}

func TestWaitForWorkspaceConnectedTimeout(t *testing.T) {
	socketPath := testSocketPath(t)

	cfg := server.DefaultConfig()
	cfg.SocketPath = socketPath
	cfg.IdleTimeout = 2 * time.Second
	srv := server.New(cfg)
	if err := srv.Start(); err != nil {
		t.Fatalf("start bus: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop() })

	err := waitForWorkspaceConnected(socketPath, "never", 200*time.Millisecond)
	if err == nil {
		t.Fatalf("expected timeout error")
	}
}

func testSocketPath(t *testing.T) string {
	t.Helper()

	dir, err := os.MkdirTemp("", "tmuxcoder-cli-test-")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "bus.sock")
}
