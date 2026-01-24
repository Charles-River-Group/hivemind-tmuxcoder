package protocol

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// ValidationError represents a validation failure with context.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation error in field '%s': %s", e.Field, e.Message)
}

// Validator validates EventEnvelope structures.
type Validator struct {
	// knownTypes is a set of valid event types.
	knownTypes map[string]bool
}

// NewValidator creates a new validator with the default known types.
func NewValidator() *Validator {
	v := &Validator{
		knownTypes: make(map[string]bool),
	}
	// Register all known event types
	v.registerDefaultTypes()
	return v
}

// registerDefaultTypes registers all M0 event types.
func (v *Validator) registerDefaultTypes() {
	types := []string{
		// Bus Control Events
		TypeBusRegisterRequest,
		TypeBusRegisterResponse,
		TypeBusUnregisterRequest,
		TypeBusUnregisterResponse,
		TypeBusUnregisterDeliver,
		TypeBusListWorkspacesRequest,
		TypeBusListWorkspacesResponse,
		TypeBusSubscribeRequest,
		TypeBusSubscribeResponse,
		TypeBusUnsubscribeRequest,
		TypeBusUnsubscribeResponse,
		TypeBusHeartbeat,
		TypeBusDrainingNotice,
		TypeBusError,
		TypeBusJournalSegmentFinalized,
		// Cross-Workspace Events
		TypeBusSendRequest,
		TypeBusSendResponse,
		TypeBusSendDeliver,
		// Backend I/O Events
		TypeBackendSend,
		TypeBackendStreamDelta,
		TypeBackendStreamEnd,
		TypeBackendError,
		// UI Events
		TypeUILogAppend,
		TypeUIStatusUpdate,
	}
	for _, t := range types {
		v.knownTypes[t] = true
	}
}

// RegisterType adds a custom event type to the validator.
func (v *Validator) RegisterType(eventType string) {
	v.knownTypes[eventType] = true
}

// IsKnownType returns true if the event type is registered.
func (v *Validator) IsKnownType(eventType string) bool {
	return v.knownTypes[eventType]
}

// Validate checks if an EventEnvelope has all required fields.
func (v *Validator) Validate(e *EventEnvelope) error {
	// Required fields
	if e.Proto == "" {
		return &ValidationError{Field: "proto", Message: "required field is empty"}
	}
	if e.Proto != ProtoVersion {
		return &ValidationError{
			Field:   "proto",
			Message: fmt.Sprintf("unsupported protocol version: %s (expected %s)", e.Proto, ProtoVersion),
		}
	}
	if e.EventID == "" {
		return &ValidationError{Field: "event_id", Message: "required field is empty"}
	}
	if e.Timestamp == "" {
		return &ValidationError{Field: "ts", Message: "required field is empty"}
	}
	if e.WorkspaceUID == "" {
		return &ValidationError{Field: "workspace_uid", Message: "required field is empty"}
	}
	if e.Source.Kind == "" {
		return &ValidationError{Field: "source.kind", Message: "required field is empty"}
	}
	if e.Source.ID == "" {
		return &ValidationError{Field: "source.id", Message: "required field is empty"}
	}
	if e.Type == "" {
		return &ValidationError{Field: "type", Message: "required field is empty"}
	}

	// Validate source.kind is a known principal kind
	if !isValidPrincipalKind(e.Source.Kind) {
		return &ValidationError{
			Field:   "source.kind",
			Message: fmt.Sprintf("unknown principal kind: %s", e.Source.Kind),
		}
	}

	// Validate dest.kind if present
	if e.Dest != nil && e.Dest.Kind != "" {
		if !isValidPrincipalKind(e.Dest.Kind) {
			return &ValidationError{
				Field:   "dest.kind",
				Message: fmt.Sprintf("unknown principal kind: %s", e.Dest.Kind),
			}
		}
	}

	// Check if event type is known (warning only, not an error)
	// Unknown types are allowed for extensibility but should be logged
	if !v.IsKnownType(e.Type) {
		// This is intentionally not an error for forward compatibility
		// Callers can use IsKnownType() to check before validation if strict mode is needed
	}

	return nil
}

// ValidateStrict performs validation and rejects unknown event types.
func (v *Validator) ValidateStrict(e *EventEnvelope) error {
	if err := v.Validate(e); err != nil {
		return err
	}
	if !v.IsKnownType(e.Type) {
		return &ValidationError{
			Field:   "type",
			Message: fmt.Sprintf("unknown event type: %s", e.Type),
		}
	}
	return nil
}

// isValidPrincipalKind checks if a principal kind is valid.
func isValidPrincipalKind(kind string) bool {
	switch kind {
	case PrincipalKindWorkspace,
		PrincipalKindUI,
		PrincipalKindOrchestrator,
		PrincipalKindService,
		PrincipalKindBus,
		PrincipalKindBackend:
		return true
	default:
		return false
	}
}

// ComputePayloadHash computes the SHA256 hash of a canonicalized payload.
// The canonicalization follows RFC 8785 (JSON Canonicalization Scheme) approximation:
// - Keys are sorted lexicographically
// - No whitespace outside strings
// - Unicode escaping is minimized
func ComputePayloadHash(payload json.RawMessage) (string, error) {
	if len(payload) == 0 {
		return "", nil
	}

	// Parse and re-serialize for canonicalization
	var data interface{}
	if err := json.Unmarshal(payload, &data); err != nil {
		return "", fmt.Errorf("failed to parse payload for hashing: %w", err)
	}

	canonical, err := canonicalize(data)
	if err != nil {
		return "", err
	}

	hash := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(hash[:]), nil
}

// canonicalize produces canonical JSON bytes for hashing.
// This is a simplified RFC 8785 implementation.
func canonicalize(data interface{}) ([]byte, error) {
	switch v := data.(type) {
	case map[string]interface{}:
		return canonicalizeObject(v)
	case []interface{}:
		return canonicalizeArray(v)
	default:
		// For primitives, standard JSON encoding is sufficient
		return json.Marshal(v)
	}
}

func canonicalizeObject(obj map[string]interface{}) ([]byte, error) {
	// Sort keys lexicographically
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	result := []byte("{")
	for i, k := range keys {
		if i > 0 {
			result = append(result, ',')
		}
		// Add key
		keyBytes, _ := json.Marshal(k)
		result = append(result, keyBytes...)
		result = append(result, ':')
		// Add value
		valBytes, err := canonicalize(obj[k])
		if err != nil {
			return nil, err
		}
		result = append(result, valBytes...)
	}
	result = append(result, '}')
	return result, nil
}

func canonicalizeArray(arr []interface{}) ([]byte, error) {
	result := []byte("[")
	for i, item := range arr {
		if i > 0 {
			result = append(result, ',')
		}
		itemBytes, err := canonicalize(item)
		if err != nil {
			return nil, err
		}
		result = append(result, itemBytes...)
	}
	result = append(result, ']')
	return result, nil
}
