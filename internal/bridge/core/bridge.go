// Package core provides the main Workspace Bridge orchestration.
// It connects PTY proxy to the Bus Server, converting I/O to structured events.
package core

import (
	"context"
	"io"
	"log"
	"sync"
	"time"

	"github.com/opencode/hivemind-tmuxcoder/internal/bridge/client"
	"github.com/opencode/hivemind-tmuxcoder/internal/bridge/pty"
	"github.com/opencode/hivemind-tmuxcoder/internal/protocol"
)

// Config holds Bridge configuration.
type Config struct {
	// Bus connection settings
	SocketPath   string
	WorkspaceUID string
	WorkspaceID  string
	Label        string
	ProjectUID   string

	// PTY command settings
	Command string
	Args    []string
	WorkDir string
	Env     []string

	// Terminal size
	Rows uint16
	Cols uint16

	// Output buffering settings
	BufferSize    int
	FlushInterval time.Duration
}

// DefaultConfig returns the default bridge configuration.
func DefaultConfig() *Config {
	return &Config{
		Rows:          24,
		Cols:          80,
		BufferSize:    4096,
		FlushInterval: 100 * time.Millisecond,
	}
}

// Bridge is the main workspace bridge that connects PTY to Bus.
type Bridge struct {
	config    *Config
	busClient *client.Client
	ptyProxy  *pty.Proxy

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu      sync.RWMutex
	running bool
}

// New creates a new Bridge with the given configuration.
func New(config *Config) (*Bridge, error) {
	if config == nil {
		config = DefaultConfig()
	}
	applyDefaults(config)

	return &Bridge{
		config: config,
	}, nil
}

// applyDefaults fills in default values for empty config fields.
func applyDefaults(config *Config) {
	def := DefaultConfig()
	if config.BufferSize == 0 {
		config.BufferSize = def.BufferSize
	}
	if config.FlushInterval == 0 {
		config.FlushInterval = def.FlushInterval
	}
	if config.Rows == 0 {
		config.Rows = def.Rows
	}
	if config.Cols == 0 {
		config.Cols = def.Cols
	}
}

// Run starts the bridge and blocks until the context is cancelled or an error occurs.
func (b *Bridge) Run(ctx context.Context) error {
	b.mu.Lock()
	if b.running {
		b.mu.Unlock()
		return nil
	}
	b.ctx, b.cancel = context.WithCancel(ctx)
	b.running = true
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		b.running = false
		b.mu.Unlock()
	}()

	// Initialize PTY proxy
	ptyConfig := &pty.Config{
		Command: b.config.Command,
		Args:    b.config.Args,
		Dir:     b.config.WorkDir,
		Env:     b.config.Env,
		Rows:    b.config.Rows,
		Cols:    b.config.Cols,
	}
	var err error
	b.ptyProxy, err = pty.New(ptyConfig)
	if err != nil {
		return err
	}

	// Initialize Bus client
	clientConfig := &client.Config{
		SocketPath:   b.config.SocketPath,
		WorkspaceUID: b.config.WorkspaceUID,
		WorkspaceID:  b.config.WorkspaceID,
		Label:        b.config.Label,
		DriveMode:    "pty",
		ProjectUID:   b.config.ProjectUID,
	}
	b.busClient = client.New(clientConfig)

	// Connect to Bus
	if err := b.busClient.Connect(b.ctx); err != nil {
		return err
	}
	defer b.busClient.Close()

	// Register event handlers
	b.busClient.OnEvent(protocol.TypeBackendSend, b.handleBackendSend)
	b.busClient.OnEvent(protocol.TypeBusSendDeliver, b.handleBusSendDeliver)

	// Subscribe to events for this workspace
	if err := b.busClient.Subscribe(
		[]string{b.busClient.WorkspaceUID()},
		nil, // All types
		true,
	); err != nil {
		log.Printf("[BRIDGE] Subscribe warning: %v", err)
	}

	// Start PTY
	if err := b.ptyProxy.Start(); err != nil {
		return err
	}
	defer b.ptyProxy.Close()

	// Send initial status
	b.sendStatusUpdate("running")

	// Start PTY output reader
	b.wg.Add(1)
	go b.readPTYOutput()

	// Wait for context cancellation or PTY exit
	select {
	case <-b.ctx.Done():
		log.Printf("[BRIDGE] Context cancelled")
	case <-b.ptyProxy.Done():
		log.Printf("[BRIDGE] PTY process exited")
	}

	// Send stopped status
	b.sendStatusUpdate("stopped")

	// Wait for goroutines
	b.wg.Wait()

	return nil
}

// readPTYOutput reads from PTY and sends ui.log.append events.
func (b *Bridge) readPTYOutput() {
	defer b.wg.Done()

	buf := make([]byte, b.config.BufferSize)
	for {
		select {
		case <-b.ctx.Done():
			return
		case <-b.ptyProxy.Done():
			return
		default:
		}

		n, err := b.ptyProxy.Read(buf)
		if err != nil {
			if err == io.EOF || b.ctx.Err() != nil {
				return
			}
			log.Printf("[BRIDGE] PTY read error: %v", err)
			continue
		}

		if n > 0 {
			text := string(buf[:n])
			event := b.busClient.NewUILogAppend("info", text)
			if err := b.busClient.Send(event); err != nil {
				log.Printf("[BRIDGE] Failed to send log event: %v", err)
			}
		}
	}
}

// handleBackendSend handles incoming backend.send events.
func (b *Bridge) handleBackendSend(event *protocol.EventEnvelope) {
	log.Printf("[BRIDGE] Received backend.send event: %s", event.EventID)

	var payload protocol.BackendSendPayload
	if err := protocol.UnmarshalPayload(event.Payload, &payload); err != nil {
		log.Printf("[BRIDGE] Invalid backend.send payload: %v", err)
		return
	}

	text := payload.Text
	if !payload.NoNewline && len(text) > 0 && text[len(text)-1] != '\n' {
		text += "\n"
	}

	log.Printf("[BRIDGE] Writing to PTY: %q", text)
	if _, err := b.ptyProxy.WriteString(text); err != nil {
		log.Printf("[BRIDGE] Failed to write to PTY: %v", err)
	} else {
		log.Printf("[BRIDGE] Successfully wrote to PTY")
	}
}

// handleBusSendDeliver handles incoming bus.send.deliver events (cross-workspace messages).
func (b *Bridge) handleBusSendDeliver(event *protocol.EventEnvelope) {
	var payload protocol.SendDeliverPayload
	if err := protocol.UnmarshalPayload(event.Payload, &payload); err != nil {
		log.Printf("[BRIDGE] Invalid bus.send.deliver payload: %v", err)
		return
	}

	// For now, just log cross-workspace messages
	// In future, this could be routed to an inbox or processed based on body.kind
	log.Printf("[BRIDGE] Received message from %s: %+v", payload.FromWorkspaceUID, payload.Body)
}

// sendStatusUpdate sends a ui.status.update event.
func (b *Bridge) sendStatusUpdate(status string) {
	event := b.busClient.NewUIStatusUpdate(status, map[string]interface{}{
		"pid":     b.ptyProxy.PID(),
		"command": b.config.Command,
	})
	if err := b.busClient.Send(event); err != nil {
		log.Printf("[BRIDGE] Failed to send status update: %v", err)
	}
}

// SendInput sends input to the PTY.
func (b *Bridge) SendInput(text string) error {
	if b.ptyProxy == nil {
		return nil
	}
	_, err := b.ptyProxy.WriteString(text)
	return err
}

// Resize resizes the PTY.
func (b *Bridge) Resize(rows, cols uint16) error {
	if b.ptyProxy == nil {
		return nil
	}
	return b.ptyProxy.Resize(rows, cols)
}

// Interrupt sends SIGINT to the PTY process.
func (b *Bridge) Interrupt() error {
	if b.ptyProxy == nil {
		return nil
	}
	return b.ptyProxy.Interrupt()
}

// Close stops the bridge.
func (b *Bridge) Close() error {
	if b.cancel != nil {
		b.cancel()
	}
	return nil
}

// Running returns true if the bridge is running.
func (b *Bridge) Running() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.running
}

// WorkspaceUID returns the workspace UID.
func (b *Bridge) WorkspaceUID() string {
	if b.busClient != nil {
		return b.busClient.WorkspaceUID()
	}
	return b.config.WorkspaceUID
}
