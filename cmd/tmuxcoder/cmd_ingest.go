package main

import (
	"fmt"
	"os"

	appingest "github.com/opencode/hivemind-tmuxcoder/internal/app/ingest"
	"github.com/spf13/cobra"
)

var ingestCmd = &cobra.Command{
	Use:   "ingest",
	Short: "Run ingest",
	Run: func(cmd *cobra.Command, args []string) {
		socketPath, _ := cmd.Flags().GetString("socket")
		workspaceUID, _ := cmd.Flags().GetString("workspace-uid")
		label, _ := cmd.Flags().GetString("label")
		watchDirs, _ := cmd.Flags().GetStringSlice("watch")
		logPath, _ := cmd.Flags().GetString("log-path")
		codexSession, _ := cmd.Flags().GetString("codex-session")
		claudeSession, _ := cmd.Flags().GetString("claude-session")
		claudeProject, _ := cmd.Flags().GetString("claude-project")
		fromBegin, _ := cmd.Flags().GetBool("from-begin")
		projectFilter, _ := cmd.Flags().GetString("project")
		sessionFilter, _ := cmd.Flags().GetString("session-id")

		cfg := appingest.Config{
			SocketPath:    socketPath,
			WorkspaceUID:  workspaceUID,
			Label:         label,
			WatchDirs:     watchDirs,
			LogPath:       logPath,
			CodexSession:  codexSession,
			ClaudeSession: claudeSession,
			ClaudeProject: claudeProject,
			FromBegin:     fromBegin,
			ProjectFilter: projectFilter,
			SessionFilter: sessionFilter,
		}

		if err := appingest.Run(cmd.Context(), cfg); err != nil {
			fmt.Printf("Ingest error: %v\n", err)
			os.Exit(1)
		}
	},
}

func init() {
	ingestCmd.Flags().String("socket", "", "Bus socket path (default: auto-detect)")
	ingestCmd.Flags().String("workspace-uid", "", "Workspace UID for events (default: auto-generate)")
	ingestCmd.Flags().String("label", "agent:session", "Client label")
	ingestCmd.Flags().StringSlice("watch", nil, "Directory to watch (repeatable)")
	ingestCmd.Flags().String("log-path", "", "Specific JSONL file path to watch (overrides --watch)")
	ingestCmd.Flags().String("codex-session", "", "Codex session ID to find and watch rollout.jsonl")
	ingestCmd.Flags().String("claude-session", "", "Claude session ID to find and watch")
	ingestCmd.Flags().String("claude-project", "", "Claude project path or name (watches latest session)")
	ingestCmd.Flags().Bool("from-begin", false, "Read from file start instead of end")
	ingestCmd.Flags().String("project", "", "Only process files matching this project path")
	ingestCmd.Flags().String("session-id", "", "Only bind to this specific session ID (in JSONL content)")

	rootCmd.AddCommand(ingestCmd)
}
