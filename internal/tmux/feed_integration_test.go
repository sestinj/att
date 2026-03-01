package tmux

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sestinj/att/internal/claude"
)

// JSONL helpers for creating session files with metadata

func jsonlMetadata(cwd string) string {
	return fmt.Sprintf(`{"type":"user","cwd":"%s","sessionId":"test"}`, cwd)
}

func jsonlAssistantEndTurn() string {
	return `{"type":"assistant","isSidechain":false,"message":{"stop_reason":"end_turn","content":[{"type":"text"}]}}`
}

func writeJSONLFile(t *testing.T, path string, lines ...string) {
	t.Helper()
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// writeAttention creates an attention file for a given session.
func writeAttention(t *testing.T, sessionID, transcriptPath string) {
	t.Helper()
	dir := claude.AttentionDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	info := claude.AttentionInfo{
		TranscriptPath:   transcriptPath,
		NotificationType: "notification",
		Timestamp:        time.Now(),
	}
	data, _ := json.Marshal(info)
	if err := os.WriteFile(filepath.Join(dir, sessionID+".json"), data, 0644); err != nil {
		t.Fatal(err)
	}
}

// removeAttention removes an attention file.
func removeAttention(t *testing.T, sessionID string) {
	t.Helper()
	path := filepath.Join(claude.AttentionDir(), sessionID+".json")
	os.Remove(path)
}

// getStatusLeft reads the status-left option from a tmux session.
func getStatusLeft(session string) (string, error) {
	out, err := exec.Command("tmux", "show-options", "-t", session, "-v", "status-left").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// TestFeedIntegration_AttentionDisplay verifies that sessions with attention
// files show "Attn*" and those without show no state indicator.
func TestFeedIntegration_AttentionDisplay(t *testing.T) {
	requireTmux(t)

	const base = "att-feedint"
	cleanupSession(t, base)
	defer cleanupSession(t, base)

	feed := base + "-feed"
	defer cleanupSession(t, feed)

	dirA, _ := filepath.EvalSymlinks(t.TempDir())
	dirB, _ := filepath.EvalSymlinks(t.TempDir())

	_, err := NewSession(base, "proj-a", dirA, "sleep 300")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	_, err = NewWindow(base, "proj-b", dirB, "sleep 300")
	if err != nil {
		t.Fatalf("NewWindow: %v", err)
	}
	if err := NewGroupedSession(feed, base); err != nil {
		t.Fatalf("NewGroupedSession: %v", err)
	}
	exec.Command("tmux", "set-option", "-t", feed, "window-status-format", "").Run()
	exec.Command("tmux", "set-option", "-t", feed, "window-status-current-format", "").Run()
	exec.Command("tmux", "set-option", "-t", feed, "window-status-separator", "").Run()
	exec.Command("tmux", "set-option", "-t", feed, "status-left-length", "200").Run()

	tmpHome := t.TempDir()
	projectsDir := filepath.Join(tmpHome, ".claude", "projects", "test-proj")
	if err := os.MkdirAll(projectsDir, 0755); err != nil {
		t.Fatal(err)
	}

	sessionA := filepath.Join(projectsDir, "session-a.jsonl")
	writeJSONLFile(t, sessionA, jsonlMetadata(dirA), jsonlAssistantEndTurn())
	sessionB := filepath.Join(projectsDir, "session-b.jsonl")
	writeJSONLFile(t, sessionB, jsonlMetadata(dirB), jsonlAssistantEndTurn())

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	// Write attention files for both sessions
	writeAttention(t, "session-a", sessionA)
	writeAttention(t, "session-b", sessionB)

	fc := &FeedController{
		dismissed:   make(map[string]bool),
		baseSession: base,
		sessionName: feed,
	}

	// --- Phase 1: Both need attention ---
	fc.refresh()
	statusLine, err := getStatusLeft(feed)
	if err != nil {
		t.Fatalf("get status-left: %v", err)
	}
	t.Logf("Phase 1 (both attention): %s", statusLine)

	attnCount := strings.Count(statusLine, "Attn*")
	if attnCount != 2 {
		t.Errorf("Phase 1: expected 2 Attn* entries, got %d: %s", attnCount, statusLine)
	}

	// --- Phase 2: Remove attention for session A ---
	removeAttention(t, "session-a")

	fc.refresh()
	statusLine, _ = getStatusLeft(feed)
	t.Logf("Phase 2 (A cleared, B still attention): %s", statusLine)

	attnCount = strings.Count(statusLine, "Attn*")
	if attnCount != 1 {
		t.Errorf("Phase 2: expected 1 Attn* entry, got %d: %s", attnCount, statusLine)
	}

	// --- Phase 3: Remove attention for B too ---
	removeAttention(t, "session-b")

	fc.refresh()
	statusLine, _ = getStatusLeft(feed)
	t.Logf("Phase 3 (both cleared): %s", statusLine)

	if strings.Contains(statusLine, "Attn*") {
		t.Errorf("Phase 3: expected no Attn* entries, got: %s", statusLine)
	}
}

// TestFeedIntegration_SamePathWithStaleSessions verifies that stale sessions
// from old Claude processes don't pollute the display.
func TestFeedIntegration_SamePathWithStaleSessions(t *testing.T) {
	requireTmux(t)

	const base = "att-stale"
	cleanupSession(t, base)
	defer cleanupSession(t, base)

	feed := base + "-feed"
	defer cleanupSession(t, feed)

	sharedDir, _ := filepath.EvalSymlinks(t.TempDir())

	_, err := NewSession(base, "sess-1", sharedDir, "sleep 300")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	_, err = NewWindow(base, "sess-2", sharedDir, "sleep 300")
	if err != nil {
		t.Fatalf("NewWindow: %v", err)
	}
	if err := NewGroupedSession(feed, base); err != nil {
		t.Fatalf("NewGroupedSession: %v", err)
	}
	exec.Command("tmux", "set-option", "-t", feed, "window-status-format", "").Run()
	exec.Command("tmux", "set-option", "-t", feed, "window-status-current-format", "").Run()
	exec.Command("tmux", "set-option", "-t", feed, "window-status-separator", "").Run()
	exec.Command("tmux", "set-option", "-t", feed, "status-left-length", "200").Run()

	tmpHome := t.TempDir()
	projectsDir := filepath.Join(tmpHome, ".claude", "projects", "test-stale")
	if err := os.MkdirAll(projectsDir, 0755); err != nil {
		t.Fatal(err)
	}

	now := time.Now()

	// 5 old stale sessions
	for i := 0; i < 5; i++ {
		f := filepath.Join(projectsDir, fmt.Sprintf("old-%d.jsonl", i))
		writeJSONLFile(t, f, jsonlMetadata(sharedDir), jsonlAssistantEndTurn())
		old := now.Add(time.Duration(-(i + 3)) * time.Hour)
		os.Chtimes(f, old, old)
	}

	// 2 active sessions with recent mod times
	activeFile1 := filepath.Join(projectsDir, "active-1.jsonl")
	writeJSONLFile(t, activeFile1, jsonlMetadata(sharedDir), jsonlAssistantEndTurn())
	os.Chtimes(activeFile1, now.Add(-1*time.Minute), now.Add(-1*time.Minute))

	activeFile2 := filepath.Join(projectsDir, "active-2.jsonl")
	writeJSONLFile(t, activeFile2, jsonlMetadata(sharedDir), jsonlAssistantEndTurn())
	os.Chtimes(activeFile2, now, now)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	// Only active sessions have attention files
	writeAttention(t, "active-1", activeFile1)

	fc := &FeedController{
		dismissed:   make(map[string]bool),
		baseSession: base,
		sessionName: feed,
	}

	fc.refresh()

	statusLine, _ := getStatusLeft(feed)
	t.Logf("Status bar: %s", statusLine)

	// Only active-1 should show attention. Stale sessions should not appear as attention.
	attnCount := strings.Count(statusLine, "Attn*")
	if attnCount != 1 {
		t.Errorf("expected 1 Attn* entry (only active-1 has attention file), got %d: %s", attnCount, statusLine)
	}
}

// TestFeedIntegration_DismissReappearsAfterAttentionClears verifies that
// a dismissed session reappears when its attention is cleared and then set again.
func TestFeedIntegration_DismissReappearsAfterAttentionClears(t *testing.T) {
	requireTmux(t)

	const base = "att-dismiss-reappear"
	cleanupSession(t, base)
	defer cleanupSession(t, base)

	feed := base + "-feed"
	defer cleanupSession(t, feed)

	sharedDir, _ := filepath.EvalSymlinks(t.TempDir())

	_, err := NewSession(base, "sess-1", sharedDir, "sleep 300")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	_, err = NewWindow(base, "sess-2", sharedDir, "sleep 300")
	if err != nil {
		t.Fatalf("NewWindow: %v", err)
	}
	if err := NewGroupedSession(feed, base); err != nil {
		t.Fatalf("NewGroupedSession: %v", err)
	}
	exec.Command("tmux", "set-option", "-t", feed, "window-status-format", "").Run()
	exec.Command("tmux", "set-option", "-t", feed, "window-status-current-format", "").Run()
	exec.Command("tmux", "set-option", "-t", feed, "window-status-separator", "").Run()
	exec.Command("tmux", "set-option", "-t", feed, "status-left-length", "200").Run()

	tmpHome := t.TempDir()
	projectsDir := filepath.Join(tmpHome, ".claude", "projects", "test-dismiss")
	if err := os.MkdirAll(projectsDir, 0755); err != nil {
		t.Fatal(err)
	}

	now := time.Now()

	s1 := filepath.Join(projectsDir, "active-1.jsonl")
	writeJSONLFile(t, s1, jsonlMetadata(sharedDir), jsonlAssistantEndTurn())
	os.Chtimes(s1, now.Add(-30*time.Second), now.Add(-30*time.Second))

	s2 := filepath.Join(projectsDir, "active-2.jsonl")
	writeJSONLFile(t, s2, jsonlMetadata(sharedDir), jsonlAssistantEndTurn())
	os.Chtimes(s2, now, now)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	// Both sessions need attention
	writeAttention(t, "active-1", s1)
	writeAttention(t, "active-2", s2)

	fc := &FeedController{
		dismissed:   make(map[string]bool),
		baseSession: base,
		sessionName: feed,
	}

	// --- Phase 1: Both have attention ---
	fc.refresh()
	statusLine, _ := getStatusLeft(feed)
	t.Logf("Phase 1 (both attention): %s", statusLine)
	if strings.Count(statusLine, "Attn*") != 2 {
		t.Fatalf("Phase 1: expected 2 Attn* entries, got: %s", statusLine)
	}

	// --- Phase 2: Dismiss both ---
	fc.dismissAndAdvance()
	fc.dismissAndAdvance()
	statusLine, _ = getStatusLeft(feed)
	t.Logf("Phase 2 (both dismissed): %s", statusLine)
	if !strings.Contains(statusLine, "All clear") {
		t.Errorf("Phase 2: expected 'All clear': %s", statusLine)
	}

	// --- Phase 3: Attention clears (user submitted prompts) ---
	removeAttention(t, "active-1")
	removeAttention(t, "active-2")

	fc.refresh()
	t.Logf("Phase 3 dismissed set: %v", fc.dismissed)
	if len(fc.dismissed) > 0 {
		t.Errorf("Phase 3: dismissed set should be empty (attention cleared), got: %v", fc.dismissed)
	}

	// --- Phase 4: One gets new attention (Claude finished) ---
	writeAttention(t, "active-1", s1)

	fc.refresh()
	statusLine, _ = getStatusLeft(feed)
	t.Logf("Phase 4 (one re-attention): %s", statusLine)

	if strings.Contains(statusLine, "All clear") {
		t.Errorf("Phase 4: BUG - still shows 'All clear': %s", statusLine)
	}
	if !strings.Contains(statusLine, "Attn*") {
		t.Errorf("Phase 4: expected Attn* for re-appearing session: %s", statusLine)
	}
}

// TestFeedIntegration_DismissOneSamePathKeepsOther verifies that dismissing
// one session at a shared workspace path does NOT dismiss the other.
func TestFeedIntegration_DismissOneSamePathKeepsOther(t *testing.T) {
	requireTmux(t)

	const base = "att-dismiss-one"
	cleanupSession(t, base)
	defer cleanupSession(t, base)

	feed := base + "-feed"
	defer cleanupSession(t, feed)

	sharedDir, _ := filepath.EvalSymlinks(t.TempDir())

	_, err := NewSession(base, "sess-1", sharedDir, "sleep 300")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	_, err = NewWindow(base, "sess-2", sharedDir, "sleep 300")
	if err != nil {
		t.Fatalf("NewWindow: %v", err)
	}
	if err := NewGroupedSession(feed, base); err != nil {
		t.Fatalf("NewGroupedSession: %v", err)
	}
	exec.Command("tmux", "set-option", "-t", feed, "window-status-format", "").Run()
	exec.Command("tmux", "set-option", "-t", feed, "window-status-current-format", "").Run()
	exec.Command("tmux", "set-option", "-t", feed, "window-status-separator", "").Run()
	exec.Command("tmux", "set-option", "-t", feed, "status-left-length", "200").Run()

	tmpHome := t.TempDir()
	projectsDir := filepath.Join(tmpHome, ".claude", "projects", "test-dismiss-one")
	if err := os.MkdirAll(projectsDir, 0755); err != nil {
		t.Fatal(err)
	}

	now := time.Now()

	s1 := filepath.Join(projectsDir, "session-1.jsonl")
	writeJSONLFile(t, s1, jsonlMetadata(sharedDir), jsonlAssistantEndTurn())
	os.Chtimes(s1, now, now)

	s2 := filepath.Join(projectsDir, "session-2.jsonl")
	writeJSONLFile(t, s2, jsonlMetadata(sharedDir), jsonlAssistantEndTurn())
	os.Chtimes(s2, now.Add(-30*time.Second), now.Add(-30*time.Second))

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	writeAttention(t, "session-1", s1)
	writeAttention(t, "session-2", s2)

	fc := &FeedController{
		dismissed:   make(map[string]bool),
		baseSession: base,
		sessionName: feed,
	}

	// --- Phase 1: Both attention ---
	fc.refresh()
	statusLine, _ := getStatusLeft(feed)
	t.Logf("Phase 1 (both attention): %s", statusLine)
	if strings.Count(statusLine, "Attn*") != 2 {
		t.Fatalf("Phase 1: expected 2 Attn* entries, got: %s", statusLine)
	}

	// --- Phase 2: Dismiss first ---
	fc.dismissAndAdvance()
	statusLine, _ = getStatusLeft(feed)
	t.Logf("Phase 2 (dismissed one): %s", statusLine)

	if strings.Contains(statusLine, "All clear") {
		t.Errorf("Phase 2: BUG - 'All clear' after dismissing only one: %s", statusLine)
	}
	if len(fc.attentionQueue) != 1 {
		t.Errorf("Phase 2: expected 1 item in attention queue, got %d", len(fc.attentionQueue))
	}
	if !strings.Contains(statusLine, "Attn*") {
		t.Errorf("Phase 2: expected remaining session to show Attn*: %s", statusLine)
	}

	// --- Phase 3: Refresh should still show only the non-dismissed session ---
	fc.refresh()
	statusLine, _ = getStatusLeft(feed)
	t.Logf("Phase 3 (after refresh): %s", statusLine)

	if strings.Contains(statusLine, "All clear") {
		t.Errorf("Phase 3: BUG - 'All clear' after refresh: %s", statusLine)
	}
	attnCount := strings.Count(statusLine, "Attn*")
	if attnCount != 1 {
		t.Errorf("Phase 3: expected 1 Attn* (non-dismissed), got %d: %s", attnCount, statusLine)
	}
}
