// Package main implements the tmuxcoder-ingest CLI.
// It watches Codex/Claude session JSONL files and sends parsed events to the bus.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/opencode/hivemind-tmuxcoder/internal/bridge/client"
	"github.com/opencode/hivemind-tmuxcoder/internal/ingest"
	"github.com/opencode/hivemind-tmuxcoder/internal/ingest/claim"
	"github.com/opencode/hivemind-tmuxcoder/internal/ingest/claude"
	"github.com/opencode/hivemind-tmuxcoder/internal/ingest/codex"
)

// Version is set at build time.
var Version = "dev"

// StringSliceFlag is a flag that can be repeated.
type StringSliceFlag []string

func (s *StringSliceFlag) String() string {
	return strings.Join(*s, ",")
}

func (s *StringSliceFlag) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func main() {
	var (
		socket        string
		workspaceUID  string
		label         string
		watchDirs     StringSliceFlag
		logPath       string
		codexSession  string
		claudeSession string
		claudeProject string
		fromBegin     bool
		projectFilter string
		sessionFilter string
		showVersion   bool
	)

	flag.StringVar(&socket, "socket", "", "Bus socket path (default: auto-detect)")
	flag.StringVar(&workspaceUID, "workspace-uid", "", "Workspace UID for events (default: auto-generate)")
	flag.StringVar(&label, "label", "agent:session", "Client label")
	flag.Var(&watchDirs, "watch", "Directory to watch (repeatable)")
	flag.StringVar(&logPath, "log-path", "", "Specific JSONL file path to watch (overrides --watch)")
	flag.StringVar(&codexSession, "codex-session", "", "Codex session ID to find and watch rollout.jsonl")
	flag.StringVar(&claudeSession, "claude-session", "", "Claude session ID to find and watch")
	flag.StringVar(&claudeProject, "claude-project", "", "Claude project path or name (watches latest session)")
	flag.BoolVar(&fromBegin, "from-begin", false, "Read from file start instead of end")
	flag.StringVar(&projectFilter, "project", "", "Only process files matching this project path")
	flag.StringVar(&sessionFilter, "session-id", "", "Only bind to this specific session ID (in JSONL content)")
	flag.BoolVar(&showVersion, "version", false, "Show version and exit")
	flag.Parse()

	if showVersion {
		fmt.Printf("tmuxcoder-ingest %s\n", Version)
		os.Exit(0)
	}

	// Handle --codex-session: find rollout.jsonl by session ID
	if codexSession != "" {
		path, err := codex.FindRolloutPath(codexSession)
		if err != nil {
			log.Fatalf("[INGEST] Failed to find Codex session %s: %v", codexSession, err)
		}
		logPath = path
		log.Printf("[INGEST] Found Codex session %s -> %s", codexSession, path)
	}

	// Handle --claude-session: find session JSONL by session ID
	if claudeSession != "" {
		path, err := claude.FindSessionPath(claudeSession)
		if err != nil {
			log.Fatalf("[INGEST] Failed to find Claude session %s: %v", claudeSession, err)
		}
		logPath = path
		log.Printf("[INGEST] Found Claude session %s -> %s", claudeSession, path)
	}

	// Handle --claude-project: find latest session in project
	if claudeProject != "" {
		projectDir, err := claude.FindProjectPath(claudeProject)
		if err != nil {
			log.Fatalf("[INGEST] Failed to find Claude project %s: %v", claudeProject, err)
		}
		path, err := claude.GetLatestSessionInProject(projectDir)
		if err != nil {
			log.Fatalf("[INGEST] Failed to find latest session in project %s: %v", projectDir, err)
		}
		logPath = path
		log.Printf("[INGEST] Found Claude project %s, latest session -> %s", claudeProject, path)
	}

	// Handle --log-path: watch single file
	var logPaths []string
	if logPath != "" {
		expanded := expandHome(logPath)
		if _, err := os.Stat(expanded); err != nil {
			// File doesn't exist yet, watch the parent directory
			log.Printf("[INGEST] Log file does not exist yet, will watch for creation: %s", expanded)
		}
		logPaths = append(logPaths, expanded)
		log.Printf("[INGEST] Starting tmuxcoder-ingest %s", Version)
		log.Printf("[INGEST] Watching specific file: %s", expanded)
	} else {
		// Expand ~ in watch directories
		expandedDirs := make([]string, 0, len(watchDirs))
		for _, dir := range watchDirs {
			expanded := expandHome(dir)
			if _, err := os.Stat(expanded); err == nil {
				expandedDirs = append(expandedDirs, expanded)
			} else {
				log.Printf("[INGEST] Warning: directory does not exist: %s", expanded)
			}
		}

		if len(expandedDirs) == 0 {
			// Default watch directories
			home, _ := os.UserHomeDir()
			defaults := []string{
				filepath.Join(home, ".codex", "sessions"),
				filepath.Join(home, ".claude", "projects"),
			}
			for _, dir := range defaults {
				if _, err := os.Stat(dir); err == nil {
					expandedDirs = append(expandedDirs, dir)
				}
			}
		}

		if len(expandedDirs) == 0 {
			log.Fatal("[INGEST] No valid watch directories found")
		}

		logPaths = expandedDirs
		log.Printf("[INGEST] Starting tmuxcoder-ingest %s", Version)
		log.Printf("[INGEST] Watching directories: %v", expandedDirs)
	}

	// Initialize claim manager
	claimMgr, err := claim.NewManager("")
	if err != nil {
		log.Fatalf("[INGEST] Failed to create claim manager: %v", err)
	}
	defer claimMgr.ReleaseAll()

	// Clean stale locks
	if err := claimMgr.CleanStale(); err != nil {
		log.Printf("[INGEST] Warning: failed to clean stale locks: %v", err)
	}

	// Create bus client
	clientCfg := client.DefaultConfig()
	if socket != "" {
		clientCfg.SocketPath = socket
	}
	if workspaceUID != "" {
		clientCfg.WorkspaceUID = workspaceUID
	}
	clientCfg.Label = label
	clientCfg.DriveMode = "ingest"

	busClient := client.New(clientCfg)

	// Connect to bus
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := busClient.Connect(ctx); err != nil {
		log.Fatalf("[INGEST] Failed to connect to bus: %v", err)
	}
	defer busClient.Close()

	log.Printf("[INGEST] Connected to bus, workspace_uid=%s", busClient.WorkspaceUID())

	// Create parser
	parser := ingest.NewParser()

	// Track claimed sessions
	claimedSessions := make(map[string]bool)

	// Create watcher with line handler
	watcherCfg := &ingest.WatcherConfig{
		WatchDirs:     logPaths,
		LogPath:       logPath,
		FromBegin:     fromBegin,
		ProjectFilter: projectFilter,
		SessionFilter: sessionFilter,
	}

	watcher, err := ingest.NewWatcher(watcherCfg, func(path string, line []byte) {
		// Debug: log first 100 chars of each line
		linePreview := string(line)
		if len(linePreview) > 100 {
			linePreview = linePreview[:100] + "..."
		}
		log.Printf("[INGEST] Processing line: %s", linePreview)

		// Parse the line
		msg, err := parser.ParseLine(line, "") // Auto-detect source
		if err != nil {
			log.Printf("[INGEST] Parse error: %v", err)
			return
		}
		if msg == nil {
			log.Printf("[INGEST] Line skipped (not a user/assistant message)")
			return // Not a relevant message
		}
		log.Printf("[INGEST] Parsed message: role=%s source=%s text_len=%d", msg.Role, msg.Source, len(msg.Text))

		// Handle session claiming
		if msg.SessionID != "" && !claimedSessions[msg.SessionID] {
			// Apply session filter if set
			if sessionFilter != "" && msg.SessionID != sessionFilter {
				return
			}

			// Try to claim the session
			claimed, err := claimMgr.Claim(msg.SessionID)
			if err != nil {
				log.Printf("[INGEST] Claim error: %v", err)
				return
			}
			if !claimed {
				log.Printf("[INGEST] Session %s already claimed by another process", msg.SessionID)
				return
			}
			claimedSessions[msg.SessionID] = true
		}

		// Format text with source label for UI display
		text := formatUILogText(msg)

		// Send to bus
		event := busClient.NewUILogAppend("info", text, msg.SessionID)
		if err := busClient.Send(event); err != nil {
			log.Printf("[INGEST] Send error: %v", err)
		} else {
			log.Printf("[INGEST] Sent to bus: role=%s source=%s text_len=%d", msg.Role, msg.Source, len(msg.Text))
		}
	})
	if err != nil {
		log.Fatalf("[INGEST] Failed to create watcher: %v", err)
	}

	// Start watcher
	if err := watcher.Start(); err != nil {
		log.Fatalf("[INGEST] Failed to start watcher: %v", err)
	}
	defer watcher.Stop()

	log.Printf("[INGEST] Watcher started, press Ctrl+C to stop")

	// Wait for interrupt
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Printf("[INGEST] Shutting down...")
}

// expandHome expands ~ to the user's home directory.
func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[2:])
	}
	return path
}

func formatUILogText(msg *ingest.ParsedMessage) string {
	label := msg.Role
	if msg.Source != "" {
		label = fmt.Sprintf("%s:%s", msg.Source, msg.Role)
	}

	text := strings.TrimRight(msg.Text, "\n")
	if text == "" {
		return fmt.Sprintf("[%s]\n", label)
	}
	return fmt.Sprintf("[%s]\n%s\n\n", label, text)
}
