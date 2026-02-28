package tmux

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type PaneInfo struct {
	PaneID      string
	SessionName string
	WindowIndex string
	PanePath    string
}

func InTmux() bool {
	return os.Getenv("TMUX") != ""
}

func ListPanes() ([]PaneInfo, error) {
	out, err := exec.Command("tmux", "list-panes", "-a",
		"-F", "#{pane_id}\t#{session_name}\t#{window_index}\t#{pane_current_path}",
	).Output()
	if err != nil {
		return nil, err
	}

	var panes []PaneInfo
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 4)
		if len(parts) < 4 {
			continue
		}
		panes = append(panes, PaneInfo{
			PaneID:      parts[0],
			SessionName: parts[1],
			WindowIndex: parts[2],
			PanePath:    parts[3],
		})
	}
	return panes, nil
}

func FocusPane(p PaneInfo) error {
	// Select the window first, then the pane
	target := p.SessionName + ":" + p.WindowIndex
	if err := exec.Command("tmux", "select-window", "-t", target).Run(); err != nil {
		return err
	}
	return exec.Command("tmux", "select-pane", "-t", p.PaneID).Run()
}

func GetStatusRight() (string, error) {
	out, err := exec.Command("tmux", "display-message", "-p", "#{status-right}").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func SetStatusRight(content string) error {
	return exec.Command("tmux", "set-option", "status-right", content).Run()
}

func BindKey(key string, command string) error {
	return exec.Command("tmux", "bind-key", "-n", key, "run-shell", command).Run()
}

// BindKeyDirect binds a key to a tmux command directly (not wrapped in run-shell).
func BindKeyDirect(key string, args ...string) error {
	cmdArgs := append([]string{"bind-key", "-n", key}, args...)
	return exec.Command("tmux", cmdArgs...).Run()
}

func UnbindKey(key string) error {
	return exec.Command("tmux", "unbind-key", "-n", key).Run()
}

// Session/window management

type WindowInfo struct {
	Index string
	Name  string
	Path  string
}

func HasSession(name string) bool {
	return exec.Command("tmux", "has-session", "-t", name).Run() == nil
}

func NewSession(name, windowName, dir, cmd string) (string, error) {
	args := []string{"new-session", "-d", "-s", name, "-n", windowName, "-P", "-F", "#{window_index}"}
	if dir != "" {
		args = append(args, "-c", dir)
	}
	if cmd != "" {
		args = append(args, cmd)
	}
	c := exec.Command("tmux", args...)
	var stderr bytes.Buffer
	c.Stderr = &stderr
	out, err := c.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return "", fmt.Errorf("%s", msg)
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func NewWindow(session, windowName, dir, cmd string) (string, error) {
	args := []string{"new-window", "-t", session + ":", "-n", windowName, "-P", "-F", "#{window_index}"}
	if dir != "" {
		args = append(args, "-c", dir)
	}
	if cmd != "" {
		args = append(args, cmd)
	}
	c := exec.Command("tmux", args...)
	var stderr bytes.Buffer
	c.Stderr = &stderr
	out, err := c.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return "", fmt.Errorf("%s", msg)
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// NewGroupedSession creates a session linked to target that shares windows
// but allows independent window selection.
func NewGroupedSession(name, target string) error {
	c := exec.Command("tmux", "new-session", "-d", "-s", name, "-t", target)
	var stderr bytes.Buffer
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return fmt.Errorf("%s", msg)
		}
		return err
	}
	return nil
}

// KillSession destroys a tmux session.
func KillSession(name string) {
	exec.Command("tmux", "kill-session", "-t", name).Run()
}

func ListWindows(session string) ([]WindowInfo, error) {
	out, err := exec.Command("tmux", "list-windows", "-t", session,
		"-F", "#{window_index}\t#{window_name}\t#{pane_current_path}",
	).Output()
	if err != nil {
		return nil, err
	}

	var windows []WindowInfo
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 3 {
			continue
		}
		windows = append(windows, WindowInfo{
			Index: parts[0],
			Name:  parts[1],
			Path:  parts[2],
		})
	}
	return windows, nil
}

func SelectWindow(session, windowRef string) error {
	return exec.Command("tmux", "select-window", "-t", session+":"+windowRef).Run()
}

func RenameWindow(session, idx, name string) error {
	return exec.Command("tmux", "rename-window", "-t", session+":"+idx, name).Run()
}

func AttachOrSwitch(name string) error {
	if InTmux() {
		return exec.Command("tmux", "switch-client", "-t", name).Run()
	}
	cmd := exec.Command("tmux", "attach-session", "-t", name)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func SetStatusRightForSession(session, content string) error {
	return exec.Command("tmux", "set-option", "-t", session, "status-right", content).Run()
}

func GetStatusRightForSession(session string) (string, error) {
	out, err := exec.Command("tmux", "show-options", "-t", session, "-v", "status-right").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func SetStatusLeftForSession(session, content string) error {
	return exec.Command("tmux", "set-option", "-t", session, "status-left", content).Run()
}

func GetStatusLeftForSession(session string) (string, error) {
	out, err := exec.Command("tmux", "show-options", "-t", session, "-v", "status-left").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
