// Package ingest provides JSONL file watching and parsing for agent sessions.
// It monitors Codex/Claude session files and sends parsed events to the bus.
package ingest

import (
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// FileState tracks the read state of a watched file.
type FileState struct {
	Path      string
	Offset    int64
	LastSize  int64
	LastSeen  time.Time
	SessionID string
	Claimed   bool
}

// WatcherConfig configures the file watcher.
type WatcherConfig struct {
	// WatchDirs are the directories to watch for JSONL files.
	WatchDirs []string

	// LogPath is a specific JSONL file path to watch (overrides WatchDirs behavior).
	LogPath string

	// FromBegin if true, reads from file start instead of end.
	FromBegin bool

	// ProjectFilter if set, only process files matching this project path.
	ProjectFilter string

	// SessionFilter if set, only bind to this specific session ID.
	SessionFilter string

	// DebounceInterval is the time to wait after a write event before reading.
	DebounceInterval time.Duration
}

// DefaultWatcherConfig returns sensible defaults.
func DefaultWatcherConfig() *WatcherConfig {
	return &WatcherConfig{
		DebounceInterval: 100 * time.Millisecond,
	}
}

// LineHandler is called for each complete line read from a file.
type LineHandler func(path string, line []byte)

// Watcher watches directories for JSONL file changes.
type Watcher struct {
	config    *WatcherConfig
	fsWatcher *fsnotify.Watcher
	states    map[string]*FileState
	mu        sync.RWMutex
	startTime time.Time
	handler   LineHandler

	// debounce tracks pending file reads
	debounce   map[string]*time.Timer
	debounceMu sync.Mutex

	stopCh chan struct{}
	wg     sync.WaitGroup
}

// NewWatcher creates a new file watcher.
func NewWatcher(config *WatcherConfig, handler LineHandler) (*Watcher, error) {
	if config == nil {
		config = DefaultWatcherConfig()
	}
	if config.DebounceInterval == 0 {
		config.DebounceInterval = 100 * time.Millisecond
	}

	fsWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	return &Watcher{
		config:    config,
		fsWatcher: fsWatcher,
		states:    make(map[string]*FileState),
		startTime: time.Now(),
		handler:   handler,
		debounce:  make(map[string]*time.Timer),
		stopCh:    make(chan struct{}),
	}, nil
}

// Start begins watching the configured directories.
func (w *Watcher) Start() error {
	// Handle single-file mode (--log-path)
	if w.config.LogPath != "" {
		logPath := w.config.LogPath
		parentDir := filepath.Dir(logPath)

		// Watch the parent directory for file creation
		if err := w.fsWatcher.Add(parentDir); err != nil {
			log.Printf("[WATCHER] Warning: failed to watch parent dir %s: %v", parentDir, err)
		} else {
			log.Printf("[WATCHER] Watching parent directory: %s", parentDir)
		}

		// Also watch the file directly if it exists (for WRITE events on macOS)
		if info, err := os.Stat(logPath); err == nil {
			// Watch the file itself
			if err := w.fsWatcher.Add(logPath); err != nil {
				log.Printf("[WATCHER] Warning: failed to watch file directly %s: %v", logPath, err)
			} else {
				log.Printf("[WATCHER] Watching file directly: %s", logPath)
			}
			w.trackFile(logPath, info)
			// Read existing content if FromBegin
			if w.config.FromBegin {
				w.scheduleRead(logPath)
			}
		}
	} else {
		// Add watch directories
		for _, dir := range w.config.WatchDirs {
			if err := w.addDirRecursive(dir); err != nil {
				log.Printf("[WATCHER] Warning: failed to watch %s: %v", dir, err)
			}
		}

		// Scan for existing JSONL files
		for _, dir := range w.config.WatchDirs {
			w.scanDirectory(dir)
		}
	}

	// Start event loop
	w.wg.Add(1)
	go w.eventLoop()

	return nil
}

// Stop stops the watcher.
func (w *Watcher) Stop() error {
	close(w.stopCh)
	w.wg.Wait()
	return w.fsWatcher.Close()
}

// addDirRecursive adds a directory and its subdirectories to the watch list.
func (w *Watcher) addDirRecursive(root string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip inaccessible paths
		}
		if info.IsDir() {
			if err := w.fsWatcher.Add(path); err != nil {
				log.Printf("[WATCHER] Failed to watch dir %s: %v", path, err)
			} else {
				log.Printf("[WATCHER] Watching directory: %s", path)
			}
		}
		return nil
	})
}

// scanDirectory scans for existing JSONL files.
func (w *Watcher) scanDirectory(dir string) {
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if filepath.Ext(path) == ".jsonl" {
			w.trackFile(path, info)
		}
		return nil
	})
}

// trackFile starts tracking a JSONL file.
func (w *Watcher) trackFile(path string, info os.FileInfo) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if _, exists := w.states[path]; exists {
		return
	}

	state := &FileState{
		Path:     path,
		LastSize: info.Size(),
		LastSeen: info.ModTime(),
	}

	// If FromBegin, start at 0; otherwise start at current size (only read new content)
	if w.config.FromBegin {
		state.Offset = 0
	} else {
		state.Offset = info.Size()
	}

	w.states[path] = state
	log.Printf("[WATCHER] Tracking file: %s (offset=%d)", path, state.Offset)
}

// eventLoop processes fsnotify events.
func (w *Watcher) eventLoop() {
	defer w.wg.Done()

	for {
		select {
		case <-w.stopCh:
			return

		case event, ok := <-w.fsWatcher.Events:
			if !ok {
				return
			}
			w.handleEvent(event)

		case err, ok := <-w.fsWatcher.Errors:
			if !ok {
				return
			}
			log.Printf("[WATCHER] Error: %v", err)
		}
	}
}

// handleEvent processes a single fsnotify event.
func (w *Watcher) handleEvent(event fsnotify.Event) {
	path := event.Name

	// Debug: log all events
	log.Printf("[WATCHER] Event: op=%v path=%s", event.Op, path)

	// Handle directory creation - add to watch (only in directory mode)
	if w.config.LogPath == "" && event.Op&fsnotify.Create != 0 {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			w.fsWatcher.Add(path)
			log.Printf("[WATCHER] Added new directory: %s", path)
			return
		}
	}

	// Only process .jsonl files
	if filepath.Ext(path) != ".jsonl" {
		return
	}

	// In single-file mode, only process the specified file
	if w.config.LogPath != "" && path != w.config.LogPath {
		log.Printf("[WATCHER] Skipping file (not target): %s != %s", path, w.config.LogPath)
		return
	}

	switch {
	case event.Op&fsnotify.Create != 0:
		log.Printf("[WATCHER] File created: %s", path)
		if info, err := os.Stat(path); err == nil {
			w.trackFile(path, info)
		}
		w.scheduleRead(path)

	case event.Op&fsnotify.Write != 0:
		log.Printf("[WATCHER] File written: %s", path)
		w.scheduleRead(path)

	case event.Op&fsnotify.Remove != 0, event.Op&fsnotify.Rename != 0:
		w.mu.Lock()
		delete(w.states, path)
		w.mu.Unlock()
		log.Printf("[WATCHER] Stopped tracking: %s", path)
	}
}

// scheduleRead schedules a debounced read for the file.
func (w *Watcher) scheduleRead(path string) {
	w.debounceMu.Lock()
	defer w.debounceMu.Unlock()

	// Cancel existing timer if any
	if timer, exists := w.debounce[path]; exists {
		timer.Stop()
	}

	// Schedule new read
	w.debounce[path] = time.AfterFunc(w.config.DebounceInterval, func() {
		w.readFile(path)
	})
}

// readFile reads new content from a file.
func (w *Watcher) readFile(path string) {
	log.Printf("[WATCHER] readFile called for: %s", path)

	w.mu.Lock()
	state, exists := w.states[path]
	if !exists {
		log.Printf("[WATCHER] File not in states, tracking now: %s", path)
		// File not tracked, try to track it now
		if info, err := os.Stat(path); err == nil {
			state = &FileState{
				Path:     path,
				Offset:   0,
				LastSize: info.Size(),
				LastSeen: info.ModTime(),
			}
			w.states[path] = state
		} else {
			w.mu.Unlock()
			log.Printf("[WATCHER] Cannot stat untracked file: %v", err)
			return
		}
	}
	offset := state.Offset
	w.mu.Unlock()

	// Get current file size
	info, err := os.Stat(path)
	if err != nil {
		log.Printf("[WATCHER] Cannot stat %s: %v", path, err)
		return
	}

	currentSize := info.Size()
	log.Printf("[WATCHER] File size check: offset=%d currentSize=%d", offset, currentSize)

	// Handle truncation
	if currentSize < offset {
		log.Printf("[WATCHER] File truncated: %s (was %d, now %d)", path, offset, currentSize)
		offset = 0
	}

	// Nothing new to read
	if currentSize == offset {
		log.Printf("[WATCHER] No new content to read (size unchanged)")
		return
	}

	// Open and read new content
	f, err := os.Open(path)
	if err != nil {
		log.Printf("[WATCHER] Cannot open %s: %v", path, err)
		return
	}
	defer f.Close()

	// Seek to offset
	if _, err := f.Seek(offset, 0); err != nil {
		log.Printf("[WATCHER] Seek failed for %s: %v", path, err)
		return
	}

	// Read new bytes
	toRead := currentSize - offset
	buf := make([]byte, toRead)
	n, err := f.Read(buf)
	if err != nil {
		log.Printf("[WATCHER] Read failed for %s: %v", path, err)
		return
	}
	buf = buf[:n]
	log.Printf("[WATCHER] Read %d bytes from %s", n, path)

	// Update state
	w.mu.Lock()
	if s, ok := w.states[path]; ok {
		s.Offset = offset + int64(n)
		s.LastSize = currentSize
		s.LastSeen = time.Now()
	}
	w.mu.Unlock()

	// Process lines
	w.processBuffer(path, buf)
}

// partialLines stores incomplete lines per file
var partialLines = make(map[string][]byte)
var partialMu sync.Mutex

// processBuffer splits buffer into lines and calls handler.
func (w *Watcher) processBuffer(path string, buf []byte) {
	partialMu.Lock()
	// Prepend any leftover from previous read
	if partial, ok := partialLines[path]; ok {
		buf = append(partial, buf...)
		delete(partialLines, path)
	}
	partialMu.Unlock()

	start := 0
	for i, b := range buf {
		if b == '\n' {
			line := buf[start:i]
			if len(line) > 0 {
				w.handler(path, line)
			}
			start = i + 1
		}
	}

	// Store incomplete line for next read
	if start < len(buf) {
		partialMu.Lock()
		partialLines[path] = append([]byte{}, buf[start:]...)
		partialMu.Unlock()
	}
}
