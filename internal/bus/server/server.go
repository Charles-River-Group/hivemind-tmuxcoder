// Package bus implements the TmuxCoder Bus Server core.
// The Bus Server is the central nervous system for workspace communication.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/opencode/hivemind-tmuxcoder/internal/protocol"
	"github.com/opencode/hivemind-tmuxcoder/pkg/util/ulid"
)

// Config holds Bus Server configuration.
type Config struct {
	SocketPath        string
	MaxConnections    int
	MaxMessageSize    int64
	HeartbeatInterval time.Duration
	IdleTimeout       time.Duration
}

// DefaultConfig returns the default Bus Server configuration.
func DefaultConfig() *Config {
	return &Config{
		SocketPath:        getDefaultSocketPath(),
		MaxConnections:    100,
		MaxMessageSize:    64 * 1024, // 64KB
		HeartbeatInterval: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}

func getDefaultSocketPath() string {
	if xdgRuntime := os.Getenv("XDG_RUNTIME_DIR"); xdgRuntime != "" {
		return filepath.Join(xdgRuntime, "tmuxcoder", "bus.sock")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".tmuxcoder", "run", "bus.sock")
}

// BusServer is the main Bus Server implementation.
type BusServer struct {
	config     *Config
	listener   net.Listener
	registry   *Registry
	router     *Router
	validator  *protocol.Validator
	serializer *protocol.Serializer

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu        sync.RWMutex
	running   bool
	startTime time.Time
}

// New creates a new Bus Server.
func New(config *Config) *BusServer {
	if config == nil {
		config = DefaultConfig()
	}
	ctx, cancel := context.WithCancel(context.Background())

	registry := NewRegistry()
	router := NewRouter(registry)

	return &BusServer{
		config:     config,
		registry:   registry,
		router:     router,
		validator:  protocol.NewValidator(),
		serializer: protocol.NewSerializer(),
		ctx:        ctx,
		cancel:     cancel,
	}
}

// Start starts the Bus Server.
func (s *BusServer) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return fmt.Errorf("bus server is already running")
	}

	// Create socket directory if needed
	socketDir := filepath.Dir(s.config.SocketPath)
	if err := os.MkdirAll(socketDir, 0700); err != nil {
		return fmt.Errorf("failed to create socket directory: %w", err)
	}

	// Remove existing socket file
	if err := os.Remove(s.config.SocketPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove existing socket: %w", err)
	}

	// Start listening
	listener, err := net.Listen("unix", s.config.SocketPath)
	if err != nil {
		return fmt.Errorf("failed to listen on socket: %w", err)
	}

	// Set socket permissions (0600 - owner only)
	if err := os.Chmod(s.config.SocketPath, 0600); err != nil {
		listener.Close()
		return fmt.Errorf("failed to set socket permissions: %w", err)
	}

	s.listener = listener
	s.running = true
	s.startTime = time.Now()

	log.Printf("[BUS] Server started on %s", s.config.SocketPath)

	// Start accept loop
	s.wg.Add(1)
	go s.acceptLoop()

	// Start heartbeat loop
	s.wg.Add(1)
	go s.heartbeatLoop()

	return nil
}

// Stop gracefully stops the Bus Server.
func (s *BusServer) Stop() error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = false
	s.mu.Unlock()

	log.Printf("[BUS] Server stopping...")

	// Send draining notice to all connections
	s.broadcastDrainingNotice()

	// Cancel context to signal shutdown
	s.cancel()

	// Close listener
	if s.listener != nil {
		s.listener.Close()
	}

	// Close all connections
	s.registry.CloseAll()

	// Wait for goroutines to finish (with timeout)
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Printf("[BUS] Server stopped gracefully")
	case <-time.After(5 * time.Second):
		log.Printf("[BUS] Server stop timed out")
	}

	// Clean up socket file
	os.Remove(s.config.SocketPath)

	return nil
}

// IsRunning returns true if the server is running.
func (s *BusServer) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}

// acceptLoop handles incoming connections.
func (s *BusServer) acceptLoop() {
	defer s.wg.Done()

	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}

		// Set accept timeout for graceful shutdown
		if unixListener, ok := s.listener.(*net.UnixListener); ok {
			unixListener.SetDeadline(time.Now().Add(1 * time.Second))
		}

		conn, err := s.listener.Accept()
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			if s.ctx.Err() != nil {
				return // Server is stopping
			}
			log.Printf("[BUS] Accept error: %v", err)
			continue
		}

		// Check connection limit
		if s.registry.Count() >= s.config.MaxConnections {
			log.Printf("[BUS] Connection limit reached, rejecting connection")
			conn.Close()
			continue
		}

		// Handle connection in goroutine
		s.wg.Add(1)
		go s.handleConnection(conn)
	}
}

// handleConnection handles a single client connection.
func (s *BusServer) handleConnection(netConn net.Conn) {
	defer s.wg.Done()
	defer netConn.Close()

	conn := NewConnection(netConn)
	log.Printf("[BUS] New connection from %s", netConn.RemoteAddr())

	// Wait for registration
	if err := s.handleRegistration(conn); err != nil {
		log.Printf("[BUS] Registration failed: %v", err)
		s.sendError(conn, protocol.ErrKindProtocol, protocol.ErrCodeBadSchema, false, err.Error())
		return
	}

	log.Printf("[BUS] Connection registered: %s (workspace: %s)", conn.ID, conn.WorkspaceUID)

	// Handle messages
	s.handleMessages(conn)

	// Cleanup
	s.registry.Unregister(conn.ID)
	log.Printf("[BUS] Connection closed: %s", conn.ID)
}

// handleRegistration processes the initial registration handshake.
func (s *BusServer) handleRegistration(conn *Connection) error {
	// Set registration timeout
	conn.netConn.SetReadDeadline(time.Now().Add(10 * time.Second))
	defer conn.netConn.SetReadDeadline(time.Time{})

	// Read first event
	event, err := conn.decoder.Decode()
	if err != nil {
		return fmt.Errorf("failed to read registration: %w", err)
	}

	// Validate event
	if err := s.validator.Validate(event); err != nil {
		return fmt.Errorf("invalid event: %w", err)
	}

	// Must be a register request
	if event.Type != protocol.TypeBusRegisterRequest {
		return fmt.Errorf("expected %s, got %s", protocol.TypeBusRegisterRequest, event.Type)
	}

	// Parse payload
	var payload protocol.RegisterRequestPayload
	if err := protocol.UnmarshalPayload(event.Payload, &payload); err != nil {
		return fmt.Errorf("invalid register payload: %w", err)
	}

	// Check workspace_uid uniqueness
	if s.registry.HasWorkspace(payload.WorkspaceUID) {
		return fmt.Errorf("workspace %s is already registered", payload.WorkspaceUID)
	}

	// Populate connection info
	conn.ID = fmt.Sprintf("conn_%s", ulid.New())
	conn.WorkspaceUID = payload.WorkspaceUID
	conn.WorkspaceID = payload.WorkspaceID
	conn.Source = payload.Source
	conn.PID = payload.PID
	conn.ProjectUID = payload.ProjectUID
	conn.DriveMode = payload.DriveMode
	conn.Label = payload.Label
	conn.Nonce = payload.Nonce
	conn.ConnectedAt = time.Now()
	conn.LastSeen = conn.ConnectedAt

	// Register connection
	if err := s.registry.Register(conn); err != nil {
		return fmt.Errorf("registration failed: %w", err)
	}

	// Send response
	responsePayload := protocol.RegisterResponsePayload{
		Status:                 "ok",
		ConnectionID:           conn.ID,
		RegisteredSource:       conn.Source,
		RegisteredWorkspaceUID: conn.WorkspaceUID,
		BusVersion:             "1.0.0",
	}
	payloadBytes, _ := protocol.MarshalPayload(responsePayload)

	response := protocol.NewEventEnvelope(
		ulid.New(),
		conn.WorkspaceUID,
		protocol.Principal{Kind: protocol.PrincipalKindBus, ID: "bus"},
		protocol.TypeBusRegisterResponse,
		payloadBytes,
	)
	response.InReplyTo = event.EventID
	response.CorrelationID = event.CorrelationID

	return conn.Send(response)
}

// handleMessages processes incoming messages from a registered connection.
func (s *BusServer) handleMessages(conn *Connection) {
	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}

		// Set read timeout for idle detection
		conn.netConn.SetReadDeadline(time.Now().Add(s.config.IdleTimeout))

		event, err := conn.decoder.Decode()
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				// Idle timeout - check if still alive
				if time.Since(conn.LastSeen) > s.config.IdleTimeout {
					log.Printf("[BUS] Connection %s idle timeout", conn.ID)
					return
				}
				continue
			}
			log.Printf("[BUS] Connection %s read error: %v", conn.ID, err)
			return
		}

		conn.LastSeen = time.Now()

		// Validate event
		if err := s.validator.Validate(event); err != nil {
			log.Printf("[BUS] Invalid event from %s: %v", conn.ID, err)
			s.sendError(conn, protocol.ErrKindProtocol, protocol.ErrCodeBadSchema, false, err.Error())
			continue
		}

		// Verify source matches registered connection
		if event.Source.Kind == protocol.PrincipalKindWorkspace && event.Source.ID != conn.WorkspaceUID {
			log.Printf("[BUS] Source spoofing attempt from %s", conn.ID)
			s.sendError(conn, protocol.ErrKindPolicy, protocol.ErrCodePolicyDeny, false, "source mismatch")
			continue
		}

		// Route event
		if err := s.router.Route(event, conn); err != nil {
			log.Printf("[BUS] Routing error for %s: %v", event.EventID, err)
		}
	}
}

// heartbeatLoop sends periodic heartbeats to all connections.
func (s *BusServer) heartbeatLoop() {
	defer s.wg.Done()

	ticker := time.NewTicker(s.config.HeartbeatInterval)
	defer ticker.Stop()

	var seq int64

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			seq++
			s.sendHeartbeats(seq)
		}
	}
}

// sendHeartbeats sends heartbeat events to all connections.
func (s *BusServer) sendHeartbeats(seq int64) {
	connections := s.registry.All()

	for _, conn := range connections {
		payload := protocol.HeartbeatPayload{
			ConnectionID: conn.ID,
			Sequence:     seq,
			TS:           time.Now().UTC().Format(time.RFC3339),
			Status:       "ok",
		}
		payloadBytes, _ := protocol.MarshalPayload(payload)

		event := protocol.NewEventEnvelope(
			ulid.New(),
			conn.WorkspaceUID,
			protocol.Principal{Kind: protocol.PrincipalKindBus, ID: "bus"},
			protocol.TypeBusHeartbeat,
			payloadBytes,
		)

		if err := conn.Send(event); err != nil {
			log.Printf("[BUS] Failed to send heartbeat to %s: %v", conn.ID, err)
			_ = conn.Close()
			s.registry.Unregister(conn.ID)
		}
	}
}

// broadcastDrainingNotice sends draining notice to all connections.
func (s *BusServer) broadcastDrainingNotice() {
	connections := s.registry.All()

	for _, conn := range connections {
		event := protocol.NewEventEnvelope(
			ulid.New(),
			conn.WorkspaceUID,
			protocol.Principal{Kind: protocol.PrincipalKindBus, ID: "bus"},
			protocol.TypeBusDrainingNotice,
			json.RawMessage(`{}`),
		)
		conn.Send(event) // Best effort
	}
}

// sendError sends an error event to a connection.
func (s *BusServer) sendError(conn *Connection, kind, code string, retryable bool, message string) {
	errPayload := protocol.NewEventError(kind, code, retryable, message)
	payloadBytes, _ := protocol.MarshalPayload(errPayload)

	workspaceUID := conn.WorkspaceUID
	if workspaceUID == "" {
		workspaceUID = protocol.GlobalWorkspaceUID
	}

	event := protocol.NewEventEnvelope(
		ulid.New(),
		workspaceUID,
		protocol.Principal{Kind: protocol.PrincipalKindBus, ID: "bus"},
		protocol.TypeBusError,
		payloadBytes,
	)

	conn.Send(event) // Best effort
}

// GetRegistry returns the connection registry.
func (s *BusServer) GetRegistry() *Registry {
	return s.registry
}

// GetRouter returns the event router.
func (s *BusServer) GetRouter() *Router {
	return s.router
}
