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

// feedE2E drives the real FeedController.Run() event loop and verifies
// what the user actually sees via tmux status bar reads and FIFO commands.
type feedE2E struct {
	t       *testing.T
	base    string // base tmux session name
	feed    string // feed's grouped session name
	fifo    string // FIFO path for sending commands
	projDir string // directory for JSONL session files
	cwd     string // workspace path for windows
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

	// Create base tmux session with 2 windows at the same path
	_, err := NewSession(base, "sess-1", cwd, "sleep 300")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	_, err = NewWindow(base, "sess-2", cwd, "sleep 300")
	if err != nil {
		t.Fatalf("NewWindow: %v", err)
	}

	// Override HOME so DiscoverSessions finds our JSONL files
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
		done:    make(chan error, 1),
	}
}

// start launches Run() in a background goroutine and waits for it to be ready.
func (e *feedE2E) start() {
	e.t.Helper()

	fc := &FeedController{
		dismissed:       make(map[string]bool),
		baseSession:     e.base,
		sessionName:     e.feed,
		fifoPath:        e.fifo,
		noAttach:        true,
		refreshInterval: 200 * time.Millisecond, // fast refresh for tests
	}

	go func() {
		e.done <- fc.Run()
	}()

	// Wait for FIFO to be created (indicates feed is running)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(e.fifo); err == nil {
			// Wait for initial refresh to complete
			time.Sleep(300 * time.Millisecond)
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	e.t.Fatal("feed did not start within 5 seconds")
}

// stop sends the quit command and waits for Run() to return.
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

// send writes a command to the FIFO (simulating a keypress).
func (e *feedE2E) send(cmd string) {
	e.t.Helper()
	f, err := os.OpenFile(e.fifo, os.O_WRONLY, 0)
	if err != nil {
		e.t.Fatalf("open fifo: %v", err)
	}
	defer f.Close()
	fmt.Fprintln(f, cmd)
	time.Sleep(100 * time.Millisecond) // let the event loop process
}

// sessionBar reads status-left -- the session entries the user sees.
func (e *feedE2E) sessionBar() string {
	out, err := exec.Command("tmux", "show-options", "-t", e.feed, "-v", "status-left").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// statusRight reads the status-right text (cursor position, attention count).
func (e *feedE2E) statusRight() string {
	out, err := exec.Command("tmux", "show-options", "-t", e.feed, "-v", "status-right").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// waitRefresh waits for the feed's ticker to fire and process a refresh.
func (e *feedE2E) waitRefresh() {
	// refreshInterval is 200ms, so 400ms is enough for at least one tick
	time.Sleep(400 * time.Millisecond)
}

// writeSession creates or overwrites a JSONL session file.
func (e *feedE2E) writeSession(name string, lines ...string) {
	e.t.Helper()
	content := strings.Join(lines, "\n") + "\n"
	path := filepath.Join(e.projDir, name+".jsonl")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		e.t.Fatal(err)
	}
}

// touchSession sets the modification time of a session file.
func (e *feedE2E) touchSession(name string, t time.Time) {
	path := filepath.Join(e.projDir, name+".jsonl")
	os.Chtimes(path, t, t)
}

// metadata returns a JSONL metadata line with the workspace path.
func (e *feedE2E) metadata() string {
	return fmt.Sprintf(`{"type":"user","cwd":"%s","sessionId":"test"}`, e.cwd)
}

// TestE2E_DismissOneSamePathKeepsOther runs the real event loop and verifies
// that dismissing one session at a shared path doesn't hide the other.
func TestE2E_DismissOneSamePathKeepsOther(t *testing.T) {
	e := newFeedE2E(t, "dismiss-one")
	now := time.Now()

	// Two sessions at the same path, both Asking
	e.writeSession("s1",
		e.metadata(),
		jsonlAssistant("tool_use", blockTool("AskUserQuestion")),
	)
	e.touchSession("s1", now)

	e.writeSession("s2",
		e.metadata(),
		jsonlAssistant("tool_use", blockTool("AskUserQuestion")),
	)
	e.touchSession("s2", now.Add(-10*time.Second))

	e.start()
	defer e.stop()

	// Phase 1: Both should be visible
	bar := e.sessionBar()
	t.Logf("Phase 1 (both asking): %s", bar)

	askCount := strings.Count(bar, "Ask*")
	if askCount != 2 {
		t.Fatalf("Phase 1: expected 2 Ask* entries, got %d: %s", askCount, bar)
	}

	// Phase 2: Dismiss the first one
	e.send("dismiss")
	bar = e.sessionBar()
	t.Logf("Phase 2 (dismissed one): %s", bar)

	if strings.Contains(bar, "All clear") {
		t.Errorf("Phase 2: BUG - 'All clear' after dismissing only one: %s", bar)
	}
	askCount = strings.Count(bar, "Ask*")
	if askCount != 1 {
		t.Errorf("Phase 2: expected exactly 1 Ask*, got %d: %s", askCount, bar)
	}

	// Phase 3: Refresh -- the non-dismissed session should still be there
	e.waitRefresh()
	bar = e.sessionBar()
	t.Logf("Phase 3 (after refresh): %s", bar)

	if strings.Contains(bar, "All clear") {
		t.Errorf("Phase 3: BUG - 'All clear' after refresh: %s", bar)
	}
	askCount = strings.Count(bar, "Ask*")
	if askCount != 1 {
		t.Errorf("Phase 3: expected 1 Ask* after refresh, got %d: %s", askCount, bar)
	}
}

// TestE2E_DismissReappearsAfterWorking runs the full lifecycle:
// both Asking -> dismiss both -> transition to Working -> one finishes -> reappears.
func TestE2E_DismissReappearsAfterWorking(t *testing.T) {
	e := newFeedE2E(t, "reappear")
	now := time.Now()

	e.writeSession("s1",
		e.metadata(),
		jsonlAssistant("tool_use", blockTool("AskUserQuestion")),
	)
	e.touchSession("s1", now)

	e.writeSession("s2",
		e.metadata(),
		jsonlAssistant("tool_use", blockTool("AskUserQuestion")),
	)
	e.touchSession("s2", now.Add(-5*time.Second))

	e.start()
	defer e.stop()

	// Both asking
	bar := e.sessionBar()
	t.Logf("Phase 1: %s", bar)
	if strings.Count(bar, "Ask*") != 2 {
		t.Fatalf("Phase 1: expected 2 Ask*, got: %s", bar)
	}

	// Dismiss both
	e.send("dismiss")
	e.send("dismiss")
	bar = e.sessionBar()
	t.Logf("Phase 2 (both dismissed): %s", bar)
	if !strings.Contains(bar, "All clear") {
		t.Errorf("Phase 2: expected 'All clear': %s", bar)
	}

	// Transition both to Working
	e.writeSession("s1",
		e.metadata(),
		jsonlAssistant("tool_use", blockTool("AskUserQuestion")),
		jsonlUser("answer 1"),
	)
	e.writeSession("s2",
		e.metadata(),
		jsonlAssistant("tool_use", blockTool("AskUserQuestion")),
		jsonlUser("answer 2"),
	)

	e.waitRefresh()
	bar = e.sessionBar()
	t.Logf("Phase 3 (both working): %s", bar)
	if !strings.Contains(bar, "All clear") {
		t.Errorf("Phase 3: expected 'All clear' (working): %s", bar)
	}

	// One finishes (goes Idle)
	e.writeSession("s1",
		e.metadata(),
		jsonlAssistant("tool_use", blockTool("AskUserQuestion")),
		jsonlUser("answer 1"),
		jsonlAssistant("end_turn", blockText()),
	)

	e.waitRefresh()
	bar = e.sessionBar()
	t.Logf("Phase 4 (one idle): %s", bar)

	if strings.Contains(bar, "All clear") {
		t.Errorf("Phase 4: BUG - 'All clear' but session finished: %s", bar)
	}
	if !strings.Contains(bar, "Idle*") {
		t.Errorf("Phase 4: expected Idle* for finished session: %s", bar)
	}
}

// TestE2E_StaleWorkingShowsPerm verifies that a session stuck in Working
// (no JSONL writes for >10s) is detected as ToolPermission.
func TestE2E_StaleWorkingShowsPerm(t *testing.T) {
	e := newFeedE2E(t, "stale-perm")

	// One session with stale Working state (old modtime)
	e.writeSession("s1",
		e.metadata(),
		jsonlUser("do this task"),
	)
	e.touchSession("s1", time.Now().Add(-staleWorkingThreshold-2*time.Second))

	// One session that's actively Working (recent modtime)
	e.writeSession("s2",
		e.metadata(),
		jsonlUser("do that task"),
	)

	e.start()
	defer e.stop()

	bar := e.sessionBar()
	status := e.statusRight()
	t.Logf("Session bar: %s", bar)
	t.Logf("Status right: %s", status)

	// Stale session should show as Perm* (needs attention)
	if !strings.Contains(bar, "Perm*") {
		t.Errorf("expected stale Working to show as Perm*, got: %s", bar)
	}

	// Active session should NOT be in the attention bar
	// (it's Working with recent modtime -> no attention needed)
	permCount := strings.Count(bar, "Perm*")
	if permCount != 1 {
		t.Errorf("expected exactly 1 Perm*, got %d: %s", permCount, bar)
	}
}

// TestE2E_NavigationAndHighlight verifies M-] and M-[ move the cursor
// and the highlight follows.
func TestE2E_NavigationAndHighlight(t *testing.T) {
	e := newFeedE2E(t, "nav")
	now := time.Now()

	// Both sessions Idle (both need attention)
	e.writeSession("s1",
		e.metadata(),
		jsonlAssistant("end_turn", blockText()),
	)
	e.touchSession("s1", now)

	e.writeSession("s2",
		e.metadata(),
		jsonlAssistant("end_turn", blockText()),
	)
	e.touchSession("s2", now.Add(-5*time.Second))

	e.start()
	defer e.stop()

	// Initial state: cursor on first attention item
	bar := e.sessionBar()
	t.Logf("Initial: %s", bar)
	if !strings.Contains(bar, "#[reverse]") {
		t.Errorf("expected a highlighted entry: %s", bar)
	}

	// Navigate next
	e.send("next")
	bar2 := e.sessionBar()
	t.Logf("After next: %s", bar2)
	if bar == bar2 {
		t.Errorf("highlight should have moved after next")
	}

	// Navigate prev -- should go back
	e.send("prev")
	bar3 := e.sessionBar()
	t.Logf("After prev: %s", bar3)
	if bar3 != bar {
		t.Errorf("prev should return to original state, got: %s", bar3)
	}
}
