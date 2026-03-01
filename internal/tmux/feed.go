package tmux

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sestinj/att/internal/claude"
	"github.com/sestinj/att/internal/config"
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
	snooze             *SnoozeStore           // time-based snooze for sessions
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

func (fc *FeedController) Run() error {
	// Clean up stale feed sessions and FIFOs from previous crashes
	fc.cleanupStale()

	// Initialize snooze store
	if fc.snooze == nil {
		home, _ := os.UserHomeDir()
		fc.snooze = LoadSnooze(filepath.Join(home, ".config", "att", "snooze.json"))
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
			case strings.HasPrefix(cmd, "snooze "):
				fc.snoozeAndAdvance(strings.TrimPrefix(cmd, "snooze "))
			case strings.HasPrefix(cmd, "new "):
				dir := strings.TrimPrefix(cmd, "new ")
				fc.newSession(dir)
			}

		case <-fullTicker.C:
			fc.refresh()
			fc.updateStatusBar()
			// Defensive re-bind: restore bindings lost to tmux restarts or config reloads
			fc.bindKeys()

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
	windows = fc.cleanupPlaceholder(windows)
	fc.allWindows = windows

	// Assign sessions to windows (filters out stale sessions)
	fc.stateByWindow = assignSessionsToWindows(windows, sessions)

	fc.applyStaleDetection()
	fc.updateDisplay()
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
	fc.stateByWindow = assignSessionsToWindows(windows, fc.discoveredSessions)
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
		isSnoozed := false
		if s, ok := fc.stateByWindow[i]; ok {
			isDismissed = fc.dismissed[s.SessionFile]
			if fc.snooze != nil {
				isSnoozed = fc.snooze.IsSnoozed(s.SessionFile)
			}
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

	// Clamp cursor
	if len(fc.allWindows) > 0 && fc.cursor >= len(fc.allWindows) {
		fc.cursor = len(fc.allWindows) - 1
	}
	if len(fc.allWindows) == 0 {
		fc.cursor = 0
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

	fc.removeFromQueueAndAdvance()
}

func (fc *FeedController) snoozeAndAdvance(durStr string) {
	if fc.snooze == nil {
		return
	}

	// Get current window's session file
	s, ok := fc.stateByWindow[fc.cursor]
	if !ok || s.SessionFile == "" {
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

	fc.snooze.Snooze(s.SessionFile, until)
	fc.removeFromQueueAndAdvance()
}

// removeFromQueueAndAdvance removes the current cursor position from the
// attention queue and advances to the next item. Used by dismiss and snooze.
func (fc *FeedController) removeFromQueueAndAdvance() {
	curIdx := -1
	for i, wi := range fc.attentionQueue {
		if wi == fc.cursor {
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
			fc.cursor = fc.attentionQueue[nextIdx]
		}
	} else if len(fc.attentionQueue) > 0 {
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
	windowName := filepath.Base(absDir)

	resolved, err := config.ResolveDir(fc.dirCommand, absDir)
	if err != nil {
		exec.Command("tmux", "display-message", fmt.Sprintf("dir_command failed: %v", err)).Run()
		return
	}
	absDir = resolved

	idx, err := NewWindow(fc.baseSession, windowName, absDir, fc.command)
	if err != nil {
		return
	}

	fc.refreshWindows()

	// Focus the newly created window
	for i, w := range fc.allWindows {
		if w.Index == idx {
			fc.cursor = i
			fc.focusCurrent()
			break
		}
	}
	fc.updateStatusBar()
}

func (fc *FeedController) killCurrent() {
	if len(fc.allWindows) == 0 {
		return
	}
	w := fc.allWindows[fc.cursor]

	// Clear any dismissed/snoozed state for this window's session
	if s, ok := fc.stateByWindow[fc.cursor]; ok && s.SessionFile != "" {
		delete(fc.dismissed, s.SessionFile)
		if fc.snooze != nil {
			fc.snooze.Unsnooze(s.SessionFile)
		}
	}

	// Kill the tmux window (sends SIGHUP to claude, closing it)
	KillWindow(fc.baseSession, w.Index)

	// In-memory update: splice the killed window out of allWindows and
	// rebuild stateByWindow with shifted indices. This avoids the expensive
	// DiscoverSessions scan — the 5s periodic refresh reconciles with truth.
	killedIdx := fc.cursor
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

	fc.applyStaleDetection()
	fc.updateDisplay()
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
	fifoTemplate := "/tmp/#{session_name}.fifo"
	guard := fmt.Sprintf("[ -p %s ]", fifoTemplate)

	for _, b := range []struct{ key, cmd string }{
		{"M-]", "next"},
		{"M-[", "prev"},
		{"M-Enter", "dismiss"},
		{"C-q", "quit"},
		{"M-a", "toggleall"},
		{"M-d", "kill"},
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
	for _, key := range []string{"M-]", "M-[", "M-Enter", "C-q", "M-a", "M-d", "M-z", "M-n"} {
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


