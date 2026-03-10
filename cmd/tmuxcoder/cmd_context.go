package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var contextCmd = &cobra.Command{
	Use:   "context",
	Short: "Fetch shared context from SQLite",
	Run: func(cmd *cobra.Command, args []string) {
		dbPath, _ := cmd.Flags().GetString("db-path")
		tmuxSessionID, _ := cmd.Flags().GetString("tmux-session-id")
		sessionID, _ := cmd.Flags().GetString("session-id")
		source, _ := cmd.Flags().GetString("source")
		limit, _ := cmd.Flags().GetInt("limit")
		maxChars, _ := cmd.Flags().GetInt("max-chars")
		format, _ := cmd.Flags().GetString("format")
		debug, _ := cmd.Flags().GetBool("debug")

		if source != "" && source != "codex" && source != "claude" {
			fmt.Println("context error: --source must be codex or claude")
			os.Exit(1)
		}
		if format != "text" && format != "json" {
			fmt.Println("context error: --format must be text or json")
			os.Exit(1)
		}

		resolvedTmuxID, err := resolveTmuxSessionID(tmuxSessionID)
		if err != nil {
			fmt.Printf("context error: %v\n", err)
			os.Exit(1)
		}

		resolvedDBPath, err := resolveDBPath(dbPath)
		if err != nil {
			fmt.Printf("context error: %v\n", err)
			os.Exit(1)
		}

		if debug {
			exe, _ := os.Executable()
			cwd, _ := os.Getwd()
			fmt.Fprintf(os.Stderr, "context debug:\n")
			fmt.Fprintf(os.Stderr, "  executable: %s\n", exe)
			fmt.Fprintf(os.Stderr, "  cwd: %s\n", cwd)
			fmt.Fprintf(os.Stderr, "  HOME: %s\n", os.Getenv("HOME"))
			fmt.Fprintf(os.Stderr, "  TMUXCODER_DB_PATH: %s\n", os.Getenv("TMUXCODER_DB_PATH"))
			fmt.Fprintf(os.Stderr, "  current_db_path: %s\n", readDBPathFile())
			fmt.Fprintf(os.Stderr, "  resolved_db_path: %s\n", resolvedDBPath)
			fmt.Fprintf(os.Stderr, "  resolved_tmux_session_id: %s\n", resolvedTmuxID)
		}

		if _, err := os.Stat(resolvedDBPath); err != nil {
			fmt.Printf("context error: db not found: %s\n", resolvedDBPath)
			os.Exit(1)
		}

		rows, err := readContextRows(resolvedDBPath, resolvedTmuxID, sessionID, source)
		if err != nil {
			readPath, cleanup, snapErr := snapshotDB(resolvedDBPath)
			if snapErr != nil {
				fmt.Printf("context error: %v\n", err)
				os.Exit(1)
			}
			if cleanup != nil {
				defer cleanup()
			}
			rows, err = readContextRows(readPath, resolvedTmuxID, sessionID, source)
			if err != nil {
				fmt.Printf("context error: %v\n", err)
				os.Exit(1)
			}
		}

		rawRows := len(rows)
		if limit > 0 && len(rows) > limit {
			rows = rows[len(rows)-limit:]
		}
		selectedRows := len(rows)
		var firstID, lastID int64
		var firstTS, lastTS string
		if len(rows) > 0 {
			firstID, firstTS = rows[0].id, rows[0].ts
			lastID, lastTS = rows[len(rows)-1].id, rows[len(rows)-1].ts
		}

		merged := mergeRoles(rows)
		merged = trimByChars(merged, maxChars)
		mergedRows := len(merged)

		if err := writeContextReadAudit(resolvedDBPath, resolvedTmuxID, sessionID, source, rawRows, selectedRows, mergedRows, firstID, lastID, firstTS, lastTS, limit, maxChars); err != nil {
			fmt.Fprintf(os.Stderr, "context audit warning: %v\n", err)
		}

		switch format {
		case "json":
			if err := writeContextJSON(os.Stdout, merged); err != nil {
				fmt.Printf("context error: %v\n", err)
				os.Exit(1)
			}
		default:
			writeContextText(os.Stdout, merged)
		}
	},
}

func init() {
	contextCmd.Flags().String("db-path", "", "SQLite DB path (default: ~/.tmuxcoder/model_outputs.db)")
	contextCmd.Flags().String("tmux-session-id", "", "Tmuxcoder session ID (optional)")
	contextCmd.Flags().String("session-id", "", "Model session ID (optional)")
	contextCmd.Flags().String("source", "", "Source to filter (codex|claude)")
	contextCmd.Flags().Int("limit", 50, "Max rows to keep (0 = all)")
	contextCmd.Flags().Int("max-chars", 12000, "Max characters in output (0 = unlimited)")
	contextCmd.Flags().String("format", "text", "Output format: text|json")
	contextCmd.Flags().Bool("debug", false, "Print resolved paths and env to stderr")

	rootCmd.AddCommand(contextCmd)
}
