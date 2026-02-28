package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sestinj/att/internal/tmux"
	"github.com/spf13/cobra"
)

var startCmd = &cobra.Command{
	Use:   "start [path]",
	Short: "Start Claude Code in a managed tmux window",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runStart,
}

func init() {
	rootCmd.AddCommand(startCmd)
}

func runStart(cmd *cobra.Command, args []string) error {
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

	// If inside a git repo, use the repo root
	if gitRoot, err := exec.Command("git", "-C", absDir, "rev-parse", "--show-toplevel").Output(); err == nil {
		absDir = strings.TrimSpace(string(gitRoot))
	}

	windowName := filepath.Base(absDir)

	var windowIdx string
	if !tmux.HasSession("att") {
		idx, err := tmux.NewSession("att", windowName, absDir, "claude")
		if err != nil {
			return fmt.Errorf("create tmux session: %w", err)
		}
		windowIdx = idx
	} else {
		idx, err := tmux.NewWindow("att", windowName, absDir, "claude")
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
