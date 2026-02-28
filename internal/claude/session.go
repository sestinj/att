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

		for _, jf := range files {
			session, err := parseSessionState(jf.path)
			if err != nil {
				continue
			}
			session.ModTime = jf.modTime
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

		case "user":
			if entry.Message != nil {
				state = StateWorking
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
			// stop_hook_summary, turn_duration, compact_boundary, local_command:
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
		// Null stop_reason: streaming partial or old-version final entry.
		// Inspect content blocks to determine state.
		hasToolUse := false
		hasText := false
		for _, block := range msg.Content {
			switch block.Type {
			case "tool_use":
				hasToolUse = true
			case "text":
				hasText = true
			}
		}
		if hasToolUse {
			return classifyToolUse(msg.Content)
		}
		if hasText {
			// Text-only with null stop_reason = end of turn on old versions
			return StateIdle
		}
		// Thinking block or empty content = response in progress
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

	return session, nil
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
