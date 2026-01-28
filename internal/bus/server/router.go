package server

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/opencode/hivemind-tmuxcoder/internal/protocol"
	"github.com/opencode/hivemind-tmuxcoder/pkg/util/ulid"
)

// Router handles event routing between connections.
type Router struct {
	registry *Registry
}

// NewRouter creates a new event router.
func NewRouter(registry *Registry) *Router {
	return &Router{
		registry: registry,
	}
}

// Route routes an event to the appropriate destination(s).
func (r *Router) Route(event *protocol.EventEnvelope, sender *Connection) error {
	// Handle different event types
	switch event.Type {
	case protocol.TypeBusSubscribeRequest:
		return r.handleSubscribe(event, sender)
	case protocol.TypeBusUnsubscribeRequest:
		return r.handleUnsubscribe(event, sender)
	case protocol.TypeBusListWorkspacesRequest:
		return r.handleListWorkspaces(event, sender)
	case protocol.TypeBusHeartbeat:
		// Client keepalive, no routing needed.
		return nil
	case protocol.TypeBusSendRequest:
		return r.handleSendRequest(event, sender)
	case protocol.TypeBackendSend:
		return r.handleBackendSend(event)
	default:
		// For other events, broadcast to subscribers
		return r.broadcast(event, sender)
	}
}

// handleSubscribe processes a subscription request.
func (r *Router) handleSubscribe(event *protocol.EventEnvelope, sender *Connection) error {
	var payload protocol.SubscribeRequestPayload
	if err := protocol.UnmarshalPayload(event.Payload, &payload); err != nil {
		return fmt.Errorf("invalid subscribe payload: %w", err)
	}

	// Create subscription filter
	filter := SubscriptionFilter{
		WorkspaceUIDs: payload.WorkspaceUIDs,
		Types:         payload.Types,
		IncludeGlobal: payload.IncludeGlobal,
		Redaction:     payload.Redaction,
	}

	// Add subscription to connection
	sender.AddSubscription(filter)

	// Send response
	responsePayload := protocol.SubscribeResponsePayload{
		Status: "ok",
	}
	payloadBytes, _ := protocol.MarshalPayload(responsePayload)

	response := protocol.NewEventEnvelope(
		ulid.New(),
		sender.WorkspaceUID,
		protocol.Principal{Kind: protocol.PrincipalKindBus, ID: "bus"},
		protocol.TypeBusSubscribeResponse,
		payloadBytes,
	)
	response.InReplyTo = event.EventID
	response.CorrelationID = event.CorrelationID

	return sender.Send(response)
}

// handleUnsubscribe processes an unsubscription request.
func (r *Router) handleUnsubscribe(event *protocol.EventEnvelope, sender *Connection) error {
	// Clear all subscriptions
	sender.ClearSubscriptions()

	// Send response
	responsePayload := protocol.SubscribeResponsePayload{
		Status: "ok",
	}
	payloadBytes, _ := protocol.MarshalPayload(responsePayload)

	response := protocol.NewEventEnvelope(
		ulid.New(),
		sender.WorkspaceUID,
		protocol.Principal{Kind: protocol.PrincipalKindBus, ID: "bus"},
		protocol.TypeBusUnsubscribeResponse,
		payloadBytes,
	)
	response.InReplyTo = event.EventID
	response.CorrelationID = event.CorrelationID

	return sender.Send(response)
}

// handleListWorkspaces returns a list of registered workspaces.
func (r *Router) handleListWorkspaces(event *protocol.EventEnvelope, sender *Connection) error {
	workspaces := r.registry.AllWorkspaces()

	var payload protocol.ListWorkspacesRequestPayload
	if len(event.Payload) > 0 {
		if err := protocol.UnmarshalPayload(event.Payload, &payload); err != nil {
			log.Printf("[ROUTER] Invalid list workspaces payload: %v", err)
		}
	}

	infos := make([]protocol.WorkspaceInfo, len(workspaces))
	count := 0
	for _, conn := range workspaces {
		if payload.ExcludeSelf && conn.WorkspaceUID == sender.WorkspaceUID {
			continue
		}
		infos[count] = conn.ToWorkspaceInfo()
		count++
	}
	infos = infos[:count]

	responsePayload := protocol.ListWorkspacesResponsePayload{
		Status:     "ok",
		Workspaces: infos,
	}
	payloadBytes, _ := protocol.MarshalPayload(responsePayload)

	response := protocol.NewEventEnvelope(
		ulid.New(),
		sender.WorkspaceUID,
		protocol.Principal{Kind: protocol.PrincipalKindBus, ID: "bus"},
		protocol.TypeBusListWorkspacesResponse,
		payloadBytes,
	)
	response.InReplyTo = event.EventID
	response.CorrelationID = event.CorrelationID

	return sender.Send(response)
}

// handleSendRequest routes a cross-workspace send request.
func (r *Router) handleSendRequest(event *protocol.EventEnvelope, sender *Connection) error {
	var payload protocol.SendRequestPayload
	if err := protocol.UnmarshalPayload(event.Payload, &payload); err != nil {
		return r.sendErrorResponse(sender, event, protocol.ErrKindProtocol,
			protocol.ErrCodeBadSchema, false, "invalid send payload")
	}

	// Find target workspace
	target := r.registry.GetByWorkspace(payload.ToWorkspaceUID)
	if target == nil {
		return r.sendErrorResponse(sender, event, protocol.ErrKindTransport,
			protocol.ErrCodeDestNotConnected, true, "destination workspace not connected")
	}

	// Create delivery event
	deliveryID := ulid.New()
	deliverPayload := protocol.SendDeliverPayload{
		FromWorkspaceUID: sender.WorkspaceUID,
		FromWorkspaceID:  sender.WorkspaceID,
		ToWorkspaceUID:   payload.ToWorkspaceUID,
		DeliveryID:       deliveryID,
		Body:             payload.Body,
		Summary:          payload.Summary,
		DeliveryMode:     "direct",
		PolicyDecision:   "allow",
		Redaction:        "none",
	}
	deliverPayloadBytes, _ := protocol.MarshalPayload(deliverPayload)

	deliverEvent := protocol.NewEventEnvelope(
		deliveryID,
		payload.ToWorkspaceUID, // Attach to receiver workspace
		sender.Source,
		protocol.TypeBusSendDeliver,
		deliverPayloadBytes,
	)
	deliverEvent.CorrelationID = event.CorrelationID
	deliverEvent.ParentEventID = event.EventID

	// Deliver to target
	if err := target.Send(deliverEvent); err != nil {
		log.Printf("[ROUTER] Failed to deliver to %s: %v", target.ID, err)
		return r.sendErrorResponse(sender, event, protocol.ErrKindTransport,
			protocol.ErrCodeDestNotConnected, true, "delivery failed")
	}

	// Send success response to sender
	responsePayload := protocol.SendResponsePayload{
		Status:         "ok",
		Delivered:      true,
		DeliverEventID: deliveryID,
	}
	payloadBytes, _ := protocol.MarshalPayload(responsePayload)

	response := protocol.NewEventEnvelope(
		ulid.New(),
		sender.WorkspaceUID,
		protocol.Principal{Kind: protocol.PrincipalKindBus, ID: "bus"},
		protocol.TypeBusSendResponse,
		payloadBytes,
	)
	response.InReplyTo = event.EventID
	response.CorrelationID = event.CorrelationID

	return sender.Send(response)
}

// handleBackendSend routes backend.send directly to the target workspace.
func (r *Router) handleBackendSend(event *protocol.EventEnvelope) error {
	log.Printf("[ROUTER] handleBackendSend: routing to workspace %s", event.WorkspaceUID)
	target := r.registry.GetByWorkspace(event.WorkspaceUID)
	if target == nil {
		log.Printf("[ROUTER] handleBackendSend: target not found! Available workspaces:")
		for _, ws := range r.registry.AllWorkspaces() {
			log.Printf("[ROUTER]   - %s (label: %s)", ws.WorkspaceUID, ws.Label)
		}
		return fmt.Errorf("backend.send target not connected: %s", event.WorkspaceUID)
	}
	log.Printf("[ROUTER] handleBackendSend: sending to connection %s", target.ID)
	return target.Send(event)
}

// broadcast sends an event to all matching subscribers.
func (r *Router) broadcast(event *protocol.EventEnvelope, sender *Connection) error {
	connections := r.registry.All()

	for _, conn := range connections {
		// Don't send back to sender
		if conn.ID == sender.ID {
			continue
		}

		// Check if connection is subscribed
		if conn.MatchesSubscription(event) {
			if err := conn.Send(event); err != nil {
				log.Printf("[ROUTER] Failed to broadcast to %s: %v", conn.ID, err)
			}
		}
	}

	return nil
}

// sendErrorResponse sends an error response to the sender.
func (r *Router) sendErrorResponse(sender *Connection, request *protocol.EventEnvelope,
	kind, code string, retryable bool, message string) error {

	errPayload := protocol.NewEventError(kind, code, retryable, message)

	var responsePayload interface{}
	switch request.Type {
	case protocol.TypeBusSendRequest:
		responsePayload = protocol.SendResponsePayload{
			Status:    "error",
			Delivered: false,
			Error:     errPayload,
		}
	default:
		responsePayload = map[string]interface{}{
			"status": "error",
			"error":  errPayload,
		}
	}

	payloadBytes, _ := json.Marshal(responsePayload)

	responseType := request.Type
	// Convert request type to response type
	switch request.Type {
	case protocol.TypeBusSendRequest:
		responseType = protocol.TypeBusSendResponse
	}

	response := protocol.NewEventEnvelope(
		ulid.New(),
		sender.WorkspaceUID,
		protocol.Principal{Kind: protocol.PrincipalKindBus, ID: "bus"},
		responseType,
		payloadBytes,
	)
	response.InReplyTo = request.EventID
	response.CorrelationID = request.CorrelationID

	return sender.Send(response)
}
