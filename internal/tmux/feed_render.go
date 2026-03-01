package tmux

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/sestinj/att/internal/claude"
)

func (fc *FeedController) getClientWidth() int {
	out, err := exec.Command("tmux", "display-message", "-t", fc.sessionName, "-p", "#{client_width}").Output()
	if err != nil {
		return 120
	}
	w, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || w <= 0 {
		return 120
	}
	return w
}

func (fc *FeedController) renderSessionLine() {
	if fc.showAll {
		// Show every window with its session state
		var sessions []claude.Session
		var snoozedFlags []bool
		for i := range fc.allWindows {
			sessions = append(sessions, fc.stateByWindow[i])
			isSnoozed := false
			if s, ok := fc.stateByWindow[i]; ok && fc.snooze != nil {
				isSnoozed = fc.snooze.IsSnoozed(s.SessionFile)
			}
			snoozedFlags = append(snoozedFlags, isSnoozed)
		}
		line := formatSessionLine(fc.allWindows, sessions, fc.cursor, fc.getClientWidth(), snoozedFlags)
		SetStatusLeftForSession(fc.sessionName, line)
		return
	}

	if len(fc.attentionQueue) == 0 && !fc.showAll {
		msg := fmt.Sprintf("All clear \u2014 %d sessions working", len(fc.allWindows))
		SetStatusLeftForSession(fc.sessionName, "#[align=centre]"+msg)
		return
	}

	// Build filtered window list from attention queue
	var filtered []WindowInfo
	var filteredSessions []claude.Session
	activeIdx := -1
	for i, wi := range fc.attentionQueue {
		filtered = append(filtered, fc.allWindows[wi])
		filteredSessions = append(filteredSessions, fc.stateByWindow[wi])
		if wi == fc.cursor {
			activeIdx = i
		}
	}

	line := formatSessionLine(filtered, filteredSessions, activeIdx, fc.getClientWidth())
	SetStatusLeftForSession(fc.sessionName, line)
}

// sessionEntryText returns the display text for a window's session entry.
func sessionEntryText(w WindowInfo, s claude.Session, snoozed bool) string {
	name := strings.TrimSuffix(w.Name, "*")
	if len(name) > 12 {
		name = name[:12]
	}

	var stateStr string
	switch s.State {
	case claude.StateWorking:
		stateStr = "Work"
	case claude.StateIdle:
		stateStr = "Idle"
	case claude.StateAsking:
		stateStr = "Ask"
	case claude.StatePlanMode:
		stateStr = "Plan"
	case claude.StateToolPermission:
		stateStr = "Perm"
	default:
		stateStr = "?"
	}

	if s.NeedsAttention() {
		stateStr += "*"
	}
	if snoozed {
		stateStr += " Snz"
	}

	return name + " " + stateStr
}

// formatSessionLine builds the tmux status-format string for the session bar.
// One entry per window, highlighted by activeIdx (cursor position).
// snoozed marks which entries are snoozed (shown in show-all mode).
func formatSessionLine(windows []WindowInfo, sessions []claude.Session, activeIdx int, width int, snoozed ...[]bool) string {
	var snoozedFlags []bool
	if len(snoozed) > 0 {
		snoozedFlags = snoozed[0]
	}
	type entry struct {
		text string
		len  int
	}

	var entries []entry
	for i, w := range windows {
		var s claude.Session
		if i < len(sessions) {
			s = sessions[i]
		}
		isSnoozed := len(snoozedFlags) > i && snoozedFlags[i]
		text := sessionEntryText(w, s, isSnoozed)
		entries = append(entries, entry{text: text, len: len(text)})
	}

	if activeIdx < 0 || activeIdx >= len(entries) {
		activeIdx = -1
	}

	sep := " | "
	sepLen := len(sep)

	// Calculate total visible width
	totalLen := 0
	for i, e := range entries {
		if i > 0 {
			totalLen += sepLen
		}
		totalLen += e.len
	}

	// Build the line with highlighting, handling overflow
	if totalLen <= width {
		var parts []string
		for i, e := range entries {
			if i == activeIdx {
				parts = append(parts, "#[reverse]"+e.text+"#[noreverse]")
			} else {
				parts = append(parts, e.text)
			}
		}
		return "#[align=centre]" + strings.Join(parts, sep)
	}

	// Overflow: center on active entry, show arrows for clipped sides
	arrowLeft := "\u25c0 "  // "< "
	arrowRight := " \u25b6" // " >"
	arrowLen := 2

	available := width - arrowLen*2
	if available < 10 {
		available = width
	}

	start, end := activeIdx, activeIdx
	if activeIdx < 0 {
		start, end = 0, 0
	}
	used := entries[start].len

	for {
		expanded := false
		if end+1 < len(entries) {
			need := sepLen + entries[end+1].len
			if used+need <= available {
				end++
				used += need
				expanded = true
			}
		}
		if start-1 >= 0 {
			need := sepLen + entries[start-1].len
			if used+need <= available {
				start--
				used += need
				expanded = true
			}
		}
		if !expanded {
			break
		}
	}

	var parts []string
	hasLeft := start > 0
	hasRight := end < len(entries)-1

	if hasLeft {
		parts = append(parts, arrowLeft)
	}
	for i := start; i <= end; i++ {
		if i > start {
			parts = append(parts, sep)
		}
		if i == activeIdx {
			parts = append(parts, "#[reverse]"+entries[i].text+"#[noreverse]")
		} else {
			parts = append(parts, entries[i].text)
		}
	}
	if hasRight {
		parts = append(parts, arrowRight)
	}

	return "#[align=centre]" + strings.Join(parts, "")
}
