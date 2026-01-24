package journal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/opencode/hivemind-tmuxcoder/internal/protocol"
)

// Manifest tracks journal segments and their metadata.
type Manifest struct {
	mu sync.RWMutex

	Version  string    `json:"version"`
	Segments []Segment `json:"segments"`

	// Current segment tracking (not persisted)
	current *Segment
}

// Segment represents a journal segment file.
type Segment struct {
	Filename          string   `json:"filename"`
	StartTS           string   `json:"start_ts"`
	EndTS             string   `json:"end_ts,omitempty"`
	FirstEventID      string   `json:"first_event_id,omitempty"`
	LastEventID       string   `json:"last_event_id,omitempty"`
	ApproxEventCount  int      `json:"approx_event_count,omitempty"`
	WorkspacesPresent []string `json:"workspaces_present,omitempty"`
	TypesPresent      []string `json:"types_present,omitempty"`
	SHA256            string   `json:"sha256,omitempty"`
}

// NewManifest creates a new empty manifest.
func NewManifest() *Manifest {
	return &Manifest{
		Version:  "1.0",
		Segments: []Segment{},
	}
}

// LoadManifest loads the manifest from disk.
func LoadManifest(directory string) (*Manifest, error) {
	path := filepath.Join(directory, "manifest.json")

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}

	return &m, nil
}

// Save persists the manifest to disk.
func (m *Manifest) Save(directory string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	path := filepath.Join(directory, "manifest.json")

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}

	// Write atomically
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return err
	}

	return os.Rename(tmpPath, path)
}

// StartSegment begins tracking a new segment.
func (m *Manifest) StartSegment(filename string, startTime time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.current = &Segment{
		Filename: filename,
		StartTS:  startTime.Format(time.RFC3339),
	}
}

// UpdateCurrentSegment updates tracking for the current segment.
func (m *Manifest) UpdateCurrentSegment(event *protocol.EventEnvelope) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.current == nil {
		return
	}

	// Track first/last event ID
	if m.current.FirstEventID == "" {
		m.current.FirstEventID = event.EventID
	}
	m.current.LastEventID = event.EventID
	m.current.ApproxEventCount++

	// Track workspaces (simplified - just track unique ones)
	if event.WorkspaceUID != "" {
		found := false
		for _, ws := range m.current.WorkspacesPresent {
			if ws == event.WorkspaceUID {
				found = true
				break
			}
		}
		if !found && len(m.current.WorkspacesPresent) < 100 { // Limit tracking
			m.current.WorkspacesPresent = append(m.current.WorkspacesPresent, event.WorkspaceUID)
		}
	}

	// Track event types
	found := false
	for _, t := range m.current.TypesPresent {
		if t == event.Type {
			found = true
			break
		}
	}
	if !found && len(m.current.TypesPresent) < 50 { // Limit tracking
		m.current.TypesPresent = append(m.current.TypesPresent, event.Type)
	}
}

// FinalizeSegment completes the current segment.
func (m *Manifest) FinalizeSegment(path string, endTime time.Time, checksum string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.current == nil {
		return
	}

	m.current.EndTS = endTime.Format(time.RFC3339)
	m.current.SHA256 = checksum

	m.Segments = append(m.Segments, *m.current)
	m.current = nil
}

// GetSegmentsInRange returns segments that may contain events in the given time range.
func (m *Manifest) GetSegmentsInRange(since, until time.Time) []Segment {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []Segment

	for _, seg := range m.Segments {
		segStart, _ := time.Parse(time.RFC3339, seg.StartTS)
		segEnd := time.Now()
		if seg.EndTS != "" {
			segEnd, _ = time.Parse(time.RFC3339, seg.EndTS)
		}

		// Check if segment overlaps with query range
		if !since.IsZero() && segEnd.Before(since) {
			continue
		}
		if !until.IsZero() && segStart.After(until) {
			continue
		}

		result = append(result, seg)
	}

	return result
}
