//go:build integration
// +build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/opencode/hivemind-tmuxcoder/internal/bus/server"
	"github.com/opencode/hivemind-tmuxcoder/internal/protocol"
	"github.com/opencode/hivemind-tmuxcoder/internal/ui"
	"github.com/opencode/hivemind-tmuxcoder/pkg/util/ulid"
)

func TestCreateWorkspaceFlow(t *testing.T) {
	muteLogs(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	socketPath := testSocketPath(t)
	cfg := server.DefaultConfig()
	cfg.SocketPath = socketPath
	cfg.IdleTimeout = 2 * time.Second
	startBus(t, cfg)

	controller := newClient(t, ctx, socketPath, "controller-sim")
	subscribeSync(t, controller, nil, []string{protocol.TypeBusCreateWorkspaceRequest}, false)

	controller.OnEvent(protocol.TypeBusCreateWorkspaceRequest, func(req *protocol.EventEnvelope) {
		var reqPayload protocol.CreateWorkspaceRequestPayload
		_ = protocol.UnmarshalPayload(req.Payload, &reqPayload)

		respPayload := protocol.CreateWorkspaceResponsePayload{
			Status:       "ok",
			WorkspaceUID: "ws-" + ulid.New(),
			WorkspaceID:  "bridge:ws",
			Label:        reqPayload.Label,
		}
		respPayloadBytes, _ := protocol.MarshalPayload(respPayload)

		resp := protocol.NewEventEnvelope(
			ulid.New(),
			req.WorkspaceUID,
			controller.Source(),
			protocol.TypeBusCreateWorkspaceResponse,
			respPayloadBytes,
		)
		resp.InReplyTo = req.EventID
		resp.CorrelationID = req.CorrelationID
		_ = controller.SendSync(resp)
	})

	uiClient := ui.NewClient(&ui.Config{SocketPath: socketPath})
	if err := uiClient.Connect(ctx); err != nil {
		t.Fatalf("connect ui: %v", err)
	}
	defer uiClient.Close()

	createCtx, createCancel := context.WithTimeout(ctx, 2*time.Second)
	defer createCancel()

	if err := uiClient.RequestCreateWorkspace(createCtx, protocol.CreateWorkspaceRequestPayload{Label: "w1"}); err != nil {
		t.Fatalf("request create workspace: %v", err)
	}
}
