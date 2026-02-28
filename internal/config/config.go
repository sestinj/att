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

// RunDirCommand executes a dir_command with dir appended as the last argument.
// The command string is split on whitespace (no shell). Returns the trimmed
// stdout as the resolved directory path. Times out after 30 seconds.
func RunDirCommand(dirCmd, dir string) (string, error) {
	parts := strings.Fields(dirCmd)
	parts = append(parts, dir)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, parts[0], parts[1:]...)
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
