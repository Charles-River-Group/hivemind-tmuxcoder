// Package core provides the main Workspace Bridge orchestration.
// It connects PTY proxy to the Bus Server, converting I/O to structured events.
package core

import (
	"context"
	"errors"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/opencode/hivemind-tmuxcoder/internal/bridge/client"
	"github.com/opencode/hivemind-tmuxcoder/internal/bridge/pty"
	"github.com/opencode/hivemind-tmuxcoder/internal/protocol"
	"github.com/opencode/hivemind-tmuxcoder/pkg/util/ulid"
	"golang.org/x/term"
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

	streamID             string
	streamSeq            int64
	kittyKeyboardEnabled bool

	// Transparent mode fields
	transparentCmd   *exec.Cmd
	transparentStdin io.WriteCloser
	transparentDone  chan struct{}
	stdinPipe        io.WriteCloser
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
// It automatically detects the execution environment and uses the appropriate mode:
// - Transparent mode: when running in a real terminal (tmux split/new-window)
// - Headless mode: when running without a terminal (background daemon)
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

	// Detect execution mode based on terminal availability
	if hasRealTerminal() {
		log.Printf("[BRIDGE] Running in transparent mode (real terminal detected)")
		return b.runTransparentMode()
	}

	log.Printf("[BRIDGE] Running in headless PTY mode")
	return b.runHeadlessMode()
}

// runHeadlessMode runs the bridge with an internal PTY (original behavior).
// Used when running as a background daemon without a real terminal.
func (b *Bridge) runHeadlessMode() error {
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

	b.streamID = ulid.New()
	b.streamSeq = 0

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

	// Close PTY before waiting on goroutines to unblock reads.
	_ = b.ptyProxy.Close()

	// Send stopped status
	b.sendStatusUpdate("stopped")

	// Wait for goroutines
	b.wg.Wait()

	return nil
}

// runTransparentMode runs the bridge in transparent proxy mode.
// The child process directly inherits stdin/stdout/stderr from the current terminal,
// while the bridge intercepts I/O for auditing and bus forwarding.
func (b *Bridge) runTransparentMode() error {
	clientConfig := &client.Config{
		SocketPath:   b.config.SocketPath,
		WorkspaceUID: b.config.WorkspaceUID,
		WorkspaceID:  b.config.WorkspaceID,
		Label:        b.config.Label,
		DriveMode:    "transparent",
		ProjectUID:   b.config.ProjectUID,
	}
	b.busClient = client.New(clientConfig)

	if err := b.busClient.Connect(b.ctx); err != nil {
		return err
	}
	defer b.busClient.Close()

	b.streamID = ulid.New()
	b.streamSeq = 0

	b.busClient.OnEvent(protocol.TypeBackendSend, b.handleBackendSend)
	b.busClient.OnEvent(protocol.TypeBusSendDeliver, b.handleBusSendDeliver)

	if err := b.busClient.Subscribe(
		[]string{b.busClient.WorkspaceUID()},
		nil, // All types
		true,
	); err != nil {
		log.Printf("[BRIDGE] Subscribe warning: %v", err)
	}

	rows, cols, err := getTerminalSize(os.Stdin)
	if err != nil {
		rows, cols = b.config.Rows, b.config.Cols
	}

	if err := b.startTransparentPTY(rows, cols); err != nil {
		return err
	}
	defer b.ptyProxy.Close()

	b.sendStatusUpdate("running")

	b.wg.Add(1)
	go b.forwardTerminalInput()

	b.wg.Add(1)
	go b.readPTYOutputToTerminal()

	b.wg.Add(1)
	go b.handleTerminalResize()

	select {
	case <-b.ctx.Done():
		log.Printf("[BRIDGE] Context cancelled")
	case <-b.ptyProxy.Done():
		log.Printf("[BRIDGE] PTY process exited")
	}

	_ = b.ptyProxy.Close()

	b.sendStatusUpdate("stopped")

	b.wg.Wait()

	return nil
}

func (b *Bridge) startTransparentPTY(rows, cols uint16) error {
	ptyConfig := &pty.Config{
		Command: b.config.Command,
		Args:    b.config.Args,
		Dir:     b.config.WorkDir,
		Env:     b.config.Env,
		Rows:    rows,
		Cols:    cols,
	}
	var err error
	b.ptyProxy, err = pty.New(ptyConfig)
	if err != nil {
		return err
	}
	if err := b.ptyProxy.Start(); err != nil {
		return err
	}
	log.Printf("[BRIDGE] Started transparent PTY PID=%d", b.ptyProxy.PID())
	return nil
}

func (b *Bridge) forwardTerminalInput() {
	defer b.wg.Done()

	if isTerminal(os.Stdin) {
		state, err := term.MakeRaw(int(os.Stdin.Fd()))
		if err == nil {
			defer term.Restore(int(os.Stdin.Fd()), state)
		}
	}

	buf := make([]byte, 4096)
	for {
		select {
		case <-b.ctx.Done():
			return
		case <-b.ptyProxy.Done():
			return
		default:
		}

		n, err := os.Stdin.Read(buf)
		if err != nil {
			if err != io.EOF {
				log.Printf("[BRIDGE] stdin read error: %v", err)
			}
			return
		}

		if n > 0 {
			if _, err := b.ptyProxy.Write(buf[:n]); err != nil {
				log.Printf("[BRIDGE] PTY write error: %v", err)
				return
			}

			text := string(buf[:n])
			event := b.busClient.NewUILogAppend("input", text)
			if err := b.busClient.Send(event); err != nil {
				log.Printf("[BRIDGE] Failed to send input log: %v", err)
			}
		}
	}
}

func (b *Bridge) readPTYOutputToTerminal() {
	defer b.wg.Done()
	defer b.sendStreamEnd("closed")

	buf := make([]byte, b.config.BufferSize)
	nonBlocking := false
	for {
		select {
		case <-b.ctx.Done():
			return
		case <-b.ptyProxy.Done():
			return
		default:
		}

		if file := b.ptyProxy.File(); file != nil && !nonBlocking {
			if err := file.SetReadDeadline(time.Now().Add(1 * time.Second)); err != nil {
				if err := syscall.SetNonblock(int(file.Fd()), true); err == nil {
					nonBlocking = true
				}
			}
		}

		n, err := b.ptyProxy.Read(buf)
		if err != nil {
			if os.IsTimeout(err) {
				continue
			}
			if nonBlocking && (errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK)) {
				time.Sleep(20 * time.Millisecond)
				continue
			}
			if err == io.EOF || b.ctx.Err() != nil {
				return
			}
			log.Printf("[BRIDGE] PTY read error: %v", err)
			return
		}

		if n > 0 {
			text := string(buf[:n])
			b.observePTYOutput(text)
			if _, err := os.Stdout.Write(buf[:n]); err != nil {
				log.Printf("[BRIDGE] stdout write error: %v", err)
				return
			}
			event := b.busClient.NewUILogAppend("info", text)
			if err := b.busClient.Send(event); err != nil {
				log.Printf("[BRIDGE] Failed to send log event: %v", err)
			}
			b.sendStreamDelta(text)
		}
	}
}

func (b *Bridge) handleTerminalResize() {
	defer b.wg.Done()

	sigwinch := make(chan os.Signal, 1)
	signal.Notify(sigwinch, syscall.SIGWINCH)
	defer signal.Stop(sigwinch)

	for {
		select {
		case <-b.ctx.Done():
			return
		case <-b.ptyProxy.Done():
			return
		case <-sigwinch:
			rows, cols, err := getTerminalSize(os.Stdin)
			if err != nil {
				continue
			}
			_ = b.ptyProxy.Resize(rows, cols)
		}
	}
}

// readPTYOutput reads from PTY and sends ui.log.append events.
func (b *Bridge) readPTYOutput() {
	defer b.wg.Done()
	defer b.sendStreamEnd("closed")

	buf := make([]byte, b.config.BufferSize)
	nonBlocking := false
	for {
		select {
		case <-b.ctx.Done():
			return
		case <-b.ptyProxy.Done():
			return
		default:
		}

		if file := b.ptyProxy.File(); file != nil && !nonBlocking {
			if err := file.SetReadDeadline(time.Now().Add(1 * time.Second)); err != nil {
				if err := syscall.SetNonblock(int(file.Fd()), true); err == nil {
					nonBlocking = true
				}
			}
		}

		n, err := b.ptyProxy.Read(buf)
		if err != nil {
			if os.IsTimeout(err) {
				continue
			}
			if nonBlocking && (errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK)) {
				time.Sleep(20 * time.Millisecond)
				continue
			}
			if err == io.EOF || b.ctx.Err() != nil {
				return
			}
			log.Printf("[BRIDGE] PTY read error: %v", err)
			continue
		}

		if n > 0 {
			text := string(buf[:n])
			b.observePTYOutput(text)
			event := b.busClient.NewUILogAppend("info", text)
			if err := b.busClient.Send(event); err != nil {
				log.Printf("[BRIDGE] Failed to send log event: %v", err)
			}
			b.sendStreamDelta(text)
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

	data := normalizePTYInput(payload.Text, payload.NoNewline)
	if b.isKittyKeyboardEnabled() {
		data = normalizeKittyPTYInput(payload.Text, payload.NoNewline)
	}
	log.Printf("[BRIDGE] Writing to PTY: %q", string(data))
	if _, err := b.ptyProxy.Write(data); err != nil {
		log.Printf("[BRIDGE] Failed to write to PTY: %v", err)
	} else {
		log.Printf("[BRIDGE] Successfully wrote to PTY")
	}
}

func normalizePTYInput(text string, noNewline bool) []byte {
	if noNewline {
		return []byte(text)
	}
	text = normalizeTextPayload(text)
	text = strings.TrimRight(text, "\r\n")
	return []byte(text + "\r")
}

func normalizeKittyPTYInput(text string, noNewline bool) []byte {
	if noNewline {
		return []byte(text)
	}
	text = normalizeTextPayload(text)
	text = strings.TrimRight(text, "\r\n")
	return []byte(text + "\x1b[13;1u")
}

func normalizeTextPayload(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return text
}

func (b *Bridge) observePTYOutput(text string) {
	if strings.Contains(text, "\x1b[>1u") || strings.Contains(text, "\x1b[>u") {
		b.mu.Lock()
		b.kittyKeyboardEnabled = true
		b.mu.Unlock()
		return
	}
	if strings.Contains(text, "\x1b[<u") {
		b.mu.Lock()
		b.kittyKeyboardEnabled = false
		b.mu.Unlock()
		return
	}
}

func (b *Bridge) isKittyKeyboardEnabled() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.kittyKeyboardEnabled
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

func (b *Bridge) sendStreamDelta(text string) {
	if b.busClient == nil {
		return
	}
	b.streamSeq++
	payload := protocol.BackendStreamDeltaPayload{
		StreamID: b.streamID,
		Sequence: b.streamSeq,
		Text:     text,
	}
	payloadBytes, _ := protocol.MarshalPayload(payload)
	event := b.busClient.NewEvent(protocol.TypeBackendStreamDelta, payloadBytes)
	if err := b.busClient.Send(event); err != nil {
		log.Printf("[BRIDGE] Failed to send stream delta: %v", err)
	}
}

func (b *Bridge) sendStreamEnd(status string) {
	if b.busClient == nil || b.streamID == "" {
		return
	}
	payload := protocol.BackendStreamEndPayload{
		StreamID: b.streamID,
		Sequence: b.streamSeq,
		TS:       time.Now().UTC().Format(time.RFC3339),
		Status:   status,
	}
	payloadBytes, _ := protocol.MarshalPayload(payload)
	event := b.busClient.NewEvent(protocol.TypeBackendStreamEnd, payloadBytes)
	if err := b.busClient.Send(event); err != nil {
		log.Printf("[BRIDGE] Failed to send stream end: %v", err)
	}
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
