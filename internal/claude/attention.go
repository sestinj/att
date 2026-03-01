package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// AttentionInfo represents a session that needs user attention,
// written by the `att hook` command when Claude Code fires a Notification hook.
type AttentionInfo struct {
	TranscriptPath   string    `json:"transcript_path"`
	NotificationType string    `json:"notification_type"`
	Timestamp        time.Time `json:"timestamp"`
}

// AttentionDir returns the path to the attention file directory.
func AttentionDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "att", "attention")
}

// ReadAttentionSet scans the attention directory and returns the set of
// transcript paths that currently need attention. File exists = needs attention.
func ReadAttentionSet() map[string]bool {
	dir := AttentionDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	result := make(map[string]bool)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var info AttentionInfo
		if err := json.Unmarshal(data, &info); err != nil {
			continue
		}
		if info.TranscriptPath != "" {
			result[info.TranscriptPath] = true
		}
	}
	return result
}

// WriteAttention creates an attention file for the given session.
func WriteAttention(sessionID string, info AttentionInfo) error {
	dir := AttentionDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.Marshal(info)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, sessionID+".json"), data, 0644)
}

// DeleteAttention removes the attention file for the given session.
func DeleteAttention(sessionID string) error {
	err := os.Remove(filepath.Join(AttentionDir(), sessionID+".json"))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// CleanupStaleAttention removes attention files whose transcript no longer
// exists or hasn't been modified within maxAge.
func CleanupStaleAttention(maxAge time.Duration) {
	dir := AttentionDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-maxAge)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			os.Remove(path)
			continue
		}
		var info AttentionInfo
		if err := json.Unmarshal(data, &info); err != nil {
			os.Remove(path)
			continue
		}
		stat, err := os.Stat(info.TranscriptPath)
		if err != nil || stat.ModTime().Before(cutoff) {
			os.Remove(path)
		}
	}
}
