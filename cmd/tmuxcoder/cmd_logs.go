package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/opencode/hivemind-tmuxcoder/internal/ui"
	"github.com/spf13/cobra"
)

var logsCmd = &cobra.Command{
	Use:   "logs",
	Short: "Logs commands",
}

var logsTailCmd = &cobra.Command{
	Use:   "tail",
	Short: "Tail logs",
	Run: func(cmd *cobra.Command, args []string) {
		socketPath, _ := cmd.Flags().GetString("socket")
		workspaceUID, _ := cmd.Flags().GetString("workspace")
		includeGlobal, _ := cmd.Flags().GetBool("include-global")
		format, _ := cmd.Flags().GetBool("format")
		eventTypesRaw, _ := cmd.Flags().GetString("types")

		renderMode := ui.RenderModeRaw
		if format {
			renderMode = ui.RenderModeFormatted
		}

		config := &ui.Config{
			SocketPath:    socketPath,
			IncludeGlobal: includeGlobal,
			Output:        os.Stdout,
			RendererMode:  renderMode,
			EventTypes:    strings.Split(eventTypesRaw, ","),
		}

		if workspaceUID != "" {
			config.WorkspaceUIDs = []string{workspaceUID}
		}

		client := ui.NewClient(config)
		if err := client.Connect(cmd.Context()); err != nil {
			fmt.Printf("Failed to connect: %v\n", err)
			os.Exit(1)
		}
		defer client.Close()

		if format {
			fmt.Println("Connected to bus. Tailing logs...")
		}

		if err := client.Tail(cmd.Context()); err != nil {
			if cmd.Context().Err() == nil {
				fmt.Printf("Tail error: %v\n", err)
			}
		}
	},
}

func init() {
	logsTailCmd.Flags().String("socket", "", "Bus server socket path (default: auto-detect)")
	logsTailCmd.Flags().String("workspace", "", "Workspace UID to filter by (optional)")
	logsTailCmd.Flags().Bool("include-global", false, "Include global bus events")
	logsTailCmd.Flags().Bool("format", false, "Enable formatted output (adds timestamps/colors)")
	logsTailCmd.Flags().String("types", "ui.log.append,ui.status.update", "Comma-separated list of event types")

	logsCmd.AddCommand(logsTailCmd)
	rootCmd.AddCommand(logsCmd)
}
