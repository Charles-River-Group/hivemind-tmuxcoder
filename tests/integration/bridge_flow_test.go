//go:build integration
// +build integration

package integration

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/opencode/hivemind-tmuxcoder/internal/bridge/client"
	"github.com/opencode/hivemind-tmuxcoder/internal/bridge/core"
	"github.com/opencode/hivemind-tmuxcoder/internal/bus/server"
	"github.com/opencode/hivemind-tmuxcoder/internal/protocol"
	"github.com/opencode/hivemind-tmuxcoder/pkg/util/ulid"
)

func TestBridgePTYRouting(t *testing.T) {
	muteLogs(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	socketPath := testSocketPath(t)
	cfg := server.DefaultConfig()
	cfg.SocketPath = socketPath
	cfg.IdleTimeout = 2 * time.Second
	startBus(t, cfg)

	bridgeUID := "bridge-pty-1"
	startBridge(t, ctx, socketPath, bridgeUID)

	waitForWorkspace(t, socketPath, bridgeUID, 2*time.Second)

	observer := newClient(t, ctx, socketPath, "observer-pty")
	eventCh := make(chan *protocol.EventEnvelope, 16)
	observer.OnEvent(protocol.TypeUILogAppend, func(event *protocol.EventEnvelope) {
		if event.WorkspaceUID != bridgeUID {
			return
		}
		select {
		case eventCh <- event:
		default:
		}
	})
	observer.OnEvent(protocol.TypeBackendStreamDelta, func(event *protocol.EventEnvelope) {
		if event.WorkspaceUID != bridgeUID {
			return
		}
		select {
		case eventCh <- event:
		default:
		}
	})

	subscribeSync(t, observer, []string{bridgeUID}, []string{
		protocol.TypeUILogAppend,
		protocol.TypeBackendStreamDelta,
	}, false)

	sendBackendInput(t, observer, bridgeUID, "hello-bridge\n")

	waitForText(t, eventCh, protocol.TypeUILogAppend, "hello-bridge", 3*time.Second)
	waitForText(t, eventCh, protocol.TypeBackendStreamDelta, "hello-bridge", 3*time.Second)
}

func TestBridgeMultipleInstances(t *testing.T) {
	muteLogs(t)

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	socketPath := testSocketPath(t)
	cfg := server.DefaultConfig()
	cfg.SocketPath = socketPath
	cfg.IdleTimeout = 2 * time.Second
	startBus(t, cfg)

	bridgeA := "bridge-a"
	bridgeB := "bridge-b"
	startBridge(t, ctx, socketPath, bridgeA)
	startBridge(t, ctx, socketPath, bridgeB)

	waitForWorkspace(t, socketPath, bridgeA, 2*time.Second)
	waitForWorkspace(t, socketPath, bridgeB, 2*time.Second)

	observer := newClient(t, ctx, socketPath, "observer-multi")
	eventCh := make(chan *protocol.EventEnvelope, 32)
	observer.OnEvent(protocol.TypeBackendStreamDelta, func(event *protocol.EventEnvelope) {
		select {
		case eventCh <- event:
		default:
		}
	})
	subscribeSync(t, observer, []string{bridgeA, bridgeB}, []string{
		protocol.TypeBackendStreamDelta,
	}, false)

	sendBackendInput(t, observer, bridgeA, "alpha\n")
	sendBackendInput(t, observer, bridgeB, "bravo\n")

	waitForWorkspaceText(t, eventCh, bridgeA, "alpha", 3*time.Second)
	waitForWorkspaceText(t, eventCh, bridgeB, "bravo", 3*time.Second)
}

func startBridge(t *testing.T, ctx context.Context, socketPath, workspaceUID string) {
	t.Helper()
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}

	config := &core.Config{
		SocketPath:   socketPath,
		WorkspaceUID: workspaceUID,
		WorkspaceID:  workspaceUID,
		Label:        workspaceUID,
		Command:      shell,
		Args:         []string{"-c", "cat"},
		WorkDir:      t.TempDir(),
		Rows:         24,
		Cols:         80,
	}

	bridge, err := core.New(config)
	if err != nil {
		t.Fatalf("create bridge: %v", err)
	}

	bridgeCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- bridge.Run(bridgeCtx)
	}()

	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("bridge did not stop")
		}
	})
}

func sendBackendInput(t *testing.T, c *client.Client, workspaceUID, text string) {
	t.Helper()
	payload := protocol.BackendSendPayload{
		Text:      text,
		NoNewline: true,
		Source:    "test",
	}
	payloadBytes, _ := protocol.MarshalPayload(payload)
	event := protocol.NewEventEnvelope(
		ulid.New(),
		workspaceUID,
		c.Source(),
		protocol.TypeBackendSend,
		payloadBytes,
	)
	if err := c.SendSync(event); err != nil {
		t.Fatalf("send backend.send: %v", err)
	}
}

func waitForWorkspace(t *testing.T, socketPath, workspaceUID string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		workspaces := listWorkspaces(t, socketPath)
		if containsWorkspace(workspaces, workspaceUID) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("workspace %s not registered", workspaceUID)
}

func waitForText(t *testing.T, ch <-chan *protocol.EventEnvelope, eventType, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case event := <-ch:
			if event.Type != eventType {
				continue
			}
			text, ok := eventText(event)
			if ok && containsText(text, want) {
				return
			}
		case <-time.After(50 * time.Millisecond):
		}
	}
	t.Fatalf("timeout waiting for %s containing %q", eventType, want)
}

func waitForWorkspaceText(t *testing.T, ch <-chan *protocol.EventEnvelope, workspaceUID, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case event := <-ch:
			if event.WorkspaceUID != workspaceUID {
				continue
			}
			text, ok := eventText(event)
			if ok && containsText(text, want) {
				return
			}
		case <-time.After(50 * time.Millisecond):
		}
	}
	t.Fatalf("timeout waiting for workspace %s text %q", workspaceUID, want)
}

func eventText(event *protocol.EventEnvelope) (string, bool) {
	switch event.Type {
	case protocol.TypeUILogAppend:
		var payload protocol.UILogAppendPayload
		if err := protocol.UnmarshalPayload(event.Payload, &payload); err != nil {
			return "", false
		}
		return payload.Text, true
	case protocol.TypeBackendStreamDelta:
		var payload protocol.BackendStreamDeltaPayload
		if err := protocol.UnmarshalPayload(event.Payload, &payload); err != nil {
			return "", false
		}
		return payload.Text, true
	default:
		return "", false
	}
}

func containsText(haystack, needle string) bool {
	return needle == "" || strings.Contains(haystack, needle)
}
