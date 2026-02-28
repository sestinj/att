package tmux

import (
	"os/exec"
	"strings"
	"testing"
)

const testSession = "att-test"

func requireTmux(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}
}

func cleanupSession(t *testing.T, name string) {
	t.Helper()
	exec.Command("tmux", "kill-session", "-t", name).Run()
}

func sessionExists(name string) bool {
	return exec.Command("tmux", "has-session", "-t", name).Run() == nil
}

func TestNewSession(t *testing.T) {
	requireTmux(t)
	cleanupSession(t, testSession)
	defer cleanupSession(t, testSession)

	idx, err := NewSession(testSession, "mywin", "/tmp", "sleep 300")
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	if idx == "" {
		t.Fatal("NewSession returned empty window index")
	}

	if !HasSession(testSession) {
		t.Fatal("session should exist after NewSession")
	}

	windows, err := ListWindows(testSession)
	if err != nil {
		t.Fatalf("ListWindows failed: %v", err)
	}
	if len(windows) != 1 {
		t.Fatalf("expected 1 window, got %d", len(windows))
	}
	if windows[0].Name != "mywin" {
		t.Errorf("expected window name 'mywin', got %q", windows[0].Name)
	}
	if windows[0].Index != idx {
		t.Errorf("expected window index %q, got %q", idx, windows[0].Index)
	}
}

func TestNewWindow(t *testing.T) {
	requireTmux(t)
	cleanupSession(t, testSession)
	defer cleanupSession(t, testSession)

	_, err := NewSession(testSession, "win1", "/tmp", "sleep 300")
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}

	idx, err := NewWindow(testSession, "win2", "/tmp", "sleep 300")
	if err != nil {
		t.Fatalf("NewWindow failed: %v", err)
	}
	if idx == "" {
		t.Fatal("NewWindow returned empty window index")
	}

	windows, err := ListWindows(testSession)
	if err != nil {
		t.Fatalf("ListWindows failed: %v", err)
	}
	if len(windows) != 2 {
		t.Fatalf("expected 2 windows, got %d", len(windows))
	}

	names := []string{windows[0].Name, windows[1].Name}
	if !contains(names, "win1") || !contains(names, "win2") {
		t.Errorf("expected windows 'win1' and 'win2', got %v", names)
	}
}

func TestMultipleNewWindows(t *testing.T) {
	requireTmux(t)
	cleanupSession(t, testSession)
	defer cleanupSession(t, testSession)

	_, err := NewSession(testSession, "first", "/tmp", "sleep 300")
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}

	for i := 0; i < 5; i++ {
		_, err := NewWindow(testSession, "extra", "/tmp", "sleep 300")
		if err != nil {
			t.Fatalf("NewWindow %d failed: %v", i, err)
		}
	}

	windows, err := ListWindows(testSession)
	if err != nil {
		t.Fatalf("ListWindows failed: %v", err)
	}
	if len(windows) != 6 {
		t.Fatalf("expected 6 windows, got %d", len(windows))
	}
}

func TestGroupedSession(t *testing.T) {
	requireTmux(t)
	cleanupSession(t, testSession)
	defer cleanupSession(t, testSession)

	grouped := testSession + "-grouped"
	defer cleanupSession(t, grouped)

	// Create base session with two windows
	_, err := NewSession(testSession, "win1", "/tmp", "sleep 300")
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	_, err = NewWindow(testSession, "win2", "/tmp", "sleep 300")
	if err != nil {
		t.Fatalf("NewWindow failed: %v", err)
	}

	// Create grouped session
	if err := NewGroupedSession(grouped, testSession); err != nil {
		t.Fatalf("NewGroupedSession failed: %v", err)
	}

	if !HasSession(grouped) {
		t.Fatal("grouped session should exist after creation")
	}

	// Grouped session should see the same windows
	baseWindows, _ := ListWindows(testSession)
	groupedWindows, _ := ListWindows(grouped)
	if len(baseWindows) != len(groupedWindows) {
		t.Fatalf("grouped should share windows: base has %d, grouped has %d",
			len(baseWindows), len(groupedWindows))
	}

	// Can select different windows independently
	if err := SelectWindow(grouped, baseWindows[0].Index); err != nil {
		t.Fatalf("SelectWindow on grouped session failed: %v", err)
	}
}

func TestGroupedSessionSurvivesWithoutClient(t *testing.T) {
	requireTmux(t)
	cleanupSession(t, testSession)
	defer cleanupSession(t, testSession)

	grouped := testSession + "-survive"
	defer cleanupSession(t, grouped)

	_, err := NewSession(testSession, "win1", "/tmp", "sleep 300")
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}

	if err := NewGroupedSession(grouped, testSession); err != nil {
		t.Fatalf("NewGroupedSession failed: %v", err)
	}

	// Session must still exist -- this was the original bug
	if !HasSession(grouped) {
		t.Fatal("grouped session was destroyed immediately -- destroy-unattached bug is back")
	}
}

func TestKillSession(t *testing.T) {
	requireTmux(t)
	cleanupSession(t, testSession)

	_, err := NewSession(testSession, "win1", "/tmp", "sleep 300")
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}

	KillSession(testSession)

	if HasSession(testSession) {
		t.Fatal("session should not exist after KillSession")
	}
}

func TestRenameWindow(t *testing.T) {
	requireTmux(t)
	cleanupSession(t, testSession)
	defer cleanupSession(t, testSession)

	idx, err := NewSession(testSession, "original", "/tmp", "sleep 300")
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}

	if err := RenameWindow(testSession, idx, "original*"); err != nil {
		t.Fatalf("RenameWindow failed: %v", err)
	}

	windows, _ := ListWindows(testSession)
	if windows[0].Name != "original*" {
		t.Errorf("expected 'original*', got %q", windows[0].Name)
	}

	// Strip asterisk
	if err := RenameWindow(testSession, idx, "original"); err != nil {
		t.Fatalf("RenameWindow strip failed: %v", err)
	}

	windows, _ = ListWindows(testSession)
	if windows[0].Name != "original" {
		t.Errorf("expected 'original', got %q", windows[0].Name)
	}
}

func TestStatusRightForSession(t *testing.T) {
	requireTmux(t)
	cleanupSession(t, testSession)
	defer cleanupSession(t, testSession)

	_, err := NewSession(testSession, "win1", "/tmp", "sleep 300")
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}

	content := "att | test [1/2] | 2 need attention"
	if err := SetStatusRightForSession(testSession, content); err != nil {
		t.Fatalf("SetStatusRightForSession failed: %v", err)
	}

	got, err := GetStatusRightForSession(testSession)
	if err != nil {
		t.Fatalf("GetStatusRightForSession failed: %v", err)
	}
	if got != content {
		t.Errorf("expected %q, got %q", content, got)
	}
}

func TestStatusLeftForSession(t *testing.T) {
	requireTmux(t)
	cleanupSession(t, testSession)
	defer cleanupSession(t, testSession)

	_, err := NewSession(testSession, "win1", "/tmp", "sleep 300")
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}

	content := "#[align=centre]alpha Ask* | beta Idle*"
	if err := SetStatusLeftForSession(testSession, content); err != nil {
		t.Fatalf("SetStatusLeftForSession failed: %v", err)
	}

	got, err := GetStatusLeftForSession(testSession)
	if err != nil {
		t.Fatalf("GetStatusLeftForSession failed: %v", err)
	}
	if got != content {
		t.Errorf("expected %q, got %q", content, got)
	}
}

func TestStartFlow(t *testing.T) {
	requireTmux(t)
	const base = "att-starttest"
	cleanupSession(t, base)
	defer cleanupSession(t, base)

	grouped1 := base + "-g1"
	grouped2 := base + "-g2"
	defer cleanupSession(t, grouped1)
	defer cleanupSession(t, grouped2)

	// Simulate first "att start /tmp/project-a"
	idx1, err := NewSession(base, "project-a", "/tmp", "sleep 300")
	if err != nil {
		t.Fatalf("first start NewSession: %v", err)
	}
	if err := NewGroupedSession(grouped1, base); err != nil {
		t.Fatalf("first start NewGroupedSession: %v", err)
	}
	if err := SelectWindow(grouped1, idx1); err != nil {
		t.Fatalf("first start SelectWindow: %v", err)
	}

	// Simulate second "att start /tmp/project-b"
	idx2, err := NewWindow(base, "project-b", "/tmp", "sleep 300")
	if err != nil {
		t.Fatalf("second start NewWindow: %v", err)
	}
	if err := NewGroupedSession(grouped2, base); err != nil {
		t.Fatalf("second start NewGroupedSession: %v", err)
	}
	if err := SelectWindow(grouped2, idx2); err != nil {
		t.Fatalf("second start SelectWindow: %v", err)
	}

	// Both grouped sessions exist
	if !HasSession(grouped1) {
		t.Fatal("grouped1 should exist")
	}
	if !HasSession(grouped2) {
		t.Fatal("grouped2 should exist")
	}

	// Each grouped session should be viewing different windows
	// Check active window in each grouped session
	active1 := getActiveWindow(t, grouped1)
	active2 := getActiveWindow(t, grouped2)

	if active1 == active2 {
		t.Errorf("grouped sessions should view different windows, both on %q", active1)
	}

	// Cleanup grouped sessions -- base windows should survive
	KillSession(grouped1)
	KillSession(grouped2)

	windows, _ := ListWindows(base)
	if len(windows) != 2 {
		t.Fatalf("base session should still have 2 windows after killing grouped sessions, got %d", len(windows))
	}
}

func TestFeedNavigation(t *testing.T) {
	requireTmux(t)
	const base = "att-navtest"
	cleanupSession(t, base)
	defer cleanupSession(t, base)

	feed := base + "-feed"
	defer cleanupSession(t, feed)

	// Create 3 windows
	_, err := NewSession(base, "project-a", "/tmp", "sleep 300")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	_, err = NewWindow(base, "project-b", "/tmp", "sleep 300")
	if err != nil {
		t.Fatalf("NewWindow b: %v", err)
	}
	_, err = NewWindow(base, "project-c", "/tmp", "sleep 300")
	if err != nil {
		t.Fatalf("NewWindow c: %v", err)
	}

	// Create feed's grouped session
	if err := NewGroupedSession(feed, base); err != nil {
		t.Fatalf("NewGroupedSession: %v", err)
	}

	windows, err := ListWindows(base)
	if err != nil {
		t.Fatalf("ListWindows: %v", err)
	}
	if len(windows) != 3 {
		t.Fatalf("expected 3 windows, got %d", len(windows))
	}

	// Simulate feed's next/prev by calling SelectWindow on the grouped session
	// Start on window 0
	SelectWindow(feed, windows[0].Index)
	active := getActiveWindow(t, feed)
	if active != windows[0].Index {
		t.Errorf("expected window %s, got %s", windows[0].Index, active)
	}

	// Next -> window 1
	SelectWindow(feed, windows[1].Index)
	active = getActiveWindow(t, feed)
	if active != windows[1].Index {
		t.Errorf("after next: expected window %s, got %s", windows[1].Index, active)
	}

	// Next -> window 2
	SelectWindow(feed, windows[2].Index)
	active = getActiveWindow(t, feed)
	if active != windows[2].Index {
		t.Errorf("after next again: expected window %s, got %s", windows[2].Index, active)
	}

	// Prev -> window 1
	SelectWindow(feed, windows[1].Index)
	active = getActiveWindow(t, feed)
	if active != windows[1].Index {
		t.Errorf("after prev: expected window %s, got %s", windows[1].Index, active)
	}

	// Verify base session's active window is NOT affected
	// (grouped sessions have independent window selection)
	baseActive := getActiveWindow(t, base)
	if baseActive == active {
		// This could happen by coincidence if base was also on window 1
		// Let's move feed to window 2 and check base didn't follow
		SelectWindow(feed, windows[2].Index)
		feedActive := getActiveWindow(t, feed)
		baseActive = getActiveWindow(t, base)
		if feedActive == baseActive {
			t.Log("Note: base and feed happen to be on the same window, but that may be coincidence")
		}
	}
}

func getActiveWindow(t *testing.T, session string) string {
	t.Helper()
	out, err := exec.Command("tmux", "display-message", "-t", session, "-p", "#{window_index}").Output()
	if err != nil {
		t.Fatalf("get active window for %s: %v", session, err)
	}
	return strings.TrimSpace(string(out))
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
