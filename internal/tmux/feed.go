package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sestinj/att/internal/claude"
)

// staleWorkingThreshold is how long a Working session can go without JSONL
// writes before we assume it's blocked on a tool permission prompt. Active
// Claude work writes progress entries every few seconds; silence means the
// process is waiting for user input (permission approval).
const staleWorkingThreshold = 10 * time.Second

type FeedController struct {
	allWindows         []WindowInfo
	discoveredSessions []claude.Session
	stateByWindow      map[int]claude.Session // session assigned to each window index
	cursor             int                    // index into allWindows
	attentionQueue     []int                  // indices into allWindows for windows needing attention
	dismissed          map[string]bool        // session file paths dismissed by user, hidden until state changes
	fifoPath           string
	origStatusRight    string
	origStatusLeft     string
	baseSession        string        // base tmux session that owns the windows (e.g. "att")
	sessionName        string        // grouped session for this feed instance
	noAttach           bool          // skip tmux attach/switch (for testing)
	showAll            bool          // show all windows, not just attention queue
	refreshInterval    time.Duration // override default 3s refresh (for testing)
}

func NewFeedController(opts ...FeedOption) *FeedController {
	fc := &FeedController{
		dismissed:   make(map[string]bool),
		baseSession: "att",
		fifoPath:    fmt.Sprintf("/tmp/att-feed-%d.fifo", os.Getpid()),
	}
	for _, opt := range opts {
		opt(fc)
	}
	return fc
}

type FeedOption func(*FeedController)

func WithNoAttach() FeedOption {
	return func(fc *FeedController) { fc.noAttach = true }
}

func WithBaseSession(name string) FeedOption {
	return func(fc *FeedController) { fc.baseSession = name }
}

func WithRefreshInterval(d time.Duration) FeedOption {
	return func(fc *FeedController) { fc.refreshInterval = d }
}

func (fc *FeedController) Run() error {
	// Ensure base att tmux session exists
	if !HasSession(fc.baseSession) {
		if _, err := NewSession(fc.baseSession, "shell", "", ""); err != nil {
			return fmt.Errorf("create tmux session: %w", err)
		}
	}

	// Create grouped session for independent window selection
	if fc.sessionName == "" {
		fc.sessionName = fmt.Sprintf("att-feed-%d", os.Getpid())
	}
	if err := NewGroupedSession(fc.sessionName, fc.baseSession); err != nil {
		return fmt.Errorf("create feed session: %w", err)
	}
	defer KillSession(fc.sessionName)

	sess := fc.sessionName

	// Single status line: hide the default window list, use status-left for
	// session entries and status-right for navigation.
	exec.Command("tmux", "set-option", "-t", sess, "window-status-format", "").Run()
	exec.Command("tmux", "set-option", "-t", sess, "window-status-current-format", "").Run()
	exec.Command("tmux", "set-option", "-t", sess, "window-status-separator", "").Run()
	exec.Command("tmux", "set-option", "-t", sess, "status-left-length", "200").Run()

	// Save original status values for our grouped session
	if orig, err := GetStatusRightForSession(sess); err == nil {
		fc.origStatusRight = orig
	}
	if orig, err := GetStatusLeftForSession(sess); err == nil {
		fc.origStatusLeft = orig
	}

	// Create FIFO for key binding commands
	if err := syscall.Mkfifo(fc.fifoPath, 0600); err != nil {
		return fmt.Errorf("create fifo: %w", err)
	}
	defer os.Remove(fc.fifoPath)

	// Bind keys and ensure cleanup
	fc.bindKeys()
	defer fc.unbindKeys()
	defer fc.restoreStatus()

	// Handle signals for clean shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Open FIFO (O_RDWR so open doesn't block)
	fifo, err := os.OpenFile(fc.fifoPath, os.O_RDWR, os.ModeNamedPipe)
	if err != nil {
		return fmt.Errorf("open fifo: %w", err)
	}
	defer fifo.Close()

	cmdCh := make(chan string, 10)
	go func() {
		buf := make([]byte, 256)
		for {
			n, err := fifo.Read(buf)
			if err != nil {
				return
			}
			cmd := strings.TrimSpace(string(buf[:n]))
			if cmd != "" {
				cmdCh <- cmd
			}
		}
	}()

	// Initial refresh
	fc.refresh()
	fc.updateStatusBar()

	// Determine quit behavior based on tmux context
	var quitFn func()
	doneCh := make(chan struct{})

	if fc.noAttach {
		quitFn = func() {}
	} else if InTmux() {
		origSessionBytes, _ := exec.Command("tmux", "display-message", "-p", "#{session_name}").Output()
		origSession := strings.TrimSpace(string(origSessionBytes))

		_ = exec.Command("tmux", "switch-client", "-t", fc.sessionName).Run()

		quitFn = func() {
			if origSession != "" {
				exec.Command("tmux", "switch-client", "-t", origSession).Run()
			}
		}
	} else {
		go func() {
			cmd := exec.Command("tmux", "attach-session", "-t", fc.sessionName)
			cmd.Stdin = os.Stdin
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			cmd.Run()
			close(doneCh)
		}()

		quitFn = func() {
			exec.Command("tmux", "detach-client", "-s", fc.sessionName).Run()
		}
	}

	// Event loop -- two-tier refresh:
	// Fast refresh (500ms): re-stat assigned session files, re-read only changed ones
	// Full refresh (5s): full DiscoverSessions scan to find new/removed sessions
	fastInterval := 500 * time.Millisecond
	fullInterval := 5 * time.Second
	if fc.refreshInterval > 0 {
		// Test mode: use the override for both tickers
		fastInterval = fc.refreshInterval
		fullInterval = fc.refreshInterval
	}

	fastTicker := time.NewTicker(fastInterval)
	fullTicker := time.NewTicker(fullInterval)
	defer fastTicker.Stop()
	defer fullTicker.Stop()

	for {
		select {
		case <-sigCh:
			return nil

		case <-doneCh:
			return nil

		case cmd := <-cmdCh:
			switch {
			case cmd == "next":
				fc.next()
			case cmd == "prev":
				fc.prev()
			case cmd == "dismiss":
				fc.dismissAndAdvance()
			case cmd == "quit":
				quitFn()
				return nil
			case cmd == "kill":
				fc.killCurrent()
			case cmd == "toggleall":
				fc.toggleShowAll()
			case strings.HasPrefix(cmd, "new "):
				dir := strings.TrimPrefix(cmd, "new ")
				fc.newSession(dir)
			}

		case <-fullTicker.C:
			fc.refresh()
			fc.updateStatusBar()

		case <-fastTicker.C:
			if fc.refreshAssigned() {
				fc.applyStaleDetection()
				fc.updateDisplay()
				fc.updateStatusBar()
			}
		}
	}
}

func (fc *FeedController) refresh() {
	sessions, err := claude.DiscoverSessions(24 * time.Hour)
	if err != nil {
		return
	}
	fc.discoveredSessions = sessions

	windows, err := ListWindows(fc.baseSession)
	if err != nil {
		return
	}
	fc.allWindows = windows

	// Assign sessions to windows (filters out stale sessions)
	fc.stateByWindow = assignSessionsToWindows(windows, sessions)

	fc.applyStaleDetection()
	fc.updateDisplay()
}

// refreshAssigned re-stats and re-reads only the JSONL files already assigned
// to windows. Returns true if any session state changed.
func (fc *FeedController) refreshAssigned() bool {
	changed := false
	for i, s := range fc.stateByWindow {
		if s.SessionFile == "" {
			continue
		}
		info, err := os.Stat(s.SessionFile)
		if err != nil || info.ModTime().Equal(s.ModTime) {
			continue
		}
		updated, err := claude.ParseSessionFile(s.SessionFile)
		if err != nil {
			continue
		}
		updated.ModTime = info.ModTime()
		fc.stateByWindow[i] = updated
		changed = true
	}
	return changed
}

// applyStaleDetection upgrades Working sessions with no recent JSONL writes
// to ToolPermission, since active Claude work writes progress entries every
// few seconds and silence means the process is waiting for user approval.
func (fc *FeedController) applyStaleDetection() {
	for i, s := range fc.stateByWindow {
		if s.State == claude.StateWorking && time.Since(s.ModTime) > staleWorkingThreshold {
			s.State = claude.StateToolPermission
			fc.stateByWindow[i] = s
		}
	}
}

// updateDisplay rebuilds the attention queue, clears stale dismissals,
// renames windows, clamps the cursor, and renders the session line.
func (fc *FeedController) updateDisplay() {
	// Build attention set from assigned sessions only (not stale ones)
	needsAttention := make(map[int]bool) // keyed by window index
	for i, s := range fc.stateByWindow {
		if s.NeedsAttention() {
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

	// Update window names with asterisk and build attention queue
	fc.attentionQueue = fc.attentionQueue[:0]
	for i, w := range fc.allWindows {
		hasAsterisk := strings.HasSuffix(w.Name, "*")
		needs := needsAttention[i]

		isDismissed := false
		if s, ok := fc.stateByWindow[i]; ok {
			isDismissed = fc.dismissed[s.SessionFile]
		}

		if needs && !isDismissed {
			fc.attentionQueue = append(fc.attentionQueue, i)
			if !hasAsterisk {
				RenameWindow(fc.baseSession, w.Index, w.Name+"*")
			}
		} else if hasAsterisk {
			RenameWindow(fc.baseSession, w.Index, strings.TrimSuffix(w.Name, "*"))
		}
	}

	// Clamp cursor
	if len(fc.allWindows) > 0 && fc.cursor >= len(fc.allWindows) {
		fc.cursor = len(fc.allWindows) - 1
	}
	if len(fc.allWindows) == 0 {
		fc.cursor = 0
	}

	// Auto-advance: if current window left the attention queue, jump to next
	if len(fc.attentionQueue) > 0 {
		inQueue := false
		for _, wi := range fc.attentionQueue {
			if wi == fc.cursor {
				inQueue = true
				break
			}
		}
		if !inQueue {
			fc.cursor = fc.attentionQueue[0]
			fc.focusCurrent()
		}
	}

	fc.renderSessionLine()
}

func (fc *FeedController) focusCurrent() {
	if fc.cursor >= len(fc.allWindows) {
		return
	}
	w := fc.allWindows[fc.cursor]
	SelectWindow(fc.sessionName, w.Index)
}

func (fc *FeedController) next() {
	if fc.showAll {
		if len(fc.allWindows) == 0 {
			return
		}
		fc.cursor = (fc.cursor + 1) % len(fc.allWindows)
	} else {
		if len(fc.attentionQueue) == 0 {
			return
		}
		fc.cursor = fc.nextInQueue(1)
	}
	fc.focusCurrent()
	fc.updateStatusBar()
	fc.renderSessionLine()
}

func (fc *FeedController) prev() {
	if fc.showAll {
		if len(fc.allWindows) == 0 {
			return
		}
		fc.cursor = (fc.cursor - 1 + len(fc.allWindows)) % len(fc.allWindows)
	} else {
		if len(fc.attentionQueue) == 0 {
			return
		}
		fc.cursor = fc.nextInQueue(-1)
	}
	fc.focusCurrent()
	fc.updateStatusBar()
	fc.renderSessionLine()
}

// nextInQueue finds the next (dir=1) or previous (dir=-1) window in the
// attention queue relative to the current cursor position.
func (fc *FeedController) nextInQueue(dir int) int {
	curIdx := -1
	for i, wi := range fc.attentionQueue {
		if wi == fc.cursor {
			curIdx = i
			break
		}
	}
	if curIdx == -1 {
		return fc.attentionQueue[0]
	}
	n := len(fc.attentionQueue)
	return fc.attentionQueue[(curIdx+dir+n)%n]
}

func (fc *FeedController) toggleShowAll() {
	fc.showAll = !fc.showAll
	fc.updateStatusBar()
	fc.renderSessionLine()
}

func (fc *FeedController) dismissAndAdvance() {
	if len(fc.attentionQueue) == 0 {
		return
	}

	// Add current window's session to dismissed set
	if s, ok := fc.stateByWindow[fc.cursor]; ok && s.SessionFile != "" {
		fc.dismissed[s.SessionFile] = true
	}

	curIdx := -1
	for i, wi := range fc.attentionQueue {
		if wi == fc.cursor {
			curIdx = i
			break
		}
	}

	if curIdx != -1 {
		// Remove current from attention queue
		fc.attentionQueue = append(fc.attentionQueue[:curIdx], fc.attentionQueue[curIdx+1:]...)
		// Advance to next item in queue
		if len(fc.attentionQueue) > 0 {
			nextIdx := curIdx
			if nextIdx >= len(fc.attentionQueue) {
				nextIdx = 0
			}
			fc.cursor = fc.attentionQueue[nextIdx]
		}
	} else if len(fc.attentionQueue) > 0 {
		// Cursor wasn't in queue -- jump to first item
		fc.cursor = fc.attentionQueue[0]
	}

	fc.focusCurrent()
	fc.updateStatusBar()
	fc.renderSessionLine()
}

func (fc *FeedController) newSession(dir string) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		absDir = dir
	}
	if gitRoot, err := exec.Command("git", "-C", absDir, "rev-parse", "--show-toplevel").Output(); err == nil {
		absDir = strings.TrimSpace(string(gitRoot))
	}
	NewWindow(fc.baseSession, filepath.Base(absDir), absDir, "claude")
	fc.refresh()
	fc.updateStatusBar()
}

func (fc *FeedController) killCurrent() {
	if len(fc.allWindows) == 0 {
		return
	}
	w := fc.allWindows[fc.cursor]

	// Clear any dismissed state for this window's session
	if s, ok := fc.stateByWindow[fc.cursor]; ok && s.SessionFile != "" {
		delete(fc.dismissed, s.SessionFile)
	}

	// Kill the tmux window (sends SIGHUP to claude, closing it)
	KillWindow(fc.baseSession, w.Index)

	// Refresh to pick up the removed window
	fc.refresh()
	fc.updateStatusBar()
}

func (fc *FeedController) updateStatusBar() {
	if len(fc.allWindows) == 0 {
		SetStatusRightForSession(fc.sessionName, "att | No windows | ^Q quit")
		return
	}

	w := fc.allWindows[fc.cursor]
	name := strings.TrimSuffix(w.Name, "*")
	attn := len(fc.attentionQueue)

	var status string
	if fc.showAll {
		status = fmt.Sprintf("att | %s [%d/%d] | ALL | M-a filter | ^Q quit",
			name, fc.cursor+1, len(fc.allWindows))
	} else if attn == 0 {
		status = fmt.Sprintf("att | %s [%d/%d] | All clear | M-a show all | ^Q quit",
			name, fc.cursor+1, len(fc.allWindows))
	} else {
		status = fmt.Sprintf("att | %s [%d/%d] | %d need attention | M-a show all | ^Q quit",
			name, fc.cursor+1, len(fc.allWindows), attn)
	}
	SetStatusRightForSession(fc.sessionName, status)
}

func (fc *FeedController) bindKeys() {
	fifo := fc.fifoPath
	BindKey("M-]", fmt.Sprintf("echo next > %s", fifo))
	BindKey("M-[", fmt.Sprintf("echo prev > %s", fifo))
	BindKey("M-Enter", fmt.Sprintf("echo dismiss > %s", fifo))
	BindKey("C-q", fmt.Sprintf("echo quit > %s", fifo))
	BindKey("M-a", fmt.Sprintf("echo toggleall > %s", fifo))
	BindKey("M-d", fmt.Sprintf("echo kill > %s", fifo))
	BindKeyDirect("M-n",
		"command-prompt", "-I", "#{pane_current_path}", "-p", "New session:",
		fmt.Sprintf("run-shell 'echo new %%1 > %s'", fifo),
	)
}

func (fc *FeedController) unbindKeys() {
	UnbindKey("M-]")
	UnbindKey("M-[")
	UnbindKey("M-Enter")
	UnbindKey("C-q")
	UnbindKey("M-a")
	UnbindKey("M-d")
	UnbindKey("M-n")
}

func (fc *FeedController) restoreStatus() {
	sess := fc.sessionName
	if fc.origStatusRight != "" {
		SetStatusRightForSession(sess, fc.origStatusRight)
	} else {
		exec.Command("tmux", "set-option", "-t", sess, "-u", "status-right").Run()
	}
	if fc.origStatusLeft != "" {
		SetStatusLeftForSession(sess, fc.origStatusLeft)
	} else {
		exec.Command("tmux", "set-option", "-t", sess, "-u", "status-left").Run()
	}
	exec.Command("tmux", "set-option", "-t", sess, "-u", "status-left-length").Run()
	exec.Command("tmux", "set-option", "-t", sess, "-u", "window-status-format").Run()
	exec.Command("tmux", "set-option", "-t", sess, "-u", "window-status-current-format").Run()
	exec.Command("tmux", "set-option", "-t", sess, "-u", "window-status-separator").Run()
}

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
		for i := range fc.allWindows {
			sessions = append(sessions, fc.stateByWindow[i])
		}
		line := formatSessionLine(fc.allWindows, sessions, fc.cursor, fc.getClientWidth())
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

// assignSessionsToWindows maps each window index to a distinct session.
// When multiple windows share the same workspace path, each window gets
// one of the most recently modified sessions at that path. Old dead sessions
// (from Claude processes that exited hours ago) are excluded.
func assignSessionsToWindows(windows []WindowInfo, sessions []claude.Session) map[int]claude.Session {
	// Count how many windows per path
	windowsPerPath := make(map[string]int)
	for _, w := range windows {
		windowsPerPath[w.Path]++
	}

	// Group sessions by workspace path, sorted by mod time descending (most recent first)
	byPath := make(map[string][]claude.Session)
	for _, s := range sessions {
		byPath[s.WorkspacePath] = append(byPath[s.WorkspacePath], s)
	}
	for path := range byPath {
		group := byPath[path]
		sort.Slice(group, func(i, j int) bool {
			return group[i].ModTime.After(group[j].ModTime)
		})
		// Keep only as many sessions as there are windows at this path.
		// Old sessions from dead Claude processes are irrelevant.
		n := windowsPerPath[path]
		if n > 0 && len(group) > n {
			group = group[:n]
		}
		byPath[path] = group
	}

	// Assign one session per window, consuming from each path's group
	result := make(map[int]claude.Session)
	used := make(map[string]int) // path -> next index to consume
	for i, w := range windows {
		group := byPath[w.Path]
		idx := used[w.Path]
		if idx < len(group) {
			result[i] = group[idx]
			used[w.Path] = idx + 1
		}
	}
	return result
}

// sessionEntryText returns the display text for a window's session entry.
func sessionEntryText(w WindowInfo, s claude.Session) string {
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

	return name + " " + stateStr
}

// formatSessionLine builds the tmux status-format string for the session bar.
// One entry per window, highlighted by activeIdx (cursor position).
func formatSessionLine(windows []WindowInfo, sessions []claude.Session, activeIdx int, width int) string {
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
		text := sessionEntryText(w, s)
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
