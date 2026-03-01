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

// feedE2E drives the real FeedController.Run() event loop and verifies
// what the user actually sees via tmux status bar reads and FIFO commands.
type feedE2E struct {
	t       *testing.T
	base    string // base tmux session name
	feed    string // feed's grouped session name
	fifo    string // FIFO path for sending commands
	projDir string // directory for JSONL session files
	cwd     string // workspace path for windows
	home    string // temp HOME directory
	done    chan error
}

func newFeedE2E(t *testing.T, name string) *feedE2E {
	t.Helper()
	requireTmux(t)

	base := "att-e2e-" + name
	cleanupSession(t, base)
	t.Cleanup(func() { cleanupSession(t, base) })

	cwd, _ := filepath.EvalSymlinks(t.TempDir())
	home := t.TempDir()
	projDir := filepath.Join(home, ".claude", "projects", "e2e-"+name)
	if err := os.MkdirAll(projDir, 0755); err != nil {
		t.Fatal(err)
	}

	_, err := NewSession(base, "sess-1", cwd, "sleep 300")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	_, err = NewWindow(base, "sess-2", cwd, "sleep 300")
	if err != nil {
		t.Fatalf("NewWindow: %v", err)
	}

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", home)
	t.Cleanup(func() { os.Setenv("HOME", origHome) })

	feed := base + "-feed"
	fifo := fmt.Sprintf("/tmp/%s.fifo", feed)
	t.Cleanup(func() { cleanupSession(t, feed) })

	return &feedE2E{
		t:       t,
		base:    base,
		feed:    feed,
		fifo:    fifo,
		projDir: projDir,
		cwd:     cwd,
		home:    home,
		done:    make(chan error, 1),
	}
}

func (e *feedE2E) start() {
	e.t.Helper()

	fc := &FeedController{
		dismissed:       make(map[string]bool),
		baseSession:     e.base,
		sessionName:     e.feed,
		fifoPath:        e.fifo,
		noAttach:        true,
		refreshInterval: 200 * time.Millisecond,
		command:         "sleep 300",
	}

	go func() {
		e.done <- fc.Run()
	}()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(e.fifo); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, err := os.Stat(e.fifo); err != nil {
		e.t.Fatal("feed did not start within 5 seconds")
	}

	for time.Now().Before(deadline) {
		if bar := e.sessionBar(); bar != "" {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	e.t.Fatal("feed status bar not populated within 5 seconds")
}

func (e *feedE2E) stop() {
	e.t.Helper()
	e.send("quit")
	select {
	case err := <-e.done:
		if err != nil {
			e.t.Errorf("Run() returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		e.t.Error("Run() did not stop within 5 seconds")
	}
}

func (e *feedE2E) send(cmd string) {
	e.t.Helper()
	f, err := os.OpenFile(e.fifo, os.O_WRONLY, 0)
	if err != nil {
		e.t.Fatalf("open fifo: %v", err)
	}
	defer f.Close()
	fmt.Fprintln(f, cmd)
	time.Sleep(300 * time.Millisecond)
}

func (e *feedE2E) sessionBar() string {
	out, err := exec.Command("tmux", "show-options", "-t", e.feed, "-v", "status-left").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (e *feedE2E) statusRight() string {
	out, err := exec.Command("tmux", "show-options", "-t", e.feed, "-v", "status-right").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (e *feedE2E) waitRefresh() {
	time.Sleep(400 * time.Millisecond)
}

func (e *feedE2E) writeSession(name string, lines ...string) {
	e.t.Helper()
	content := strings.Join(lines, "\n") + "\n"
	path := filepath.Join(e.projDir, name+".jsonl")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		e.t.Fatal(err)
	}
}

func (e *feedE2E) touchSession(name string, t time.Time) {
	path := filepath.Join(e.projDir, name+".jsonl")
	os.Chtimes(path, t, t)
}

func (e *feedE2E) metadata() string {
	return fmt.Sprintf(`{"type":"user","cwd":"%s","sessionId":"test"}`, e.cwd)
}

// writeAttn creates an attention file for the named session.
func (e *feedE2E) writeAttn(sessionName string) {
	e.t.Helper()
	dir := filepath.Join(e.home, ".config", "att", "attention")
	os.MkdirAll(dir, 0755)
	transcript := filepath.Join(e.projDir, sessionName+".jsonl")
	info := claude.AttentionInfo{
		TranscriptPath:   transcript,
		NotificationType: "notification",
		Timestamp:        time.Now(),
	}
	data, _ := json.Marshal(info)
	os.WriteFile(filepath.Join(dir, sessionName+".json"), data, 0644)
}

// removeAttn removes the attention file for the named session.
func (e *feedE2E) removeAttn(sessionName string) {
	dir := filepath.Join(e.home, ".config", "att", "attention")
	os.Remove(filepath.Join(dir, sessionName+".json"))
}

// TestE2E_DismissOneSamePathKeepsOther runs the real event loop and verifies
// that dismissing one session at a shared path doesn't hide the other.
func TestE2E_DismissOneSamePathKeepsOther(t *testing.T) {
	e := newFeedE2E(t, "dismiss-one")
	now := time.Now()

	e.writeSession("s1", e.metadata(), jsonlAssistantEndTurn())
	e.touchSession("s1", now)
	e.writeSession("s2", e.metadata(), jsonlAssistantEndTurn())
	e.touchSession("s2", now.Add(-10*time.Second))

	// Both need attention
	e.writeAttn("s1")
	e.writeAttn("s2")

	e.start()
	defer e.stop()

	bar := e.sessionBar()
	t.Logf("Phase 1 (both attention): %s", bar)
	attnCount := strings.Count(bar, "Attn*")
	if attnCount != 2 {
		t.Fatalf("Phase 1: expected 2 Attn* entries, got %d: %s", attnCount, bar)
	}

	// Dismiss the first one
	e.send("dismiss")
	bar = e.sessionBar()
	t.Logf("Phase 2 (dismissed one): %s", bar)
	if strings.Contains(bar, "All clear") {
		t.Errorf("Phase 2: BUG - 'All clear' after dismissing only one: %s", bar)
	}
	attnCount = strings.Count(bar, "Attn*")
	if attnCount != 1 {
		t.Errorf("Phase 2: expected exactly 1 Attn*, got %d: %s", attnCount, bar)
	}

	// After refresh -- non-dismissed session should still be there
	e.waitRefresh()
	bar = e.sessionBar()
	t.Logf("Phase 3 (after refresh): %s", bar)
	if strings.Contains(bar, "All clear") {
		t.Errorf("Phase 3: BUG - 'All clear' after refresh: %s", bar)
	}
}

// TestE2E_DismissReappearsAfterAttentionClears runs the full lifecycle:
// both attention -> dismiss both -> attention clears -> new attention -> reappears.
func TestE2E_DismissReappearsAfterAttentionClears(t *testing.T) {
	e := newFeedE2E(t, "reappear")
	now := time.Now()

	e.writeSession("s1", e.metadata(), jsonlAssistantEndTurn())
	e.touchSession("s1", now)
	e.writeSession("s2", e.metadata(), jsonlAssistantEndTurn())
	e.touchSession("s2", now.Add(-5*time.Second))

	e.writeAttn("s1")
	e.writeAttn("s2")

	e.start()
	defer e.stop()

	// Both attention
	bar := e.sessionBar()
	t.Logf("Phase 1: %s", bar)
	if strings.Count(bar, "Attn*") != 2 {
		t.Fatalf("Phase 1: expected 2 Attn*, got: %s", bar)
	}

	// Dismiss first
	e.send("dismiss")
	// Dismiss second (send waits 300ms so the event loop processes the first)
	e.send("dismiss")
	bar = e.sessionBar()
	t.Logf("Phase 2 (both dismissed): %s", bar)
	if !strings.Contains(bar, "All clear") {
		// May need another refresh cycle to see All clear
		e.waitRefresh()
		bar = e.sessionBar()
		t.Logf("Phase 2 (after refresh): %s", bar)
		if !strings.Contains(bar, "All clear") {
			t.Errorf("Phase 2: expected 'All clear': %s", bar)
		}
	}

	// Attention clears (user submitted prompts)
	e.removeAttn("s1")
	e.removeAttn("s2")

	e.waitRefresh()
	bar = e.sessionBar()
	t.Logf("Phase 3 (attention cleared): %s", bar)
	if !strings.Contains(bar, "All clear") {
		t.Errorf("Phase 3: expected 'All clear' (working): %s", bar)
	}

	// One gets new attention (Claude finished a task)
	e.writeAttn("s1")

	e.waitRefresh()
	bar = e.sessionBar()
	t.Logf("Phase 4 (one re-attention): %s", bar)

	if strings.Contains(bar, "All clear") {
		t.Errorf("Phase 4: BUG - 'All clear' but session has attention: %s", bar)
	}
	if !strings.Contains(bar, "Attn*") {
		t.Errorf("Phase 4: expected Attn* for re-appearing session: %s", bar)
	}
}

// TestE2E_NavigationAndHighlight verifies M-] and M-[ move the cursor.
func TestE2E_NavigationAndHighlight(t *testing.T) {
	e := newFeedE2E(t, "nav")
	now := time.Now()

	e.writeSession("s1", e.metadata(), jsonlAssistantEndTurn())
	e.touchSession("s1", now)
	e.writeSession("s2", e.metadata(), jsonlAssistantEndTurn())
	e.touchSession("s2", now.Add(-5*time.Second))

	e.writeAttn("s1")
	e.writeAttn("s2")

	e.start()
	defer e.stop()

	bar := e.sessionBar()
	t.Logf("Initial: %s", bar)
	if !strings.Contains(bar, "#[reverse]") {
		t.Errorf("expected a highlighted entry: %s", bar)
	}

	e.send("next")
	bar2 := e.sessionBar()
	t.Logf("After next: %s", bar2)
	if bar == bar2 {
		t.Errorf("highlight should have moved after next")
	}

	e.send("prev")
	e.waitRefresh()
	bar3 := e.sessionBar()
	t.Logf("After prev: %s", bar3)
	if bar3 != bar {
		t.Errorf("prev should return to original state, got: %s", bar3)
	}
}

// TestE2E_NewSessionFocus verifies that M-n (new session) creates a window
// and the cursor moves to it.
func TestE2E_NewSessionFocus(t *testing.T) {
	e := newFeedE2E(t, "new-focus")
	e.start()
	defer e.stop()

	status := e.statusRight()
	t.Logf("Initial: %s", status)
	if !strings.Contains(status, "[1/2]") {
		t.Fatalf("expected [1/2], got: %s", status)
	}

	e.send(fmt.Sprintf("new %s", e.cwd))

	status = e.statusRight()
	t.Logf("After new: %s", status)
	if !strings.Contains(status, "/3]") {
		t.Errorf("expected 3 windows, got: %s", status)
	}
	if !strings.Contains(status, "[3/3]") {
		t.Errorf("expected cursor on new window [3/3], got: %s", status)
	}
}

// TestE2E_PlaceholderCleanup verifies that the _init placeholder window
// is cleaned up once a real window is created via "new".
func TestE2E_PlaceholderCleanup(t *testing.T) {
	requireTmux(t)

	base := "att-e2e-placeholder"
	cleanupSession(t, base)
	t.Cleanup(func() { cleanupSession(t, base) })

	cwd, _ := filepath.EvalSymlinks(t.TempDir())
	home := t.TempDir()
	projDir := filepath.Join(home, ".claude", "projects", "e2e-placeholder")
	if err := os.MkdirAll(projDir, 0755); err != nil {
		t.Fatal(err)
	}

	_, err := NewSession(base, "_init", cwd, "tail -f /dev/null")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	windows, err := ListWindows(base)
	if err != nil {
		t.Fatalf("ListWindows: %v", err)
	}
	if len(windows) != 1 || windows[0].Name != "_init" {
		t.Fatalf("expected 1 _init window, got: %v", windows)
	}

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", home)
	t.Cleanup(func() { os.Setenv("HOME", origHome) })

	feed := base + "-feed"
	fifo := fmt.Sprintf("/tmp/%s.fifo", feed)
	t.Cleanup(func() { cleanupSession(t, feed) })

	e := &feedE2E{
		t:       t,
		base:    base,
		feed:    feed,
		fifo:    fifo,
		projDir: projDir,
		cwd:     cwd,
		home:    home,
		done:    make(chan error, 1),
	}

	fc := &FeedController{
		dismissed:       make(map[string]bool),
		baseSession:     base,
		sessionName:     feed,
		fifoPath:        fifo,
		noAttach:        true,
		refreshInterval: 200 * time.Millisecond,
		command:         "sleep 300",
	}

	go func() {
		e.done <- fc.Run()
	}()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(fifo); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, err := os.Stat(fifo); err != nil {
		t.Fatal("feed did not start within 5 seconds")
	}
	for time.Now().Before(deadline) {
		if bar := e.statusRight(); bar != "" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	status := e.statusRight()
	t.Logf("Before new: %s", status)
	if !strings.Contains(status, "[1/1]") {
		t.Fatalf("expected [1/1] with placeholder, got: %s", status)
	}

	e.send(fmt.Sprintf("new %s", cwd))
	e.waitRefresh()

	status = e.statusRight()
	t.Logf("After new + refresh: %s", status)

	windows, err = ListWindows(base)
	if err != nil {
		t.Fatalf("ListWindows: %v", err)
	}
	for _, w := range windows {
		if w.Name == "_init" {
			t.Errorf("_init placeholder should have been cleaned up, windows: %v", windows)
		}
	}
	if len(windows) != 1 {
		t.Errorf("expected 1 window after cleanup, got %d: %v", len(windows), windows)
	}

	e.send("quit")
	select {
	case err := <-e.done:
		if err != nil {
			t.Errorf("Run() returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("Run() did not stop within 5 seconds")
	}
}
