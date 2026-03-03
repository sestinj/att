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
		// Show every window with its session info
		var sessions []claude.Session
		var attnFlags []bool
		var snoozedFlags []bool
		var pinnedFlags []bool
		var priorities []int
		for i := range fc.allWindows {
			s := fc.stateByWindow[i]
			sessions = append(sessions, s)
			attnFlags = append(attnFlags, fc.attention[s.SessionFile])
			isSnoozed := false
			if s.SessionFile != "" && fc.snooze != nil {
				isSnoozed = fc.snooze.IsSnoozed(s.SessionFile)
			}
			snoozedFlags = append(snoozedFlags, isSnoozed)
			isPinned := false
			if s.SessionFile != "" && fc.pin != nil {
				isPinned = fc.pin.IsPinned(s.SessionFile)
			}
			pinnedFlags = append(pinnedFlags, isPinned)
			p := DefaultPriority
			if s.SessionFile != "" && fc.priority != nil {
				p = fc.priority.Get(s.SessionFile)
			}
			priorities = append(priorities, p)
		}
		line := formatSessionLine(fc.allWindows, sessions, attnFlags, fc.cursorPos(), fc.getClientWidth(), snoozedFlags, pinnedFlags, priorities)
		SetStatusLeftForSession(fc.sessionName, line)
		return
	}

	if len(fc.attentionQueue) == 0 && !fc.showAll {
		msg := fmt.Sprintf("All clear \u2014 %d sessions working", len(fc.allWindows))
		SetStatusLeftForSession(fc.sessionName, "#[align=centre]"+msg)
		return
	}

	// Build filtered window list from attention queue (includes pinned items)
	var filtered []WindowInfo
	var filteredSessions []claude.Session
	var attnFlags []bool
	var snoozedFlags []bool
	var pinnedFlags []bool
	var priorities []int
	activeIdx := -1
	for i, wi := range fc.attentionQueue {
		s := fc.stateByWindow[wi]
		filtered = append(filtered, fc.allWindows[wi])
		filteredSessions = append(filteredSessions, s)
		attnFlags = append(attnFlags, fc.attention[s.SessionFile])
		if fc.allWindows[wi].Index == fc.cursor {
			activeIdx = i
		}
		isSnoozed := false
		if s.SessionFile != "" && fc.snooze != nil {
			isSnoozed = fc.snooze.IsSnoozed(s.SessionFile)
		}
		snoozedFlags = append(snoozedFlags, isSnoozed)
		isPinned := false
		if s.SessionFile != "" && fc.pin != nil {
			isPinned = fc.pin.IsPinned(s.SessionFile)
		}
		pinnedFlags = append(pinnedFlags, isPinned)
		p := DefaultPriority
		if s.SessionFile != "" && fc.priority != nil {
			p = fc.priority.Get(s.SessionFile)
		}
		priorities = append(priorities, p)
	}

	line := formatSessionLine(filtered, filteredSessions, attnFlags, activeIdx, fc.getClientWidth(), snoozedFlags, pinnedFlags, priorities)
	SetStatusLeftForSession(fc.sessionName, line)
}

// sessionEntryText returns the display text for a window's session entry.
func sessionEntryText(w WindowInfo, s claude.Session, needsAttention bool, snoozed bool, pinned bool, priority int) string {
	name := s.Summary
	if name == "" {
		name = strings.TrimSuffix(w.Name, "*")
	}
	if len(name) > 20 {
		name = name[:20]
	}

	var stateStr string
	if needsAttention {
		stateStr = "Attn*"
	}
	if snoozed {
		if stateStr == "" {
			stateStr = "🕐"
		} else {
			stateStr += " 🕐"
		}
	}
	if pinned {
		if stateStr == "" {
			stateStr = "📌"
		} else {
			stateStr += " 📌"
		}
	}

	var prefix string
	if priority != DefaultPriority {
		prefix = fmt.Sprintf("P%d ", priority)
	}

	if stateStr == "" {
		return prefix + name
	}
	return prefix + name + " " + stateStr
}

// formatSessionLine builds the tmux status-format string for the session bar.
// One entry per window, highlighted by activeIdx (cursor position).
// attn marks which entries need attention, snoozed/pinned mark state flags.
// priorities holds the priority level for each entry (nil means all default).
func formatSessionLine(windows []WindowInfo, sessions []claude.Session, attn []bool, activeIdx int, width int, snoozed []bool, pinned []bool, priorities ...[]int) string {
	snoozedFlags := snoozed
	pinnedFlags := pinned
	var priorityFlags []int
	if len(priorities) > 0 {
		priorityFlags = priorities[0]
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
		needsAttn := len(attn) > i && attn[i]
		isSnoozed := len(snoozedFlags) > i && snoozedFlags[i]
		isPinned := len(pinnedFlags) > i && pinnedFlags[i]
		p := DefaultPriority
		if len(priorityFlags) > i {
			p = priorityFlags[i]
		}
		text := sessionEntryText(w, s, needsAttn, isSnoozed, isPinned, p)
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
