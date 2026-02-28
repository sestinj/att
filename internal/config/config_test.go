package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_MissingFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := Load()
	if cfg.Command != "" || len(cfg.Projects) != 0 || cfg.DirCommand != "" {
		t.Errorf("expected zero-value Config for missing file, got %+v", cfg)
	}
}

func TestLoad_ValidFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir := filepath.Join(home, ".config", "att")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}

	data := `{
		"command": "claude --dangerously-skip-permissions",
		"projects": ["/home/user/repo1", "/home/user/repo2"],
		"dir_command": "wt-cycle next"
	}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := Load()
	if cfg.Command != "claude --dangerously-skip-permissions" {
		t.Errorf("Command = %q, want %q", cfg.Command, "claude --dangerously-skip-permissions")
	}
	if len(cfg.Projects) != 2 {
		t.Fatalf("Projects len = %d, want 2", len(cfg.Projects))
	}
	if cfg.Projects[0] != "/home/user/repo1" {
		t.Errorf("Projects[0] = %q, want %q", cfg.Projects[0], "/home/user/repo1")
	}
	if cfg.DirCommand != "wt-cycle next" {
		t.Errorf("DirCommand = %q, want %q", cfg.DirCommand, "wt-cycle next")
	}
}

func TestLoad_MalformedJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir := filepath.Join(home, ".config", "att")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{invalid"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := Load()
	if cfg.Command != "" || len(cfg.Projects) != 0 || cfg.DirCommand != "" {
		t.Errorf("expected zero-value Config for malformed JSON, got %+v", cfg)
	}
}

func TestLoad_EmptyFields(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir := filepath.Join(home, ".config", "att")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := Load()
	if cfg.Command != "" {
		t.Errorf("Command = %q, want empty", cfg.Command)
	}
	if len(cfg.Projects) != 0 {
		t.Errorf("Projects len = %d, want 0", len(cfg.Projects))
	}
	if cfg.DirCommand != "" {
		t.Errorf("DirCommand = %q, want empty", cfg.DirCommand)
	}
}
