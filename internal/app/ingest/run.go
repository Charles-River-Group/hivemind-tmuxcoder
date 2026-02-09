package ingest

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	bridgeclient "github.com/opencode/hivemind-tmuxcoder/internal/bridge/client"
	"github.com/opencode/hivemind-tmuxcoder/internal/ingest"
	"github.com/opencode/hivemind-tmuxcoder/internal/ingest/claim"
	"github.com/opencode/hivemind-tmuxcoder/internal/ingest/claude"
	"github.com/opencode/hivemind-tmuxcoder/internal/ingest/codex"
)

type Config struct {
	SocketPath    string
	WorkspaceUID  string
	Label         string
	WatchDirs     []string
	LogPath       string
	CodexSession  string
	ClaudeSession string
	ClaudeProject string
	FromBegin     bool
	ProjectFilter string
	SessionFilter string
}

func Run(ctx context.Context, cfg Config) error {
	logPath := cfg.LogPath

	if cfg.CodexSession != "" {
		path, err := codex.FindRolloutPath(cfg.CodexSession)
		if err != nil {
			return fmt.Errorf("failed to find Codex session %s: %w", cfg.CodexSession, err)
		}
		logPath = path
		log.Printf("[INGEST] Found Codex session %s -> %s", cfg.CodexSession, path)
	}

	if cfg.ClaudeSession != "" {
		path, err := claude.FindSessionPath(cfg.ClaudeSession)
		if err != nil {
			return fmt.Errorf("failed to find Claude session %s: %w", cfg.ClaudeSession, err)
		}
		logPath = path
		log.Printf("[INGEST] Found Claude session %s -> %s", cfg.ClaudeSession, path)
	}

	if cfg.ClaudeProject != "" {
		projectDir, err := claude.FindProjectPath(cfg.ClaudeProject)
		if err != nil {
			return fmt.Errorf("failed to find Claude project %s: %w", cfg.ClaudeProject, err)
		}
		path, err := claude.GetLatestSessionInProject(projectDir)
		if err != nil {
			return fmt.Errorf("failed to find latest session in project %s: %w", projectDir, err)
		}
		logPath = path
		log.Printf("[INGEST] Found Claude project %s, latest session -> %s", cfg.ClaudeProject, path)
	}

	var logPaths []string
	if logPath != "" {
		expanded := expandHome(logPath)
		if _, err := os.Stat(expanded); err != nil {
			log.Printf("[INGEST] Log file does not exist yet, will watch for creation: %s", expanded)
		}
		logPaths = append(logPaths, expanded)
		log.Printf("[INGEST] Watching specific file: %s", expanded)
	} else {
		expandedDirs := make([]string, 0, len(cfg.WatchDirs))
		for _, dir := range cfg.WatchDirs {
			expanded := expandHome(dir)
			if _, err := os.Stat(expanded); err == nil {
				expandedDirs = append(expandedDirs, expanded)
			} else {
				log.Printf("[INGEST] Warning: directory does not exist: %s", expanded)
			}
		}

		if len(expandedDirs) == 0 {
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
			return fmt.Errorf("no valid watch directories found")
		}

		logPaths = expandedDirs
		log.Printf("[INGEST] Watching directories: %v", expandedDirs)
	}

	claimMgr, err := claim.NewManager("")
	if err != nil {
		return fmt.Errorf("failed to create claim manager: %w", err)
	}
	defer claimMgr.ReleaseAll()

	if err := claimMgr.CleanStale(); err != nil {
		log.Printf("[INGEST] Warning: failed to clean stale locks: %v", err)
	}

	preclaimID := resolveSessionID(cfg, logPath)
	if preclaimID != "" {
		claimed, err := claimMgr.Claim(preclaimID)
		if err != nil {
			return fmt.Errorf("claim error: %w", err)
		}
		if !claimed {
			return fmt.Errorf("session %s already claimed by another process", preclaimID)
		}
	}

	clientCfg := bridgeclient.DefaultConfig()
	if cfg.SocketPath != "" {
		clientCfg.SocketPath = cfg.SocketPath
	}
	if cfg.WorkspaceUID != "" {
		clientCfg.WorkspaceUID = cfg.WorkspaceUID
	}
	if cfg.Label != "" {
		clientCfg.Label = cfg.Label
	}
	clientCfg.DriveMode = "ingest"

	busClient := bridgeclient.New(clientCfg)
	if err := busClient.Connect(ctx); err != nil {
		return fmt.Errorf("failed to connect to bus: %w", err)
	}
	defer busClient.Close()

	log.Printf("[INGEST] Connected to bus, workspace_uid=%s", busClient.WorkspaceUID())

	parser := ingest.NewParser()
	claimedSessions := make(map[string]bool)
	if preclaimID != "" {
		claimedSessions[preclaimID] = true
	}

	watcherCfg := &ingest.WatcherConfig{
		WatchDirs:     logPaths,
		LogPath:       logPath,
		FromBegin:     cfg.FromBegin,
		ProjectFilter: cfg.ProjectFilter,
		SessionFilter: cfg.SessionFilter,
	}

	watcher, err := ingest.NewWatcher(watcherCfg, func(path string, line []byte) {
		linePreview := string(line)
		if len(linePreview) > 100 {
			linePreview = linePreview[:100] + "..."
		}
		log.Printf("[INGEST] Processing line: %s", linePreview)

		msg, err := parser.ParseLine(line, "")
		if err != nil {
			log.Printf("[INGEST] Parse error: %v", err)
			return
		}
		if msg == nil {
			log.Printf("[INGEST] Line skipped (not a user/assistant message)")
			return
		}
		log.Printf("[INGEST] Parsed message: role=%s source=%s text_len=%d", msg.Role, msg.Source, len(msg.Text))

		if msg.SessionID != "" && !claimedSessions[msg.SessionID] {
			if cfg.SessionFilter != "" && msg.SessionID != cfg.SessionFilter {
				return
			}

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

		text := formatUILogText(msg)

		event := busClient.NewUILogAppend("info", text, msg.SessionID)
		if err := busClient.Send(event); err != nil {
			log.Printf("[INGEST] Send error: %v", err)
		} else {
			log.Printf("[INGEST] Sent to bus: role=%s source=%s text_len=%d", msg.Role, msg.Source, len(msg.Text))
		}
	})
	if err != nil {
		return fmt.Errorf("failed to create watcher: %w", err)
	}

	if err := watcher.Start(); err != nil {
		return fmt.Errorf("failed to start watcher: %w", err)
	}
	defer watcher.Stop()

	log.Printf("[INGEST] Watcher started")
	<-ctx.Done()
	log.Printf("[INGEST] Shutting down...")
	return nil
}

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

func resolveSessionID(cfg Config, logPath string) string {
	if cfg.CodexSession != "" {
		return cfg.CodexSession
	}
	if cfg.ClaudeSession != "" {
		return cfg.ClaudeSession
	}
	if cfg.SessionFilter != "" {
		return cfg.SessionFilter
	}
	if logPath == "" {
		return ""
	}
	base := filepath.Base(logPath)
	if strings.HasPrefix(base, "rollout-") {
		if sessionID := codex.GetSessionIDFromPath(base); sessionID != "" {
			return sessionID
		}
	}
	if strings.HasSuffix(base, ".jsonl") {
		candidate := strings.TrimSuffix(base, ".jsonl")
		if looksLikeUUID(candidate) {
			return candidate
		}
	}
	return ""
}

func looksLikeUUID(value string) bool {
	if len(value) < 32 {
		return false
	}
	parts := strings.Split(value, "-")
	return len(parts) >= 5
}
