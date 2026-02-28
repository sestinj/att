package cmd

import (
	"os"

	"github.com/sestinj/att/internal/config"
	"github.com/sestinj/att/internal/tmux"
	"github.com/spf13/cobra"
)

var (
	feedNoAttach    bool
	feedBaseSession string
	flagCommand     string

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
	rootCmd.PersistentFlags().StringVar(&flagCommand, "command", "", "Command to run in new windows (default: claude)")
}

func runFeed(cmd *cobra.Command, args []string) error {
	cfg := config.Load()

	command := cfg.Command
	if flagCommand != "" {
		command = flagCommand
	}

	var opts []tmux.FeedOption
	if feedNoAttach {
		opts = append(opts, tmux.WithNoAttach())
	}
	if feedBaseSession != "" {
		opts = append(opts, tmux.WithBaseSession(feedBaseSession))
	}
	if command != "" {
		opts = append(opts, tmux.WithCommand(command))
	}
	if len(cfg.Projects) > 0 {
		opts = append(opts, tmux.WithProjects(cfg.Projects))
	}
	if cfg.DirCommand != "" {
		opts = append(opts, tmux.WithDirCommand(cfg.DirCommand))
	}
	fc := tmux.NewFeedController(opts...)
	return fc.Run()
}
