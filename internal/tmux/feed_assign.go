package tmux

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/sestinj/att/internal/claude"
)

// cleanupStale removes att-feed-* tmux sessions and /tmp/att-feed-*.fifo
// files left behind by previous att processes that crashed (SIGKILL, etc.)
// without running their deferred cleanup.
func (fc *FeedController) cleanupStale() {
	// Kill grouped feed sessions whose PID no longer exists
	sessions, _ := exec.Command("tmux", "list-sessions", "-F", "#{session_name}").Output()
	for _, name := range strings.Split(strings.TrimSpace(string(sessions)), "\n") {
		if !strings.HasPrefix(name, "att-feed-") {
			continue
		}
		pidStr := strings.TrimPrefix(name, "att-feed-")
		pid, err := strconv.Atoi(pidStr)
		if err != nil {
			continue
		}
		// Check if the PID is still alive
		if err := syscall.Kill(pid, 0); err != nil {
			KillSession(name)
		}
	}

	// Remove stale FIFOs
	matches, _ := filepath.Glob("/tmp/att-feed-*.fifo")
	for _, path := range matches {
		base := filepath.Base(path)
		pidStr := strings.TrimPrefix(base, "att-feed-")
		pidStr = strings.TrimSuffix(pidStr, ".fifo")
		pid, err := strconv.Atoi(pidStr)
		if err != nil {
			continue
		}
		if err := syscall.Kill(pid, 0); err != nil {
			os.Remove(path)
		}
	}
}

// cleanupPlaceholder removes _init placeholder windows once real windows exist.
func (fc *FeedController) cleanupPlaceholder(windows []WindowInfo) []WindowInfo {
	if len(windows) < 2 {
		return windows
	}
	killed := false
	for _, w := range windows {
		if w.Name == "_init" {
			KillWindow(fc.baseSession, w.Index)
			killed = true
		}
	}
	if !killed {
		return windows
	}
	updated, err := ListWindows(fc.baseSession)
	if err != nil {
		return windows
	}
	return updated
}

// assignSessionsToWindows maps each window index to a distinct session.
// When multiple windows share the same workspace path, each window gets
// one of the most recently modified sessions at that path. Old dead sessions
// (from Claude processes that exited hours ago) are excluded.
func assignSessionsToWindows(windows []WindowInfo, sessions []claude.Session) map[int]claude.Session {
	// Count how many windows per path
	windowsPerPath := make(map[string]int)
	for _, w := range windows {
		windowsPerPath[w.Path]++
	}

	// Group sessions by workspace path, sorted by mod time descending (most recent first)
	byPath := make(map[string][]claude.Session)
	for _, s := range sessions {
		byPath[s.WorkspacePath] = append(byPath[s.WorkspacePath], s)
	}
	for path := range byPath {
		group := byPath[path]
		sort.SliceStable(group, func(i, j int) bool {
			ai, aj := group[i].NeedsAttention(), group[j].NeedsAttention()
			if ai != aj {
				return ai
			}
			return group[i].ModTime.After(group[j].ModTime)
		})
		// Keep only as many sessions as there are windows at this path.
		// Old sessions from dead Claude processes are irrelevant.
		n := windowsPerPath[path]
		if n > 0 && len(group) > n {
			group = group[:n]
		}
		byPath[path] = group
	}

	// Assign one session per window, consuming from each path's group
	result := make(map[int]claude.Session)
	used := make(map[string]int) // path -> next index to consume
	for i, w := range windows {
		group := byPath[w.Path]
		idx := used[w.Path]
		if idx < len(group) {
			result[i] = group[idx]
			used[w.Path] = idx + 1
		}
	}
	return result
}
