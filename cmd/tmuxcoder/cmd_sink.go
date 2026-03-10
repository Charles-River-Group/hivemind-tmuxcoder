package main

import (
	"fmt"
	"os"

	appsink "github.com/opencode/hivemind-tmuxcoder/internal/app/sink"
	"github.com/spf13/cobra"
)

var sinkCmd = &cobra.Command{
	Use:   "sink",
	Short: "Run SQLite sink",
	Run: func(cmd *cobra.Command, args []string) {
		socketPath, _ := cmd.Flags().GetString("socket")
		dbPath, _ := cmd.Flags().GetString("db-path")
		tmuxSessionID, _ := cmd.Flags().GetString("tmux-session-id")
		includeGlobal, _ := cmd.Flags().GetBool("include-global")
		sourcesRaw, _ := cmd.Flags().GetString("sources")
		rebuild, _ := cmd.Flags().GetBool("rebuild")

		resolvedDBPath, err := resolveDBPath(dbPath)
		if err != nil {
			fmt.Printf("Sink error: %v\n", err)
			os.Exit(1)
		}
		if err := writeDBPathFile(resolvedDBPath); err != nil {
			fmt.Printf("Failed to write db path file: %v\n", err)
			os.Exit(1)
		}

		cfg := appsink.Config{
			SocketPath:    socketPath,
			DBPath:        resolvedDBPath,
			IncludeGlobal: includeGlobal,
			Sources:       parseCSV(sourcesRaw),
			Rebuild:       rebuild,
			TmuxSessionID: tmuxSessionID,
		}

		if err := appsink.Run(cmd.Context(), cfg); err != nil {
			fmt.Printf("Sink error: %v\n", err)
			os.Exit(1)
		}
	},
}

func init() {
	sinkCmd.Flags().String("socket", "", "Bus server socket path (default: auto-detect)")
	sinkCmd.Flags().String("db-path", "", "SQLite DB path")
	sinkCmd.Flags().String("tmux-session-id", "", "Tmuxcoder session ID for sink writes")
	sinkCmd.Flags().Bool("include-global", false, "Include global bus events")
	sinkCmd.Flags().String("sources", "codex,claude", "Comma-separated sources to keep (empty = all)")
	sinkCmd.Flags().Bool("rebuild", false, "Drop and recreate model_outputs on start")

	rootCmd.AddCommand(sinkCmd)
}
