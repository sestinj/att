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

func makeSessions(paths []string, files []string) []claude.Session {
	var sessions []claude.Session
	for i, p := range paths {
		sessions = append(sessions, claude.Session{
			WorkspacePath: p,
			ProjectName:   filepath.Base(p),
			SessionFile:   files[i],
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
		[]string{"session-alpha.jsonl", "session-beta.jsonl", "session-gamma.jsonl"},
	)
	attn := []bool{false, true, true}

	// Cursor on alpha (index 0) — not needing attention
	line := formatSessionLine(windows, sessions, attn, 0, 200, nil, nil)
	if !strings.Contains(line, "#[reverse]alpha#[noreverse]") {
		t.Errorf("cursor=0: expected alpha highlighted, got: %s", line)
	}
	if strings.Contains(line, "#[reverse]beta") {
		t.Errorf("cursor=0: beta should not be highlighted, got: %s", line)
	}

	// Cursor on beta (index 1) — needs attention
	line = formatSessionLine(windows, sessions, attn, 1, 200, nil, nil)
	if !strings.Contains(line, "#[reverse]beta Attn*#[noreverse]") {
		t.Errorf("cursor=1: expected beta highlighted with Attn*, got: %s", line)
	}
	if strings.Contains(line, "#[reverse]alpha") {
		t.Errorf("cursor=1: alpha should not be highlighted, got: %s", line)
	}

	// Cursor on gamma (index 2) — needs attention
	line = formatSessionLine(windows, sessions, attn, 2, 200, nil, nil)
	if !strings.Contains(line, "#[reverse]gamma Attn*#[noreverse]") {
		t.Errorf("cursor=2: expected gamma highlighted with Attn*, got: %s", line)
	}
}

func TestFormatSessionLine_NoArrowsWhenFits(t *testing.T) {
	windows := makeWindows(
		[]string{"alpha", "beta"},
		[]string{"/home/user/alpha", "/home/user/beta"},
	)
	sessions := makeSessions(
		[]string{"/home/user/alpha", "/home/user/beta"},
		[]string{"session-alpha.jsonl", "session-beta.jsonl"},
	)

	line := formatSessionLine(windows, sessions, nil, 0, 200, nil, nil)
	if strings.Contains(line, "\u25c0") || strings.Contains(line, "\u25b6") {
		t.Errorf("should not have arrows when everything fits, got: %s", line)
	}
}

func TestFormatSessionLine_Overflow_ShowsArrows(t *testing.T) {
	names := []string{"project-a", "project-b", "project-c", "project-d", "project-e", "project-f", "project-g", "project-h"}
	paths := make([]string, len(names))
	files := make([]string, len(names))
	for i, n := range names {
		paths[i] = "/home/user/" + n
		files[i] = fmt.Sprintf("session-%s.jsonl", n)
	}
	windows := makeWindows(names, paths)
	sessions := makeSessions(paths, files)

	// Active in the middle
	line := formatSessionLine(windows, sessions, nil, 3, 80, nil, nil)

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
	files := make([]string, len(names))
	for i, n := range names {
		paths[i] = "/home/user/" + n
		files[i] = fmt.Sprintf("session-%s.jsonl", n)
	}
	windows := makeWindows(names, paths)
	sessions := makeSessions(paths, files)

	// Active is first -- should have right arrow but no left
	// Use width=40 to force overflow (6 entries * ~6 chars + separators)
	line := formatSessionLine(windows, sessions, nil, 0, 40, nil, nil)
	if strings.Contains(line, "\u25c0") {
		t.Errorf("should not have left arrow when active is first, got: %s", line)
	}
	if !strings.Contains(line, "\u25b6") {
		t.Errorf("should have right arrow when active is first, got: %s", line)
	}

	// Active is last -- should have left arrow but no right
	line = formatSessionLine(windows, sessions, nil, len(names)-1, 40, nil, nil)
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
		[]string{"session-long.jsonl"},
	)
	line := formatSessionLine(windows, sessions, nil, 0, 200, nil, nil)

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
	sessions := []claude.Session{
		{WorkspacePath: "/home/user/alpha"},
		{}, // no session for orphan
	}

	line := formatSessionLine(windows, sessions, nil, 1, 200, nil, nil)
	// orphan is active, highlighted
	if !strings.Contains(line, "#[reverse]orphan#[noreverse]") {
		t.Errorf("expected orphan highlighted, got: %s", line)
	}
}

func TestAssignSessionsToWindows(t *testing.T) {
	now := time.Now()

	t.Run("most recent session assigned first", func(t *testing.T) {
		windows := makeWindows([]string{"proj"}, []string{"/proj"})
		sessions := []claude.Session{
			{WorkspacePath: "/proj", ModTime: now.Add(-1 * time.Hour), SessionFile: "old.jsonl"},
			{WorkspacePath: "/proj", ModTime: now, SessionFile: "new.jsonl"},
		}
		result := assignSessionsToWindows(windows, sessions, nil)
		s := result[0]
		if s.SessionFile != "new.jsonl" {
			t.Errorf("expected most recent session (new.jsonl), got %s", s.SessionFile)
		}
	})

	t.Run("two windows same path get different sessions by recency", func(t *testing.T) {
		windows := makeWindows([]string{"proj", "proj"}, []string{"/proj", "/proj"})
		sessions := []claude.Session{
			{WorkspacePath: "/proj", ModTime: now, SessionFile: "s1.jsonl"},
			{WorkspacePath: "/proj", ModTime: now.Add(-1 * time.Hour), SessionFile: "s2.jsonl"},
		}
		attn := map[string]bool{"s1.jsonl": true}
		result := assignSessionsToWindows(windows, sessions, attn)
		s0 := result[0]
		s1 := result[1]
		if s0.SessionFile == s1.SessionFile {
			t.Errorf("expected different sessions for each window, both got %s", s0.SessionFile)
		}
		// First window gets attention-needing session
		if s0.SessionFile != "s1.jsonl" {
			t.Errorf("expected first window to get attention-needing session, got %s", s0.SessionFile)
		}
	})

	t.Run("old sessions excluded when more sessions than windows", func(t *testing.T) {
		windows := makeWindows([]string{"proj"}, []string{"/proj"})
		sessions := []claude.Session{
			{WorkspacePath: "/proj", ModTime: now, SessionFile: "recent.jsonl"},
			{WorkspacePath: "/proj", ModTime: now.Add(-5 * time.Hour), SessionFile: "old1.jsonl"},
			{WorkspacePath: "/proj", ModTime: now.Add(-6 * time.Hour), SessionFile: "old2.jsonl"},
		}
		result := assignSessionsToWindows(windows, sessions, nil)
		s := result[0]
		if s.SessionFile != "recent.jsonl" {
			t.Errorf("expected most recent session (recent.jsonl), got %s", s.SessionFile)
		}
	})

	t.Run("attention-needing session preferred over newer non-attention", func(t *testing.T) {
		windows := makeWindows([]string{"proj"}, []string{"/proj"})
		sessions := []claude.Session{
			{WorkspacePath: "/proj", ModTime: now.Add(-1 * time.Hour), SessionFile: "asking.jsonl"},
			{WorkspacePath: "/proj", ModTime: now, SessionFile: "working.jsonl"},
		}
		attn := map[string]bool{"asking.jsonl": true}
		result := assignSessionsToWindows(windows, sessions, attn)
		s := result[0]
		if s.SessionFile != "asking.jsonl" {
			t.Errorf("expected attention-needing session to win over newer, got %s", s.SessionFile)
		}
	})

	t.Run("different paths assigned independently", func(t *testing.T) {
		windows := makeWindows([]string{"alpha", "beta"}, []string{"/a", "/b"})
		sessions := []claude.Session{
			{WorkspacePath: "/a", ModTime: now, SessionFile: "a.jsonl"},
			{WorkspacePath: "/b", ModTime: now, SessionFile: "b.jsonl"},
		}
		result := assignSessionsToWindows(windows, sessions, nil)
		if result[0].SessionFile != "a.jsonl" {
			t.Errorf("expected alpha=a.jsonl, got %s", result[0].SessionFile)
		}
		if result[1].SessionFile != "b.jsonl" {
			t.Errorf("expected beta=b.jsonl, got %s", result[1].SessionFile)
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
			0: {SessionFile: "session-a.jsonl"},
			2: {SessionFile: "session-c.jsonl"},
			3: {SessionFile: "session-d.jsonl"},
		},
		attention:      map[string]bool{"session-a.jsonl": true, "session-c.jsonl": true, "session-d.jsonl": true},
		attentionQueue: []int{0, 2, 3},
		cursor:         "0",
		dismissed:      make(map[string]bool),
	}

	// Dismiss from alpha -> should advance to gamma
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
	if len(fc.attentionQueue) != 1 {
		t.Errorf("expected 1 item in queue, got %d", len(fc.attentionQueue))
	}

	// Dismiss from delta -> queue empty
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
			1: {SessionFile: "session-b.jsonl"},
		},
		attention:      map[string]bool{"session-b.jsonl": true},
		attentionQueue: []int{1},
		cursor:         "1",
		dismissed:      make(map[string]bool),
	}

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
			0: {SessionFile: "session-a.jsonl"},
			1: {SessionFile: "session-b.jsonl"},
			2: {SessionFile: "session-c.jsonl"},
		},
		attention:      map[string]bool{"session-b.jsonl": true, "session-c.jsonl": true},
		attentionQueue: []int{1, 2},
		cursor:         "0",
		dismissed:      make(map[string]bool),
	}

	fc.dismissAndAdvance()
	if fc.cursor != "1" {
		t.Errorf("expected cursor=\"1\" (first in queue), got cursor=%s", fc.cursor)
	}
	if !fc.dismissed["session-a.jsonl"] {
		t.Errorf("expected session-a.jsonl to be in dismissed set")
	}
	if len(fc.attentionQueue) != 2 {
		t.Errorf("expected 2 items in queue, got %d", len(fc.attentionQueue))
	}
}

func TestDismissedClearedWhenAttentionClears(t *testing.T) {
	now := time.Now()
	fc := &FeedController{
		allWindows: makeWindows(
			[]string{"alpha", "beta", "gamma"},
			[]string{"/a", "/b", "/c"},
		),
		stateByWindow: map[int]claude.Session{
			0: {WorkspacePath: "/a", SessionFile: "session-a.jsonl", ModTime: now},
			1: {WorkspacePath: "/b", SessionFile: "session-b.jsonl", ModTime: now},
			2: {WorkspacePath: "/c", SessionFile: "session-c.jsonl", ModTime: now},
		},
		// Only session-b and session-c need attention; session-a does not
		attention:      map[string]bool{"session-b.jsonl": true, "session-c.jsonl": true},
		attentionQueue: []int{0, 1, 2},
		cursor:         "0",
		dismissed:      map[string]bool{"session-a.jsonl": true, "session-b.jsonl": true},
	}

	// Build per-window attention (same logic as updateDisplay)
	needsAttention := make(map[int]bool)
	for i, s := range fc.stateByWindow {
		if fc.attention[s.SessionFile] {
			needsAttention[i] = true
		}
	}

	// Clear dismissed sessions that no longer need attention
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

	// session-a: not in attention set → should be cleared from dismissed
	if fc.dismissed["session-a.jsonl"] {
		t.Errorf("expected session-a cleared from dismissed (no longer needs attention)")
	}
	// session-b: still needs attention → should remain dismissed
	if !fc.dismissed["session-b.jsonl"] {
		t.Errorf("expected session-b to remain dismissed")
	}
}

func TestSnoozeExcludesFromAttentionQueue(t *testing.T) {
	snooze := &SnoozeStore{
		entries: map[string]time.Time{
			"session-b.jsonl": time.Now().Add(1 * time.Hour),
		},
	}

	fc := &FeedController{
		allWindows: makeWindows(
			[]string{"alpha", "beta", "gamma"},
			[]string{"/a", "/b", "/c"},
		),
		stateByWindow: map[int]claude.Session{
			0: {SessionFile: "session-a.jsonl"},
			1: {SessionFile: "session-b.jsonl"},
			2: {SessionFile: "session-c.jsonl"},
		},
		attention: map[string]bool{"session-a.jsonl": true, "session-b.jsonl": true, "session-c.jsonl": true},
		dismissed: make(map[string]bool),
		snooze:    snooze,
	}

	fc.updateDisplay()

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
			"session-b.jsonl": time.Now().Add(-1 * time.Minute),
		},
	}

	fc := &FeedController{
		allWindows: makeWindows(
			[]string{"alpha", "beta"},
			[]string{"/a", "/b"},
		),
		stateByWindow: map[int]claude.Session{
			0: {SessionFile: "session-a.jsonl"},
			1: {SessionFile: "session-b.jsonl"},
		},
		attention: map[string]bool{"session-a.jsonl": true, "session-b.jsonl": true},
		dismissed: make(map[string]bool),
		snooze:    snooze,
	}

	fc.updateDisplay()

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
		{WorkspacePath: "/a"},
		{WorkspacePath: "/b", SessionFile: "session-b.jsonl"},
	}
	attn := []bool{false, true}

	snoozedFlags := make([]bool, len(windows))
	for i := range windows {
		if i < len(sessions) {
			snoozedFlags[i] = snooze.IsSnoozed(sessions[i].SessionFile)
		}
	}

	line := formatSessionLine(windows, sessions, attn, 1, 200, snoozedFlags, nil)

	if !strings.Contains(line, "Snz") {
		t.Errorf("expected Snz label for snoozed session, got: %s", line)
	}
}

func TestSnoozeStorePersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snooze.json")

	store := LoadSnooze(path)
	expiry := time.Now().Add(1 * time.Hour).Truncate(time.Second)
	store.Snooze("/tmp/session.jsonl", expiry)

	store2 := LoadSnooze(path)
	if !store2.IsSnoozed("/tmp/session.jsonl") {
		t.Errorf("expected session to be snoozed after reload")
	}

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
			0: {SessionFile: "session-a.jsonl"},
			1: {SessionFile: "session-b.jsonl"},
			2: {SessionFile: "session-c.jsonl"},
		},
		attention:      map[string]bool{"session-a.jsonl": true, "session-b.jsonl": true, "session-c.jsonl": true},
		attentionQueue: []int{0, 1, 2},
		cursor:         "0",
		dismissed:      make(map[string]bool),
		snooze:         snooze,
	}

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
	allWindows := makeWindows(
		[]string{"alpha", "beta", "gamma", "delta"},
		[]string{"/a", "/b", "/c", "/d"},
	)
	allSessions := []claude.Session{
		{WorkspacePath: "/a", SessionFile: "a.jsonl"},
		{WorkspacePath: "/b", SessionFile: "b.jsonl"},
		{WorkspacePath: "/c", SessionFile: "c.jsonl"},
		{WorkspacePath: "/d", SessionFile: "d.jsonl"},
	}
	attention := map[string]bool{"b.jsonl": true, "d.jsonl": true}
	stateByWindow := assignSessionsToWindows(allWindows, allSessions, attention)

	attentionQueue := []int{1, 3} // beta and delta
	cursor := 3

	var filtered []WindowInfo
	var filteredSessions []claude.Session
	var attnFlags []bool
	activeIdx := -1
	for i, wi := range attentionQueue {
		filtered = append(filtered, allWindows[wi])
		filteredSessions = append(filteredSessions, stateByWindow[wi])
		attnFlags = append(attnFlags, attention[stateByWindow[wi].SessionFile])
		if wi == cursor {
			activeIdx = i
		}
	}

	line := formatSessionLine(filtered, filteredSessions, attnFlags, activeIdx, 200, nil, nil)

	if strings.Contains(line, "alpha") {
		t.Errorf("non-attention session alpha should not appear, got: %s", line)
	}
	if strings.Contains(line, "gamma") {
		t.Errorf("non-attention session gamma should not appear, got: %s", line)
	}
	if !strings.Contains(line, "beta Attn*") {
		t.Errorf("expected beta Attn*, got: %s", line)
	}
	if !strings.Contains(line, "#[reverse]delta Attn*#[noreverse]") {
		t.Errorf("expected delta highlighted, got: %s", line)
	}
}

func TestKillCurrent_IndexShift(t *testing.T) {
	fc := &FeedController{
		allWindows: makeWindows(
			[]string{"alpha", "beta", "gamma", "delta"},
			[]string{"/a", "/b", "/c", "/d"},
		),
		stateByWindow: map[int]claude.Session{
			0: {SessionFile: "session-a.jsonl"},
			1: {SessionFile: "session-b.jsonl"},
			2: {SessionFile: "session-c.jsonl"},
			3: {SessionFile: "session-d.jsonl"},
		},
		cursor:    "1",
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

	if len(fc.allWindows) != 3 {
		t.Fatalf("expected 3 windows, got %d", len(fc.allWindows))
	}
	if fc.allWindows[0].Name != "alpha" || fc.allWindows[1].Name != "gamma" || fc.allWindows[2].Name != "delta" {
		t.Errorf("unexpected window order: %v", fc.allWindows)
	}
	if fc.stateByWindow[0].SessionFile != "session-a.jsonl" {
		t.Errorf("index 0: expected session-a, got %s", fc.stateByWindow[0].SessionFile)
	}
	if fc.stateByWindow[1].SessionFile != "session-c.jsonl" {
		t.Errorf("index 1: expected session-c, got %s", fc.stateByWindow[1].SessionFile)
	}
	if fc.stateByWindow[2].SessionFile != "session-d.jsonl" {
		t.Errorf("index 2: expected session-d, got %s", fc.stateByWindow[2].SessionFile)
	}
}

func TestKillCurrent_KillFirst(t *testing.T) {
	fc := &FeedController{
		allWindows: makeWindows(
			[]string{"alpha", "beta", "gamma"},
			[]string{"/a", "/b", "/c"},
		),
		stateByWindow: map[int]claude.Session{
			0: {SessionFile: "session-a.jsonl"},
			1: {SessionFile: "session-b.jsonl"},
			2: {SessionFile: "session-c.jsonl"},
		},
		cursor:    "0",
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
			0: {SessionFile: "session-a.jsonl"},
			1: {SessionFile: "session-b.jsonl"},
		},
		cursor:    "1",
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
			"session-a.jsonl": 2,
			"session-c.jsonl": 0,
		},
		path: filepath.Join(t.TempDir(), "priority.json"),
	}

	fc := &FeedController{
		allWindows: makeWindows(
			[]string{"alpha", "beta", "gamma"},
			[]string{"/a", "/b", "/c"},
		),
		stateByWindow: map[int]claude.Session{
			0: {SessionFile: "session-a.jsonl"},
			1: {SessionFile: "session-b.jsonl"},
			2: {SessionFile: "session-c.jsonl"},
		},
		attention: map[string]bool{"session-a.jsonl": true, "session-b.jsonl": true, "session-c.jsonl": true},
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
			0: {SessionFile: "session-a.jsonl"},
			1: {SessionFile: "session-b.jsonl"},
			2: {SessionFile: "session-c.jsonl"},
		},
		attention: map[string]bool{"session-a.jsonl": true, "session-b.jsonl": true, "session-c.jsonl": true},
		dismissed: make(map[string]bool),
		priority:  priority,
	}

	fc.updateDisplay()

	if len(fc.attentionQueue) != 3 {
		t.Fatalf("expected 3 items in attention queue, got %d", len(fc.attentionQueue))
	}
	for i, expected := range []int{0, 1, 2} {
		if fc.attentionQueue[i] != expected {
			t.Errorf("queue[%d] = %d, expected %d (same priority should preserve order)", i, fc.attentionQueue[i], expected)
		}
	}
}

func TestFuzzyMatch(t *testing.T) {
	tests := []struct {
		pattern string
		str     string
		want    bool
	}{
		// Substring matches
		{"foo", "foobar", true},
		{"bar", "foobar", true},
		{"oba", "foobar", true},
		// Fuzzy matches
		{"fb", "foobar", true},
		{"fbr", "foobar", true},
		// Case insensitive
		{"FOO", "foobar", true},
		{"Fb", "FooBar", true},
		// No match
		{"xyz", "foobar", false},
		{"baf", "foobar", false}, // b before a, but a before f — 'f' can't follow 'a'
		// Empty
		{"", "anything", true},
	}

	for _, tt := range tests {
		got, _ := fuzzyMatch(tt.pattern, tt.str)
		if got != tt.want {
			t.Errorf("fuzzyMatch(%q, %q) = %v, want %v", tt.pattern, tt.str, got, tt.want)
		}
	}
}

func TestFuzzyMatch_SubstringBeatsGappy(t *testing.T) {
	// "att" as substring in "attention" should score lower (better) than fuzzy in "a_t_t"
	_, substringScore := fuzzyMatch("att", "attention")
	_, fuzzyScore := fuzzyMatch("att", "a_test_thing")
	if substringScore >= fuzzyScore {
		t.Errorf("substring score (%d) should be less than fuzzy score (%d)", substringScore, fuzzyScore)
	}
}

func TestFindWithMenu_SingleResult_JumpsDirect(t *testing.T) {
	fc := &FeedController{
		allWindows: makeWindows(
			[]string{"alpha", "beta", "gamma"},
			[]string{"/a", "/b", "/c"},
		),
		stateByWindow: map[int]claude.Session{
			0: {Summary: "Fix auth bug"},
			1: {Summary: "Add logging"},
			2: {Summary: "Refactor DB"},
		},
		cursor:    "0",
		dismissed: make(map[string]bool),
		attention: make(map[string]bool),
	}

	fc.findWithMenu("logging")
	if fc.cursor != "1" {
		t.Errorf("expected cursor to jump to window 1 (logging), got %s", fc.cursor)
	}
}

func TestFindWithMenu_NoResults(t *testing.T) {
	fc := &FeedController{
		allWindows: makeWindows(
			[]string{"alpha", "beta"},
			[]string{"/a", "/b"},
		),
		stateByWindow: map[int]claude.Session{
			0: {Summary: "Fix auth bug"},
			1: {Summary: "Add logging"},
		},
		cursor:    "0",
		dismissed: make(map[string]bool),
		attention: make(map[string]bool),
	}

	fc.findWithMenu("zzzzz")
	// cursor should not change
	if fc.cursor != "0" {
		t.Errorf("expected cursor unchanged, got %s", fc.cursor)
	}
}

func TestSessionEntryTextWithPriority(t *testing.T) {
	w := WindowInfo{Index: "0", Name: "myproject", Path: "/proj"}
	s := claude.Session{}

	// Default priority, needs attention
	text := sessionEntryText(w, s, true, false, false, DefaultPriority)
	if strings.HasPrefix(text, "P") {
		t.Errorf("default priority should not show prefix, got: %s", text)
	}
	if !strings.Contains(text, "myproject Attn*") {
		t.Errorf("expected 'myproject Attn*', got: %s", text)
	}

	// P0, needs attention
	text = sessionEntryText(w, s, true, false, false, 0)
	if !strings.HasPrefix(text, "P0 ") {
		t.Errorf("P0 should show 'P0 ' prefix, got: %s", text)
	}
	if !strings.Contains(text, "P0 myproject Attn*") {
		t.Errorf("expected 'P0 myproject Attn*', got: %s", text)
	}

	// P3 (demoted), no attention
	text = sessionEntryText(w, s, false, false, false, 3)
	if !strings.HasPrefix(text, "P3 ") {
		t.Errorf("P3 should show 'P3 ' prefix, got: %s", text)
	}
	if strings.Contains(text, "Attn") {
		t.Errorf("no attention should not show Attn, got: %s", text)
	}
}

func TestPinnedSessionAppearsInFilteredView(t *testing.T) {
	pin := &PinStore{
		entries: map[string]bool{
			"session-b.jsonl": true,
		},
	}

	fc := &FeedController{
		allWindows: makeWindows(
			[]string{"alpha", "beta", "gamma"},
			[]string{"/a", "/b", "/c"},
		),
		stateByWindow: map[int]claude.Session{
			0: {SessionFile: "session-a.jsonl"},
			1: {SessionFile: "session-b.jsonl"},
			2: {SessionFile: "session-c.jsonl"},
		},
		// Only alpha needs attention; beta is pinned but no attention
		attention: map[string]bool{"session-a.jsonl": true},
		dismissed: make(map[string]bool),
		pin:       pin,
	}

	fc.updateDisplay()

	// attentionQueue should contain alpha (attention) + beta (pinned)
	if len(fc.attentionQueue) != 2 {
		t.Fatalf("expected 2 items in display queue (1 attention + 1 pinned), got %d", len(fc.attentionQueue))
	}
	if fc.attentionCount != 1 {
		t.Errorf("expected attentionCount=1, got %d", fc.attentionCount)
	}
	// First item should be attention, second should be pinned
	if fc.attentionQueue[0] != 0 {
		t.Errorf("expected first in queue = window 0 (attention), got %d", fc.attentionQueue[0])
	}
	if fc.attentionQueue[1] != 1 {
		t.Errorf("expected second in queue = window 1 (pinned), got %d", fc.attentionQueue[1])
	}
}

func TestPinnedAndAttentionShowsBothFlags(t *testing.T) {
	w := WindowInfo{Index: "0", Name: "myproject", Path: "/proj"}
	s := claude.Session{}

	text := sessionEntryText(w, s, true, false, true, DefaultPriority)
	if !strings.Contains(text, "Attn*") {
		t.Errorf("expected Attn* flag, got: %s", text)
	}
	if !strings.Contains(text, "Pin") {
		t.Errorf("expected Pin flag, got: %s", text)
	}
	if text != "myproject Attn* Pin" {
		t.Errorf("expected 'myproject Attn* Pin', got: %s", text)
	}
}

func TestPinOnlyShowsPinFlag(t *testing.T) {
	w := WindowInfo{Index: "0", Name: "myproject", Path: "/proj"}
	s := claude.Session{}

	text := sessionEntryText(w, s, false, false, true, DefaultPriority)
	if !strings.Contains(text, "Pin") {
		t.Errorf("expected Pin flag, got: %s", text)
	}
	if strings.Contains(text, "Attn") {
		t.Errorf("should not show Attn flag, got: %s", text)
	}
	if text != "myproject Pin" {
		t.Errorf("expected 'myproject Pin', got: %s", text)
	}
}

func TestTogglePin(t *testing.T) {
	pin := &PinStore{
		entries: make(map[string]bool),
		path:    filepath.Join(t.TempDir(), "pin.json"),
	}

	fc := &FeedController{
		allWindows: makeWindows(
			[]string{"alpha"},
			[]string{"/a"},
		),
		stateByWindow: map[int]claude.Session{
			0: {SessionFile: "session-a.jsonl"},
		},
		attention: make(map[string]bool),
		dismissed: make(map[string]bool),
		pin:       pin,
		cursor:    "0",
	}

	// Pin
	fc.togglePin()
	if !pin.IsPinned("session-a.jsonl") {
		t.Errorf("expected session-a.jsonl to be pinned after toggle")
	}

	// Unpin
	fc.togglePin()
	if pin.IsPinned("session-a.jsonl") {
		t.Errorf("expected session-a.jsonl to not be pinned after second toggle")
	}
}

func TestAllClearOnlyWhenNoPinsAndNoAttention(t *testing.T) {
	pin := &PinStore{
		entries: map[string]bool{
			"session-a.jsonl": true,
		},
	}

	fc := &FeedController{
		allWindows: makeWindows(
			[]string{"alpha", "beta"},
			[]string{"/a", "/b"},
		),
		stateByWindow: map[int]claude.Session{
			0: {SessionFile: "session-a.jsonl"},
			1: {SessionFile: "session-b.jsonl"},
		},
		// No attention needed
		attention: make(map[string]bool),
		dismissed: make(map[string]bool),
		pin:       pin,
	}

	fc.updateDisplay()

	// Queue should not be empty because alpha is pinned
	if len(fc.attentionQueue) == 0 {
		t.Errorf("expected non-empty queue due to pinned session")
	}
	if fc.attentionCount != 0 {
		t.Errorf("expected attentionCount=0 (no attention items), got %d", fc.attentionCount)
	}

	// Now unpin and verify queue is empty
	pin.Remove("session-a.jsonl")
	fc.updateDisplay()

	if len(fc.attentionQueue) != 0 {
		t.Errorf("expected empty queue after unpin, got %d", len(fc.attentionQueue))
	}
}

func TestPinStorePersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pin.json")

	store := LoadPin(path)
	store.Toggle("/tmp/session.jsonl")

	store2 := LoadPin(path)
	if !store2.IsPinned("/tmp/session.jsonl") {
		t.Errorf("expected session to be pinned after reload")
	}

	store2.Toggle("/tmp/session.jsonl")
	store3 := LoadPin(path)
	if store3.IsPinned("/tmp/session.jsonl") {
		t.Errorf("expected session to not be pinned after toggle off and reload")
	}
}

func TestPinnedNotDuplicatedWhenAlsoNeedsAttention(t *testing.T) {
	pin := &PinStore{
		entries: map[string]bool{
			"session-a.jsonl": true,
		},
	}

	fc := &FeedController{
		allWindows: makeWindows(
			[]string{"alpha", "beta"},
			[]string{"/a", "/b"},
		),
		stateByWindow: map[int]claude.Session{
			0: {SessionFile: "session-a.jsonl"},
			1: {SessionFile: "session-b.jsonl"},
		},
		// alpha needs attention AND is pinned — should appear only once
		attention: map[string]bool{"session-a.jsonl": true},
		dismissed: make(map[string]bool),
		pin:       pin,
	}

	fc.updateDisplay()

	count := 0
	for _, wi := range fc.attentionQueue {
		if wi == 0 {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected pinned+attention session to appear once, appeared %d times", count)
	}
}
