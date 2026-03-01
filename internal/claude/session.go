package claude

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type SessionState int

const (
	StateUnknown        SessionState = iota
	StateWorking                     // Claude is executing tools or generating a response
	StateIdle                        // Claude finished, waiting for user input
	StateAsking                      // Claude used AskUserQuestion
	StatePlanMode                    // Claude used ExitPlanMode
	StateToolPermission              // Claude called a tool, waiting for user to approve
)

func (s SessionState) String() string {
	switch s {
	case StateWorking:
		return "Working"
	case StateIdle:
		return "Idle"
	case StateAsking:
		return "Asking"
	case StatePlanMode:
		return "Plan"
	case StateToolPermission:
		return "Permission"
	default:
		return "Unknown"
	}
}

type Session struct {
	WorkspacePath string
	ProjectName   string
	SessionFile   string
	State         SessionState
	ModTime       time.Time
	Summary       string // from sessions-index.json or custom-title
}

func (s Session) NeedsAttention() bool {
	return s.State == StateIdle || s.State == StateAsking || s.State == StatePlanMode || s.State == StateToolPermission
}

// DiscoverSessions finds active Claude Code sessions by scanning JSONL history files.
func DiscoverSessions(maxAge time.Duration) ([]Session, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	projectsDir := filepath.Join(homeDir, ".claude", "projects")
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return nil, err
	}

	cutoff := time.Now().Add(-maxAge)
	var sessions []Session

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		dirPath := filepath.Join(projectsDir, entry.Name())
		files := findRecentJSONLs(dirPath, cutoff)
		if len(files) == 0 {
			continue
		}

		index := loadSessionIndex(dirPath)

		for _, jf := range files {
			session, err := parseSessionState(jf.path)
			if err != nil {
				continue
			}
			session.ModTime = jf.modTime

			// Apply summary: custom-title (set during parse) > index > empty
			if session.Summary == "" {
				sessionID := strings.TrimSuffix(filepath.Base(jf.path), ".jsonl")
				if summary, ok := index[sessionID]; ok {
					session.Summary = summary
				}
			}

			sessions = append(sessions, session)
		}
	}

	return sessions, nil
}

type jsonlFile struct {
	path    string
	modTime time.Time
}

// findRecentJSONLs returns all JSONL files in dir modified after cutoff.
func findRecentJSONLs(dir string, cutoff time.Time) []jsonlFile {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var files []jsonlFile
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(cutoff) {
			files = append(files, jsonlFile{
				path:    filepath.Join(dir, entry.Name()),
				modTime: info.ModTime(),
			})
		}
	}
	return files
}

type jsonlEntry struct {
	Type        string           `json:"type"`
	Subtype     string           `json:"subtype,omitempty"`
	CWD         string           `json:"cwd,omitempty"`
	SessionID   string           `json:"sessionId,omitempty"`
	Message     *messageEnvelope `json:"message,omitempty"`
	IsSidechain bool             `json:"isSidechain,omitempty"`
	Data        *progressData    `json:"data,omitempty"`
}

type progressData struct {
	Type string `json:"type"`
}

type messageEnvelope struct {
	StopReason *string        `json:"stop_reason"`
	Content    []contentBlock `json:"-"`
}

// UnmarshalJSON handles both array and string content fields in JSONL entries.
// User entries sometimes have content as a raw string instead of an array.
func (m *messageEnvelope) UnmarshalJSON(data []byte) error {
	var raw struct {
		StopReason *string         `json:"stop_reason"`
		Content    json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	m.StopReason = raw.StopReason
	var blocks []contentBlock
	if json.Unmarshal(raw.Content, &blocks) == nil {
		m.Content = blocks
	}
	return nil
}

type contentBlock struct {
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
}

// ClassifySessionState determines the current state of a Claude Code session
// from raw JSONL content. It processes all entries forward, tracking state
// transitions:
//
//   - assistant entries set the state based on stop_reason and content
//   - user entries reset state to Working (Claude will process them)
//   - progress entries upgrade ToolPermission to Working (tool is executing)
//   - system entries handle api errors (retrying = Working)
//   - non-conversation entries are skipped entirely
//
// Unknown entry types default to not needing attention (no state change).
func ClassifySessionState(data []byte) SessionState {
	state := StateUnknown
	// Tracks whether a user message appeared after the last assistant entry.
	// Guards against a race where stop_hook_summary arrives after the next
	// user message due to async hook execution.
	var userAfterAssistant bool

	for _, line := range bytes.Split(data, []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}

		var entry jsonlEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}

		if entry.IsSidechain {
			continue
		}

		switch entry.Type {
		case "assistant":
			if entry.Message == nil {
				continue
			}
			state = classifyAssistantMessage(entry.Message)
			userAfterAssistant = false

		case "user":
			if entry.Message != nil {
				state = StateWorking
				userAfterAssistant = true
			}

		case "progress":
			if entry.Data != nil {
				switch entry.Data.Type {
				case "bash_progress", "mcp_progress", "agent_progress", "waiting_for_task":
					// Execution progress means the tool is actually running,
					// not waiting for permission.
					if state == StateToolPermission {
						state = StateWorking
					}
				// hook_progress: does NOT indicate tool execution, no state change.
				}
			}

		case "system":
			switch entry.Subtype {
			case "api_error":
				// Claude Code is retrying an API call — still working.
				state = StateWorking
			case "stop_hook_summary", "turn_duration":
				// Turn-completion signals. Claude Code often writes the last
				// assistant entry with stop_reason=null (streaming partial),
				// so these are the reliable indicators that the turn ended.
				// Guard: only transition if no user message came after the
				// assistant entry (race condition from async hooks).
				if state == StateWorking && !userAfterAssistant {
					state = StateIdle
				}
			// compact_boundary, local_command:
			// Post-turn bookkeeping or informational. No state change.
			}

		// Non-conversation entry types: no state change.
		// file-history-snapshot, queue-operation, pr-link, custom-title, agent-name,
		// and any unknown future types are ignored — defaulting to not needing attention.
		}
	}

	return state
}

func classifyAssistantMessage(msg *messageEnvelope) SessionState {
	sr := ""
	if msg.StopReason != nil {
		sr = *msg.StopReason
	}

	switch sr {
	case "end_turn", "stop_sequence":
		return StateIdle

	case "tool_use":
		return classifyToolUse(msg.Content)

	case "":
		// Null stop_reason = streaming partial. But Claude Code often doesn't
		// write the final entry with the real stop_reason. If the content
		// already has interactive tool blocks, classify by the tool.
		toolState := classifyToolUse(msg.Content)
		if toolState == StateAsking || toolState == StatePlanMode {
			return toolState
		}
		return StateWorking

	default:
		return StateWorking
	}
}

func classifyToolUse(content []contentBlock) SessionState {
	for _, block := range content {
		if block.Type == "tool_use" {
			switch block.Name {
			case "AskUserQuestion":
				return StateAsking
			case "ExitPlanMode":
				return StatePlanMode
			}
		}
	}
	// Regular tool call with no subsequent execution = waiting for permission
	return StateToolPermission
}

// ParseSessionFile reads a single JSONL session file and returns its state.
// Use this for targeted re-reads of known files (e.g. fast refresh) instead
// of running a full DiscoverSessions scan.
func ParseSessionFile(filePath string) (Session, error) {
	return parseSessionState(filePath)
}

func parseSessionState(filePath string) (Session, error) {
	session := Session{
		SessionFile: filePath,
		State:       StateUnknown,
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return session, err
	}

	headLen := 4096
	if len(data) < headLen {
		headLen = len(data)
	}
	parseMetadata(string(data[:headLen]), &session)

	session.State = ClassifySessionState(data)
	session.Summary = extractCustomTitle(data)

	return session, nil
}

// loadSessionIndex reads sessions-index.json from a project directory and
// returns a map of sessionId -> summary. Returns an empty map on any error.
func loadSessionIndex(dirPath string) map[string]string {
	data, err := os.ReadFile(filepath.Join(dirPath, "sessions-index.json"))
	if err != nil {
		return nil
	}

	var idx struct {
		Entries []struct {
			SessionID string `json:"sessionId"`
			Summary   string `json:"summary"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil
	}

	m := make(map[string]string, len(idx.Entries))
	for _, e := range idx.Entries {
		if e.SessionID != "" && e.Summary != "" {
			m[e.SessionID] = e.Summary
		}
	}
	return m
}

// extractCustomTitle scans JSONL data for custom-title entries and returns
// the last customTitle value found, or empty string if none.
func extractCustomTitle(data []byte) string {
	var title string
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		if len(line) == 0 || !bytes.Contains(line, []byte(`"custom-title"`)) {
			continue
		}
		var entry struct {
			Type        string `json:"type"`
			CustomTitle string `json:"customTitle"`
		}
		if json.Unmarshal(line, &entry) == nil && entry.Type == "custom-title" && entry.CustomTitle != "" {
			title = entry.CustomTitle
		}
	}
	return title
}

func parseMetadata(head string, session *Session) {
	for _, line := range strings.Split(head, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry jsonlEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry.CWD != "" && session.WorkspacePath == "" {
			session.WorkspacePath = entry.CWD
			session.ProjectName = filepath.Base(entry.CWD)
		}
		if session.WorkspacePath != "" {
			break
		}
	}
}
