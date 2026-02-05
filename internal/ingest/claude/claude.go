// Package claude provides utilities for working with Claude Code session files.
package claude

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// DefaultProjectsDir is the default Claude projects directory.
	DefaultProjectsDir = ".claude/projects"
)

// errFound is used to stop filepath.Walk when session is found
var errFound = errors.New("found")

// FindSessionPath finds the JSONL file path for a given session ID.
// Session files are stored as: ~/.claude/projects/<project-hash>/<session-id>.jsonl
func FindSessionPath(sessionID string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot get home dir: %w", err)
	}

	projectsDir := filepath.Join(home, DefaultProjectsDir)
	return FindSessionPathInDir(projectsDir, sessionID)
}

// FindSessionPathInDir finds the session JSONL file path for a given session ID
// within the specified projects directory.
func FindSessionPathInDir(projectsDir, sessionID string) (string, error) {
	var foundPath string

	// Normalize sessionID (remove .jsonl suffix if present)
	sessionID = strings.TrimSuffix(sessionID, ".jsonl")

	err := filepath.Walk(projectsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip inaccessible paths
		}
		if info.IsDir() {
			return nil
		}

		// Check if filename matches the session ID
		filename := filepath.Base(path)
		if !strings.HasSuffix(filename, ".jsonl") {
			return nil
		}

		// Match exact session ID or session ID prefix
		nameWithoutExt := strings.TrimSuffix(filename, ".jsonl")
		if nameWithoutExt == sessionID || strings.HasPrefix(nameWithoutExt, sessionID) {
			foundPath = path
			return errFound // Stop walking once found
		}
		return nil
	})

	if err != nil && !errors.Is(err, errFound) {
		return "", fmt.Errorf("error searching for session: %w", err)
	}

	if foundPath == "" {
		return "", fmt.Errorf("session file not found for: %s", sessionID)
	}

	return foundPath, nil
}

// FindProjectPath finds the project directory for a given project identifier.
// The identifier can be:
// - A project path (e.g., "/Users/hhx/work/myproject") -> matches "-Users-hhx-work-myproject"
// - A project hash/name (e.g., "-Users-hhx-work-myproject")
func FindProjectPath(projectID string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot get home dir: %w", err)
	}

	projectsDir := filepath.Join(home, DefaultProjectsDir)
	return FindProjectPathInDir(projectsDir, projectID)
}

// FindProjectPathInDir finds the project directory within the specified projects directory.
func FindProjectPathInDir(projectsDir, projectID string) (string, error) {
	// Convert project path to Claude's escaped format if needed
	// e.g., "/Users/hhx/work/myproject" -> "-Users-hhx-work-myproject"
	escapedID := projectID
	if strings.HasPrefix(projectID, "/") {
		escapedID = strings.ReplaceAll(projectID, "/", "-")
	}

	// Check if directory exists directly
	fullPath := filepath.Join(projectsDir, escapedID)
	if info, err := os.Stat(fullPath); err == nil && info.IsDir() {
		return fullPath, nil
	}

	// Search for matching directory
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return "", fmt.Errorf("cannot read projects dir: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		// Match by suffix (project path) or contains
		if strings.HasSuffix(name, escapedID) || strings.Contains(name, escapedID) {
			return filepath.Join(projectsDir, name), nil
		}
	}

	return "", fmt.Errorf("project not found: %s", projectID)
}

// GetLatestSessionInProject gets the most recently modified session file in a project.
func GetLatestSessionInProject(projectDir string) (string, error) {
	var latestPath string
	var latestTime int64

	err := filepath.Walk(projectDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			// Skip subagents directory for main session
			if filepath.Base(path) == "subagents" {
				return filepath.SkipDir
			}
			return nil
		}

		if !strings.HasSuffix(path, ".jsonl") {
			return nil
		}

		if info.ModTime().Unix() > latestTime {
			latestTime = info.ModTime().Unix()
			latestPath = path
		}
		return nil
	})

	if err != nil {
		return "", err
	}

	if latestPath == "" {
		return "", fmt.Errorf("no session files found in project: %s", projectDir)
	}

	return latestPath, nil
}
