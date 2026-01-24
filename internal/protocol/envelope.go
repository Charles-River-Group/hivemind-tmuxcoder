// Package protocol defines the tmuxcoder.event.v1 event protocol.
// It provides the core event envelope structure, type definitions,
// and serialization logic for the Bus Server.
package protocol

import (
	"encoding/json"
	"time"
)

// ProtoVersion is the protocol version string.
const ProtoVersion = "tmuxcoder.event.v1"

// GlobalWorkspaceUID is the special UUID for bus-global events.
const GlobalWorkspaceUID = "00000000-0000-0000-0000-000000000000"

// EventEnvelope is the standard envelope for all events on the bus.
// It implements the tmuxcoder.event.v1 protocol as defined in coder.md §5.5.
type EventEnvelope struct {
	// Proto is the protocol version (must be "tmuxcoder.event.v1").
	Proto string `json:"proto"`

	// EventID is a globally unique identifier (ULID recommended).
	EventID string `json:"event_id"`

	// Timestamp is the RFC3339 formatted event creation time.
	Timestamp string `json:"ts"`

	// WorkspaceUID is the workspace context for storage/rendering.
	// This is NOT necessarily the sender's identity.
	// Use GlobalWorkspaceUID for bus-global events.
	WorkspaceUID string `json:"workspace_uid"`

	// WorkspaceID is an optional operator-friendly alias (e.g., "tmux:$1:@3:%7").
	// This is mutable and should NOT be used for routing.
	WorkspaceID string `json:"workspace_id,omitempty"`

	// Source identifies the principal that emitted the event.
	// The bus binds this to the connection at registration time.
	Source Principal `json:"source"`

	// Dest is the optional routing destination.
	// If nil, the event is broadcast to subscribers.
	Dest *Principal `json:"dest,omitempty"`

	// Type is the event type string (e.g., "bus.register.request").
	Type string `json:"type"`

	// Payload is the type-specific payload as raw JSON.
	Payload json.RawMessage `json:"payload"`

	// CorrelationID is the end-to-end trace ID across components.
	CorrelationID string `json:"correlation_id,omitempty"`

	// ParentEventID is the direct causal parent event ID.
	ParentEventID string `json:"parent_event_id,omitempty"`

	// InReplyTo is the request event ID this event responds to.
	InReplyTo string `json:"in_reply_to,omitempty"`

	// OperationID is the workspace-local operation handle.
	OperationID string `json:"operation_id,omitempty"`

	// TaskID is the plan-local DAG node identifier.
	TaskID string `json:"task_id,omitempty"`

	// PayloadHash is the SHA256 hash of the canonical payload (for audit).
	PayloadHash string `json:"payload_hash,omitempty"`
}

// Principal identifies a bus participant (workspace, UI, orchestrator, etc.).
type Principal struct {
	// Kind is the principal type.
	// Valid values: "workspace", "ui", "orchestrator", "service", "bus", "backend"
	Kind string `json:"kind"`

	// ID is the principal identifier.
	// For workspaces, this is the workspace_uid.
	// For other types, this is a unique client ID.
	ID string `json:"id"`
}

// PrincipalKind constants for principal types.
const (
	PrincipalKindWorkspace    = "workspace"
	PrincipalKindUI           = "ui"
	PrincipalKindOrchestrator = "orchestrator"
	PrincipalKindService      = "service"
	PrincipalKindBus          = "bus"
	PrincipalKindBackend      = "backend"
)

// NewEventEnvelope creates a new event envelope with required fields populated.
func NewEventEnvelope(eventID, workspaceUID string, source Principal, eventType string, payload json.RawMessage) *EventEnvelope {
	return &EventEnvelope{
		Proto:        ProtoVersion,
		EventID:      eventID,
		Timestamp:    time.Now().UTC().Format(time.RFC3339),
		WorkspaceUID: workspaceUID,
		Source:       source,
		Type:         eventType,
		Payload:      payload,
	}
}

// IsGlobalEvent returns true if this event is a bus-global event.
func (e *EventEnvelope) IsGlobalEvent() bool {
	return e.WorkspaceUID == GlobalWorkspaceUID
}

// HasDest returns true if this event has a specific destination.
func (e *EventEnvelope) HasDest() bool {
	return e.Dest != nil && e.Dest.ID != ""
}

// SetCorrelation sets the correlation and parent event ID.
// If correlationID is empty, it defaults to the event's own EventID.
func (e *EventEnvelope) SetCorrelation(correlationID, parentEventID string) {
	if correlationID == "" {
		e.CorrelationID = e.EventID
	} else {
		e.CorrelationID = correlationID
	}
	e.ParentEventID = parentEventID
}

// Clone creates a shallow copy of the envelope.
func (e *EventEnvelope) Clone() *EventEnvelope {
	clone := *e
	if e.Dest != nil {
		destCopy := *e.Dest
		clone.Dest = &destCopy
	}
	return &clone
}
