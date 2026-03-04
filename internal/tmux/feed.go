package tmux

import (
	"fmt"
	"log"
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

type FeedController struct {
	allWindows         []WindowInfo
	discoveredSessions []claude.Session
	stateByWindow      map[int]claude.Session // session assigned to each window index
	attention          map[string]bool        // transcript paths needing attention (from attention dir)
	cursor             string                 // tmux window Index (e.g. "0", "3")
	attentionQueue     []int                  // indices into allWindows for windows needing attention
	dismissed          map[string]bool        // session file paths dismissed by user, hidden until state changes
	snooze             *SnoozeStore           // time-based snooze for sessions
	priority           *PriorityStore         // P0-P4 priority levels for sessions
	pin                *PinStore              // pinned sessions stay visible regardless of attention
	attentionCount     int                    // count of attention-only items (excludes pinned-only)
	promptCache        map[string]promptCacheEntry // pre-computed prompt text keyed by session file
	fifoPath           string
	origStatusRight    string
	origStatusLeft     string
	baseSession        string        // base tmux session that owns the windows (e.g. "att")
	sessionName        string        // grouped session for this feed instance
	noAttach           bool          // skip tmux attach/switch (for testing)
	showAll            bool          // show all windows, not just attention queue
	refreshInterval    time.Duration // override default 3s refresh (for testing)
	command            string        // command to run in new windows (default: "claude")
	projects           []string      // project directories for M-n menu
	dirCommand         string        // command to transform directory before launch
}

func NewFeedController(opts ...FeedOption) *FeedController {
	fc := &FeedController{
		dismissed:   make(map[string]bool),
		baseSession: "att",
		fifoPath:    fmt.Sprintf("/tmp/att-feed-%d.fifo", os.Getpid()),
		command:     "claude",
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

func WithCommand(cmd string) FeedOption {
	return func(fc *FeedController) { fc.command = cmd }
}

func WithProjects(projects []string) FeedOption {
	return func(fc *FeedController) { fc.projects = projects }
}

func WithDirCommand(cmd string) FeedOption {
	return func(fc *FeedController) { fc.dirCommand = cmd }
}

// cursorPos resolves the cursor (tmux window Index) to a slice position
// in allWindows. Returns -1 if the window no longer exists.
func (fc *FeedController) cursorPos() int {
	for i, w := range fc.allWindows {
		if w.Index == fc.cursor {
			return i
		}
	}
	return -1
}

func (fc *FeedController) Run() error {
	// Clean up stale feed sessions and FIFOs from previous crashes
	fc.cleanupStale()

	// Initialize snooze store
	if fc.snooze == nil {
		home, _ := os.UserHomeDir()
		fc.snooze = LoadSnooze(filepath.Join(home, ".config", "att", "snooze.json"))
	}

	// Initialize priority store
	if fc.priority == nil {
		home, _ := os.UserHomeDir()
		fc.priority = LoadPriority(filepath.Join(home, ".config", "att", "priority.json"))
	}

	// Initialize pin store
	if fc.pin == nil {
		home, _ := os.UserHomeDir()
		fc.pin = LoadPin(filepath.Join(home, ".config", "att", "pin.json"))
	}

	// Ensure base att tmux session exists
	if !HasSession(fc.baseSession) {
		// Create with a blank placeholder (not a shell) — use M-n to start a real session
		if _, err := NewSession(fc.baseSession, "_init", "", "tail -f /dev/null"); err != nil {
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

	fc.bindKeys()
	defer fc.unbindKeys()
	defer fc.restoreStatus()

	// Handle signals for clean shutdown (SIGHUP from tmux session destroy)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

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
			// A single read may contain multiple newline-delimited commands
			// (e.g. rapid M-a then M-i writes "toggleall\npin\n" in one read).
			for _, line := range strings.Split(string(buf[:n]), "\n") {
				cmd := strings.TrimSpace(line)
				if cmd != "" {
					cmdCh <- cmd
				}
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
			case cmd == "pin":
				fc.togglePin()
			case strings.HasPrefix(cmd, "priority "):
				fc.setPriority(strings.TrimPrefix(cmd, "priority "))
			case strings.HasPrefix(cmd, "snooze "):
				fc.snoozeAndAdvance(strings.TrimPrefix(cmd, "snooze "))
			case strings.HasPrefix(cmd, "new "):
				dir := strings.TrimPrefix(cmd, "new ")
				fc.newSession(dir)
			case cmd == "find", strings.HasPrefix(cmd, "find "):
				query := ""
				if strings.HasPrefix(cmd, "find ") {
					query = strings.TrimPrefix(cmd, "find ")
				}
				fc.find(query)
			case strings.HasPrefix(cmd, "goto "):
				fc.gotoWindow(strings.TrimPrefix(cmd, "goto "))
			}

		case <-fullTicker.C:
			fc.refresh()
			fc.updateStatusBar()
			// Defensive re-bind: restore bindings lost to tmux restarts or config reloads
			fc.bindKeys()

		case <-fastTicker.C:
			if fc.refreshAssigned() {
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

	// Read attention state from hook-written files
	fc.attention = claude.ReadAttentionSet()

	// Clean up attention files for dead transcripts
	claude.CleanupStaleAttention(24 * time.Hour)

	windows, err := ListWindows(fc.baseSession)
	if err != nil {
		return
	}
	windows = fc.cleanupPlaceholder(windows)
	fc.allWindows = windows

	// Assign sessions to windows (filters out stale sessions)
	fc.stateByWindow = assignSessionsToWindows(windows, sessions, fc.attention)

	fc.refreshPromptCache()
	fc.updateDisplay()
}

type promptCacheEntry struct {
	modTime time.Time
	text    string
}

// refreshPromptCache extracts user prompts from session JSONL files,
// skipping sessions whose files haven't changed since the last extraction.
func (fc *FeedController) refreshPromptCache() {
	if fc.promptCache == nil {
		fc.promptCache = make(map[string]promptCacheEntry)
	}

	active := make(map[string]bool)
	for _, s := range fc.discoveredSessions {
		if s.SessionFile == "" {
			continue
		}
		active[s.SessionFile] = true

		if cached, ok := fc.promptCache[s.SessionFile]; ok && !s.ModTime.After(cached.modTime) {
			continue
		}

		prompts := claude.ExtractUserPrompts(s.SessionFile, 8)
		var parts []string
		for _, p := range prompts {
			if idx := strings.IndexByte(p, '\n'); idx >= 0 {
				p = p[:idx]
			}
			if len(p) > 80 {
				p = p[:80]
			}
			if p != "" {
				parts = append(parts, p)
			}
		}

		fc.promptCache[s.SessionFile] = promptCacheEntry{
			modTime: s.ModTime,
			text:    strings.Join(parts, " | "),
		}
	}

	// Clean up entries for sessions no longer discovered
	for key := range fc.promptCache {
		if !active[key] {
			delete(fc.promptCache, key)
		}
	}
}

// refreshWindows is a lightweight alternative to refresh() that skips the
// expensive DiscoverSessions scan. It re-fetches the tmux window list (one
// fast tmux command, ~10ms) and reassigns sessions from the cached
// discoveredSessions. Used after newSession() where we need the updated
// window list but don't need to rescan the filesystem.
func (fc *FeedController) refreshWindows() {
	windows, err := ListWindows(fc.baseSession)
	if err != nil {
		return
	}
	windows = fc.cleanupPlaceholder(windows)
	fc.allWindows = windows
	fc.stateByWindow = assignSessionsToWindows(windows, fc.discoveredSessions, fc.attention)
	fc.updateDisplay()
}

// refreshAssigned re-reads the attention directory (one os.ReadDir call)
// and returns true if the attention set changed.
func (fc *FeedController) refreshAssigned() bool {
	updated := claude.ReadAttentionSet()

	// Compare with previous attention set
	changed := len(updated) != len(fc.attention)
	if !changed {
		for k := range updated {
			if !fc.attention[k] {
				changed = true
				break
			}
		}
	}

	if changed {
		fc.attention = updated
	}
	return changed
}

// updateDisplay rebuilds the attention queue, clears stale dismissals,
// renames windows, clamps the cursor, and renders the session line.
func (fc *FeedController) updateDisplay() {
	// Build attention set from assigned sessions + attention directory
	needsAttention := make(map[int]bool) // keyed by window index
	for i, s := range fc.stateByWindow {
		if fc.attention[s.SessionFile] {
			needsAttention[i] = true
		}
	}

	// Clear dismissed windows that no longer need attention
	for wID := range fc.dismissed {
		stillNeeded := false
		for i, w := range fc.allWindows {
			if w.ID == wID && needsAttention[i] {
				stillNeeded = true
				break
			}
		}
		if !stillNeeded {
			delete(fc.dismissed, wID)
		}
	}

	// Update window names with asterisk and build attention queue
	fc.attentionQueue = fc.attentionQueue[:0]
	for i, w := range fc.allWindows {
		hasAsterisk := strings.HasSuffix(w.Name, "*")
		needs := needsAttention[i]

		isDismissed := fc.dismissed[w.ID]
		isSnoozed := false
		if fc.snooze != nil {
			isSnoozed = fc.snooze.IsSnoozed(w.ID)
		}

		if needs && !isDismissed && !isSnoozed {
			fc.attentionQueue = append(fc.attentionQueue, i)
			if !hasAsterisk {
				RenameWindow(fc.baseSession, w.Index, w.Name+"*")
			}
		} else if hasAsterisk {
			RenameWindow(fc.baseSession, w.Index, strings.TrimSuffix(w.Name, "*"))
		}
	}

	// Sort attention queue by priority (lower number = higher priority).
	// SliceStable preserves window-index order within the same priority level.
	if fc.priority != nil {
		sort.SliceStable(fc.attentionQueue, func(i, j int) bool {
			wi := fc.allWindows[fc.attentionQueue[i]]
			wj := fc.allWindows[fc.attentionQueue[j]]
			return fc.priority.Get(wi.ID) < fc.priority.Get(wj.ID)
		})
	}

	// Save attention-only count, then append pinned-but-not-already-queued windows
	fc.attentionCount = len(fc.attentionQueue)
	if fc.pin != nil {
		inQueue := make(map[int]bool)
		for _, wi := range fc.attentionQueue {
			inQueue[wi] = true
		}
		var pinned []int
		for i, w := range fc.allWindows {
			if w.ID != "" && fc.pin.IsPinned(w.ID) && !inQueue[i] {
				pinned = append(pinned, i)
			}
		}
		if fc.priority != nil {
			sort.SliceStable(pinned, func(a, b int) bool {
				wa := fc.allWindows[pinned[a]]
				wb := fc.allWindows[pinned[b]]
				return fc.priority.Get(wa.ID) < fc.priority.Get(wb.ID)
			})
		}
		fc.attentionQueue = append(fc.attentionQueue, pinned...)
	}

	// Clamp cursor: if the window it points to no longer exists, fall back
	if len(fc.allWindows) > 0 && fc.cursorPos() == -1 {
		fc.cursor = fc.allWindows[0].Index
	}
	if len(fc.allWindows) == 0 {
		fc.cursor = ""
	}

	fc.renderSessionLine()
}

func (fc *FeedController) focusCurrent() {
	if fc.cursor == "" {
		return
	}
	SelectWindow(fc.sessionName, fc.cursor)
}

func (fc *FeedController) next() {
	if fc.showAll {
		if len(fc.allWindows) == 0 {
			return
		}
		pos := fc.cursorPos()
		if pos == -1 {
			pos = 0
		} else {
			pos = (pos + 1) % len(fc.allWindows)
		}
		fc.cursor = fc.allWindows[pos].Index
	} else {
		if len(fc.attentionQueue) == 0 {
			return
		}
		qi := fc.nextInQueue(1)
		fc.cursor = fc.allWindows[qi].Index
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
		pos := fc.cursorPos()
		if pos == -1 {
			pos = 0
		} else {
			pos = (pos - 1 + len(fc.allWindows)) % len(fc.allWindows)
		}
		fc.cursor = fc.allWindows[pos].Index
	} else {
		if len(fc.attentionQueue) == 0 {
			return
		}
		qi := fc.nextInQueue(-1)
		fc.cursor = fc.allWindows[qi].Index
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
		if fc.allWindows[wi].Index == fc.cursor {
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

	// Add current window to dismissed set
	if pos := fc.cursorPos(); pos >= 0 {
		w := fc.allWindows[pos]
		if w.ID != "" {
			fc.dismissed[w.ID] = true
		}
	}

	fc.removeFromQueueAndAdvance()
}

func (fc *FeedController) snoozeAndAdvance(durStr string) {
	if fc.snooze == nil {
		return
	}

	pos := fc.cursorPos()
	if pos < 0 {
		return
	}
	w := fc.allWindows[pos]
	if w.ID == "" {
		return
	}

	// Parse duration
	var until time.Time
	switch durStr {
	case "tomorrow":
		// Next day at 9am local time
		now := time.Now()
		tomorrow := now.AddDate(0, 0, 1)
		until = time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), 9, 0, 0, 0, now.Location())
	default:
		dur, err := time.ParseDuration(durStr)
		if err != nil {
			return
		}
		until = time.Now().Add(dur)
	}

	fc.snooze.Snooze(w.ID, until)
	fc.removeFromQueueAndAdvance()
}

func (fc *FeedController) setPriority(levelStr string) {
	if fc.priority == nil {
		return
	}
	level, err := strconv.Atoi(strings.TrimSpace(levelStr))
	if err != nil || level < 0 || level > 4 {
		return
	}
	pos := fc.cursorPos()
	if pos < 0 {
		return
	}
	w := fc.allWindows[pos]
	if w.ID == "" {
		return
	}
	fc.priority.Set(w.ID, level)
	fc.updateDisplay()
	fc.updateStatusBar()
}

func (fc *FeedController) togglePin() {
	if fc.pin == nil {
		return
	}
	// Use the actual active tmux window so M-i works on whatever
	// session the user is viewing, even if it's not in the attention queue.
	targetIdx := fc.cursor
	if fc.sessionName != "" {
		if idx, err := ActiveWindowIndex(fc.sessionName); err == nil && idx != "" {
			targetIdx = idx
		}
	}
	pos := -1
	for i, w := range fc.allWindows {
		if w.Index == targetIdx {
			pos = i
			break
		}
	}
	if pos < 0 {
		return
	}
	w := fc.allWindows[pos]
	if w.ID == "" {
		return
	}
	fc.pin.Toggle(w.ID)
	fc.cursor = targetIdx
	fc.updateDisplay()
	fc.updateStatusBar()
}

// removeFromQueueAndAdvance removes the current cursor position from the
// attention queue and advances to the next item. Used by dismiss and snooze.
func (fc *FeedController) removeFromQueueAndAdvance() {
	curIdx := -1
	for i, wi := range fc.attentionQueue {
		if fc.allWindows[wi].Index == fc.cursor {
			curIdx = i
			break
		}
	}

	if curIdx != -1 {
		fc.attentionQueue = append(fc.attentionQueue[:curIdx], fc.attentionQueue[curIdx+1:]...)
		if len(fc.attentionQueue) > 0 {
			nextIdx := curIdx
			if nextIdx >= len(fc.attentionQueue) {
				nextIdx = 0
			}
			fc.cursor = fc.allWindows[fc.attentionQueue[nextIdx]].Index
		}
	} else if len(fc.attentionQueue) > 0 {
		fc.cursor = fc.allWindows[fc.attentionQueue[0]].Index
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
	windowName := filepath.Base(absDir)

	// Dir resolution runs inside the shell wrapper (after `read`) so the
	// tmux window appears instantly with the `> ` prompt.
	var dirSnippet string
	if fc.dirCommand != "" {
		dirSnippet = fmt.Sprintf(
			`d=$(%s 2>/dev/null); [ -n "$d" ] && cd "$d"; `,
			fc.dirCommand,
		)
	} else {
		dirSnippet = `d=$(git rev-parse --show-toplevel 2>/dev/null); [ -n "$d" ] && cd "$d"; `
	}

	wrappedCmd := fmt.Sprintf(
		`sh -c 'printf "> "; IFS= read -r p; %sif [ -n "$p" ]; then printf "%%s\n" "$p" | %s; else %s; fi'`,
		dirSnippet, fc.command, fc.command,
	)

	idx, err := NewWindow(fc.baseSession, windowName, absDir, wrappedCmd)
	if err != nil {
		return
	}

	fc.refreshWindows()

	// Focus the newly created window
	fc.cursor = idx
	fc.focusCurrent()
	fc.updateStatusBar()
}

// fuzzyMatch checks if pattern matches str. Prefers substring matches (scored
// by position) and falls back to fuzzy character-by-character matching.
// Returns (matched, score) where lower score is better.
func fuzzyMatch(pattern, str string) (bool, int) {
	p := strings.ToLower(pattern)
	s := strings.ToLower(str)

	// Substring match: score = position (prefix = 0 = best)
	if idx := strings.Index(s, p); idx >= 0 {
		return true, idx
	}

	// Fuzzy: each pattern char must appear in order
	pi := 0
	gaps := 0
	prev := -1
	for i := 0; i < len(s) && pi < len(p); i++ {
		if s[i] == p[pi] {
			if prev >= 0 && i > prev+1 {
				gaps += i - prev - 1
			}
			prev = i
			pi++
		}
	}
	if pi < len(p) {
		return false, 0
	}
	// Offset so fuzzy matches always rank below substring matches
	return true, 1000 + gaps
}

func (fc *FeedController) find(query string) {
	if len(fc.allWindows) == 0 {
		return
	}

	// Interactive mode: use fzf in a tmux popup if available
	if query == "" {
		if fzfPath, err := exec.LookPath("fzf"); err == nil {
			fc.findWithFzf(fzfPath)
			return
		}
	}

	// Fallback: fuzzy match + display-menu (or direct jump for single match)
	fc.findWithMenu(query)
}

// buildFzfLines generates the tab-delimited lines fed to fzf.
// Format: windowIndex\tprefix+name\tprompts
// Field 1 (windowIndex) is hidden by --with-nth=2.. but used to identify the selection.
// Field 2 (name) and field 3 (prompts) are displayed and searched.
func (fc *FeedController) buildFzfLines() []string {
	var lines []string
	for i, w := range fc.allWindows {
		s := fc.stateByWindow[i]
		name := s.Summary
		if name == "" {
			name = strings.TrimSuffix(w.Name, "*")
		}
		prefix := "  "
		if fc.attention[s.SessionFile] {
			prefix = "* "
		}

		// Look up pre-computed prompt text from cache
		var promptStr string
		if entry, ok := fc.promptCache[s.SessionFile]; ok {
			promptStr = entry.text
		}

		// Strip control characters that would corrupt the line-based format
		for _, bad := range []string{"\t", "\n", "\r"} {
			name = strings.ReplaceAll(name, bad, " ")
			promptStr = strings.ReplaceAll(promptStr, bad, " ")
		}

		lines = append(lines, fmt.Sprintf("%s\t%s%s\t%s", w.Index, prefix, name, promptStr))
	}
	return lines
}

func (fc *FeedController) findWithFzf(fzfPath string) {
	lines := fc.buildFzfLines()

	sessFile := fc.fifoPath + ".sessions"
	if err := os.WriteFile(sessFile, []byte(strings.Join(lines, "\n")+"\n"), 0600); err != nil {
		return
	}

	// Write a wrapper script to avoid quoting issues with tmux display-popup.
	// Uses absolute fzf path so the popup works even if PATH doesn't include it.
	// --with-nth=2.. shows name + prompts (fzf only searches displayed fields).
	scriptFile := fc.fifoPath + ".find.sh"
	script := fmt.Sprintf("#!/bin/sh\n"+
		"idx=$('%s' --prompt='Find: ' --with-nth=2.. --delimiter='\\t' --reverse --no-info < '%s')\n"+
		"rc=$?\n"+
		"rm -f '%s' '%s'\n"+
		"[ $rc -eq 0 ] && idx=$(printf '%%s' \"$idx\" | cut -f1) && echo \"goto $idx\" > '%s'\n",
		fzfPath, sessFile, sessFile, scriptFile, fc.fifoPath,
	)
	if err := os.WriteFile(scriptFile, []byte(script), 0700); err != nil {
		os.Remove(sessFile)
		return
	}

	exec.Command("tmux", "display-popup", "-t", fc.sessionName, "-w", "60%", "-h", "50%", "-E", scriptFile).Run()
}

func (fc *FeedController) findWithMenu(query string) {
	type result struct {
		windowIdx int
		name      string
		score     int
	}

	var results []result
	for i, w := range fc.allWindows {
		s := fc.stateByWindow[i]
		name := s.Summary
		if name == "" {
			name = strings.TrimSuffix(w.Name, "*")
		}

		if query == "" {
			results = append(results, result{windowIdx: i, name: name, score: i})
			continue
		}

		bestScore := -1
		for _, target := range []string{name, strings.TrimSuffix(w.Name, "*"), w.Path} {
			if ok, score := fuzzyMatch(query, target); ok {
				if bestScore < 0 || score < bestScore {
					bestScore = score
				}
			}
		}
		if bestScore >= 0 {
			results = append(results, result{windowIdx: i, name: name, score: bestScore})
		}
	}

	if len(results) == 0 {
		return
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].score < results[j].score
	})

	// Single result: jump directly
	if len(results) == 1 {
		fc.gotoWindow(fc.allWindows[results[0].windowIdx].Index)
		return
	}

	if len(results) > 10 {
		results = results[:10]
	}

	guard := fmt.Sprintf("[ -p %s ]", fc.fifoPath)
	args := []string{"display-menu", "-t", fc.sessionName, "-T", "Find"}
	for i, r := range results {
		shortcut := ""
		if i < 9 {
			shortcut = strconv.Itoa(i + 1)
		}
		idx := fc.allWindows[r.windowIdx].Index
		cmd := fmt.Sprintf("run-shell '%s && echo goto %s > %s || true'", guard, idx, fc.fifoPath)
		args = append(args, r.name, shortcut, cmd)
	}

	exec.Command("tmux", args...).Run()
}

func (fc *FeedController) gotoWindow(windowIndex string) {
	for _, w := range fc.allWindows {
		if w.Index == windowIndex {
			fc.cursor = windowIndex
			fc.focusCurrent()
			fc.updateStatusBar()
			fc.renderSessionLine()
			return
		}
	}
}

func (fc *FeedController) killCurrent() {
	pos := fc.cursorPos()
	if pos < 0 || len(fc.allWindows) == 0 {
		return
	}
	w := fc.allWindows[pos]

	// Clear any dismissed/snoozed/priority/pin state for this window
	if w.ID != "" {
		delete(fc.dismissed, w.ID)
		if fc.snooze != nil {
			fc.snooze.Unsnooze(w.ID)
		}
		if fc.priority != nil {
			fc.priority.Remove(w.ID)
		}
		if fc.pin != nil {
			fc.pin.Remove(w.ID)
		}
	}

	// Kill the tmux window (sends SIGHUP to claude, closing it)
	KillWindow(fc.baseSession, w.Index)

	// In-memory update: splice the killed window out of allWindows and
	// rebuild stateByWindow with shifted indices. This avoids the expensive
	// DiscoverSessions scan — the 5s periodic refresh reconciles with truth.
	fc.allWindows = append(fc.allWindows[:pos], fc.allWindows[pos+1:]...)

	newState := make(map[int]claude.Session, len(fc.stateByWindow))
	for i, s := range fc.stateByWindow {
		if i == pos {
			continue
		}
		if i > pos {
			newState[i-1] = s
		} else {
			newState[i] = s
		}
	}
	fc.stateByWindow = newState

	// Set cursor to the window now at the killed position (or last if killed was last)
	if len(fc.allWindows) > 0 {
		newPos := pos
		if newPos >= len(fc.allWindows) {
			newPos = len(fc.allWindows) - 1
		}
		fc.cursor = fc.allWindows[newPos].Index
	} else {
		fc.cursor = ""
	}

	fc.updateDisplay()
	fc.updateStatusBar()
	fc.focusCurrent()
}

func (fc *FeedController) updateStatusBar() {
	pos := fc.cursorPos()
	if pos < 0 || len(fc.allWindows) == 0 {
		SetStatusRightForSession(fc.sessionName, "att | No windows | ^Q quit")
		return
	}

	w := fc.allWindows[pos]
	name := strings.TrimSuffix(w.Name, "*")
	attn := fc.attentionCount
	displayCount := len(fc.attentionQueue) // includes pinned items

	// Prepend priority indicator when non-default
	if fc.priority != nil {
		if p := fc.priority.Get(w.ID); p != DefaultPriority {
			name = fmt.Sprintf("P%d %s", p, name)
		}
	}

	var status string
	if fc.showAll {
		status = fmt.Sprintf("att | %s [%d/%d] | ALL | M-a filter | ^Q quit",
			name, pos+1, len(fc.allWindows))
	} else if displayCount == 0 {
		status = fmt.Sprintf("att | %s [%d/%d] | All clear | M-a show all | ^Q quit",
			name, pos+1, len(fc.allWindows))
	} else if attn > 0 {
		status = fmt.Sprintf("att | %s [%d/%d] | %d need attention | M-a show all | ^Q quit",
			name, pos+1, len(fc.allWindows), attn)
	} else {
		status = fmt.Sprintf("att | %s [%d/%d] | %d pinned | M-a show all | ^Q quit",
			name, pos+1, len(fc.allWindows), displayCount)
	}
	SetStatusRightForSession(fc.sessionName, status)
}

func (fc *FeedController) bindKeys() {
	fifoTemplate := "/tmp/#{session_name}.fifo"
	guard := fmt.Sprintf("[ -p %s ]", fifoTemplate)

	for _, b := range []struct{ key, cmd string }{
		{"M-]", "next"},
		{"M-[", "prev"},
		{"M-Enter", "dismiss"},
		{"C-q", "quit"},
		{"M-a", "toggleall"},
		{"M-d", "kill"},
		{"M-i", "pin"},
	} {
		cmd := fmt.Sprintf("%s && echo %s > %s || true", guard, b.cmd, fifoTemplate)
		if err := BindKey(b.key, cmd); err != nil {
			log.Printf("att: bind %s failed: %v", b.key, err)
		}
	}

	snoozeCmd := func(dur string) string {
		return fmt.Sprintf("run-shell '%s && echo snooze %s > %s || true'", guard, dur, fifoTemplate)
	}
	if err := BindKeyDirect("M-z", "display-menu", "-T", "Snooze",
		"15 minutes", "1", snoozeCmd("15m"),
		"30 minutes", "2", snoozeCmd("30m"),
		"1 hour", "3", snoozeCmd("1h"),
		"2 hours", "4", snoozeCmd("2h"),
		"4 hours", "5", snoozeCmd("4h"),
		"Tomorrow", "6", snoozeCmd("tomorrow"),
	); err != nil {
		log.Printf("att: bind M-z failed: %v", err)
	}

	priorityCmd := func(level string) string {
		return fmt.Sprintf("run-shell '%s && echo priority %s > %s || true'", guard, level, fifoTemplate)
	}
	if err := BindKeyDirect("M-p", "display-menu", "-T", "Priority",
		"P0 - Critical", "0", priorityCmd("0"),
		"P1 - High", "1", priorityCmd("1"),
		"P2 - Medium", "2", priorityCmd("2"),
		"P3 - Low", "3", priorityCmd("3"),
		"P4 - Default", "4", priorityCmd("4"),
	); err != nil {
		log.Printf("att: bind M-p failed: %v", err)
	}

	// M-/: fuzzy find sessions (sends "find" to FIFO, handler opens fzf popup or menu)
	// Avoid M-f because many terminals send the same escape sequence for Alt+Right arrow.
	findCmd := fmt.Sprintf("%s && echo find > %s || true", guard, fifoTemplate)
	if err := BindKey("M-/", findCmd); err != nil {
		log.Printf("att: bind M-/ failed: %v", err)
	}

	newCmd := fmt.Sprintf("run-shell '%s && echo new %%%%1 > %s || true'", guard, fifoTemplate)
	if len(fc.projects) > 0 {
		menuArgs := []string{"display-menu", "-T", "New session"}
		for i, p := range fc.projects {
			name := filepath.Base(p)
			shortcut := ""
			if i < 9 {
				shortcut = strconv.Itoa(i + 1)
			}
			cmd := fmt.Sprintf("run-shell '%s && echo new %s > %s || true'", guard, p, fifoTemplate)
			menuArgs = append(menuArgs, name, shortcut, cmd)
		}
		// Separator then free-text fallback
		menuArgs = append(menuArgs, "")
		customCmd := fmt.Sprintf(`command-prompt -I #{pane_current_path} -p "New session:" "%s"`, newCmd)
		menuArgs = append(menuArgs, "Custom...", "c", customCmd)
		if err := BindKeyDirect("M-n", menuArgs...); err != nil {
			log.Printf("att: bind M-n failed: %v", err)
		}
	} else {
		if err := BindKeyDirect("M-n",
			"command-prompt", "-I", "#{pane_current_path}", "-p", "New session:",
			fmt.Sprintf("run-shell '%s && echo new %%%%1 > %s || true'", guard, fifoTemplate),
		); err != nil {
			log.Printf("att: bind M-n failed: %v", err)
		}
	}
}

func (fc *FeedController) unbindKeys() {
	// Check if other feed sessions are still running — if so, keep bindings
	out, _ := exec.Command("tmux", "list-sessions", "-F", "#{session_name}").Output()
	for _, name := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.HasPrefix(name, "att-feed-") && name != fc.sessionName {
			return // other feed still running, keep bindings
		}
	}
	for _, key := range []string{"M-]", "M-[", "M-Enter", "C-q", "M-a", "M-d", "M-i", "M-z", "M-p", "M-/", "M-n"} {
		UnbindKey(key)
	}
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


