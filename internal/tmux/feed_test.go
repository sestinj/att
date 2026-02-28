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
	line := formatSessionLine(windows, sessions, 0, 200)
	if !strings.Contains(line, "#[reverse]alpha Work#[noreverse]") {
		t.Errorf("cursor=0: expected alpha highlighted, got: %s", line)
	}
	if strings.Contains(line, "#[reverse]beta") {
		t.Errorf("cursor=0: beta should not be highlighted, got: %s", line)
	}

	// Cursor on beta (index 1)
	line = formatSessionLine(windows, sessions, 1, 200)
	if !strings.Contains(line, "#[reverse]beta Idle*#[noreverse]") {
		t.Errorf("cursor=1: expected beta highlighted, got: %s", line)
	}
	if strings.Contains(line, "#[reverse]alpha") {
		t.Errorf("cursor=1: alpha should not be highlighted, got: %s", line)
	}

	// Cursor on gamma (index 2)
	line = formatSessionLine(windows, sessions, 2, 200)
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

	line := formatSessionLine(windows, sessions, 0, 200)
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
	line := formatSessionLine(windows, sessions, 3, 80)

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
	line := formatSessionLine(windows, sessions, 0, 60)
	if strings.Contains(line, "\u25c0") {
		t.Errorf("should not have left arrow when active is first, got: %s", line)
	}
	if !strings.Contains(line, "\u25b6") {
		t.Errorf("should have right arrow when active is first, got: %s", line)
	}

	// Active is last -- should have left arrow but no right
	line = formatSessionLine(windows, sessions, len(names)-1, 60)
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
	line := formatSessionLine(windows, sessions, 0, 200)

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

	line := formatSessionLine(windows, sessions, 1, 200)
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
			{WorkspacePath: "/proj", State: claude.StateIdle, ModTime: now.Add(-1 * time.Hour)},
			{WorkspacePath: "/proj", State: claude.StateWorking, ModTime: now},
		}
		result := assignSessionsToWindows(windows, sessions)
		s := result[0]
		if s.State != claude.StateWorking {
			t.Errorf("expected most recent session (Working), got %v", s.State)
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
			{WorkspacePath: "/proj", State: claude.StateWorking, ModTime: now},
			{WorkspacePath: "/proj", State: claude.StateIdle, ModTime: now.Add(-5 * time.Hour)},
			{WorkspacePath: "/proj", State: claude.StateIdle, ModTime: now.Add(-6 * time.Hour)},
		}
		result := assignSessionsToWindows(windows, sessions)
		// Only 1 window, so only the most recent session should be used
		s := result[0]
		if s.State != claude.StateWorking {
			t.Errorf("expected most recent session (Working), got %v (stale session leaked)", s.State)
		}
	})

	t.Run("two windows exclude stale sessions", func(t *testing.T) {
		windows := makeWindows([]string{"proj", "proj"}, []string{"/proj", "/proj"})
		sessions := []claude.Session{
			{WorkspacePath: "/proj", State: claude.StateAsking, ModTime: now},
			{WorkspacePath: "/proj", State: claude.StateWorking, ModTime: now.Add(-5 * time.Minute)},
			{WorkspacePath: "/proj", State: claude.StateIdle, ModTime: now.Add(-3 * time.Hour)},
			{WorkspacePath: "/proj", State: claude.StateIdle, ModTime: now.Add(-4 * time.Hour)},
			{WorkspacePath: "/proj", State: claude.StateIdle, ModTime: now.Add(-5 * time.Hour)},
		}
		result := assignSessionsToWindows(windows, sessions)
		// 2 windows -> only the 2 most recent sessions should be assigned
		s0 := result[0]
		s1 := result[1]
		if s0.State != claude.StateAsking {
			t.Errorf("expected first window = Asking (most recent), got %v", s0.State)
		}
		if s1.State != claude.StateWorking {
			t.Errorf("expected second window = Working (second most recent), got %v", s1.State)
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
		cursor:         0,              // on alpha
		dismissed:      make(map[string]bool),
	}

	// Dismiss from alpha -> should advance to gamma (next in queue)
	fc.dismissAndAdvance()
	if fc.cursor != 2 {
		t.Errorf("expected cursor=2 (gamma), got cursor=%d", fc.cursor)
	}
	if !fc.dismissed["session-a.jsonl"] {
		t.Errorf("expected session-a.jsonl to be in dismissed set")
	}
	if len(fc.attentionQueue) != 2 {
		t.Errorf("expected 2 items in queue, got %d", len(fc.attentionQueue))
	}

	// Dismiss from gamma -> should advance to delta
	fc.dismissAndAdvance()
	if fc.cursor != 3 {
		t.Errorf("expected cursor=3 (delta), got cursor=%d", fc.cursor)
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
		cursor:         1,
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
		cursor:         0,           // on alpha (not in queue)
		dismissed:      make(map[string]bool),
	}

	// Cursor not in queue: dismisses current window, advances to first queue item
	fc.dismissAndAdvance()
	if fc.cursor != 1 {
		t.Errorf("expected cursor=1 (first in queue), got cursor=%d", fc.cursor)
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
		cursor:         0,
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

func TestStaleWorkingDetectedAsToolPermission(t *testing.T) {
	now := time.Now()
	windows := makeWindows(
		[]string{"alpha", "beta"},
		[]string{"/a", "/b"},
	)
	sessions := []claude.Session{
		{WorkspacePath: "/a", State: claude.StateWorking, ModTime: now},                                         // actively working
		{WorkspacePath: "/b", State: claude.StateWorking, ModTime: now.Add(-staleWorkingThreshold - time.Second)}, // stale
	}

	fc := &FeedController{
		allWindows:    windows,
		stateByWindow: assignSessionsToWindows(windows, sessions),
		dismissed:     make(map[string]bool),
	}

	// Apply the same stale Working override as refresh()
	for i, s := range fc.stateByWindow {
		if s.State == claude.StateWorking && time.Since(s.ModTime) > staleWorkingThreshold {
			s.State = claude.StateToolPermission
			fc.stateByWindow[i] = s
		}
	}

	// Active session stays Working
	if fc.stateByWindow[0].State != claude.StateWorking {
		t.Errorf("expected alpha=Working (active), got %v", fc.stateByWindow[0].State)
	}
	// Stale session detected as ToolPermission
	if fc.stateByWindow[1].State != claude.StateToolPermission {
		t.Errorf("expected beta=ToolPermission (stale Working), got %v", fc.stateByWindow[1].State)
	}
	// ToolPermission needs attention
	if !fc.stateByWindow[1].NeedsAttention() {
		t.Errorf("expected stale Working (now ToolPermission) to need attention")
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

	line := formatSessionLine(filtered, filteredSessions, activeIdx, 200)

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
