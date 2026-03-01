package config

import (
	"os"
	"os/exec"
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

func TestResolveDir_NoDirCommand_GitRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	// Create a temp git repo (resolve symlinks for macOS /var -> /private/var)
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	sub := filepath.Join(dir, "sub")
	os.MkdirAll(sub, 0755)
	exec.Command("git", "-C", dir, "init").Run()

	// ResolveDir from a subdirectory should return the git root
	resolved, err := ResolveDir("", sub)
	if err != nil {
		t.Fatalf("ResolveDir: %v", err)
	}
	if resolved != dir {
		t.Errorf("ResolveDir = %q, want git root %q", resolved, dir)
	}
}

func TestResolveDir_NoDirCommand_NoGit(t *testing.T) {
	dir := t.TempDir()

	// Outside a git repo, should return dir unchanged
	resolved, err := ResolveDir("", dir)
	if err != nil {
		t.Fatalf("ResolveDir: %v", err)
	}
	if resolved != dir {
		t.Errorf("ResolveDir = %q, want %q", resolved, dir)
	}
}

func TestResolveDir_WithDirCommand(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	os.MkdirAll(target, 0755)

	// dir_command that echoes a fixed path
	resolved, err := ResolveDir("echo "+target, dir)
	if err != nil {
		t.Fatalf("ResolveDir: %v", err)
	}
	if resolved != target {
		t.Errorf("ResolveDir = %q, want %q", resolved, target)
	}
}
