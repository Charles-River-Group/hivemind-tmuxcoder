package protocol

import "encoding/json"

// Event type constants for the tmuxcoder.event.v1 protocol.
// Organized by category as defined in implementation_phase1_m0.md §3.2.

// Bus Control Events
const (
	TypeBusRegisterRequest         = "bus.register.request"
	TypeBusRegisterResponse        = "bus.register.response"
	TypeBusUnregisterRequest       = "bus.unregister.request"
	TypeBusUnregisterResponse      = "bus.unregister.response"
	TypeBusUnregisterDeliver       = "bus.unregister.deliver"
	TypeBusListWorkspacesRequest   = "bus.list_workspaces.request"
	TypeBusListWorkspacesResponse  = "bus.list_workspaces.response"
	TypeBusSubscribeRequest        = "bus.subscribe.request"
	TypeBusSubscribeResponse       = "bus.subscribe.response"
	TypeBusUnsubscribeRequest      = "bus.unsubscribe.request"
	TypeBusUnsubscribeResponse     = "bus.unsubscribe.response"
	TypeBusCreateWorkspaceRequest  = "bus.request.create_workspace"
	TypeBusCreateWorkspaceResponse = "bus.response.create_workspace"
	TypeBusHeartbeat               = "bus.heartbeat"
	TypeBusDrainingNotice          = "bus.draining.notice"
	TypeBusError                   = "bus.error"
	TypeBusJournalSegmentFinalized = "bus.journal.segment.finalized"
)

// Cross-Workspace Events
const (
	TypeBusSendRequest  = "bus.send.request"
	TypeBusSendResponse = "bus.send.response"
	TypeBusSendDeliver  = "bus.send.deliver"
)

// Backend I/O Events
const (
	TypeBackendSend        = "backend.send"
	TypeBackendStreamDelta = "backend.stream.delta"
	TypeBackendStreamEnd   = "backend.stream.end"
	TypeBackendError       = "backend.error"
)

// UI Events
const (
	TypeUILogAppend    = "ui.log.append"
	TypeUIStatusUpdate = "ui.status.update"
)

// --- Payload Structs ---

// BackendSendPayload is the payload for backend.send events.
// Used to inject input into a workspace's PTY/stdin.
type BackendSendPayload struct {
	Text      string `json:"text"`                 // Text to inject
	NoNewline bool   `json:"no_newline,omitempty"` // Don't append newline
	Source    string `json:"source,omitempty"`     // Source context (e.g., "ui", "orchestrator")
}

// BackendStreamDeltaPayload is the payload for backend.stream.delta events.
// It represents a streaming chunk from a backend/PTY.
type BackendStreamDeltaPayload struct {
	StreamID string `json:"stream_id"`
	Sequence int64  `json:"seq"`
	Text     string `json:"text"`
	MIME     string `json:"mime,omitempty"`
}

// BackendStreamEndPayload is the payload for backend.stream.end events.
// It signals the end of a stream for a given stream_id.
type BackendStreamEndPayload struct {
	StreamID string `json:"stream_id"`
	Sequence int64  `json:"seq"`
	TS       string `json:"ts"`
	Status   string `json:"status,omitempty"`
}

// RegisterRequestPayload is the payload for bus.register.request.
type RegisterRequestPayload struct {
	Source       Principal `json:"source"`
	PID          int       `json:"pid"`
	StartTS      string    `json:"start_ts"`
	Nonce        string    `json:"nonce"`
	WorkspaceUID string    `json:"workspace_uid"`
	WorkspaceID  string    `json:"workspace_id,omitempty"`
	ProjectUID   string    `json:"project_uid,omitempty"`
	DriveMode    string    `json:"drive_mode,omitempty"` // pty | agent | daemon
	Label        string    `json:"label,omitempty"`
}

// RegisterResponsePayload is the payload for bus.register.response.
type RegisterResponsePayload struct {
	Status                 string      `json:"status"` // "ok" or "error"
	ConnectionID           string      `json:"connection_id,omitempty"`
	RegisteredSource       Principal   `json:"registered_source,omitempty"`
	RegisteredWorkspaceUID string      `json:"registered_workspace_uid,omitempty"`
	BusVersion             string      `json:"bus_version,omitempty"`
	Error                  *EventError `json:"error,omitempty"`
}

// SubscribeRequestPayload is the payload for bus.subscribe.request.
type SubscribeRequestPayload struct {
	WorkspaceUIDs []string `json:"workspace_uids,omitempty"`
	Types         []string `json:"types,omitempty"`
	IncludeGlobal bool     `json:"include_global,omitempty"`
	Redaction     string   `json:"redaction,omitempty"` // metadata | full
}

// SubscribeResponsePayload is the payload for bus.subscribe.response.
type SubscribeResponsePayload struct {
	Status string      `json:"status"`
	Error  *EventError `json:"error,omitempty"`
}

// ListWorkspacesRequestPayload is the payload for bus.list_workspaces.request.
type ListWorkspacesRequestPayload struct {
	ExcludeSelf bool `json:"exclude_self,omitempty"`
}

// ListWorkspacesResponsePayload is the payload for bus.list_workspaces.response.
type ListWorkspacesResponsePayload struct {
	Status     string          `json:"status"`
	Workspaces []WorkspaceInfo `json:"workspaces,omitempty"`
	Error      *EventError     `json:"error,omitempty"`
}

// CreateWorkspaceRequestPayload is the payload for bus.request.create_workspace.
type CreateWorkspaceRequestPayload struct {
	WorkspaceUID string   `json:"workspace_uid,omitempty"`
	Label        string   `json:"label,omitempty"`
	Command      string   `json:"command,omitempty"`
	Args         []string `json:"args,omitempty"`
	WorkDir      string   `json:"work_dir,omitempty"`
	Layout       string   `json:"layout,omitempty"` // bridge-only | split-pane | new-window
	TmuxSession  string   `json:"tmux_session,omitempty"`
	TmuxWindow   string   `json:"tmux_window,omitempty"`
	TmuxPane     string   `json:"tmux_pane,omitempty"`
}

// CreateWorkspaceResponsePayload is the payload for bus.response.create_workspace.
type CreateWorkspaceResponsePayload struct {
	Status       string      `json:"status"`
	WorkspaceUID string      `json:"workspace_uid,omitempty"`
	WorkspaceID  string      `json:"workspace_id,omitempty"`
	Label        string      `json:"label,omitempty"`
	Error        *EventError `json:"error,omitempty"`
}

// WorkspaceInfo represents a registered workspace in the bus.
type WorkspaceInfo struct {
	WorkspaceUID  string `json:"workspace_uid"`
	WorkspaceID   string `json:"workspace_id,omitempty"`
	Label         string `json:"label,omitempty"`
	StatusSummary string `json:"status_summary,omitempty"`
}

// SendRequestPayload is the payload for bus.send.request.
type SendRequestPayload struct {
	ToWorkspaceUID string      `json:"to_workspace_uid"`
	ToWorkspaceID  string      `json:"to_workspace_id,omitempty"`
	Body           MessageBody `json:"body"`
	Summary        string      `json:"summary,omitempty"`
}

// SendResponsePayload is the payload for bus.send.response.
type SendResponsePayload struct {
	Status         string      `json:"status"`
	Delivered      bool        `json:"delivered"`
	DeliverEventID string      `json:"deliver_event_id,omitempty"`
	Error          *EventError `json:"error,omitempty"`
}

// SendDeliverPayload is the payload for bus.send.deliver.
type SendDeliverPayload struct {
	FromWorkspaceUID string      `json:"from_workspace_uid"`
	FromWorkspaceID  string      `json:"from_workspace_id,omitempty"`
	ToWorkspaceUID   string      `json:"to_workspace_uid"`
	DeliveryID       string      `json:"delivery_id"`
	Body             MessageBody `json:"body"`
	Summary          string      `json:"summary,omitempty"`
	DeliveryMode     string      `json:"delivery_mode,omitempty"`   // direct | inbox
	PolicyDecision   string      `json:"policy_decision,omitempty"` // allow | deny | pending
	Redaction        string      `json:"redaction,omitempty"`       // none | metadata | full
}

// MessageBody is a tagged union for cross-workspace message content.
type MessageBody struct {
	Kind        string       `json:"kind"` // text | artifact_ref | task_ref
	Text        string       `json:"text,omitempty"`
	ArtifactRef *ArtifactRef `json:"artifact_ref,omitempty"`
	TaskRef     *TaskRef     `json:"task_ref,omitempty"`
}

// ArtifactRef references a content-addressed artifact.
type ArtifactRef struct {
	Store             string `json:"store"` // shared | workspace
	StoreWorkspaceUID string `json:"store_workspace_uid,omitempty"`
	SHA256            string `json:"sha256"`
	SizeBytes         int64  `json:"size_bytes"`
	MIME              string `json:"mime"`
	CreatedTS         string `json:"created_ts"`
	Label             string `json:"label,omitempty"`
}

// TaskRef references a task in another workspace.
type TaskRef struct {
	WorkspaceUID string `json:"workspace_uid"`
	TaskID       string `json:"task_id"`
	Label        string `json:"label,omitempty"`
}

// HeartbeatPayload is the payload for bus.heartbeat.
type HeartbeatPayload struct {
	ConnectionID string     `json:"connection_id"`
	Sequence     int64      `json:"sequence"`
	TS           string     `json:"ts"`
	Status       string     `json:"status"` // ok | draining | overloaded
	QueueDepth   QueueDepth `json:"queue_depth,omitempty"`
}

// QueueDepth reports the current queue depths.
type QueueDepth struct {
	Control int `json:"control"`
	Data    int `json:"data"`
}

// UILogAppendPayload is the payload for ui.log.append.
type UILogAppendPayload struct {
	Level     string                 `json:"level"` // debug | info | warn | error
	Text      string                 `json:"text"`
	SessionID string                 `json:"session_id,omitempty"`
	Context   map[string]interface{} `json:"context,omitempty"`
}

// UIStatusUpdatePayload is the payload for ui.status.update.
type UIStatusUpdatePayload struct {
	WorkspaceUID string                 `json:"workspace_uid"`
	Status       string                 `json:"status"` // running | idle | blocked
	SinceTS      string                 `json:"since_ts"`
	Details      map[string]interface{} `json:"details,omitempty"`
}

// EventError represents a structured error in event payloads.
type EventError struct {
	Kind      string `json:"kind"` // policy | transport | backend | timeout | protocol | canceled
	Code      string `json:"code"` // DEST_NOT_CONNECTED, POLICY_DENY, etc.
	Retryable bool   `json:"retryable"`
	Message   string `json:"message"`
}

// Error code constants.
const (
	ErrCodeDestNotConnected   = "DEST_NOT_CONNECTED"
	ErrCodePolicyDeny         = "POLICY_DENY"
	ErrCodePayloadTooLarge    = "PAYLOAD_TOO_LARGE"
	ErrCodeRateLimited        = "RATE_LIMITED"
	ErrCodeBadSchema          = "BAD_SCHEMA"
	ErrCodeUnknownAction      = "UNKNOWN_ACTION"
	ErrCodeVersionUnsupported = "VERSION_UNSUPPORTED"
	ErrCodeBackpressure       = "BACKPRESSURE"
	ErrCodeHeartbeatTimeout   = "HEARTBEAT_TIMEOUT"
	ErrCodeLockBusy           = "LOCK_BUSY"
	ErrCodeLockTokenInvalid   = "LOCK_TOKEN_INVALID"
	ErrCodeLockExpired        = "LOCK_EXPIRED"
)

// Error kind constants.
const (
	ErrKindPolicy    = "policy"
	ErrKindTransport = "transport"
	ErrKindBackend   = "backend"
	ErrKindTimeout   = "timeout"
	ErrKindProtocol  = "protocol"
	ErrKindCanceled  = "canceled"
)

// NewEventError creates a new EventError.
func NewEventError(kind, code string, retryable bool, message string) *EventError {
	return &EventError{
		Kind:      kind,
		Code:      code,
		Retryable: retryable,
		Message:   message,
	}
}

// MarshalPayload marshals a payload struct to json.RawMessage.
func MarshalPayload(v interface{}) (json.RawMessage, error) {
	return json.Marshal(v)
}

// UnmarshalPayload unmarshals a json.RawMessage to a payload struct.
func UnmarshalPayload(data json.RawMessage, v interface{}) error {
	return json.Unmarshal(data, v)
}
