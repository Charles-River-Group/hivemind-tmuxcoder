// Package journal implements the event journal for TmuxCoder Bus Server.
// The journal is an append-only NDJSON log of all events.
package journal

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/opencode/hivemind-tmuxcoder/internal/protocol"
)

// Config holds journal configuration.
type Config struct {
	Directory        string
	MaxSegmentBytes  int64
	RotationInterval time.Duration
	RetentionDays    int
}

// DefaultConfig returns the default journal configuration.
func DefaultConfig() *Config {
	home, _ := os.UserHomeDir()
	return &Config{
		Directory:        filepath.Join(home, ".tmuxcoder", "state", "journal"),
		MaxSegmentBytes:  10 * 1024 * 1024, // 10MB
		RotationInterval: time.Hour,
		RetentionDays:    30,
	}
}

// Writer writes events to the journal in NDJSON format.
type Writer struct {
	config *Config

	mu           sync.Mutex
	currentFile  *os.File
	currentPath  string
	currentBytes int64
	segmentStart time.Time

	manifest *Manifest
	encoder  *json.Encoder

	closed bool
}

// NewWriter creates a new journal writer.
func NewWriter(config *Config) (*Writer, error) {
	if config == nil {
		config = DefaultConfig()
	}

	// Create directory if needed
	if err := os.MkdirAll(config.Directory, 0700); err != nil {
		return nil, fmt.Errorf("failed to create journal directory: %w", err)
	}

	w := &Writer{
		config: config,
	}

	// Load or create manifest
	manifest, err := LoadManifest(config.Directory)
	if err != nil {
		manifest = NewManifest()
	}
	w.manifest = manifest

	// Open new segment
	if err := w.openNewSegment(); err != nil {
		return nil, err
	}

	return w, nil
}

// Append writes an event to the journal.
func (w *Writer) Append(event *protocol.EventEnvelope) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return fmt.Errorf("journal writer is closed")
	}

	// Check if rotation is needed
	if w.shouldRotate() {
		if err := w.rotate(); err != nil {
			return fmt.Errorf("rotation failed: %w", err)
		}
	}

	// Write event as NDJSON
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}
	data = append(data, '\n')

	n, err := w.currentFile.Write(data)
	if err != nil {
		return fmt.Errorf("failed to write event: %w", err)
	}
	w.currentBytes += int64(n)

	// Update manifest tracking
	w.manifest.UpdateCurrentSegment(event)

	return nil
}

// Rotate forces a rotation of the current segment.
func (w *Writer) Rotate() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.rotate()
}

// shouldRotate checks if the current segment should be rotated.
func (w *Writer) shouldRotate() bool {
	// Size-based rotation
	if w.currentBytes >= w.config.MaxSegmentBytes {
		return true
	}

	// Time-based rotation
	if time.Since(w.segmentStart) >= w.config.RotationInterval {
		return true
	}

	return false
}

// rotate closes the current segment and opens a new one.
func (w *Writer) rotate() error {
	// Finalize current segment
	if err := w.finalizeCurrentSegment(); err != nil {
		return err
	}

	// Open new segment
	return w.openNewSegment()
}

// openNewSegment creates a new journal segment file.
func (w *Writer) openNewSegment() error {
	now := time.Now().UTC()
	filename := fmt.Sprintf("events.%s.ndjson", now.Format("20060102T150405Z"))
	path := filepath.Join(w.config.Directory, filename)

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("failed to create segment file: %w", err)
	}

	w.currentFile = f
	w.currentPath = path
	w.currentBytes = 0
	w.segmentStart = now
	w.encoder = json.NewEncoder(f)

	// Start new segment in manifest
	w.manifest.StartSegment(filename, now)

	return nil
}

// finalizeCurrentSegment closes the current segment and updates manifest.
func (w *Writer) finalizeCurrentSegment() error {
	if w.currentFile == nil {
		return nil
	}

	// Sync and close file
	if err := w.currentFile.Sync(); err != nil {
		return fmt.Errorf("failed to sync segment: %w", err)
	}
	if err := w.currentFile.Close(); err != nil {
		return fmt.Errorf("failed to close segment: %w", err)
	}

	// Compute checksum
	checksum, err := computeFileChecksum(w.currentPath)
	if err != nil {
		// Non-fatal, just log
		checksum = ""
	}

	// Finalize segment in manifest
	w.manifest.FinalizeSegment(w.currentPath, time.Now(), checksum)

	// Save manifest
	if err := w.manifest.Save(w.config.Directory); err != nil {
		return fmt.Errorf("failed to save manifest: %w", err)
	}

	w.currentFile = nil
	w.currentPath = ""

	return nil
}

// Close closes the journal writer.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return nil
	}
	w.closed = true

	return w.finalizeCurrentSegment()
}

// Query queries the journal for events matching the given criteria.
func (w *Writer) Query(query Query) ([]*protocol.EventEnvelope, string, error) {
	w.mu.Lock()
	segments := w.manifest.GetSegmentsInRange(query.SinceTS, query.UntilTS)
	w.mu.Unlock()

	var results []*protocol.EventEnvelope

	for _, seg := range segments {
		path := filepath.Join(w.config.Directory, seg.Filename)
		events, err := w.scanSegment(path, query)
		if err != nil {
			continue // Best effort
		}
		results = append(results, events...)

		if len(results) >= query.Limit {
			results = results[:query.Limit]
			break
		}
	}

	// Return cursor for pagination (simplified)
	cursor := ""
	if len(results) > 0 {
		cursor = results[len(results)-1].EventID
	}

	return results, cursor, nil
}

// scanSegment scans a segment file for matching events.
func (w *Writer) scanSegment(path string, query Query) ([]*protocol.EventEnvelope, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	decoder := protocol.NewStreamDecoder(f)
	var results []*protocol.EventEnvelope

	for {
		event, err := decoder.Decode()
		if err == io.EOF {
			break
		}
		if err != nil {
			break // Skip malformed lines
		}

		if query.Matches(event) {
			results = append(results, event)
		}
	}

	return results, nil
}

// Query represents a journal query.
type Query struct {
	WorkspaceUID  string
	SinceTS       time.Time
	UntilTS       time.Time
	Types         []string
	CorrelationID string
	OperationID   string
	Limit         int
}

// Matches checks if an event matches the query criteria.
func (q *Query) Matches(event *protocol.EventEnvelope) bool {
	// Filter by workspace
	if q.WorkspaceUID != "" && event.WorkspaceUID != q.WorkspaceUID {
		return false
	}

	// Filter by types
	if len(q.Types) > 0 {
		matched := false
		for _, t := range q.Types {
			if t == event.Type {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	// Filter by correlation ID
	if q.CorrelationID != "" && event.CorrelationID != q.CorrelationID {
		return false
	}

	// Filter by operation ID
	if q.OperationID != "" && event.OperationID != q.OperationID {
		return false
	}

	return true
}

// computeFileChecksum computes SHA256 checksum of a file.
func computeFileChecksum(path string) (string, error) {
	// TODO: Implement actual checksum computation
	return "", nil
}
