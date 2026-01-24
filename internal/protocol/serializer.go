package protocol

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
)

// Serializer handles NDJSON serialization and deserialization for events.
// NDJSON (Newline Delimited JSON) uses one JSON object per line.
type Serializer struct{}

// NewSerializer creates a new NDJSON serializer.
func NewSerializer() *Serializer {
	return &Serializer{}
}

// Encode serializes an EventEnvelope to NDJSON bytes (with trailing newline).
func (s *Serializer) Encode(e *EventEnvelope) ([]byte, error) {
	data, err := json.Marshal(e)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal event: %w", err)
	}
	// Append newline for NDJSON format
	return append(data, '\n'), nil
}

// Decode deserializes NDJSON bytes to an EventEnvelope.
// The input should be a single JSON line (with or without trailing newline).
func (s *Serializer) Decode(data []byte) (*EventEnvelope, error) {
	var e EventEnvelope
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, fmt.Errorf("failed to unmarshal event: %w", err)
	}
	return &e, nil
}

// StreamEncoder writes events to an io.Writer as NDJSON.
type StreamEncoder struct {
	w   io.Writer
	enc *json.Encoder
}

// NewStreamEncoder creates a new stream encoder.
func NewStreamEncoder(w io.Writer) *StreamEncoder {
	return &StreamEncoder{
		w:   w,
		enc: json.NewEncoder(w),
	}
}

// Encode writes an event to the stream.
func (e *StreamEncoder) Encode(event *EventEnvelope) error {
	return e.enc.Encode(event)
}

// StreamDecoder reads events from an io.Reader as NDJSON.
type StreamDecoder struct {
	scanner *bufio.Scanner
}

// NewStreamDecoder creates a new stream decoder.
func NewStreamDecoder(r io.Reader) *StreamDecoder {
	scanner := bufio.NewScanner(r)
	// Set a larger buffer for potentially large events
	scanner.Buffer(make([]byte, 64*1024), 1024*1024) // 64KB initial, 1MB max
	return &StreamDecoder{
		scanner: scanner,
	}
}

// Decode reads and decodes the next event from the stream.
// Returns io.EOF when there are no more events.
func (d *StreamDecoder) Decode() (*EventEnvelope, error) {
	if !d.scanner.Scan() {
		if err := d.scanner.Err(); err != nil {
			return nil, fmt.Errorf("scanner error: %w", err)
		}
		return nil, io.EOF
	}

	line := d.scanner.Bytes()
	if len(line) == 0 {
		// Skip empty lines
		return d.Decode()
	}

	var e EventEnvelope
	if err := json.Unmarshal(line, &e); err != nil {
		return nil, fmt.Errorf("failed to unmarshal event line: %w", err)
	}
	return &e, nil
}

// Err returns any error that occurred during scanning.
func (d *StreamDecoder) Err() error {
	return d.scanner.Err()
}
