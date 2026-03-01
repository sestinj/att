package tmux

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sestinj/att/internal/claude"
)

func makeWindows(names []string, paths []string) []WindowInfo {
	var windows []WindowInfo
	for i, name := range names {
		windows = append(windows, WindowInfo{
			Index: string(rune('0' + i)),
			Name:  name,
			Path:  paths[i],
		})
	}
	return windows
}

func makeSessions(paths []string, states []claude.SessionState) []claude.Session {
	var sessions []claude.Session
	for i, p := range paths {
		sessions = append(sessions, claude.Session{
			WorkspacePath: p,
			ProjectName:   filepath.Base(p),
			SessionFile:   fmt.Sprintf("session-%s.jsonl", filepath.Base(p)),
			State:         states[i],
		})
	}
	return sessions
}

func TestFormatSessionLine_HighlightFollowsCursor(t *testing.T) {
	windows := makeWindows(
		[]string{"alpha", "beta", "gamma"},
		[]string{"/home/user/alpha", "/home/user/beta", "/home/user/gamma"},
	)
	sessions := makeSessions(
		[]string{"/home/user/alpha", "/home/user/beta", "/home/user/gamma"},
		[]claude.SessionState{claude.StateWorking, claude.StateIdle, claude.StateAsking},
	)

	// Cursor on alpha (index 0)
	line := formatSessionLine(windows, sessions, 0, 200, nil)
	if !strings.Contains(line, "#[reverse]alpha Work#[noreverse]") {
		t.Errorf("cursor=0: expected alpha highlighted, got: %s", line)
	}
	if strings.Contains(line, "#[reverse]beta") {
		t.Errorf("cursor=0: beta should not be highlighted, got: %s", line)
	}

	// Cursor on beta (index 1)
	line = formatSessionLine(windows, sessions, 1, 200, nil)
	if !strings.Contains(line, "#[reverse]beta Idle*#[noreverse]") {
		t.Errorf("cursor=1: expected beta highlighted, got: %s", line)
	}
	if strings.Contains(line, "#[reverse]alpha") {
		t.Errorf("cursor=1: alpha should not be highlighted, got: %s", line)
	}

	// Cursor on gamma (index 2)
	line = formatSessionLine(windows, sessions, 2, 200, nil)
	if !strings.Contains(line, "#[reverse]gamma Ask*#[noreverse]") {
		t.Errorf("cursor=2: expected gamma highlighted, got: %s", line)
	}
}

func TestFormatSessionLine_NoArrowsWhenFits(t *testing.T) {
	windows := makeWindows(
		[]string{"alpha", "beta"},
		[]string{"/home/user/alpha", "/home/user/beta"},
	)
	sessions := makeSessions(
		[]string{"/home/user/alpha", "/home/user/beta"},
		[]claude.SessionState{claude.StateWorking, claude.StateIdle},
	)

	line := formatSessionLine(windows, sessions, 0, 200, nil)
	if strings.Contains(line, "\u25c0") || strings.Contains(line, "\u25b6") {
		t.Errorf("should not have arrows when everything fits, got: %s", line)
	}
}

func TestFormatSessionLine_Overflow_ShowsArrows(t *testing.T) {
	names := []string{"project-a", "project-b", "project-c", "project-d", "project-e", "project-f", "project-g", "project-h"}
	paths := make([]string, len(names))
	states := make([]claude.SessionState, len(names))
	for i, n := range names {
		paths[i] = "/home/user/" + n
		states[i] = claude.StateWorking
	}
	windows := makeWindows(names, paths)
	sessions := makeSessions(paths, states)

	// Active in the middle
	line := formatSessionLine(windows, sessions, 3, 80, nil)

	if !strings.Contains(line, "#[reverse]") {
		t.Errorf("expected active highlight, got: %s", line)
	}

	hasLeft := strings.Contains(line, "\u25c0")
	hasRight := strings.Contains(line, "\u25b6")
	if !hasLeft && !hasRight {
		t.Errorf("expected at least one arrow for overflow, got: %s", line)
	}
}

func TestFormatSessionLine_Overflow_ActiveAtEdges(t *testing.T) {
	names := []string{"proj-a", "proj-b", "proj-c", "proj-d", "proj-e", "proj-f"}
	paths := make([]string, len(names))
	states := make([]claude.SessionState, len(names))
	for i, n := range names {
		paths[i] = "/home/user/" + n
		states[i] = claude.StateIdle
	}
	windows := makeWindows(names, paths)
	sessions := makeSessions(paths, states)

	// Active is first -- should have right arrow but no left
	line := formatSessionLine(windows, sessions, 0, 60, nil)
	if strings.Contains(line, "\u25c0") {
		t.Errorf("should not have left arrow when active is first, got: %s", line)
	}
	if !strings.Contains(line, "\u25b6") {
		t.Errorf("should have right arrow when active is first, got: %s", line)
	}

	// Active is last -- should have left arrow but no right
	line = formatSessionLine(windows, sessions, len(names)-1, 60, nil)
	if !strings.Contains(line, "\u25c0") {
		t.Errorf("should have left arrow when active is last, got: %s", line)
	}
	if strings.Contains(line, "\u25b6") {
		t.Errorf("should not have right arrow when active is last, got: %s", line)
	}
}

func TestFormatSessionLine_TruncatesLongNames(t *testing.T) {
	windows := makeWindows(
		[]string{"a-very-long-project-name"},
		[]string{"/home/user/long"},
	)
	sessions := makeSessions(
		[]string{"/home/user/long"},
		[]claude.SessionState{claude.StateWorking},
	)
	line := formatSessionLine(windows, sessions, 0, 200, nil)

	// Window name is truncated to 12 chars
	if strings.Contains(line, "a-very-long-project-name") {
		t.Errorf("name should be truncated, got: %s", line)
	}
	if !strings.Contains(line, "a-very-long-") {
		t.Errorf("expected truncated name, got: %s", line)
	}
}

func TestFormatSessionLine_WindowWithNoSession(t *testing.T) {
	windows := makeWindows(
		[]string{"alpha", "orphan"},
		[]string{"/home/user/alpha", "/home/user/orphan"},
	)
	// Only provide a session for alpha; orphan gets zero-value session
	sessions := []claude.Session{
		{WorkspacePath: "/home/user/alpha", State: claude.StateWorking},
		{}, // no session for orphan
	}

	line := formatSessionLine(windows, sessions, 1, 200, nil)
	// orphan has no session -- should show "?"
	if !strings.Contains(line, "orphan ?") {
		t.Errorf("expected orphan with ? state, got: %s", line)
	}
	// orphan is active
	if !strings.Contains(line, "#[reverse]orphan ?#[noreverse]") {
		t.Errorf("expected orphan highlighted, got: %s", line)
	}
}

func TestAssignSessionsToWindows(t *testing.T) {
	now := time.Now()

	t.Run("most recent session assigned first", func(t *testing.T) {
		windows := makeWindows([]string{"proj"}, []string{"/proj"})
		sessions := []claude.Session{
			{WorkspacePath: "/proj", State: claude.StateWorking, ModTime: now.Add(-1 * time.Hour), SessionFile: "old.jsonl"},
			{WorkspacePath: "/proj", State: claude.StateWorking, ModTime: now, SessionFile: "new.jsonl"},
		}
		result := assignSessionsToWindows(windows, sessions)
		s := result[0]
		if s.SessionFile != "new.jsonl" {
			t.Errorf("expected most recent session (new.jsonl), got %s", s.SessionFile)
		}
	})

	t.Run("two windows same path get different sessions by recency", func(t *testing.T) {
		windows := makeWindows([]string{"proj", "proj"}, []string{"/proj", "/proj"})
		sessions := []claude.Session{
			{WorkspacePath: "/proj", State: claude.StateAsking, ModTime: now},
			{WorkspacePath: "/proj", State: claude.StateIdle, ModTime: now.Add(-1 * time.Hour)},
		}
		result := assignSessionsToWindows(windows, sessions)
		s0 := result[0]
		s1 := result[1]
		if s0.State == s1.State {
			t.Errorf("expected different sessions for each window, both got %v", s0.State)
		}
		// First window gets most recent (Asking), second gets next (Idle)
		if s0.State != claude.StateAsking {
			t.Errorf("expected first window to get Asking (most recent), got %v", s0.State)
		}
		if s1.State != claude.StateIdle {
			t.Errorf("expected second window to get Idle, got %v", s1.State)
		}
	})

	t.Run("old sessions excluded when more sessions than windows", func(t *testing.T) {
		windows := makeWindows([]string{"proj"}, []string{"/proj"})
		sessions := []claude.Session{
			{WorkspacePath: "/proj", State: claude.StateWorking, ModTime: now, SessionFile: "recent.jsonl"},
			{WorkspacePath: "/proj", State: claude.StateWorking, ModTime: now.Add(-5 * time.Hour), SessionFile: "old1.jsonl"},
			{WorkspacePath: "/proj", State: claude.StateWorking, ModTime: now.Add(-6 * time.Hour), SessionFile: "old2.jsonl"},
		}
		result := assignSessionsToWindows(windows, sessions)
		// Only 1 window, so only the most recent session should be used
		s := result[0]
		if s.SessionFile != "recent.jsonl" {
			t.Errorf("expected most recent session (recent.jsonl), got %s (stale session leaked)", s.SessionFile)
		}
	})

	t.Run("two windows exclude stale sessions", func(t *testing.T) {
		windows := makeWindows([]string{"proj", "proj"}, []string{"/proj", "/proj"})
		sessions := []claude.Session{
			{WorkspacePath: "/proj", State: claude.StateAsking, ModTime: now},
			{WorkspacePath: "/proj", State: claude.StateWorking, ModTime: now.Add(-5 * time.Minute)},
			{WorkspacePath: "/proj", State: claude.StateWorking, ModTime: now.Add(-3 * time.Hour)},
			{WorkspacePath: "/proj", State: claude.StateWorking, ModTime: now.Add(-4 * time.Hour)},
			{WorkspacePath: "/proj", State: claude.StateWorking, ModTime: now.Add(-5 * time.Hour)},
		}
		result := assignSessionsToWindows(windows, sessions)
		// 2 windows -> Asking (attention) + most recent Working survive
		s0 := result[0]
		s1 := result[1]
		if s0.State != claude.StateAsking {
			t.Errorf("expected first window = Asking (attention priority), got %v", s0.State)
		}
		if s1.State != claude.StateWorking {
			t.Errorf("expected second window = Working (most recent non-attention), got %v", s1.State)
		}
	})

	t.Run("attention-needing session preferred over newer non-attention", func(t *testing.T) {
		windows := makeWindows([]string{"proj"}, []string{"/proj"})
		sessions := []claude.Session{
			{WorkspacePath: "/proj", State: claude.StateAsking, ModTime: now.Add(-1 * time.Hour)},
			{WorkspacePath: "/proj", State: claude.StateWorking, ModTime: now},
		}
		result := assignSessionsToWindows(windows, sessions)
		s := result[0]
		if s.State != claude.StateAsking {
			t.Errorf("expected attention-needing session (Asking) to win over newer Working, got %v", s.State)
		}
	})

	t.Run("different paths assigned independently", func(t *testing.T) {
		windows := makeWindows([]string{"alpha", "beta"}, []string{"/a", "/b"})
		sessions := []claude.Session{
			{WorkspacePath: "/a", State: claude.StateWorking, ModTime: now},
			{WorkspacePath: "/b", State: claude.StateAsking, ModTime: now},
		}
		result := assignSessionsToWindows(windows, sessions)
		if result[0].State != claude.StateWorking {
			t.Errorf("expected alpha=Working, got %v", result[0].State)
		}
		if result[1].State != claude.StateAsking {
			t.Errorf("expected beta=Asking, got %v", result[1].State)
		}
	})
}

func TestDismissAndAdvance(t *testing.T) {
	fc := &FeedController{
		allWindows: makeWindows(
			[]string{"alpha", "beta", "gamma", "delta"},
			[]string{"/a", "/b", "/c", "/d"},
		),
		stateByWindow: map[int]claude.Session{
			0: {SessionFile: "session-a.jsonl", State: claude.StateIdle},
			2: {SessionFile: "session-c.jsonl", State: claude.StateAsking},
			3: {SessionFile: "session-d.jsonl", State: claude.StateIdle},
		},
		attentionQueue: []int{0, 2, 3}, // alpha, gamma, delta need attention
		cursor:         "0",            // on alpha
		dismissed:      make(map[string]bool),
	}

	// Dismiss from alpha -> should advance to gamma (next in queue)
	fc.dismissAndAdvance()
	if fc.cursor != "2" {
		t.Errorf("expected cursor=\"2\" (gamma), got cursor=%s", fc.cursor)
	}
	if !fc.dismissed["session-a.jsonl"] {
		t.Errorf("expected session-a.jsonl to be in dismissed set")
	}
	if len(fc.attentionQueue) != 2 {
		t.Errorf("expected 2 items in queue, got %d", len(fc.attentionQueue))
	}

	// Dismiss from gamma -> should advance to delta
	fc.dismissAndAdvance()
	if fc.cursor != "3" {
		t.Errorf("expected cursor=\"3\" (delta), got cursor=%s", fc.cursor)
	}
	if !fc.dismissed["session-c.jsonl"] {
		t.Errorf("expected session-c.jsonl to be in dismissed set")
	}
	if len(fc.attentionQueue) != 1 {
		t.Errorf("expected 1 item in queue, got %d", len(fc.attentionQueue))
	}

	// Dismiss from delta -> queue empty, cursor stays
	fc.dismissAndAdvance()
	if !fc.dismissed["session-d.jsonl"] {
		t.Errorf("expected session-d.jsonl to be in dismissed set")
	}
	if len(fc.attentionQueue) != 0 {
		t.Errorf("expected 0 items in queue, got %d", len(fc.attentionQueue))
	}
}

func TestDismissAndAdvance_SingleItem(t *testing.T) {
	fc := &FeedController{
		allWindows: makeWindows(
			[]string{"alpha", "beta"},
			[]string{"/a", "/b"},
		),
		stateByWindow: map[int]claude.Session{
			1: {SessionFile: "session-b.jsonl", State: claude.StateIdle},
		},
		attentionQueue: []int{1}, // only beta needs attention
		cursor:         "1",
		dismissed:      make(map[string]bool),
	}

	// Single item: dismiss removes it from queue and adds to dismissed
	fc.dismissAndAdvance()
	if !fc.dismissed["session-b.jsonl"] {
		t.Errorf("expected session-b.jsonl to be in dismissed set")
	}
	if len(fc.attentionQueue) != 0 {
		t.Errorf("expected empty queue, got %d", len(fc.attentionQueue))
	}
}

func TestDismissAndAdvance_CursorNotInQueue(t *testing.T) {
	fc := &FeedController{
		allWindows: makeWindows(
			[]string{"alpha", "beta", "gamma"},
			[]string{"/a", "/b", "/c"},
		),
		stateByWindow: map[int]claude.Session{
			0: {SessionFile: "session-a.jsonl", State: claude.StateWorking},
			1: {SessionFile: "session-b.jsonl", State: claude.StateIdle},
			2: {SessionFile: "session-c.jsonl", State: claude.StateAsking},
		},
		attentionQueue: []int{1, 2}, // beta, gamma
		cursor:         "0",         // on alpha (not in queue)
		dismissed:      make(map[string]bool),
	}

	// Cursor not in queue: dismisses current window, advances to first queue item
	fc.dismissAndAdvance()
	if fc.cursor != "1" {
		t.Errorf("expected cursor=\"1\" (first in queue), got cursor=%s", fc.cursor)
	}
	if !fc.dismissed["session-a.jsonl"] {
		t.Errorf("expected session-a.jsonl to be in dismissed set")
	}
	// Queue unchanged since cursor wasn't in it
	if len(fc.attentionQueue) != 2 {
		t.Errorf("expected 2 items in queue, got %d", len(fc.attentionQueue))
	}
}

func TestDismissedClearedOnStateChange(t *testing.T) {
	now := time.Now()
	fc := &FeedController{
		allWindows: makeWindows(
			[]string{"alpha", "beta", "gamma"},
			[]string{"/a", "/b", "/c"},
		),
		stateByWindow: map[int]claude.Session{
			0: {WorkspacePath: "/a", SessionFile: "session-a.jsonl", State: claude.StateWorking, ModTime: now},
			1: {WorkspacePath: "/b", SessionFile: "session-b.jsonl", State: claude.StateIdle, ModTime: now},
			2: {WorkspacePath: "/c", SessionFile: "session-c.jsonl", State: claude.StateIdle, ModTime: now},
		},
		attentionQueue: []int{0, 1, 2},
		cursor:         "0",
		dismissed:      map[string]bool{"session-a.jsonl": true, "session-b.jsonl": true},
	}

	// Build per-window attention (same logic as refresh)
	needsAttention := make(map[int]bool)
	for i, s := range fc.stateByWindow {
		if s.NeedsAttention() {
			needsAttention[i] = true
		}
	}

	// Clear dismissed sessions that no longer need attention (same logic as refresh)
	for sessionFile := range fc.dismissed {
		stillNeeded := false
		for i, s := range fc.stateByWindow {
			if s.SessionFile == sessionFile && needsAttention[i] {
				stillNeeded = true
				break
			}
		}
		if !stillNeeded {
			delete(fc.dismissed, sessionFile)
		}
	}

	// session-a should be cleared from dismissed (assigned session is Working)
	if fc.dismissed["session-a.jsonl"] {
		t.Errorf("expected session-a cleared from dismissed (transitioned to Working)")
	}
	// session-b should stay dismissed (assigned session is still Idle)
	if !fc.dismissed["session-b.jsonl"] {
		t.Errorf("expected session-b to remain dismissed")
	}
}

func TestSnoozeExcludesFromAttentionQueue(t *testing.T) {
	snooze := &SnoozeStore{
		entries: map[string]time.Time{
			"session-b.jsonl": time.Now().Add(1 * time.Hour), // snoozed
		},
	}

	fc := &FeedController{
		allWindows: makeWindows(
			[]string{"alpha", "beta", "gamma"},
			[]string{"/a", "/b", "/c"},
		),
		stateByWindow: map[int]claude.Session{
			0: {SessionFile: "session-a.jsonl", State: claude.StateIdle},
			1: {SessionFile: "session-b.jsonl", State: claude.StateIdle},
			2: {SessionFile: "session-c.jsonl", State: claude.StateAsking},
		},
		dismissed: make(map[string]bool),
		snooze:    snooze,
	}

	fc.updateDisplay()

	// beta is snoozed, so only alpha and gamma should be in the attention queue
	if len(fc.attentionQueue) != 2 {
		t.Fatalf("expected 2 items in attention queue, got %d", len(fc.attentionQueue))
	}
	for _, wi := range fc.attentionQueue {
		if wi == 1 {
			t.Errorf("snoozed session beta should not be in attention queue")
		}
	}
}

func TestSnoozeExpiredRestoresToQueue(t *testing.T) {
	snooze := &SnoozeStore{
		entries: map[string]time.Time{
			"session-b.jsonl": time.Now().Add(-1 * time.Minute), // expired
		},
	}

	fc := &FeedController{
		allWindows: makeWindows(
			[]string{"alpha", "beta"},
			[]string{"/a", "/b"},
		),
		stateByWindow: map[int]claude.Session{
			0: {SessionFile: "session-a.jsonl", State: claude.StateIdle},
			1: {SessionFile: "session-b.jsonl", State: claude.StateIdle},
		},
		dismissed: make(map[string]bool),
		snooze:    snooze,
	}

	fc.updateDisplay()

	// Expired snooze should not filter beta out
	if len(fc.attentionQueue) != 2 {
		t.Fatalf("expected 2 items in attention queue (expired snooze), got %d", len(fc.attentionQueue))
	}
}

func TestSnoozedVisibleInShowAll(t *testing.T) {
	snooze := &SnoozeStore{
		entries: map[string]time.Time{
			"session-b.jsonl": time.Now().Add(1 * time.Hour),
		},
	}

	windows := makeWindows(
		[]string{"alpha", "beta"},
		[]string{"/a", "/b"},
	)
	sessions := []claude.Session{
		{WorkspacePath: "/a", State: claude.StateWorking},
		{WorkspacePath: "/b", SessionFile: "session-b.jsonl", State: claude.StateIdle},
	}

	snoozedFlags := make([]bool, len(windows))
	for i := range windows {
		if i < len(sessions) {
			snoozedFlags[i] = snooze.IsSnoozed(sessions[i].SessionFile)
		}
	}

	line := formatSessionLine(windows, sessions, 1, 200, snoozedFlags, nil)

	// beta should show "Snz" label
	if !strings.Contains(line, "Snz") {
		t.Errorf("expected Snz label for snoozed session, got: %s", line)
	}
	// alpha should not show Snz
	if strings.Contains(line, "alpha Work Snz") {
		t.Errorf("non-snoozed session should not show Snz, got: %s", line)
	}
}

func TestSnoozeStorePersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snooze.json")

	// Create and save
	store := LoadSnooze(path)
	expiry := time.Now().Add(1 * time.Hour).Truncate(time.Second)
	store.Snooze("/tmp/session.jsonl", expiry)

	// Reload and verify
	store2 := LoadSnooze(path)
	if !store2.IsSnoozed("/tmp/session.jsonl") {
		t.Errorf("expected session to be snoozed after reload")
	}

	// Unsnooze and verify
	store2.Unsnooze("/tmp/session.jsonl")
	store3 := LoadSnooze(path)
	if store3.IsSnoozed("/tmp/session.jsonl") {
		t.Errorf("expected session to not be snoozed after unsnooze")
	}
}

func TestSnoozeAndAdvance(t *testing.T) {
	snooze := &SnoozeStore{
		entries: make(map[string]time.Time),
		path:    filepath.Join(t.TempDir(), "snooze.json"),
	}

	fc := &FeedController{
		allWindows: makeWindows(
			[]string{"alpha", "beta", "gamma"},
			[]string{"/a", "/b", "/c"},
		),
		stateByWindow: map[int]claude.Session{
			0: {SessionFile: "session-a.jsonl", State: claude.StateIdle},
			1: {SessionFile: "session-b.jsonl", State: claude.StateIdle},
			2: {SessionFile: "session-c.jsonl", State: claude.StateAsking},
		},
		attentionQueue: []int{0, 1, 2},
		cursor:         "0",
		dismissed:      make(map[string]bool),
		snooze:         snooze,
	}

	// Snooze alpha -> should advance to beta
	fc.snoozeAndAdvance("1h")
	if fc.cursor != "1" {
		t.Errorf("expected cursor=\"1\" (beta), got cursor=%s", fc.cursor)
	}
	if !snooze.IsSnoozed("session-a.jsonl") {
		t.Errorf("expected session-a.jsonl to be snoozed")
	}
	if len(fc.attentionQueue) != 2 {
		t.Errorf("expected 2 items in queue, got %d", len(fc.attentionQueue))
	}
}

func TestRenderSessionLine_OnlyAttentionWindows(t *testing.T) {
	// Simulate what renderSessionLine does: filter to attention windows only
	allWindows := makeWindows(
		[]string{"alpha", "beta", "gamma", "delta"},
		[]string{"/a", "/b", "/c", "/d"},
	)
	allSessions := []claude.Session{
		{WorkspacePath: "/a", State: claude.StateWorking},
		{WorkspacePath: "/b", State: claude.StateIdle},
		{WorkspacePath: "/c", State: claude.StateWorking},
		{WorkspacePath: "/d", State: claude.StateAsking},
	}
	stateByWindow := assignSessionsToWindows(allWindows, allSessions)

	attentionQueue := []int{1, 3} // beta and delta
	cursor := 3                   // on delta

	// Build filtered list (same logic as renderSessionLine)
	var filtered []WindowInfo
	var filteredSessions []claude.Session
	activeIdx := -1
	for i, wi := range attentionQueue {
		filtered = append(filtered, allWindows[wi])
		filteredSessions = append(filteredSessions, stateByWindow[wi])
		if wi == cursor {
			activeIdx = i
		}
	}

	line := formatSessionLine(filtered, filteredSessions, activeIdx, 200, nil)

	// Should contain beta and delta, NOT alpha or gamma
	if strings.Contains(line, "alpha") {
		t.Errorf("working session alpha should not appear, got: %s", line)
	}
	if strings.Contains(line, "gamma") {
		t.Errorf("working session gamma should not appear, got: %s", line)
	}
	if !strings.Contains(line, "beta Idle*") {
		t.Errorf("expected beta Idle*, got: %s", line)
	}
	if !strings.Contains(line, "#[reverse]delta Ask*#[noreverse]") {
		t.Errorf("expected delta highlighted, got: %s", line)
	}
}

func TestKillCurrent_IndexShift(t *testing.T) {
	// Simulate killCurrent's in-memory update: killing window at index 1
	// should shift indices 2,3 down to 1,2.
	fc := &FeedController{
		allWindows: makeWindows(
			[]string{"alpha", "beta", "gamma", "delta"},
			[]string{"/a", "/b", "/c", "/d"},
		),
		stateByWindow: map[int]claude.Session{
			0: {SessionFile: "session-a.jsonl", State: claude.StateWorking},
			1: {SessionFile: "session-b.jsonl", State: claude.StateIdle},
			2: {SessionFile: "session-c.jsonl", State: claude.StateAsking},
			3: {SessionFile: "session-d.jsonl", State: claude.StateWorking},
		},
		cursor:    "1", // killing beta
		dismissed: make(map[string]bool),
	}

	// Perform the same in-memory splice as killCurrent
	killedIdx := fc.cursorPos()
	fc.allWindows = append(fc.allWindows[:killedIdx], fc.allWindows[killedIdx+1:]...)

	newState := make(map[int]claude.Session, len(fc.stateByWindow))
	for i, s := range fc.stateByWindow {
		if i == killedIdx {
			continue
		}
		if i > killedIdx {
			newState[i-1] = s
		} else {
			newState[i] = s
		}
	}
	fc.stateByWindow = newState

	// Verify windows
	if len(fc.allWindows) != 3 {
		t.Fatalf("expected 3 windows, got %d", len(fc.allWindows))
	}
	if fc.allWindows[0].Name != "alpha" || fc.allWindows[1].Name != "gamma" || fc.allWindows[2].Name != "delta" {
		t.Errorf("unexpected window order: %v", fc.allWindows)
	}

	// Verify state mapping shifted correctly
	if fc.stateByWindow[0].SessionFile != "session-a.jsonl" {
		t.Errorf("index 0: expected session-a, got %s", fc.stateByWindow[0].SessionFile)
	}
	if fc.stateByWindow[1].SessionFile != "session-c.jsonl" {
		t.Errorf("index 1: expected session-c (shifted from 2), got %s", fc.stateByWindow[1].SessionFile)
	}
	if fc.stateByWindow[2].SessionFile != "session-d.jsonl" {
		t.Errorf("index 2: expected session-d (shifted from 3), got %s", fc.stateByWindow[2].SessionFile)
	}
	if _, ok := fc.stateByWindow[3]; ok {
		t.Errorf("index 3 should not exist after kill")
	}
}

func TestKillCurrent_KillFirst(t *testing.T) {
	fc := &FeedController{
		allWindows: makeWindows(
			[]string{"alpha", "beta", "gamma"},
			[]string{"/a", "/b", "/c"},
		),
		stateByWindow: map[int]claude.Session{
			0: {SessionFile: "session-a.jsonl", State: claude.StateIdle},
			1: {SessionFile: "session-b.jsonl", State: claude.StateWorking},
			2: {SessionFile: "session-c.jsonl", State: claude.StateAsking},
		},
		cursor:    "0", // killing alpha
		dismissed: make(map[string]bool),
	}

	killedIdx := fc.cursorPos()
	fc.allWindows = append(fc.allWindows[:killedIdx], fc.allWindows[killedIdx+1:]...)

	newState := make(map[int]claude.Session, len(fc.stateByWindow))
	for i, s := range fc.stateByWindow {
		if i == killedIdx {
			continue
		}
		if i > killedIdx {
			newState[i-1] = s
		} else {
			newState[i] = s
		}
	}
	fc.stateByWindow = newState

	if len(fc.allWindows) != 2 {
		t.Fatalf("expected 2 windows, got %d", len(fc.allWindows))
	}
	if fc.stateByWindow[0].SessionFile != "session-b.jsonl" {
		t.Errorf("index 0: expected session-b, got %s", fc.stateByWindow[0].SessionFile)
	}
	if fc.stateByWindow[1].SessionFile != "session-c.jsonl" {
		t.Errorf("index 1: expected session-c, got %s", fc.stateByWindow[1].SessionFile)
	}
}

func TestKillCurrent_KillLast(t *testing.T) {
	fc := &FeedController{
		allWindows: makeWindows(
			[]string{"alpha", "beta"},
			[]string{"/a", "/b"},
		),
		stateByWindow: map[int]claude.Session{
			0: {SessionFile: "session-a.jsonl", State: claude.StateWorking},
			1: {SessionFile: "session-b.jsonl", State: claude.StateIdle},
		},
		cursor:    "1", // killing last (beta)
		dismissed: make(map[string]bool),
	}

	killedIdx := fc.cursorPos()
	fc.allWindows = append(fc.allWindows[:killedIdx], fc.allWindows[killedIdx+1:]...)

	newState := make(map[int]claude.Session, len(fc.stateByWindow))
	for i, s := range fc.stateByWindow {
		if i == killedIdx {
			continue
		}
		if i > killedIdx {
			newState[i-1] = s
		} else {
			newState[i] = s
		}
	}
	fc.stateByWindow = newState

	if len(fc.allWindows) != 1 {
		t.Fatalf("expected 1 window, got %d", len(fc.allWindows))
	}
	if fc.stateByWindow[0].SessionFile != "session-a.jsonl" {
		t.Errorf("index 0: expected session-a, got %s", fc.stateByWindow[0].SessionFile)
	}
	if _, ok := fc.stateByWindow[1]; ok {
		t.Errorf("index 1 should not exist after kill")
	}
}

func TestPrioritySortsAttentionQueue(t *testing.T) {
	priority := &PriorityStore{
		entries: map[string]int{
			"session-a.jsonl": 2, // P2
			"session-c.jsonl": 0, // P0
			// session-b.jsonl defaults to P4
		},
		path: filepath.Join(t.TempDir(), "priority.json"),
	}

	fc := &FeedController{
		allWindows: makeWindows(
			[]string{"alpha", "beta", "gamma"},
			[]string{"/a", "/b", "/c"},
		),
		stateByWindow: map[int]claude.Session{
			0: {SessionFile: "session-a.jsonl", State: claude.StateIdle},
			1: {SessionFile: "session-b.jsonl", State: claude.StateIdle},
			2: {SessionFile: "session-c.jsonl", State: claude.StateAsking},
		},
		dismissed: make(map[string]bool),
		priority:  priority,
	}

	fc.updateDisplay()

	// Expected order: P0 (gamma/session-c), P2 (alpha/session-a), P4 (beta/session-b)
	if len(fc.attentionQueue) != 3 {
		t.Fatalf("expected 3 items in attention queue, got %d", len(fc.attentionQueue))
	}
	if fc.attentionQueue[0] != 2 {
		t.Errorf("expected first in queue = window 2 (P0), got %d", fc.attentionQueue[0])
	}
	if fc.attentionQueue[1] != 0 {
		t.Errorf("expected second in queue = window 0 (P2), got %d", fc.attentionQueue[1])
	}
	if fc.attentionQueue[2] != 1 {
		t.Errorf("expected third in queue = window 1 (P4/default), got %d", fc.attentionQueue[2])
	}
}

func TestPrioritySameLevelPreservesWindowOrder(t *testing.T) {
	priority := &PriorityStore{
		entries: map[string]int{
			"session-a.jsonl": 1,
			"session-b.jsonl": 1,
			"session-c.jsonl": 1,
		},
		path: filepath.Join(t.TempDir(), "priority.json"),
	}

	fc := &FeedController{
		allWindows: makeWindows(
			[]string{"alpha", "beta", "gamma"},
			[]string{"/a", "/b", "/c"},
		),
		stateByWindow: map[int]claude.Session{
			0: {SessionFile: "session-a.jsonl", State: claude.StateIdle},
			1: {SessionFile: "session-b.jsonl", State: claude.StateAsking},
			2: {SessionFile: "session-c.jsonl", State: claude.StateIdle},
		},
		dismissed: make(map[string]bool),
		priority:  priority,
	}

	fc.updateDisplay()

	// Same priority: should preserve window index order (0, 1, 2)
	if len(fc.attentionQueue) != 3 {
		t.Fatalf("expected 3 items in attention queue, got %d", len(fc.attentionQueue))
	}
	for i, expected := range []int{0, 1, 2} {
		if fc.attentionQueue[i] != expected {
			t.Errorf("queue[%d] = %d, expected %d (same priority should preserve order)", i, fc.attentionQueue[i], expected)
		}
	}
}

func TestSessionEntryTextWithPriority(t *testing.T) {
	w := WindowInfo{Index: "0", Name: "myproject", Path: "/proj"}
	s := claude.Session{State: claude.StateIdle}

	// Default priority (P4): no prefix
	text := sessionEntryText(w, s, false, DefaultPriority)
	if strings.HasPrefix(text, "P") {
		t.Errorf("default priority should not show prefix, got: %s", text)
	}
	if !strings.Contains(text, "myproject Idle*") {
		t.Errorf("expected 'myproject Idle*', got: %s", text)
	}

	// P0: should show prefix
	text = sessionEntryText(w, s, false, 0)
	if !strings.HasPrefix(text, "P0 ") {
		t.Errorf("P0 should show 'P0 ' prefix, got: %s", text)
	}
	if !strings.Contains(text, "P0 myproject Idle*") {
		t.Errorf("expected 'P0 myproject Idle*', got: %s", text)
	}

	// P2: should show prefix
	text = sessionEntryText(w, s, false, 2)
	if !strings.HasPrefix(text, "P2 ") {
		t.Errorf("P2 should show 'P2 ' prefix, got: %s", text)
	}
}
