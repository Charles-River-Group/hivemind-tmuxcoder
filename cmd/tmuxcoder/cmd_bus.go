package main

import (
	"fmt"
	"os"

	appbus "github.com/opencode/hivemind-tmuxcoder/internal/app/bus"
	"github.com/spf13/cobra"
)

var busCmd = &cobra.Command{
	Use:   "bus",
	Short: "Run bus server",
	Run: func(cmd *cobra.Command, args []string) {
		socketPath, _ := cmd.Flags().GetString("socket")
		if err := appbus.Run(cmd.Context(), appbus.Config{SocketPath: socketPath}); err != nil {
			fmt.Printf("Bus error: %v\n", err)
			os.Exit(1)
		}
	},
}

func init() {
	busCmd.Flags().String("socket", "", "Bus server socket path (default: auto-detect)")
	rootCmd.AddCommand(busCmd)
}
