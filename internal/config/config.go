package config

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Config holds user configuration from ~/.config/att/config.json.
type Config struct {
	Command    string   `json:"command"`
	Projects   []string `json:"projects"`
	DirCommand string   `json:"dir_command"`
}

// Load reads the config file and returns a Config. On any error (missing file,
// malformed JSON, etc.) it returns a zero-value Config silently.
func Load() Config {
	home, err := os.UserHomeDir()
	if err != nil {
		return Config{}
	}
	path := filepath.Join(home, ".config", "att", "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}
	}
	return cfg
}

// ResolveDir resolves a working directory for launching a session. If dirCommand
// is set, it runs the command to transform the directory; otherwise it falls back
// to the git repository root (if inside one). Returns absDir unchanged if neither
// applies.
func ResolveDir(dirCommand, absDir string) (string, error) {
	if dirCommand != "" {
		return RunDirCommand(dirCommand, absDir)
	}
	if gitRoot, err := exec.Command("git", "-C", absDir, "rev-parse", "--show-toplevel").Output(); err == nil {
		return strings.TrimSpace(string(gitRoot)), nil
	}
	return absDir, nil
}

// RunDirCommand executes a dir_command with the working directory set to dir.
// The command string is split on whitespace (no shell). Returns the trimmed
// stdout as the resolved directory path. Times out after 30 seconds.
func RunDirCommand(dirCmd, dir string) (string, error) {
	parts := strings.Fields(dirCmd)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, parts[0], parts[1:]...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("dir_command %q: %w", dirCmd, err)
	}
	result := strings.TrimSpace(string(out))
	if result == "" {
		return "", fmt.Errorf("dir_command %q returned empty output", dirCmd)
	}
	return result, nil
}
