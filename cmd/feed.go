package cmd

import (
	"github.com/spf13/cobra"
)

var feedCmd = &cobra.Command{
	Use:   "feed",
	Short: "Attach to att tmux session with status bar showing Claude Code session states",
	RunE:  runFeed,
}

func init() {
	feedCmd.Flags().BoolVar(&feedNoAttach, "no-attach", false, "Run without attaching to tmux (for testing)")
	feedCmd.Flags().StringVar(&feedBaseSession, "session", "", "Base tmux session name (default: att)")
	rootCmd.AddCommand(feedCmd)
}
