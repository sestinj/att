package cmd

import (
	"os"

	"github.com/sestinj/att/internal/tmux"
	"github.com/spf13/cobra"
)

var (
	feedNoAttach    bool
	feedBaseSession string

	// Set via ldflags at build time.
	version = "dev"
)

var rootCmd = &cobra.Command{
	Use:     "att",
	Short:   "tmux session manager for Claude Code",
	Version: version,
	RunE:    runFeed, // bare "att" runs feed
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.Flags().BoolVar(&feedNoAttach, "no-attach", false, "Run without attaching to tmux (for testing)")
	rootCmd.Flags().StringVar(&feedBaseSession, "session", "", "Base tmux session name (default: att)")
}

func runFeed(cmd *cobra.Command, args []string) error {
	var opts []tmux.FeedOption
	if feedNoAttach {
		opts = append(opts, tmux.WithNoAttach())
	}
	if feedBaseSession != "" {
		opts = append(opts, tmux.WithBaseSession(feedBaseSession))
	}
	fc := tmux.NewFeedController(opts...)
	return fc.Run()
}
