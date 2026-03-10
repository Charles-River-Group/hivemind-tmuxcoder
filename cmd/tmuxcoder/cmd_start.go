package main

import (
	"fmt"
	"os"
	"time"

	appbus "github.com/opencode/hivemind-tmuxcoder/internal/app/bus"
	appingest "github.com/opencode/hivemind-tmuxcoder/internal/app/ingest"
	appsink "github.com/opencode/hivemind-tmuxcoder/internal/app/sink"
	"github.com/opencode/hivemind-tmuxcoder/internal/ui"
	"github.com/spf13/cobra"
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start bus+ingest+sink (optional ui)",
	Run: func(cmd *cobra.Command, args []string) {
		socket, _ := cmd.Flags().GetString("socket")
		watchDirs, _ := cmd.Flags().GetStringSlice("watch")
		logPath, _ := cmd.Flags().GetString("log-path")
		codexSession, _ := cmd.Flags().GetString("codex-session")
		claudeSession, _ := cmd.Flags().GetString("claude-session")
		claudeProject, _ := cmd.Flags().GetString("claude-project")
		fromBegin, _ := cmd.Flags().GetBool("from-begin")
		projectFilter, _ := cmd.Flags().GetString("project")
		sessionFilter, _ := cmd.Flags().GetString("session-id")
		sinkEnabled, _ := cmd.Flags().GetBool("sink")
		sinkInclude, _ := cmd.Flags().GetBool("sink-include-global")
		sinkDBPath, _ := cmd.Flags().GetString("sink-db-path")
		sinkSources, _ := cmd.Flags().GetString("sink-sources")
		sinkRebuild, _ := cmd.Flags().GetBool("sink-rebuild")
		tmuxSessionID, _ := cmd.Flags().GetString("tmux-session-id")
		uiEnabled, _ := cmd.Flags().GetBool("ui")
		writeRules, _ := cmd.Flags().GetBool("write-rules")
		rulesFiles, _ := cmd.Flags().GetString("rules-files")

		if writeRules {
			if err := writeRulesTemplates(parseCSV(rulesFiles)); err != nil {
				fmt.Printf("Failed to write rules: %v\n", err)
				os.Exit(1)
			}
		}

		for _, dir := range []string{"~/.codex/skills", "~/.claude/skills"} {
			if err := ensureSkillInstalled("sqlite-context", expandHome(dir)); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not install sqlite-context skill to %s: %v\n", dir, err)
			}
		}

		if tmuxSessionID != "" {
			_ = os.Setenv("TMUXCODER_SESSION_ID", tmuxSessionID)
			if err := writeSessionFile(tmuxSessionID); err != nil {
				fmt.Printf("Failed to write session file: %v\n", err)
				os.Exit(1)
			}
		}

		if sinkEnabled {
			resolvedDBPath, err := resolveDBPath(sinkDBPath)
			if err != nil {
				fmt.Printf("Failed to resolve db path: %v\n", err)
				os.Exit(1)
			}
			if err := writeDBPathFile(resolvedDBPath); err != nil {
				fmt.Printf("Failed to write db path file: %v\n", err)
				os.Exit(1)
			}
			sinkDBPath = resolvedDBPath
		}

		busCfg := appbus.Config{SocketPath: socket}
		errCh := make(chan error, 4)

		go func() {
			if err := appbus.Run(cmd.Context(), busCfg); err != nil {
				errCh <- err
			}
		}()

		socketPath := busSocketPath(socket)
		if err := waitForSocket(socketPath, 3*time.Second); err != nil {
			fmt.Printf("start error: %v\n", err)
			os.Exit(1)
		}

		ingestCfg := appingest.Config{
			SocketPath:    socket,
			WatchDirs:     watchDirs,
			LogPath:       logPath,
			CodexSession:  codexSession,
			ClaudeSession: claudeSession,
			ClaudeProject: claudeProject,
			FromBegin:     fromBegin,
			ProjectFilter: projectFilter,
			SessionFilter: sessionFilter,
		}
		go func() {
			if err := appingest.Run(cmd.Context(), ingestCfg); err != nil {
				errCh <- err
			}
		}()

		if sinkEnabled {
			sinkCfg := appsink.Config{
				SocketPath:    socket,
				DBPath:        sinkDBPath,
				IncludeGlobal: sinkInclude,
				Sources:       parseCSV(sinkSources),
				Rebuild:       sinkRebuild,
				TmuxSessionID: tmuxSessionID,
			}
			go func() {
				if err := appsink.Run(cmd.Context(), sinkCfg); err != nil {
					errCh <- err
				}
			}()
		}

		if uiEnabled {
			uiCfg := ui.TmuxConfig{}
			if socket != "" {
				uiCfg.SocketPath = socket
			}
			go func() {
				if err := ui.LaunchTmuxUI(cmd.Context(), uiCfg); err != nil {
					errCh <- err
				}
			}()
		}

		select {
		case <-cmd.Context().Done():
			return
		case err := <-errCh:
			if err != nil {
				fmt.Printf("start error: %v\n", err)
				os.Exit(1)
			}
		}
	},
}

func init() {
	startCmd.Flags().String("socket", "", "Bus socket path (default: auto-detect)")
	startCmd.Flags().StringSlice("watch", nil, "Directory to watch (repeatable)")
	startCmd.Flags().String("log-path", "", "Specific JSONL file path to watch (overrides --watch)")
	startCmd.Flags().String("codex-session", "", "Codex session ID to find and watch rollout.jsonl")
	startCmd.Flags().String("claude-session", "", "Claude session ID to find and watch")
	startCmd.Flags().String("claude-project", "", "Claude project path or name (watches latest session)")
	startCmd.Flags().Bool("from-begin", false, "Read from file start instead of end")
	startCmd.Flags().String("project", "", "Only process files matching this project path")
	startCmd.Flags().String("session-id", "", "Only bind to this specific session ID (in JSONL content)")
	startCmd.Flags().Bool("sink", true, "Start SQLite sink")
	startCmd.Flags().Bool("sink-include-global", false, "Include global bus events in sink")
	startCmd.Flags().String("sink-db-path", "", "SQLite DB path for sink")
	startCmd.Flags().String("sink-sources", "codex,claude", "Comma-separated sources to keep")
	startCmd.Flags().Bool("sink-rebuild", false, "Drop and recreate model_outputs on start")
	startCmd.Flags().String("tmux-session-id", "", "Tmuxcoder session ID for sink writes")
	startCmd.Flags().Bool("ui", false, "Launch tmux UI")
	startCmd.Flags().Bool("write-rules", true, "Write shared-context rules to AGENTS.md and CLAUDE.md")
	startCmd.Flags().String("rules-files", "AGENTS.md,CLAUDE.md", "Comma-separated rules files to write")

	rootCmd.AddCommand(startCmd)
}
