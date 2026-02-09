// Package client provides a Bus Server client for the Workspace Bridge.
// It handles Unix socket connection, NDJSON event protocol, and reconnection.
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/opencode/hivemind-tmuxcoder/internal/protocol"
	"github.com/opencode/hivemind-tmuxcoder/pkg/util/ulid"
)

// Config holds Bus Client configuration.
type Config struct {
	// SocketPath is the path to the Bus Server Unix socket.
	// If empty, defaults to $XDG_RUNTIME_DIR/tmuxcoder/bus.sock or ~/.tmuxcoder/run/bus.sock
	SocketPath string

	// WorkspaceUID is the stable UUID for this workspace.
	// If empty, a new UUID will be generated.
	WorkspaceUID string

	// WorkspaceID is the operator-friendly alias (e.g., "tmux:$1:@2:%3").
	WorkspaceID string

	// Label is the human-readable workspace label.
	Label string

	// DriveMode indicates how the bridge drives the tool (pty, tmux_io, native).
	DriveMode string

	// ProjectUID is the sha256 hash of the project root path.
	ProjectUID string

	// ReconnectInterval is the base interval for reconnection attempts.
	ReconnectInterval time.Duration

	// MaxReconnectInterval is the maximum backoff interval.
	MaxReconnectInterval time.Duration
}

// DefaultConfig returns the default client configuration.
func DefaultConfig() *Config {
	return &Config{
		SocketPath:           getDefaultSocketPath(),
		DriveMode:            "pty",
		ReconnectInterval:    time.Second,
		MaxReconnectInterval: 30 * time.Second,
	}
}

func getDefaultSocketPath() string {
	if xdgRuntime := os.Getenv("XDG_RUNTIME_DIR"); xdgRuntime != "" {
		return filepath.Join(xdgRuntime, "tmuxcoder", "bus.sock")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".tmuxcoder", "run", "bus.sock")
}

// EventHandler is a callback for received events.
type EventHandler func(*protocol.EventEnvelope)

// Client is a Bus Server client.
type Client struct {
	config *Config

	conn    net.Conn
	encoder *protocol.StreamEncoder
	decoder *protocol.StreamDecoder

	source       protocol.Principal
	connectionID string
	registered   bool

	sendCh   chan *protocol.EventEnvelope
	handlers map[string][]EventHandler
	mu       sync.RWMutex

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New creates a new Bus Client.
func New(config *Config) *Client {
	if config == nil {
		config = DefaultConfig()
	}
	if config.SocketPath == "" {
		config.SocketPath = getDefaultSocketPath()
	}
	if config.WorkspaceUID == "" {
		config.WorkspaceUID = ulid.New()
	}
	if config.ReconnectInterval == 0 {
		config.ReconnectInterval = time.Second
	}
	if config.MaxReconnectInterval == 0 {
		config.MaxReconnectInterval = 30 * time.Second
	}

	return &Client{
		config:   config,
		source:   protocol.Principal{Kind: protocol.PrincipalKindWorkspace, ID: config.WorkspaceUID},
		sendCh:   make(chan *protocol.EventEnvelope, 100),
		handlers: make(map[string][]EventHandler),
	}
}

// Connect connects to the Bus Server and performs registration.
func (c *Client) Connect(ctx context.Context) error {
	c.ctx, c.cancel = context.WithCancel(ctx)

	// Connect to socket
	conn, err := net.Dial("unix", c.config.SocketPath)
	if err != nil {
		return fmt.Errorf("failed to connect to bus: %w", err)
	}
	c.conn = conn
	c.encoder = protocol.NewStreamEncoder(conn)
	c.decoder = protocol.NewStreamDecoder(conn)

	// Perform registration handshake
	if err := c.register(); err != nil {
		c.conn.Close()
		return fmt.Errorf("registration failed: %w", err)
	}

	c.registered = true
	log.Printf("[CLIENT] Connected to bus, workspace_uid=%s, connection_id=%s",
		c.config.WorkspaceUID, c.connectionID)

	// Start background goroutines
	c.wg.Add(2)
	go c.sendLoop()
	go c.recvLoop()

	return nil
}

// register performs the registration handshake with the Bus Server.
func (c *Client) register() error {
	payload := protocol.RegisterRequestPayload{
		Source:       c.source,
		PID:          os.Getpid(),
		StartTS:      time.Now().Format(time.RFC3339),
		Nonce:        ulid.New(),
		WorkspaceUID: c.config.WorkspaceUID,
		WorkspaceID:  c.config.WorkspaceID,
		ProjectUID:   c.config.ProjectUID,
		DriveMode:    c.config.DriveMode,
		Label:        c.config.Label,
	}
	payloadBytes, _ := protocol.MarshalPayload(payload)

	regEvent := protocol.NewEventEnvelope(
		ulid.New(),
		c.config.WorkspaceUID,
		c.source,
		protocol.TypeBusRegisterRequest,
		payloadBytes,
	)

	if err := c.encoder.Encode(regEvent); err != nil {
		return fmt.Errorf("failed to send register request: %w", err)
	}

	// Wait for response
	resp, err := c.decoder.Decode()
	if err != nil {
		return fmt.Errorf("failed to read register response: %w", err)
	}

	if resp.Type != protocol.TypeBusRegisterResponse {
		return fmt.Errorf("unexpected response type: %s", resp.Type)
	}

	var respPayload protocol.RegisterResponsePayload
	if err := protocol.UnmarshalPayload(resp.Payload, &respPayload); err != nil {
		return fmt.Errorf("failed to parse register response: %w", err)
	}

	if respPayload.Status != "ok" {
		if respPayload.Error != nil {
			return fmt.Errorf("registration error: %s - %s", respPayload.Error.Code, respPayload.Error.Message)
		}
		return fmt.Errorf("registration failed with status: %s", respPayload.Status)
	}

	c.connectionID = respPayload.ConnectionID
	return nil
}

// Subscribe subscribes to events matching the given filters.
func (c *Client) Subscribe(workspaceUIDs []string, types []string, includeGlobal bool) error {
	payload := protocol.SubscribeRequestPayload{
		WorkspaceUIDs: workspaceUIDs,
		Types:         types,
		IncludeGlobal: includeGlobal,
	}
	payloadBytes, _ := protocol.MarshalPayload(payload)

	subEvent := protocol.NewEventEnvelope(
		ulid.New(),
		c.config.WorkspaceUID,
		c.source,
		protocol.TypeBusSubscribeRequest,
		payloadBytes,
	)

	return c.Send(subEvent)
}

// Send sends an event to the Bus Server.
func (c *Client) Send(event *protocol.EventEnvelope) error {
	select {
	case c.sendCh <- event:
		return nil
	case <-c.ctx.Done():
		return c.ctx.Err()
	default:
		return fmt.Errorf("send buffer full")
	}
}

// SendSync sends an event synchronously (bypassing the send buffer).
func (c *Client) SendSync(event *protocol.EventEnvelope) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.encoder.Encode(event)
}

// OnEvent registers a handler for a specific event type.
func (c *Client) OnEvent(eventType string, handler EventHandler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.handlers[eventType] = append(c.handlers[eventType], handler)
}

// OnAnyEvent registers a handler for all events.
func (c *Client) OnAnyEvent(handler EventHandler) {
	c.OnEvent("*", handler)
}

// Close closes the connection to the Bus Server.
func (c *Client) Close() error {
	if c.cancel != nil {
		c.cancel()
	}

	var closeErr error
	if c.conn != nil {
		closeErr = c.conn.Close()
		c.conn = nil
	}

	// Wait for goroutines with timeout
	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		log.Printf("[CLIENT] Timeout waiting for goroutines")
	}

	return closeErr
}

// WorkspaceUID returns the workspace UID.
func (c *Client) WorkspaceUID() string {
	return c.config.WorkspaceUID
}

// Source returns the client's principal.
func (c *Client) Source() protocol.Principal {
	return c.source
}

// sendLoop handles outgoing events.
func (c *Client) sendLoop() {
	defer c.wg.Done()

	for {
		select {
		case event := <-c.sendCh:
			c.mu.Lock()
			err := c.encoder.Encode(event)
			c.mu.Unlock()
			if err != nil {
				log.Printf("[CLIENT] Send error: %v", err)
			}
		case <-c.ctx.Done():
			return
		}
	}
}

// recvLoop handles incoming events.
func (c *Client) recvLoop() {
	defer c.wg.Done()

	consecutiveErrors := 0
	maxConsecutiveErrors := 5

	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		event, err := c.decoder.Decode()
		if err != nil {
			if err == io.EOF || c.ctx.Err() != nil {
				log.Printf("[CLIENT] Connection closed")
				return
			}

			// Check for fatal network errors
			if netErr, ok := err.(net.Error); ok && !netErr.Timeout() {
				log.Printf("[CLIENT] Fatal network error: %v", err)
				return
			}

			consecutiveErrors++
			if consecutiveErrors >= maxConsecutiveErrors {
				log.Printf("[CLIENT] Too many consecutive errors (%d), closing connection", consecutiveErrors)
				return
			}

			log.Printf("[CLIENT] Receive error: %v (consecutive: %d)", err, consecutiveErrors)
			time.Sleep(100 * time.Millisecond) // Avoid tight loop on errors
			continue
		}

		consecutiveErrors = 0 // Reset on successful read
		c.dispatchEvent(event)
	}
}

// dispatchEvent dispatches an event to registered handlers.
func (c *Client) dispatchEvent(event *protocol.EventEnvelope) {
	// Handle heartbeat: respond to keep connection alive
	if event.Type == protocol.TypeBusHeartbeat {
		c.respondToHeartbeat(event)
		return
	}

	c.mu.RLock()

	// Log non-heartbeat events
	log.Printf("[CLIENT] Received event: type=%s, id=%s", event.Type, event.EventID)

	// Call type-specific handlers
	if handlers, ok := c.handlers[event.Type]; ok {
		typeHandlers := make([]EventHandler, len(handlers))
		copy(typeHandlers, handlers)
		c.mu.RUnlock()

		for _, h := range typeHandlers {
			h(event)
		}
	} else {
		// Call catch-all handlers
		if handlers, ok := c.handlers["*"]; ok {
			catchAll := make([]EventHandler, len(handlers))
			copy(catchAll, handlers)
			c.mu.RUnlock()

			for _, h := range catchAll {
				h(event)
			}
		} else {
			c.mu.RUnlock()
			// No handler found
			if event.Type != protocol.TypeBusSubscribeResponse {
				log.Printf("[CLIENT] No handler registered for event type: %s", event.Type)
			}
		}
	}
}

// respondToHeartbeat sends a heartbeat response to keep the connection alive.
func (c *Client) respondToHeartbeat(heartbeat *protocol.EventEnvelope) {
	// Parse the heartbeat payload to get sequence number
	var payload protocol.HeartbeatPayload
	if err := protocol.UnmarshalPayload(heartbeat.Payload, &payload); err != nil {
		log.Printf("[CLIENT] Failed to parse heartbeat payload: %v", err)
		return
	}

	// Send back a heartbeat response to update LastSeen on the Bus Server
	responsePayload := protocol.HeartbeatPayload{
		ConnectionID: c.connectionID,
		Sequence:     payload.Sequence,
		TS:           time.Now().UTC().Format(time.RFC3339),
		Status:       "client",
	}
	payloadBytes, _ := protocol.MarshalPayload(responsePayload)

	response := protocol.NewEventEnvelope(
		ulid.New(),
		c.config.WorkspaceUID,
		c.source,
		protocol.TypeBusHeartbeat,
		payloadBytes,
	)
	response.InReplyTo = heartbeat.EventID

	// Send response asynchronously to avoid blocking
	if err := c.Send(response); err != nil {
		log.Printf("[CLIENT] Failed to send heartbeat response: %v", err)
	}
}

// NewEvent creates a new EventEnvelope with common fields populated.
func (c *Client) NewEvent(eventType string, payload json.RawMessage) *protocol.EventEnvelope {
	return protocol.NewEventEnvelope(
		ulid.New(),
		c.config.WorkspaceUID,
		c.source,
		eventType,
		payload,
	)
}

// NewUILogAppend creates a ui.log.append event.
func (c *Client) NewUILogAppend(level, text, sessionID string) *protocol.EventEnvelope {
	payload := protocol.UILogAppendPayload{
		Level:     level,
		Text:      text,
		SessionID: sessionID,
	}
	payloadBytes, _ := protocol.MarshalPayload(payload)
	return c.NewEvent(protocol.TypeUILogAppend, payloadBytes)
}

// NewUIStatusUpdate creates a ui.status.update event.
func (c *Client) NewUIStatusUpdate(status string, details map[string]interface{}) *protocol.EventEnvelope {
	payload := protocol.UIStatusUpdatePayload{
		WorkspaceUID: c.config.WorkspaceUID,
		Status:       status,
		SinceTS:      time.Now().Format(time.RFC3339),
		Details:      details,
	}
	payloadBytes, _ := protocol.MarshalPayload(payload)
	return c.NewEvent(protocol.TypeUIStatusUpdate, payloadBytes)
}
