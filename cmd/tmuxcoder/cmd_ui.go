package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/opencode/hivemind-tmuxcoder/internal/ui"
	"github.com/spf13/cobra"
)

var uiCmd = &cobra.Command{
	Use:   "ui",
	Short: "Launch tmux UI",
	Run: func(cmd *cobra.Command, args []string) {
		socketPath, _ := cmd.Flags().GetString("socket")
		sessionName, _ := cmd.Flags().GetString("session")
		windowName, _ := cmd.Flags().GetString("window")
		workspaceUID, _ := cmd.Flags().GetString("workspace")
		includeGlobal, _ := cmd.Flags().GetBool("include-global")
		eventTypesRaw, _ := cmd.Flags().GetString("types")

		config := ui.TmuxConfig{
			SocketPath:    socketPath,
			SessionName:   sessionName,
			WindowName:    windowName,
			WorkspaceUID:  workspaceUID,
			IncludeGlobal: includeGlobal,
			EventTypes:    strings.Split(eventTypesRaw, ","),
		}

		if err := ui.LaunchTmuxUI(cmd.Context(), config); err != nil {
			fmt.Printf("Failed to launch tmux UI: %v\n", err)
			os.Exit(1)
		}
	},
}

func init() {
	uiCmd.Flags().String("socket", "", "Bus server socket path (default: auto-detect)")
	uiCmd.Flags().String("session", "", "tmux session name (default: current or tmuxcoder)")
	uiCmd.Flags().String("window", "tmuxcoder", "tmux window name")
	uiCmd.Flags().String("workspace", "", "Workspace UID to filter logs")
	uiCmd.Flags().Bool("include-global", false, "Include global bus events in logs")
	uiCmd.Flags().String("types", "ui.log.append,ui.status.update", "Comma-separated list of event types")

	rootCmd.AddCommand(uiCmd)
}
