// Package codex provides utilities for working with Codex session files.
package codex

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// DefaultSessionsDir is the default Codex sessions directory.
	DefaultSessionsDir = ".codex/sessions"
)

// FindRolloutPath finds the rollout.jsonl file path for a given session ID.
// Session files are stored as: ~/.codex/sessions/YYYY/MM/DD/rollout-<timestamp>-<session_id>.jsonl
func FindRolloutPath(sessionID string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot get home dir: %w", err)
	}

	sessionsDir := filepath.Join(home, DefaultSessionsDir)
	return FindRolloutPathInDir(sessionsDir, sessionID)
}

// FindRolloutPathInDir finds the rollout.jsonl file path for a given session ID
// within the specified sessions directory.
func FindRolloutPathInDir(sessionsDir, sessionID string) (string, error) {
	var foundPath string

	err := filepath.Walk(sessionsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip inaccessible paths
		}
		if info.IsDir() {
			return nil
		}

		// Check if filename contains the session ID
		filename := filepath.Base(path)
		if strings.HasPrefix(filename, "rollout-") &&
			strings.HasSuffix(filename, ".jsonl") &&
			strings.Contains(filename, sessionID) {
			foundPath = path
			return filepath.SkipDir // Stop walking once found
		}
		return nil
	})

	if err != nil && err != filepath.SkipDir {
		return "", fmt.Errorf("error searching for session: %w", err)
	}

	if foundPath == "" {
		return "", fmt.Errorf("rollout file not found for session: %s", sessionID)
	}

	return foundPath, nil
}

// GetSessionIDFromPath extracts the session ID from a rollout file path.
// Path format: rollout-<timestamp>-<session_id>.jsonl
func GetSessionIDFromPath(path string) string {
	filename := filepath.Base(path)

	// Remove prefix and suffix
	if !strings.HasPrefix(filename, "rollout-") || !strings.HasSuffix(filename, ".jsonl") {
		return ""
	}

	// Extract the part after "rollout-" and before ".jsonl"
	name := strings.TrimPrefix(filename, "rollout-")
	name = strings.TrimSuffix(name, ".jsonl")

	// Format: YYYY-MM-DDTHH-MM-SS-<session_id>
	// The session_id starts after the timestamp (which has 19 chars: YYYY-MM-DDTHH-MM-SS)
	// But the timestamp uses hyphens too, so we find the UUID-like session ID
	parts := strings.Split(name, "-")
	if len(parts) < 7 {
		return ""
	}

	// Session ID is typically a UUID: 8-4-4-4-12 format
	// The timestamp takes: YYYY-MM-DDTHH-MM-SS (parts[0] through parts[5] after split)
	// Session ID is the remaining parts joined
	// Actually: 2026-02-05T14-41-52-019c2c89-2970-7362-ad74-c624a13aac39
	// Parts: [2026, 02, 05T14, 41, 52, 019c2c89, 2970, 7362, ad74, c624a13aac39]
	// Session ID parts start at index 5
	if len(parts) >= 10 {
		sessionParts := parts[5:10]
		return strings.Join(sessionParts, "-")
	}

	return ""
}
