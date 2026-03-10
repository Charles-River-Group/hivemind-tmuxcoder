package main

import (
	"fmt"
	"os"
	"time"

	"github.com/opencode/hivemind-tmuxcoder/internal/ui"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show bus/ingest/sink status",
	Run: func(cmd *cobra.Command, args []string) {
		socketPath, _ := cmd.Flags().GetString("socket")
		dbPath, _ := cmd.Flags().GetString("db-path")
		claimDir, _ := cmd.Flags().GetString("claim-dir")
		tmuxSessionID, _ := cmd.Flags().GetString("tmux-session-id")
		watchInterval, _ := cmd.Flags().GetDuration("watch")
		clearScreen, _ := cmd.Flags().GetBool("clear")
		uiEnabled, _ := cmd.Flags().GetBool("ui")
		uiSession, _ := cmd.Flags().GetString("ui-session")
		uiWindow, _ := cmd.Flags().GetString("ui-window")

		if uiEnabled {
			if watchInterval <= 0 {
				watchInterval = 2 * time.Second
			}
			statusCfg := ui.StatusConfig{
				SessionName:   uiSession,
				WindowName:    uiWindow,
				SocketPath:    socketPath,
				DBPath:        dbPath,
				ClaimDir:      claimDir,
				TmuxSessionID: tmuxSessionID,
				Interval:      watchInterval,
				Clear:         true,
			}
			if err := ui.LaunchTmuxStatus(cmd.Context(), statusCfg); err != nil {
				fmt.Printf("Status UI error: %v\n", err)
				os.Exit(1)
			}
			return
		}

		if watchInterval > 0 {
			for {
				if cmd.Context().Err() != nil {
					return
				}
				if clearScreen {
					fmt.Print("\033[H\033[2J")
				}
				printBusStatus(socketPath)
				fmt.Println()
				printIngestStatus(claimDir)
				fmt.Println()
				printSinkStatus(dbPath, tmuxSessionID)
				time.Sleep(watchInterval)
			}
		}

		printBusStatus(socketPath)
		fmt.Println()
		printIngestStatus(claimDir)
		fmt.Println()
		printSinkStatus(dbPath, tmuxSessionID)
	},
}

func init() {
	statusCmd.Flags().String("socket", "", "Bus socket path (default: auto-detect)")
	statusCmd.Flags().String("db-path", "", "SQLite DB path (default: ~/.tmuxcoder/model_outputs.db)")
	statusCmd.Flags().String("claim-dir", "", "Ingest claim dir (default: ~/.tmuxcoder/run/ingest-claims)")
	statusCmd.Flags().String("tmux-session-id", "", "Tmuxcoder session ID (optional)")
	statusCmd.Flags().Duration("watch", 0, "Refresh interval (e.g. 2s)")
	statusCmd.Flags().Bool("clear", false, "Clear screen on each refresh when --watch is set")
	statusCmd.Flags().Bool("ui", false, "Open tmux UI status window")
	statusCmd.Flags().String("ui-session", "", "tmux session name (default: current or tmuxcoder)")
	statusCmd.Flags().String("ui-window", "tmuxcoder-status", "tmux window name")

	rootCmd.AddCommand(statusCmd)
}
