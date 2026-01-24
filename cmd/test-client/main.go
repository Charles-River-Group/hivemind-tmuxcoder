// Test client for manual verification of Bus Server
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/opencode/hivemind-tmuxcoder/internal/protocol"
	"github.com/opencode/hivemind-tmuxcoder/pkg/util/ulid"
)

func main() {
	// Connect to bus
	socketPath := getSocketPath()
	fmt.Printf("Connecting to %s...\n", socketPath)

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		fmt.Printf("Failed to connect: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()
	fmt.Println("Connected!")

	encoder := json.NewEncoder(conn)
	decoder := protocol.NewStreamDecoder(conn)

	// 1. Send registration request
	workspaceUID := "test-" + ulid.New()
	fmt.Printf("\n=== Registering workspace: %s ===\n", workspaceUID)

	regPayload := protocol.RegisterRequestPayload{
		Source:       protocol.Principal{Kind: "workspace", ID: workspaceUID},
		PID:          os.Getpid(),
		StartTS:      time.Now().Format(time.RFC3339),
		Nonce:        ulid.New(),
		WorkspaceUID: workspaceUID,
		WorkspaceID:  "test:manual",
		DriveMode:    "test",
		Label:        "Manual Test Client",
	}
	payloadBytes, _ := json.Marshal(regPayload)

	regEvent := &protocol.EventEnvelope{
		Proto:        protocol.ProtoVersion,
		EventID:      ulid.New(),
		Timestamp:    time.Now().Format(time.RFC3339),
		WorkspaceUID: workspaceUID,
		Source:       protocol.Principal{Kind: "workspace", ID: workspaceUID},
		Type:         protocol.TypeBusRegisterRequest,
		Payload:      payloadBytes,
	}

	if err := encoder.Encode(regEvent); err != nil {
		fmt.Printf("Failed to send register: %v\n", err)
		os.Exit(1)
	}

	// Read response
	resp, err := decoder.Decode()
	if err != nil {
		fmt.Printf("Failed to read response: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Response: type=%s\n", resp.Type)
	fmt.Printf("Payload: %s\n", string(resp.Payload))

	// 2. List workspaces
	fmt.Println("\n=== Listing workspaces ===")
	listEvent := &protocol.EventEnvelope{
		Proto:        protocol.ProtoVersion,
		EventID:      ulid.New(),
		Timestamp:    time.Now().Format(time.RFC3339),
		WorkspaceUID: workspaceUID,
		Source:       protocol.Principal{Kind: "workspace", ID: workspaceUID},
		Type:         protocol.TypeBusListWorkspacesRequest,
		Payload:      json.RawMessage(`{}`),
	}

	if err := encoder.Encode(listEvent); err != nil {
		fmt.Printf("Failed to send list: %v\n", err)
		os.Exit(1)
	}

	resp, err = decoder.Decode()
	if err != nil {
		fmt.Printf("Failed to read response: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Response: type=%s\n", resp.Type)
	fmt.Printf("Payload: %s\n", string(resp.Payload))

	// 3. Subscribe to events
	fmt.Println("\n=== Subscribing to events ===")
	subPayload := protocol.SubscribeRequestPayload{
		WorkspaceUIDs: []string{workspaceUID},
		IncludeGlobal: true,
	}
	subPayloadBytes, _ := json.Marshal(subPayload)

	subEvent := &protocol.EventEnvelope{
		Proto:        protocol.ProtoVersion,
		EventID:      ulid.New(),
		Timestamp:    time.Now().Format(time.RFC3339),
		WorkspaceUID: workspaceUID,
		Source:       protocol.Principal{Kind: "workspace", ID: workspaceUID},
		Type:         protocol.TypeBusSubscribeRequest,
		Payload:      subPayloadBytes,
	}

	if err := encoder.Encode(subEvent); err != nil {
		fmt.Printf("Failed to send subscribe: %v\n", err)
		os.Exit(1)
	}

	resp, err = decoder.Decode()
	if err != nil {
		fmt.Printf("Failed to read response: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Response: type=%s\n", resp.Type)
	fmt.Printf("Payload: %s\n", string(resp.Payload))

	// 4. Wait for heartbeats
	fmt.Println("\n=== Waiting for heartbeats (Ctrl+C to exit) ===")
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	for {
		resp, err = decoder.Decode()
		if err != nil {
			fmt.Printf("Connection closed: %v\n", err)
			break
		}
		fmt.Printf("[%s] type=%s\n", time.Now().Format("15:04:05"), resp.Type)
	}
}

func getSocketPath() string {
	if xdgRuntime := os.Getenv("XDG_RUNTIME_DIR"); xdgRuntime != "" {
		return filepath.Join(xdgRuntime, "tmuxcoder", "bus.sock")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".tmuxcoder", "run", "bus.sock")
}
