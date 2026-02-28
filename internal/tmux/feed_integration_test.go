package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// JSONL helpers (same as claude package test helpers, re-declared since unexported)

func jsonlMetadata(cwd string) string {
	return fmt.Sprintf(`{"type":"user","cwd":"%s","sessionId":"test"}`, cwd)
}

func jsonlAssistant(stopReason string, blocks ...string) string {
	sr := "null"
	if stopReason != "" {
		sr = fmt.Sprintf(`"%s"`, stopReason)
	}
	content := "[" + strings.Join(blocks, ",") + "]"
	return fmt.Sprintf(`{"type":"assistant","isSidechain":false,"message":{"stop_reason":%s,"content":%s}}`,
		sr, content)
}

func jsonlUser(text string) string {
	return fmt.Sprintf(`{"type":"user","isSidechain":false,"message":{"role":"user","content":"%s"}}`, text)
}

func jsonlProgress(dataType string) string {
	return fmt.Sprintf(`{"type":"progress","data":{"type":"%s"}}`, dataType)
}

func blockText() string { return `{"type":"text"}` }
func blockTool(name string) string {
	return fmt.Sprintf(`{"type":"tool_use","name":"%s"}`, name)
}

func writeJSONLFile(t *testing.T, path string, lines ...string) {
	t.Helper()
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// getStatusLeft reads the status-left option from a tmux session.
func getStatusLeft(session string) (string, error) {
	out, err := exec.Command("tmux", "show-options", "-t", session, "-v", "status-left").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// TestFeedIntegration_IndependentStates verifies that two windows at
// different workspace paths show independent session states in the status bar.
func TestFeedIntegration_IndependentStates(t *testing.T) {
	requireTmux(t)

	const base = "att-feedint"
	cleanupSession(t, base)
	defer cleanupSession(t, base)

	feed := base + "-feed"
	defer cleanupSession(t, feed)

	// Create temp directories for two "projects"
	// Resolve symlinks because tmux resolves pane_current_path (macOS /var -> /private/var)
	dirA, _ := filepath.EvalSymlinks(t.TempDir())
	dirB, _ := filepath.EvalSymlinks(t.TempDir())

	// Create base tmux session with two windows
	_, err := NewSession(base, "proj-a", dirA, "sleep 300")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	_, err = NewWindow(base, "proj-b", dirB, "sleep 300")
	if err != nil {
		t.Fatalf("NewWindow: %v", err)
	}

	// Create grouped session for feed
	if err := NewGroupedSession(feed, base); err != nil {
		t.Fatalf("NewGroupedSession: %v", err)
	}

	// Set up single status line (hide default window list)
	exec.Command("tmux", "set-option", "-t", feed, "window-status-format", "").Run()
	exec.Command("tmux", "set-option", "-t", feed, "window-status-current-format", "").Run()
	exec.Command("tmux", "set-option", "-t", feed, "window-status-separator", "").Run()
	exec.Command("tmux", "set-option", "-t", feed, "status-left-length", "200").Run()

	// Set up fake HOME with JSONL files
	tmpHome := t.TempDir()
	projectsDir := filepath.Join(tmpHome, ".claude", "projects", "test-proj")
	if err := os.MkdirAll(projectsDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Both sessions start in Asking state
	writeJSONLFile(t, filepath.Join(projectsDir, "session-a.jsonl"),
		jsonlMetadata(dirA),
		jsonlAssistant("tool_use", blockTool("AskUserQuestion")),
	)
	writeJSONLFile(t, filepath.Join(projectsDir, "session-b.jsonl"),
		jsonlMetadata(dirB),
		jsonlAssistant("tool_use", blockTool("AskUserQuestion")),
	)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	// Create FeedController pointed at our test session
	fc := &FeedController{
		dismissed:   make(map[string]bool),
		baseSession: base,
		sessionName: feed,
	}

	// --- Phase 1: Both Asking ---
	fc.refresh()

	statusLine, err := getStatusLeft(feed)
	if err != nil {
		t.Fatalf("get status-left: %v", err)
	}
	t.Logf("Phase 1 (both asking): %s", statusLine)

	if !strings.Contains(statusLine, "proj-a Ask*") {
		t.Errorf("Phase 1: expected proj-a Ask*, got: %s", statusLine)
	}
	if !strings.Contains(statusLine, "proj-b Ask*") {
		t.Errorf("Phase 1: expected proj-b Ask*, got: %s", statusLine)
	}

	// --- Phase 2: User answers session A, it goes to Working ---
	writeJSONLFile(t, filepath.Join(projectsDir, "session-a.jsonl"),
		jsonlMetadata(dirA),
		jsonlAssistant("tool_use", blockTool("AskUserQuestion")),
		jsonlUser("here is my answer"),
	)

	fc.refresh()

	statusLine, err = getStatusLeft(feed)
	if err != nil {
		t.Fatalf("get status-left: %v", err)
	}
	t.Logf("Phase 2 (A answered -> working, B still asking): %s", statusLine)

	// Session A should no longer be in attention queue (it's Working now)
	// Session B should still show Ask*
	if !strings.Contains(statusLine, "proj-b Ask*") {
		t.Errorf("Phase 2: expected proj-b Ask*, got: %s", statusLine)
	}
	// proj-a should NOT show Ask* anymore
	if strings.Contains(statusLine, "proj-a Ask*") {
		t.Errorf("Phase 2: proj-a should not show Ask* anymore, got: %s", statusLine)
	}

	// --- Phase 3: Session A goes Idle (Claude finished) ---
	writeJSONLFile(t, filepath.Join(projectsDir, "session-a.jsonl"),
		jsonlMetadata(dirA),
		jsonlAssistant("tool_use", blockTool("AskUserQuestion")),
		jsonlUser("here is my answer"),
		jsonlAssistant("end_turn", blockText()),
	)

	fc.refresh()

	statusLine, err = getStatusLeft(feed)
	if err != nil {
		t.Fatalf("get status-left: %v", err)
	}
	t.Logf("Phase 3 (A idle, B still asking): %s", statusLine)

	// Both should be in attention queue now but with DIFFERENT states
	if !strings.Contains(statusLine, "proj-a Idle*") {
		t.Errorf("Phase 3: expected proj-a Idle*, got: %s", statusLine)
	}
	if !strings.Contains(statusLine, "proj-b Ask*") {
		t.Errorf("Phase 3: expected proj-b Ask*, got: %s", statusLine)
	}
}

// TestFeedIntegration_SamePathWithStaleSessions verifies that stale sessions
// from old Claude processes don't pollute the display. This is THE bug:
// 7 JSONL files at the same path, 5 old Idle ones, and the 2 most recent
// being Asking and Working. Windows should show Asking and Working, not Idle.
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

	// 5 old stale Idle sessions (dead Claude processes from earlier)
	for i := 0; i < 5; i++ {
		f := filepath.Join(projectsDir, fmt.Sprintf("old-%d.jsonl", i))
		writeJSONLFile(t, f,
			jsonlMetadata(sharedDir),
			jsonlAssistant("end_turn", blockText()),
		)
		old := now.Add(time.Duration(-(i+3)) * time.Hour)
		os.Chtimes(f, old, old)
	}

	// 2 ACTIVE sessions: one Asking, one Working
	askingFile := filepath.Join(projectsDir, "active-asking.jsonl")
	writeJSONLFile(t, askingFile,
		jsonlMetadata(sharedDir),
		jsonlAssistant("tool_use", blockTool("AskUserQuestion")),
	)
	os.Chtimes(askingFile, now.Add(-1*time.Minute), now.Add(-1*time.Minute))

	workingFile := filepath.Join(projectsDir, "active-working.jsonl")
	writeJSONLFile(t, workingFile,
		jsonlMetadata(sharedDir),
		jsonlUser("do this task"),
	)
	os.Chtimes(workingFile, now, now)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	fc := &FeedController{
		dismissed:   make(map[string]bool),
		baseSession: base,
		sessionName: feed,
	}

	fc.refresh()

	stateByWindow := assignSessionsToWindows(fc.allWindows, fc.discoveredSessions)
	t.Logf("Discovered %d sessions, assigned:", len(fc.discoveredSessions))
	for i, w := range fc.allWindows {
		s := stateByWindow[i]
		t.Logf("  window[%d] %s -> state=%v file=%s", i, w.Name, s.State, filepath.Base(s.SessionFile))
	}

	statusLine, _ := getStatusLeft(feed)
	t.Logf("Status bar: %s", statusLine)

	// THE KEY ASSERTION: stale Idle sessions must NOT leak into the display
	idleCount := strings.Count(statusLine, "Idle")
	if idleCount > 0 {
		t.Errorf("BUG: stale Idle sessions leaked into display (%d Idle entries): %s", idleCount, statusLine)
	}

	// We should see the active sessions
	if !strings.Contains(statusLine, "Ask*") {
		t.Errorf("expected Ask* for the active asking session, got: %s", statusLine)
	}
}

// TestFeedIntegration_DismissReappearsAfterWorking verifies that a dismissed
// session reappears when it finishes working. This reproduces the bug where
// stale sessions kept needsAttention permanently true, preventing dismissed
// paths from ever being cleared.
func TestFeedIntegration_DismissReappearsAfterWorking(t *testing.T) {
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

	// 3 old stale Idle sessions (will try to keep dismissed stuck)
	for i := 0; i < 3; i++ {
		f := filepath.Join(projectsDir, fmt.Sprintf("stale-%d.jsonl", i))
		writeJSONLFile(t, f,
			jsonlMetadata(sharedDir),
			jsonlAssistant("end_turn", blockText()),
		)
		old := now.Add(time.Duration(-(i+3)) * time.Hour)
		os.Chtimes(f, old, old)
	}

	// 2 active sessions, both Asking
	active1 := filepath.Join(projectsDir, "active-1.jsonl")
	writeJSONLFile(t, active1,
		jsonlMetadata(sharedDir),
		jsonlAssistant("tool_use", blockTool("AskUserQuestion")),
	)
	os.Chtimes(active1, now.Add(-30*time.Second), now.Add(-30*time.Second))

	active2 := filepath.Join(projectsDir, "active-2.jsonl")
	writeJSONLFile(t, active2,
		jsonlMetadata(sharedDir),
		jsonlAssistant("tool_use", blockTool("AskUserQuestion")),
	)
	os.Chtimes(active2, now, now)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	fc := &FeedController{
		dismissed:   make(map[string]bool),
		baseSession: base,
		sessionName: feed,
	}

	// --- Phase 1: Both Asking, verify they appear ---
	fc.refresh()
	statusLine, _ := getStatusLeft(feed)
	t.Logf("Phase 1 (both asking): %s", statusLine)

	askCount := strings.Count(statusLine, "Ask*")
	if askCount != 2 {
		t.Fatalf("Phase 1: expected 2 Ask* entries, got %d: %s", askCount, statusLine)
	}

	// --- Phase 2: User dismisses both ---
	fc.dismissAndAdvance() // dismiss first
	fc.dismissAndAdvance() // dismiss second
	statusLine, _ = getStatusLeft(feed)
	t.Logf("Phase 2 (both dismissed): %s", statusLine)

	if !strings.Contains(statusLine, "All clear") {
		t.Errorf("Phase 2: expected 'All clear' after dismissing both, got: %s", statusLine)
	}

	// --- Phase 3: Both sessions transition to Working ---
	writeJSONLFile(t, active1,
		jsonlMetadata(sharedDir),
		jsonlAssistant("tool_use", blockTool("AskUserQuestion")),
		jsonlUser("my answer 1"),
	)
	os.Chtimes(active1, now.Add(1*time.Second), now.Add(1*time.Second))

	writeJSONLFile(t, active2,
		jsonlMetadata(sharedDir),
		jsonlAssistant("tool_use", blockTool("AskUserQuestion")),
		jsonlUser("my answer 2"),
	)
	os.Chtimes(active2, now.Add(2*time.Second), now.Add(2*time.Second))

	fc.refresh()
	statusLine, _ = getStatusLeft(feed)
	t.Logf("Phase 3 (both working, dismissed should clear): %s", statusLine)
	t.Logf("Phase 3 dismissed set: %v", fc.dismissed)

	if len(fc.dismissed) > 0 {
		t.Errorf("Phase 3: dismissed set should be empty (both Working), got: %v", fc.dismissed)
	}

	// --- Phase 4: One finishes -> should reappear ---
	writeJSONLFile(t, active1,
		jsonlMetadata(sharedDir),
		jsonlAssistant("tool_use", blockTool("AskUserQuestion")),
		jsonlUser("my answer 1"),
		jsonlAssistant("end_turn", blockText()),
	)
	os.Chtimes(active1, now.Add(3*time.Second), now.Add(3*time.Second))

	fc.refresh()
	statusLine, _ = getStatusLeft(feed)
	t.Logf("Phase 4 (one finished -> idle, should reappear): %s", statusLine)

	if strings.Contains(statusLine, "All clear") {
		t.Errorf("Phase 4: BUG - still shows 'All clear', dismissed session never reappeared: %s", statusLine)
	}
	if !strings.Contains(statusLine, "Idle*") {
		t.Errorf("Phase 4: expected Idle* for finished session, got: %s", statusLine)
	}
}

// TestFeedIntegration_DismissOneSamePathKeepsOther verifies that dismissing
// one session at a shared workspace path does NOT dismiss the other.
// This reproduces the bug where dismissed was keyed by workspace path,
// causing both sessions to vanish when only one was dismissed.
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

	// Two sessions at the same path, both needing attention
	session1 := filepath.Join(projectsDir, "session-1.jsonl")
	writeJSONLFile(t, session1,
		jsonlMetadata(sharedDir),
		jsonlAssistant("tool_use", blockTool("AskUserQuestion")),
	)
	os.Chtimes(session1, now, now)

	session2 := filepath.Join(projectsDir, "session-2.jsonl")
	writeJSONLFile(t, session2,
		jsonlMetadata(sharedDir),
		jsonlAssistant("tool_use", blockTool("AskUserQuestion")),
	)
	os.Chtimes(session2, now.Add(-30*time.Second), now.Add(-30*time.Second))

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	fc := &FeedController{
		dismissed:   make(map[string]bool),
		baseSession: base,
		sessionName: feed,
	}

	// --- Phase 1: Both Asking ---
	fc.refresh()
	statusLine, _ := getStatusLeft(feed)
	t.Logf("Phase 1 (both asking): %s", statusLine)

	askCount := strings.Count(statusLine, "Ask*")
	if askCount != 2 {
		t.Fatalf("Phase 1: expected 2 Ask* entries, got %d: %s", askCount, statusLine)
	}
	if len(fc.attentionQueue) != 2 {
		t.Fatalf("Phase 1: expected 2 items in attention queue, got %d", len(fc.attentionQueue))
	}

	// --- Phase 2: Dismiss the first one only ---
	fc.dismissAndAdvance()
	statusLine, _ = getStatusLeft(feed)
	t.Logf("Phase 2 (dismissed one): %s", statusLine)
	t.Logf("Phase 2 dismissed set: %v", fc.dismissed)
	t.Logf("Phase 2 attention queue: %v", fc.attentionQueue)

	// THE KEY ASSERTION: the other session must still be visible
	if strings.Contains(statusLine, "All clear") {
		t.Errorf("Phase 2: BUG - shows 'All clear' after dismissing only one session: %s", statusLine)
	}
	if len(fc.attentionQueue) != 1 {
		t.Errorf("Phase 2: expected 1 item in attention queue, got %d", len(fc.attentionQueue))
	}
	if !strings.Contains(statusLine, "Ask*") {
		t.Errorf("Phase 2: expected remaining session to show Ask*, got: %s", statusLine)
	}
	if len(fc.dismissed) != 1 {
		t.Errorf("Phase 2: expected exactly 1 dismissed session, got %d: %v", len(fc.dismissed), fc.dismissed)
	}

	// --- Phase 3: Refresh should still show only the non-dismissed session ---
	fc.refresh()
	statusLine, _ = getStatusLeft(feed)
	t.Logf("Phase 3 (after refresh): %s", statusLine)

	if strings.Contains(statusLine, "All clear") {
		t.Errorf("Phase 3: BUG - shows 'All clear' after refresh, dismissed leaked to other session: %s", statusLine)
	}
	askCount = strings.Count(statusLine, "Ask*")
	if askCount != 1 {
		t.Errorf("Phase 3: expected exactly 1 Ask* (the non-dismissed session), got %d: %s", askCount, statusLine)
	}
}

// TestFeedIntegration_SamePathDifferentSessions verifies that two windows at
// the SAME workspace path show independent session states (the "tied together" bug).
func TestFeedIntegration_SamePathDifferentSessions(t *testing.T) {
	requireTmux(t)

	const base = "att-samepath"
	cleanupSession(t, base)
	defer cleanupSession(t, base)

	feed := base + "-feed"
	defer cleanupSession(t, feed)

	// Both windows point to the same directory
	// Resolve symlinks because tmux resolves pane_current_path (macOS /var -> /private/var)
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

	// JSONL files: two sessions at the same workspace path
	tmpHome := t.TempDir()
	projectsDir := filepath.Join(tmpHome, ".claude", "projects", "test-samepath")
	if err := os.MkdirAll(projectsDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Both sessions start Asking
	writeJSONLFile(t, filepath.Join(projectsDir, "session-1.jsonl"),
		jsonlMetadata(sharedDir),
		jsonlAssistant("tool_use", blockTool("AskUserQuestion")),
	)
	writeJSONLFile(t, filepath.Join(projectsDir, "session-2.jsonl"),
		jsonlMetadata(sharedDir),
		jsonlAssistant("tool_use", blockTool("AskUserQuestion")),
	)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	fc := &FeedController{
		dismissed:   make(map[string]bool),
		baseSession: base,
		sessionName: feed,
	}

	// --- Phase 1: Both Asking ---
	fc.refresh()

	// Log discovered sessions and assignment
	t.Logf("Discovered %d sessions:", len(fc.discoveredSessions))
	for _, s := range fc.discoveredSessions {
		t.Logf("  path=%s state=%v file=%s", s.WorkspacePath, s.State, filepath.Base(s.SessionFile))
	}

	stateByWindow := assignSessionsToWindows(fc.allWindows, fc.discoveredSessions)
	t.Logf("Window assignments:")
	for i, w := range fc.allWindows {
		s := stateByWindow[i]
		t.Logf("  window[%d] name=%s path=%s -> state=%v file=%s", i, w.Name, w.Path, s.State, filepath.Base(s.SessionFile))
	}

	statusLine, _ := getStatusLeft(feed)
	t.Logf("Phase 1 (both asking): %s", statusLine)

	if !strings.Contains(statusLine, "Ask*") {
		t.Errorf("Phase 1: expected at least one Ask*, got: %s", statusLine)
	}

	// Count Ask* occurrences
	askCount := strings.Count(statusLine, "Ask*")
	if askCount != 2 {
		t.Errorf("Phase 1: expected 2 Ask* entries, got %d in: %s", askCount, statusLine)
	}

	// --- Phase 2: Session 1 transitions to Idle, Session 2 still Asking ---
	writeJSONLFile(t, filepath.Join(projectsDir, "session-1.jsonl"),
		jsonlMetadata(sharedDir),
		jsonlAssistant("tool_use", blockTool("AskUserQuestion")),
		jsonlUser("my answer"),
		jsonlAssistant("end_turn", blockText()),
	)
	// Touch to ensure modtime is newer
	now := time.Now()
	os.Chtimes(filepath.Join(projectsDir, "session-1.jsonl"), now, now)

	fc.refresh()

	stateByWindow = assignSessionsToWindows(fc.allWindows, fc.discoveredSessions)
	t.Logf("Phase 2 window assignments:")
	for i, w := range fc.allWindows {
		s := stateByWindow[i]
		t.Logf("  window[%d] name=%s path=%s -> state=%v file=%s", i, w.Name, w.Path, s.State, filepath.Base(s.SessionFile))
	}

	statusLine, _ = getStatusLeft(feed)
	t.Logf("Phase 2 (session-1 idle, session-2 asking): %s", statusLine)

	// THE CRITICAL ASSERTION: they must show DIFFERENT states
	hasIdle := strings.Contains(statusLine, "Idle*")
	hasAsk := strings.Contains(statusLine, "Ask*")

	if !hasIdle {
		t.Errorf("Phase 2: expected one Idle* entry, got: %s", statusLine)
	}
	if !hasAsk {
		t.Errorf("Phase 2: expected one Ask* entry, got: %s", statusLine)
	}
	if strings.Count(statusLine, "Idle*") == 2 {
		t.Errorf("Phase 2: BUG - both windows show Idle* (tied together!): %s", statusLine)
	}
	if strings.Count(statusLine, "Ask*") == 2 {
		t.Errorf("Phase 2: BUG - both windows show Ask* (tied together!): %s", statusLine)
	}
}
