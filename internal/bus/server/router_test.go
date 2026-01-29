package server

import (
	"net"
	"testing"
	"time"

	"github.com/opencode/hivemind-tmuxcoder/internal/protocol"
	"github.com/opencode/hivemind-tmuxcoder/pkg/util/ulid"
)

func TestCreateWorkspaceResponseDirectDeliveryWithoutSubscription(t *testing.T) {
	uiServerConn, uiClientConn := net.Pipe()
	t.Cleanup(func() { _ = uiServerConn.Close() })
	t.Cleanup(func() { _ = uiClientConn.Close() })

	controllerServerConn, controllerClientConn := net.Pipe()
	t.Cleanup(func() { _ = controllerServerConn.Close() })
	t.Cleanup(func() { _ = controllerClientConn.Close() })

	uiConn := NewConnection(uiServerConn)
	uiConn.ID = "conn-ui"
	uiConn.WorkspaceUID = "ui-test"
	uiConn.Label = "tmuxcoder-ui"

	controllerConn := NewConnection(controllerServerConn)
	controllerConn.ID = "conn-controller"
	controllerConn.WorkspaceUID = "controller-test"
	controllerConn.Label = "tmuxcoder-controller"

	registry := NewRegistry()
	if err := registry.Register(uiConn); err != nil {
		t.Fatalf("register ui: %v", err)
	}
	if err := registry.Register(controllerConn); err != nil {
		t.Fatalf("register controller: %v", err)
	}

	router := NewRouter(registry)

	requestID := ulid.New()
	respPayload := protocol.CreateWorkspaceResponsePayload{
		Status:       "ok",
		WorkspaceUID: "ws-1",
		WorkspaceID:  "bridge:ws-1",
		Label:        "w1",
	}
	respPayloadBytes, _ := protocol.MarshalPayload(respPayload)
	event := protocol.NewEventEnvelope(
		ulid.New(),
		uiConn.WorkspaceUID,
		protocol.Principal{Kind: protocol.PrincipalKindWorkspace, ID: controllerConn.WorkspaceUID},
		protocol.TypeBusCreateWorkspaceResponse,
		respPayloadBytes,
	)
	event.InReplyTo = requestID

	decoder := protocol.NewStreamDecoder(uiClientConn)
	if err := uiClientConn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	gotCh := make(chan *protocol.EventEnvelope, 1)
	errCh := make(chan error, 1)
	go func() {
		got, err := decoder.Decode()
		if err != nil {
			errCh <- err
			return
		}
		gotCh <- got
	}()

	if err := router.Route(event, controllerConn); err != nil {
		t.Fatalf("route: %v", err)
	}

	var got *protocol.EventEnvelope
	select {
	case got = <-gotCh:
	case err := <-errCh:
		t.Fatalf("decode: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for response")
	}
	if got.Type != protocol.TypeBusCreateWorkspaceResponse {
		t.Fatalf("unexpected type: %s", got.Type)
	}
	if got.WorkspaceUID != uiConn.WorkspaceUID {
		t.Fatalf("unexpected workspace uid: %s", got.WorkspaceUID)
	}
	if got.InReplyTo != requestID {
		t.Fatalf("unexpected in_reply_to: %s", got.InReplyTo)
	}
}
