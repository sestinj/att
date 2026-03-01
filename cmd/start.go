package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/sestinj/att/internal/config"
	"github.com/sestinj/att/internal/tmux"
	"github.com/spf13/cobra"
)

var flagPrompt string

var startCmd = &cobra.Command{
	Use:   "start [path]",
	Short: "Start Claude Code in a managed tmux window",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runStart,
}

func init() {
	startCmd.Flags().StringVarP(&flagPrompt, "prompt", "p", "", "Initial prompt to send to the session")
	rootCmd.AddCommand(startCmd)
}

func runStart(cmd *cobra.Command, args []string) error {
	cfg := config.Load()

	command := cfg.Command
	if flagCommand != "" {
		command = flagCommand
	}
	if command == "" {
		command = "claude"
	}

	dir := ""
	if len(args) > 0 {
		dir = args[0]
	}

	if dir == "" {
		dir, _ = os.Getwd()
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}

	windowName := filepath.Base(absDir)

	resolved, err := config.ResolveDir(cfg.DirCommand, absDir)
	if err != nil {
		return fmt.Errorf("dir_command failed: %w", err)
	}
	absDir = resolved

	// If --prompt is set, write it to a temp file and pipe it into the command
	if flagPrompt != "" {
		f, err := os.CreateTemp("", "att-prompt-*")
		if err != nil {
			return fmt.Errorf("create temp file: %w", err)
		}
		if _, err := f.WriteString(flagPrompt); err != nil {
			f.Close()
			os.Remove(f.Name())
			return fmt.Errorf("write prompt: %w", err)
		}
		f.Close()
		command = fmt.Sprintf(`sh -c 'cat %s; rm -f %s' | %s`, f.Name(), f.Name(), command)
	}

	var windowIdx string
	if !tmux.HasSession("att") {
		idx, err := tmux.NewSession("att", windowName, absDir, command)
		if err != nil {
			return fmt.Errorf("create tmux session: %w", err)
		}
		windowIdx = idx
	} else {
		idx, err := tmux.NewWindow("att", windowName, absDir, command)
		if err != nil {
			return fmt.Errorf("create tmux window: %w", err)
		}
		windowIdx = idx
	}

	// Each start gets its own grouped session so terminals have independent views
	groupedName := fmt.Sprintf("att-%d", os.Getpid())
	if err := tmux.NewGroupedSession(groupedName, "att"); err != nil {
		return fmt.Errorf("create grouped session: %w", err)
	}

	// Select the window we just created in our grouped session
	tmux.SelectWindow(groupedName, windowIdx)

	err = tmux.AttachOrSwitch(groupedName)
	// Clean up grouped session after detach
	tmux.KillSession(groupedName)
	return err
}
