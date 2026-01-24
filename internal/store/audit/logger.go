// Package audit implements the audit log for TmuxCoder Bus Server.
// The audit log records all permission-sensitive operations in logfmt format.
package audit

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/opencode/hivemind-tmuxcoder/internal/protocol"
)

// Config holds audit log configuration.
type Config struct {
	Directory     string
	Format        string // logfmt | jsonl
	MaxFileBytes  int64
	RetentionDays int
}

// DefaultConfig returns the default audit configuration.
func DefaultConfig() *Config {
	home, _ := os.UserHomeDir()
	return &Config{
		Directory:     filepath.Join(home, ".tmuxcoder", "state", "audit"),
		Format:        "logfmt",
		MaxFileBytes:  100 * 1024 * 1024, // 100MB
		RetentionDays: 180,
	}
}

// Action represents an auditable action.
type Action struct {
	Timestamp     time.Time
	EventID       string
	EventType     string
	SourceKind    string
	SourceID      string
	DestWorkspace string
	PayloadHash   string
	Decision      string // allow | deny | pending
	Reason        string
}

// Logger writes audit entries.
type Logger struct {
	config *Config

	mu          sync.Mutex
	currentFile *os.File
	currentPath string
	currentSize int64

	closed bool
}

// NewLogger creates a new audit logger.
func NewLogger(config *Config) (*Logger, error) {
	if config == nil {
		config = DefaultConfig()
	}

	// Create directory if needed
	if err := os.MkdirAll(config.Directory, 0700); err != nil {
		return nil, fmt.Errorf("failed to create audit directory: %w", err)
	}

	l := &Logger{
		config: config,
	}

	// Open log file
	if err := l.openLogFile(); err != nil {
		return nil, err
	}

	return l, nil
}

// Log writes an audit entry.
func (l *Logger) Log(action Action) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.closed {
		return fmt.Errorf("audit logger is closed")
	}

	// Check for rotation
	if l.currentSize >= l.config.MaxFileBytes {
		if err := l.rotate(); err != nil {
			return fmt.Errorf("rotation failed: %w", err)
		}
	}

	// Format entry
	var line string
	if l.config.Format == "jsonl" {
		line = l.formatJSONL(action)
	} else {
		line = l.formatLogfmt(action)
	}

	// Write
	n, err := l.currentFile.WriteString(line + "\n")
	if err != nil {
		return fmt.Errorf("failed to write audit entry: %w", err)
	}
	l.currentSize += int64(n)

	return nil
}

// LogEvent creates and logs an audit action from an event.
func (l *Logger) LogEvent(event *protocol.EventEnvelope, decision string, reason string) error {
	action := Action{
		Timestamp:   time.Now(),
		EventID:     event.EventID,
		EventType:   event.Type,
		SourceKind:  event.Source.Kind,
		SourceID:    event.Source.ID,
		PayloadHash: event.PayloadHash,
		Decision:    decision,
		Reason:      reason,
	}

	if event.Dest != nil {
		action.DestWorkspace = event.Dest.ID
	}

	return l.Log(action)
}

// formatLogfmt formats an action as logfmt.
func (l *Logger) formatLogfmt(a Action) string {
	return fmt.Sprintf(
		"ts=%s event_id=%s type=%s source_kind=%s source_id=%s dest_workspace_uid=%s payload_hash=%s decision=%s reason=%q",
		a.Timestamp.Format(time.RFC3339),
		a.EventID,
		a.EventType,
		a.SourceKind,
		a.SourceID,
		a.DestWorkspace,
		a.PayloadHash,
		a.Decision,
		a.Reason,
	)
}

// formatJSONL formats an action as JSON.
func (l *Logger) formatJSONL(a Action) string {
	return fmt.Sprintf(
		`{"ts":"%s","event_id":"%s","type":"%s","source_kind":"%s","source_id":"%s","dest_workspace_uid":"%s","payload_hash":"%s","decision":"%s","reason":"%s"}`,
		a.Timestamp.Format(time.RFC3339),
		a.EventID,
		a.EventType,
		a.SourceKind,
		a.SourceID,
		a.DestWorkspace,
		a.PayloadHash,
		a.Decision,
		escapeJSON(a.Reason),
	)
}

// openLogFile opens the current audit log file.
func (l *Logger) openLogFile() error {
	filename := "audit.logfmt"
	if l.config.Format == "jsonl" {
		filename = "audit.jsonl"
	}

	l.currentPath = filepath.Join(l.config.Directory, filename)

	f, err := os.OpenFile(l.currentPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("failed to open audit file: %w", err)
	}

	// Get current size
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}

	l.currentFile = f
	l.currentSize = info.Size()

	return nil
}

// rotate rotates the audit log file.
func (l *Logger) rotate() error {
	if l.currentFile != nil {
		l.currentFile.Sync()
		l.currentFile.Close()
	}

	// Rename current file with timestamp
	ext := filepath.Ext(l.currentPath)
	base := l.currentPath[:len(l.currentPath)-len(ext)]
	rotatedPath := fmt.Sprintf("%s.%s%s", base, time.Now().Format("20060102T150405Z"), ext)

	if err := os.Rename(l.currentPath, rotatedPath); err != nil {
		// Non-fatal, just continue
	}

	return l.openLogFile()
}

// Close closes the audit logger.
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.closed {
		return nil
	}
	l.closed = true

	if l.currentFile != nil {
		l.currentFile.Sync()
		return l.currentFile.Close()
	}
	return nil
}

// escapeJSON escapes a string for JSON.
func escapeJSON(s string) string {
	// Simple escape for now
	result := ""
	for _, c := range s {
		switch c {
		case '"':
			result += "\\\""
		case '\\':
			result += "\\\\"
		case '\n':
			result += "\\n"
		case '\r':
			result += "\\r"
		case '\t':
			result += "\\t"
		default:
			result += string(c)
		}
	}
	return result
}
