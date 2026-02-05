// Package claim provides atomic session binding with lock files.
// It ensures only one sidecar can process a given session.
package claim

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const (
	// DefaultClaimDir is the default directory for claim lock files.
	DefaultClaimDir = ".tmuxcoder/run/ingest-claims"

	// StaleTTL is the maximum age for a lock file before it's considered stale.
	StaleTTL = 24 * time.Hour
)

// LockInfo contains information stored in a lock file.
type LockInfo struct {
	PID      int    `json:"pid"`
	StartTS  string `json:"start_ts"`
	Hostname string `json:"hostname"`
}

// Manager manages session claim lock files.
type Manager struct {
	claimDir string
	locks    map[string]string // sessionID -> lock file path
}

// NewManager creates a new claim manager.
// claimDir defaults to ~/.tmuxcoder/run/ingest-claims if empty.
func NewManager(claimDir string) (*Manager, error) {
	if claimDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("cannot get home dir: %w", err)
		}
		claimDir = filepath.Join(home, DefaultClaimDir)
	}

	// Ensure directory exists
	if err := os.MkdirAll(claimDir, 0755); err != nil {
		return nil, fmt.Errorf("cannot create claim dir: %w", err)
	}

	return &Manager{
		claimDir: claimDir,
		locks:    make(map[string]string),
	}, nil
}

// CleanStale removes stale lock files on startup.
// A lock is stale if:
// 1. The PID doesn't exist
// 2. The lock is older than StaleTTL
func (m *Manager) CleanStale() error {
	entries, err := os.ReadDir(m.claimDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	now := time.Now()
	cleaned := 0

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".lock" {
			continue
		}

		lockPath := filepath.Join(m.claimDir, entry.Name())
		if m.isStale(lockPath, now) {
			if err := os.Remove(lockPath); err == nil {
				log.Printf("[CLAIM] Cleaned stale lock: %s", entry.Name())
				cleaned++
			}
		}
	}

	if cleaned > 0 {
		log.Printf("[CLAIM] Cleaned %d stale locks", cleaned)
	}
	return nil
}

// isStale checks if a lock file is stale.
func (m *Manager) isStale(lockPath string, now time.Time) bool {
	// Check file age
	info, err := os.Stat(lockPath)
	if err != nil {
		return true // Can't stat, consider stale
	}

	if now.Sub(info.ModTime()) > StaleTTL {
		return true
	}

	// Read lock info
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return true
	}

	var lockInfo LockInfo
	if err := json.Unmarshal(data, &lockInfo); err != nil {
		return true
	}

	// Check if PID exists
	if !processExists(lockInfo.PID) {
		return true
	}

	return false
}

// Claim attempts to claim a session.
// Returns true if claim succeeded, false if already claimed by another process.
func (m *Manager) Claim(sessionID string) (bool, error) {
	lockPath := filepath.Join(m.claimDir, sessionID+".lock")

	// Try to create lock file atomically
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		if os.IsExist(err) {
			// Check if existing lock is stale
			if m.isStale(lockPath, time.Now()) {
				os.Remove(lockPath)
				// Retry
				f, err = os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
				if err != nil {
					return false, nil // Someone else got it
				}
			} else {
				return false, nil // Valid claim exists
			}
		} else {
			return false, err
		}
	}
	defer f.Close()

	// Write lock info
	hostname, _ := os.Hostname()
	lockInfo := LockInfo{
		PID:      os.Getpid(),
		StartTS:  time.Now().Format(time.RFC3339),
		Hostname: hostname,
	}
	data, _ := json.Marshal(lockInfo)
	if _, err := f.Write(data); err != nil {
		os.Remove(lockPath)
		return false, err
	}

	m.locks[sessionID] = lockPath
	log.Printf("[CLAIM] Claimed session: %s", sessionID)
	return true, nil
}

// Release releases a claimed session.
func (m *Manager) Release(sessionID string) error {
	lockPath, ok := m.locks[sessionID]
	if !ok {
		return nil
	}

	if err := os.Remove(lockPath); err != nil && !os.IsNotExist(err) {
		return err
	}

	delete(m.locks, sessionID)
	log.Printf("[CLAIM] Released session: %s", sessionID)
	return nil
}

// ReleaseAll releases all claimed sessions.
func (m *Manager) ReleaseAll() {
	for sessionID := range m.locks {
		m.Release(sessionID)
	}
}

// IsClaimed checks if a session is claimed by this process.
func (m *Manager) IsClaimed(sessionID string) bool {
	_, ok := m.locks[sessionID]
	return ok
}

// processExists checks if a process with the given PID exists.
func processExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix, FindProcess always succeeds. Use Signal(0) to check if process exists.
	err = proc.Signal(syscall.Signal(0))
	return err == nil
}
