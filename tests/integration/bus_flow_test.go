//go:build integration
// +build integration

package integration

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opencode/hivemind-tmuxcoder/internal/bridge/client"
	"github.com/opencode/hivemind-tmuxcoder/internal/bus/server"
	"github.com/opencode/hivemind-tmuxcoder/internal/protocol"
	"github.com/opencode/hivemind-tmuxcoder/internal/ui"
	"github.com/opencode/hivemind-tmuxcoder/pkg/util/ulid"
)

func TestEndToEndBusFlow(t *testing.T) {
	muteLogs(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	socketPath := testSocketPath(t)
	cfg := server.DefaultConfig()
	cfg.SocketPath = socketPath
	cfg.IdleTimeout = 2 * time.Second
	startBus(t, cfg)

	bridge := newClient(t, ctx, socketPath, "bridge-e2e")
	observer := newClient(t, ctx, socketPath, "observer-e2e")

	logCh := make(chan *protocol.EventEnvelope, 1)
	observer.OnEvent(protocol.TypeUILogAppend, func(event *protocol.EventEnvelope) {
		select {
		case logCh <- event:
		default:
		}
	})

	subscribeSync(t, observer, []string{bridge.WorkspaceUID()}, []string{protocol.TypeUILogAppend}, false)

	bridge.OnEvent(protocol.TypeBackendSend, func(_ *protocol.EventEnvelope) {
		event := bridge.NewUILogAppend("info", "pong\n")
		_ = bridge.Send(event)
	})

	payload := protocol.BackendSendPayload{
		Text:      "ping",
		NoNewline: false,
		Source:    "test",
	}
	payloadBytes, _ := protocol.MarshalPayload(payload)
	send := protocol.NewEventEnvelope(
		ulid.New(),
		bridge.WorkspaceUID(),
		observer.Source(),
		protocol.TypeBackendSend,
		payloadBytes,
	)
	if err := observer.SendSync(send); err != nil {
		t.Fatalf("send backend.send: %v", err)
	}

	got := waitForEvent(t, logCh, 2*time.Second)
	var logPayload protocol.UILogAppendPayload
	if err := protocol.UnmarshalPayload(got.Payload, &logPayload); err != nil {
		t.Fatalf("unmarshal log payload: %v", err)
	}
	if logPayload.Text != "pong\n" {
		t.Fatalf("unexpected log text: %q", logPayload.Text)
	}
}

func TestCrossWorkspaceMessage(t *testing.T) {
	muteLogs(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	socketPath := testSocketPath(t)
	cfg := server.DefaultConfig()
	cfg.SocketPath = socketPath
	cfg.IdleTimeout = 2 * time.Second
	startBus(t, cfg)

	sender := newClient(t, ctx, socketPath, "sender-1")
	receiver := newClient(t, ctx, socketPath, "receiver-1")

	deliverCh := make(chan *protocol.EventEnvelope, 1)
	receiver.OnEvent(protocol.TypeBusSendDeliver, func(event *protocol.EventEnvelope) {
		select {
		case deliverCh <- event:
		default:
		}
	})

	responseCh := make(chan *protocol.EventEnvelope, 1)
	sender.OnEvent(protocol.TypeBusSendResponse, func(event *protocol.EventEnvelope) {
		select {
		case responseCh <- event:
		default:
		}
	})

	body := protocol.MessageBody{
		Kind: "text",
		Text: "hello",
	}
	payload := protocol.SendRequestPayload{
		ToWorkspaceUID: receiver.WorkspaceUID(),
		Body:           body,
		Summary:        "hello",
	}
	payloadBytes, _ := protocol.MarshalPayload(payload)
	send := protocol.NewEventEnvelope(
		ulid.New(),
		sender.WorkspaceUID(),
		sender.Source(),
		protocol.TypeBusSendRequest,
		payloadBytes,
	)
	if err := sender.SendSync(send); err != nil {
		t.Fatalf("send bus.send.request: %v", err)
	}

	_ = waitForEvent(t, responseCh, 2*time.Second)
	got := waitForEvent(t, deliverCh, 2*time.Second)

	var deliverPayload protocol.SendDeliverPayload
	if err := protocol.UnmarshalPayload(got.Payload, &deliverPayload); err != nil {
		t.Fatalf("unmarshal deliver payload: %v", err)
	}
	if deliverPayload.FromWorkspaceUID != sender.WorkspaceUID() {
		t.Fatalf("unexpected sender uid: %s", deliverPayload.FromWorkspaceUID)
	}
	if deliverPayload.ToWorkspaceUID != receiver.WorkspaceUID() {
		t.Fatalf("unexpected receiver uid: %s", deliverPayload.ToWorkspaceUID)
	}
}

func TestIdleTimeoutRemovesConnection(t *testing.T) {
	muteLogs(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	socketPath := testSocketPath(t)
	cfg := server.DefaultConfig()
	cfg.SocketPath = socketPath
	cfg.IdleTimeout = 150 * time.Millisecond
	startBus(t, cfg)

	idle := newClient(t, ctx, socketPath, "idle-1")

	time.Sleep(500 * time.Millisecond)

	workspaces := listWorkspaces(t, socketPath)
	if containsWorkspace(workspaces, idle.WorkspaceUID()) {
		t.Fatalf("expected idle workspace to be removed")
	}
}

func TestHeartbeatKeepsConnectionAlive(t *testing.T) {
	muteLogs(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	socketPath := testSocketPath(t)
	cfg := server.DefaultConfig()
	cfg.SocketPath = socketPath
	cfg.IdleTimeout = 150 * time.Millisecond
	startBus(t, cfg)

	active := newClient(t, ctx, socketPath, "active-1")

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				_ = sendHeartbeat(active)
			}
		}
	}()

	time.Sleep(500 * time.Millisecond)

	workspaces := listWorkspaces(t, socketPath)
	if !containsWorkspace(workspaces, active.WorkspaceUID()) {
		t.Fatalf("expected workspace to remain registered")
	}

	close(stop)
	wg.Wait()
}

func TestConcurrentPublishers(t *testing.T) {
	muteLogs(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	socketPath := testSocketPath(t)
	cfg := server.DefaultConfig()
	cfg.SocketPath = socketPath
	cfg.IdleTimeout = 2 * time.Second
	startBus(t, cfg)

	publisherCount := 3
	subscriberCount := 4
	eventsPerPublisher := 5
	totalEvents := publisherCount * eventsPerPublisher

	publishers := make([]*client.Client, 0, publisherCount)
	for i := 0; i < publisherCount; i++ {
		publishers = append(publishers, newClient(t, ctx, socketPath, fmt.Sprintf("pub-%d", i)))
	}

	type subTracker struct {
		count *int64
	}
	subs := make([]subTracker, 0, subscriberCount)
	for i := 0; i < subscriberCount; i++ {
		sub := newClient(t, ctx, socketPath, fmt.Sprintf("sub-%d", i))
		var count int64
		sub.OnEvent(protocol.TypeUILogAppend, func(_ *protocol.EventEnvelope) {
			atomic.AddInt64(&count, 1)
		})
		workspaceUIDs := make([]string, 0, publisherCount)
		for _, pub := range publishers {
			workspaceUIDs = append(workspaceUIDs, pub.WorkspaceUID())
		}
		subscribeSync(t, sub, workspaceUIDs, []string{protocol.TypeUILogAppend}, false)
		subs = append(subs, subTracker{count: &count})
	}

	var wg sync.WaitGroup
	for _, pub := range publishers {
		pub := pub
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < eventsPerPublisher; i++ {
				event := pub.NewUILogAppend("info", "concurrent")
				_ = pub.SendSync(event)
			}
		}()
	}
	wg.Wait()

	deadline := time.Now().Add(3 * time.Second)
	for i := range subs {
		waitForCount(t, subs[i].count, int64(totalEvents), deadline)
	}

	if shouldLog() {
		for i := range subs {
			t.Logf("subscriber %d received %d/%d events", i, atomic.LoadInt64(subs[i].count), totalEvents)
		}
	}
}

func TestClientSendBackpressure(t *testing.T) {
	muteLogs(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	socketPath := testSocketPath(t)
	cfg := server.DefaultConfig()
	cfg.SocketPath = socketPath
	cfg.IdleTimeout = 2 * time.Second
	startBus(t, cfg)

	sender := newClient(t, ctx, socketPath, "burst-1")

	const (
		goroutines = 8
		eventsEach = 500
	)

	var fullCount int64
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < eventsEach; j++ {
				event := sender.NewUILogAppend("info", "burst")
				if err := sender.Send(event); err != nil {
					if strings.Contains(err.Error(), "send buffer full") {
						atomic.AddInt64(&fullCount, 1)
					}
				}
			}
		}()
	}
	wg.Wait()

	if atomic.LoadInt64(&fullCount) == 0 {
		t.Fatalf("expected send buffer backpressure, got none")
	}

	if shouldLog() {
		t.Logf("send buffer full count: %d", atomic.LoadInt64(&fullCount))
	}
}

func TestFaultInjectionInvalidEvent(t *testing.T) {
	muteLogs(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	socketPath := testSocketPath(t)
	cfg := server.DefaultConfig()
	cfg.SocketPath = socketPath
	cfg.IdleTimeout = 2 * time.Second
	startBus(t, cfg)

	c := newClient(t, ctx, socketPath, "faulty-1")

	errCh := make(chan *protocol.EventEnvelope, 1)
	c.OnEvent(protocol.TypeBusError, func(event *protocol.EventEnvelope) {
		select {
		case errCh <- event:
		default:
		}
	})

	bad := &protocol.EventEnvelope{
		Proto:        protocol.ProtoVersion,
		EventID:      ulid.New(),
		Timestamp:    time.Now().UTC().Format(time.RFC3339),
		WorkspaceUID: c.WorkspaceUID(),
		Source:       protocol.Principal{Kind: "invalid", ID: "bad"},
		Type:         protocol.TypeUILogAppend,
		Payload:      []byte(`{"text":"bad"}`),
	}
	if err := c.SendSync(bad); err != nil {
		t.Fatalf("send invalid event: %v", err)
	}

	got := waitForEvent(t, errCh, 2*time.Second)
	var payload protocol.EventError
	if err := protocol.UnmarshalPayload(got.Payload, &payload); err != nil {
		t.Fatalf("unmarshal error payload: %v", err)
	}
	if payload.Kind != protocol.ErrKindProtocol || payload.Code != protocol.ErrCodeBadSchema {
		t.Fatalf("unexpected error kind/code: %s/%s", payload.Kind, payload.Code)
	}
}

func startBus(t *testing.T, cfg *server.Config) *server.BusServer {
	t.Helper()
	srv := server.New(cfg)
	if err := srv.Start(); err != nil {
		t.Fatalf("start bus: %v", err)
	}
	t.Cleanup(func() {
		_ = srv.Stop()
	})
	return srv
}

func newClient(t *testing.T, ctx context.Context, socketPath, workspaceUID string) *client.Client {
	t.Helper()
	cfg := &client.Config{
		SocketPath:   socketPath,
		WorkspaceUID: workspaceUID,
		WorkspaceID:  workspaceUID,
		Label:        workspaceUID,
	}
	c := client.New(cfg)
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("connect client %s: %v", workspaceUID, err)
	}
	t.Cleanup(func() {
		_ = c.Close()
	})
	return c
}

func subscribeSync(t *testing.T, c *client.Client, workspaceUIDs, types []string, includeGlobal bool) {
	t.Helper()
	respCh := make(chan *protocol.EventEnvelope, 1)

	payload := protocol.SubscribeRequestPayload{
		WorkspaceUIDs: workspaceUIDs,
		Types:         types,
		IncludeGlobal: includeGlobal,
	}
	payloadBytes, _ := protocol.MarshalPayload(payload)
	event := protocol.NewEventEnvelope(
		ulid.New(),
		c.WorkspaceUID(),
		c.Source(),
		protocol.TypeBusSubscribeRequest,
		payloadBytes,
	)

	c.OnEvent(protocol.TypeBusSubscribeResponse, func(resp *protocol.EventEnvelope) {
		if resp.InReplyTo != event.EventID {
			return
		}
		select {
		case respCh <- resp:
		default:
		}
	})

	if err := c.SendSync(event); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	waitForEvent(t, respCh, 2*time.Second)
}

func sendHeartbeat(c *client.Client) error {
	payload := protocol.HeartbeatPayload{
		TS:     time.Now().UTC().Format(time.RFC3339),
		Status: "client",
	}
	payloadBytes, _ := protocol.MarshalPayload(payload)
	event := protocol.NewEventEnvelope(
		ulid.New(),
		c.WorkspaceUID(),
		c.Source(),
		protocol.TypeBusHeartbeat,
		payloadBytes,
	)
	return c.SendSync(event)
}

func listWorkspaces(t *testing.T, socketPath string) []protocol.WorkspaceInfo {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	uiClient := ui.NewClient(&ui.Config{SocketPath: socketPath})
	if err := uiClient.Connect(ctx); err != nil {
		t.Fatalf("connect ui client: %v", err)
	}
	defer uiClient.Close()

	workspaces, err := uiClient.ListWorkspaces(ctx)
	if err != nil {
		t.Fatalf("list workspaces: %v", err)
	}
	return workspaces
}

func containsWorkspace(workspaces []protocol.WorkspaceInfo, uid string) bool {
	for _, ws := range workspaces {
		if ws.WorkspaceUID == uid {
			return true
		}
	}
	return false
}

func waitForEvent(t *testing.T, ch <-chan *protocol.EventEnvelope, timeout time.Duration) *protocol.EventEnvelope {
	t.Helper()
	select {
	case event := <-ch:
		return event
	case <-time.After(timeout):
		t.Fatalf("timeout waiting for event")
		return nil
	}
}

func waitForCount(t *testing.T, counter *int64, want int64, deadline time.Time) {
	t.Helper()
	for time.Now().Before(deadline) {
		if atomic.LoadInt64(counter) >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for count=%d, got=%d", want, atomic.LoadInt64(counter))
}

func muteLogs(t *testing.T) {
	t.Helper()
	if debugEnabled() {
		return
	}
	prev := log.Writer()
	log.SetOutput(io.Discard)
	t.Cleanup(func() {
		log.SetOutput(prev)
	})
}

func debugEnabled() bool {
	return os.Getenv("TMUXCODER_TEST_LOGS") != ""
}

func shouldLog() bool {
	return debugEnabled() || testing.Verbose()
}

func testSocketPath(t *testing.T) string {
	t.Helper()

	base := "/tmp"
	dir, err := os.MkdirTemp(base, "tmuxcoder-test-")
	if err != nil {
		dir, err = os.MkdirTemp("", "tmuxcoder-test-")
		if err != nil {
			t.Fatalf("create temp dir: %v", err)
		}
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})
	return filepath.Join(dir, "bus.sock")
}
