package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/opencode/hivemind-tmuxcoder/internal/protocol"
	"github.com/opencode/hivemind-tmuxcoder/pkg/util/ulid"
)

// RenderMode controls how events are rendered to output.
type RenderMode int

const (
	RenderModeRaw RenderMode = iota
	RenderModeFormatted
)

const defaultKeepAliveInterval = 30 * time.Second

// Config holds UI client configuration.
type Config struct {
	SocketPath    string
	ClientID      string
	WorkspaceUIDs []string
	IncludeGlobal bool
	Output        io.Writer
	RendererMode  RenderMode
	EventTypes    []string
	// KeepAliveInterval controls client heartbeats for long-lived operations.
	KeepAliveInterval time.Duration
}

// Client is a lightweight UI client that connects to the bus.
type Client struct {
	config *Config

	conn    net.Conn
	encoder *protocol.StreamEncoder
	decoder *protocol.StreamDecoder

	source       protocol.Principal
	workspaceUID string
}

// NewClient creates a new UI client with defaults applied.
func NewClient(config *Config) *Client {
	if config == nil {
		config = &Config{}
	}
	if config.SocketPath == "" {
		config.SocketPath = defaultSocketPath()
	}
	if config.Output == nil {
		config.Output = os.Stdout
	}
	if config.KeepAliveInterval == 0 {
		config.KeepAliveInterval = defaultKeepAliveInterval
	}

	clientID := config.ClientID
	if clientID == "" {
		clientID = "ui-" + strings.ToLower(ulid.New())
	}

	return &Client{
		config:       config,
		source:       protocol.Principal{Kind: protocol.PrincipalKindUI, ID: clientID},
		workspaceUID: clientID,
	}
}

// Connect connects to the bus and performs registration.
func (c *Client) Connect(ctx context.Context) error {
	if c.conn != nil {
		return nil
	}

	conn, err := net.Dial("unix", c.config.SocketPath)
	if err != nil {
		return fmt.Errorf("failed to connect to bus: %w", err)
	}
	c.conn = conn
	c.encoder = protocol.NewStreamEncoder(conn)
	c.decoder = protocol.NewStreamDecoder(conn)

	if err := c.register(ctx); err != nil {
		c.conn.Close()
		c.conn = nil
		return err
	}

	return nil
}

// Close closes the connection to the bus.
func (c *Client) Close() error {
	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn = nil
	return err
}

// WorkspaceUID returns the client's workspace UID.
func (c *Client) WorkspaceUID() string {
	return c.workspaceUID
}

// Tail subscribes to events and renders them until the context is cancelled.
func (c *Client) Tail(ctx context.Context) error {
	if c.conn == nil {
		return fmt.Errorf("not connected")
	}

	if err := c.subscribe(ctx); err != nil {
		return err
	}

	renderer := NewRenderer(c.config.Output, c.config.RendererMode)
	typeFilter := makeTypeFilter(c.config.EventTypes)
	keepAliveInterval := c.config.KeepAliveInterval
	lastKeepAlive := time.Now()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if keepAliveInterval > 0 && time.Since(lastKeepAlive) >= keepAliveInterval {
			if err := c.sendKeepAlive(); err != nil {
				return err
			}
			lastKeepAlive = time.Now()
		}

		if err := c.conn.SetReadDeadline(time.Now().Add(1 * time.Second)); err != nil {
			return err
		}

		event, err := c.decoder.Decode()
		if err != nil {
			if isTimeout(err) {
				continue
			}
			if errors.Is(err, io.EOF) {
				return err
			}
			return err
		}

		if shouldRenderEvent(event, typeFilter) {
			renderer.Render(event)
		}
	}
}

func (c *Client) sendKeepAlive() error {
	payload := protocol.HeartbeatPayload{
		TS:     time.Now().UTC().Format(time.RFC3339),
		Status: "client",
	}
	payloadBytes, _ := protocol.MarshalPayload(payload)

	event := protocol.NewEventEnvelope(
		ulid.New(),
		c.workspaceUID,
		c.source,
		protocol.TypeBusHeartbeat,
		payloadBytes,
	)

	return c.encoder.Encode(event)
}

// SendInput sends a backend.send event to a target workspace.
func (c *Client) SendInput(ctx context.Context, workspaceUID string, text string, noNewline bool) error {
	if c.conn == nil {
		return fmt.Errorf("not connected")
	}

	payload := protocol.BackendSendPayload{
		Text:      text,
		NoNewline: noNewline,
		Source:    "ui",
	}
	payloadBytes, _ := protocol.MarshalPayload(payload)

	event := protocol.NewEventEnvelope(
		ulid.New(),
		workspaceUID,
		c.source,
		protocol.TypeBackendSend,
		payloadBytes,
	)

	return c.encoder.Encode(event)
}

// SendMessage sends a cross-workspace message using bus.send.request.
func (c *Client) SendMessage(ctx context.Context, toWorkspaceUID string, text string) error {
	if c.conn == nil {
		return fmt.Errorf("not connected")
	}

	body := protocol.MessageBody{
		Kind: "text",
		Text: text,
	}
	payload := protocol.SendRequestPayload{
		ToWorkspaceUID: toWorkspaceUID,
		Body:           body,
		Summary:        text,
	}
	payloadBytes, _ := protocol.MarshalPayload(payload)

	requestID := ulid.New()
	event := protocol.NewEventEnvelope(
		requestID,
		c.workspaceUID,
		c.source,
		protocol.TypeBusSendRequest,
		payloadBytes,
	)

	if err := c.encoder.Encode(event); err != nil {
		return err
	}

	// Wait for response
	resp, err := c.waitForResponse(ctx, requestID, protocol.TypeBusSendResponse)
	if err != nil {
		return err
	}

	var respPayload protocol.SendResponsePayload
	if err := protocol.UnmarshalPayload(resp.Payload, &respPayload); err != nil {
		return err
	}
	if respPayload.Status != "ok" {
		if respPayload.Error != nil {
			return fmt.Errorf("send failed: %s - %s", respPayload.Error.Code, respPayload.Error.Message)
		}
		return fmt.Errorf("send failed: %s", respPayload.Status)
	}

	return nil
}

// RequestCreateWorkspace sends a bus.request.create_workspace event and waits for response.
func (c *Client) RequestCreateWorkspace(ctx context.Context, payload protocol.CreateWorkspaceRequestPayload) error {
	if c.conn == nil {
		return fmt.Errorf("not connected")
	}

	payloadBytes, _ := protocol.MarshalPayload(payload)
	requestID := ulid.New()
	event := protocol.NewEventEnvelope(
		requestID,
		c.workspaceUID,
		c.source,
		protocol.TypeBusCreateWorkspaceRequest,
		payloadBytes,
	)

	if err := c.encoder.Encode(event); err != nil {
		return err
	}

	// Wait for response
	resp, err := c.waitForResponse(ctx, requestID, protocol.TypeBusCreateWorkspaceResponse)
	if err != nil {
		return err
	}

	var respPayload protocol.CreateWorkspaceResponsePayload
	if err := protocol.UnmarshalPayload(resp.Payload, &respPayload); err != nil {
		return err
	}
	if respPayload.Status != "ok" {
		if respPayload.Error != nil {
			return fmt.Errorf("create workspace failed: %s - %s", respPayload.Error.Code, respPayload.Error.Message)
		}
		return fmt.Errorf("create workspace failed: %s", respPayload.Status)
	}

	return nil
}

// ListWorkspaces returns the list of active workspaces.
func (c *Client) ListWorkspaces(ctx context.Context) ([]protocol.WorkspaceInfo, error) {
	if c.conn == nil {
		return nil, fmt.Errorf("not connected")
	}

	payloadBytes, _ := protocol.MarshalPayload(protocol.ListWorkspacesRequestPayload{
		ExcludeSelf: true,
	})
	requestID := ulid.New()

	event := protocol.NewEventEnvelope(
		requestID,
		c.workspaceUID,
		c.source,
		protocol.TypeBusListWorkspacesRequest,
		payloadBytes,
	)

	if err := c.encoder.Encode(event); err != nil {
		return nil, err
	}

	resp, err := c.waitForResponse(ctx, requestID, protocol.TypeBusListWorkspacesResponse)
	if err != nil {
		return nil, err
	}

	var payload protocol.ListWorkspacesResponsePayload
	if err := protocol.UnmarshalPayload(resp.Payload, &payload); err != nil {
		return nil, err
	}
	if payload.Status != "ok" {
		if payload.Error != nil {
			return nil, fmt.Errorf("bus error: %s - %s", payload.Error.Code, payload.Error.Message)
		}
		return nil, fmt.Errorf("bus error: %s", payload.Status)
	}

	return payload.Workspaces, nil
}

func (c *Client) register(ctx context.Context) error {
	payload := protocol.RegisterRequestPayload{
		Source:       c.source,
		PID:          os.Getpid(),
		StartTS:      time.Now().UTC().Format(time.RFC3339),
		Nonce:        ulid.New(),
		WorkspaceUID: c.workspaceUID,
		WorkspaceID:  "ui:" + shortID(c.workspaceUID),
		DriveMode:    "ui",
		Label:        "tmuxcoder-ui",
	}
	payloadBytes, _ := protocol.MarshalPayload(payload)

	event := protocol.NewEventEnvelope(
		ulid.New(),
		c.workspaceUID,
		c.source,
		protocol.TypeBusRegisterRequest,
		payloadBytes,
	)

	if err := c.encoder.Encode(event); err != nil {
		return err
	}

	if err := c.conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return err
	}

	resp, err := c.decoder.Decode()
	if err != nil {
		return err
	}

	if resp.Type != protocol.TypeBusRegisterResponse {
		return fmt.Errorf("unexpected response type: %s", resp.Type)
	}

	var respPayload protocol.RegisterResponsePayload
	if err := protocol.UnmarshalPayload(resp.Payload, &respPayload); err != nil {
		return err
	}

	if respPayload.Status != "ok" {
		if respPayload.Error != nil {
			return fmt.Errorf("registration failed: %s - %s", respPayload.Error.Code, respPayload.Error.Message)
		}
		return fmt.Errorf("registration failed: %s", respPayload.Status)
	}

	return nil
}

func (c *Client) subscribe(ctx context.Context) error {
	payload := protocol.SubscribeRequestPayload{
		WorkspaceUIDs: c.config.WorkspaceUIDs,
		Types:         cleanTypes(c.config.EventTypes),
		IncludeGlobal: c.config.IncludeGlobal,
	}
	payloadBytes, _ := protocol.MarshalPayload(payload)

	requestID := ulid.New()
	event := protocol.NewEventEnvelope(
		requestID,
		c.workspaceUID,
		c.source,
		protocol.TypeBusSubscribeRequest,
		payloadBytes,
	)

	if err := c.encoder.Encode(event); err != nil {
		return err
	}

	resp, err := c.waitForResponse(ctx, requestID, protocol.TypeBusSubscribeResponse)
	if err != nil {
		return err
	}

	var respPayload protocol.SubscribeResponsePayload
	if err := protocol.UnmarshalPayload(resp.Payload, &respPayload); err != nil {
		return err
	}
	if respPayload.Status != "ok" {
		if respPayload.Error != nil {
			return fmt.Errorf("subscribe failed: %s - %s", respPayload.Error.Code, respPayload.Error.Message)
		}
		return fmt.Errorf("subscribe failed: %s", respPayload.Status)
	}

	return nil
}

func (c *Client) waitForResponse(ctx context.Context, requestID, responseType string) (*protocol.EventEnvelope, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		if err := c.conn.SetReadDeadline(time.Now().Add(1 * time.Second)); err != nil {
			return nil, err
		}

		event, err := c.decoder.Decode()
		if err != nil {
			if isTimeout(err) {
				continue
			}
			if errors.Is(err, io.EOF) {
				return nil, err
			}
			return nil, err
		}

		if event.Type == responseType && event.InReplyTo == requestID {
			return event, nil
		}
	}
}

func defaultSocketPath() string {
	if xdgRuntime := os.Getenv("XDG_RUNTIME_DIR"); xdgRuntime != "" {
		return filepath.Join(xdgRuntime, "tmuxcoder", "bus.sock")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".tmuxcoder", "run", "bus.sock")
}

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func isTimeout(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func cleanTypes(types []string) []string {
	result := make([]string, 0, len(types))
	for _, t := range types {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		result = append(result, t)
	}
	return result
}

func makeTypeFilter(types []string) map[string]bool {
	cleaned := cleanTypes(types)
	if len(cleaned) == 0 {
		return nil
	}
	filter := make(map[string]bool, len(cleaned))
	for _, t := range cleaned {
		filter[t] = true
	}
	return filter
}

func shouldRenderEvent(event *protocol.EventEnvelope, typeFilter map[string]bool) bool {
	if event.Type == protocol.TypeBusHeartbeat {
		return false
	}
	if typeFilter == nil {
		return true
	}
	return typeFilter[event.Type]
}
