package claude

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Session struct {
	WorkspacePath string
	ProjectName   string
	SessionFile   string
	ModTime       time.Time
	Summary       string // from sessions-index.json or custom-title
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
			session, err := parseSessionMetadata(jf.path)
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

func parseSessionMetadata(filePath string) (Session, error) {
	session := Session{
		SessionFile: filePath,
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

// ExtractUserPrompts reads a session JSONL file and returns up to limit
// user prompt texts. For large files, reads the first 8KB (for the initial
// prompt) and the last 64KB (for recent prompts).
func ExtractUserPrompts(sessionFile string, limit int) []string {
	const maxHead = 8 * 1024
	const maxTail = 64 * 1024

	f, err := os.Open(sessionFile)
	if err != nil {
		return nil
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return nil
	}

	var headData, tailData []byte
	if fi.Size() <= maxHead+maxTail {
		// Small file: read the whole thing
		all, err := io.ReadAll(f)
		if err != nil {
			return nil
		}
		headData = all
	} else {
		// Read first 8KB for the initial prompt
		head := make([]byte, maxHead)
		n, _ := f.Read(head)
		headData = head[:n]

		// Read last 64KB for recent prompts
		tail := make([]byte, maxTail)
		n, err = f.ReadAt(tail, fi.Size()-maxTail)
		if err != nil && err != io.EOF {
			tailData = nil
		} else {
			tailData = tail[:n]
			// Skip first partial line
			if idx := bytes.IndexByte(tailData, '\n'); idx >= 0 {
				tailData = tailData[idx+1:]
			}
		}
	}

	headPrompts := scanMessageTexts(headData)
	tailPrompts := scanMessageTexts(tailData)

	// Deduplicate: head prompts first, then tail
	seen := make(map[string]bool)
	var prompts []string
	for _, p := range headPrompts {
		if !seen[p] {
			seen[p] = true
			prompts = append(prompts, p)
		}
	}
	for _, p := range tailPrompts {
		if !seen[p] {
			seen[p] = true
			prompts = append(prompts, p)
		}
	}

	if len(prompts) > limit {
		// Keep first prompt + most recent
		result := make([]string, 0, limit)
		result = append(result, prompts[0])
		result = append(result, prompts[len(prompts)-limit+1:]...)
		prompts = result
	}
	return prompts
}

func scanMessageTexts(data []byte) []string {
	var texts []string
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		// Fast pre-filter: only parse user or assistant entries
		isUser := bytes.Contains(line, []byte(`"type":"user"`)) ||
			bytes.Contains(line, []byte(`"type": "user"`))
		isAssistant := bytes.Contains(line, []byte(`"type":"assistant"`)) ||
			bytes.Contains(line, []byte(`"type": "assistant"`))
		if !isUser && !isAssistant {
			continue
		}

		var entry struct {
			Type    string `json:"type"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &entry) != nil {
			continue
		}
		if entry.Type != "user" && entry.Type != "assistant" {
			continue
		}

		text := extractPromptText(entry.Message.Content)
		if text != "" && isRealPrompt(text) {
			texts = append(texts, text)
		}
	}
	return texts
}

// isRealPrompt filters out system/interrupt messages that aren't actual user prompts.
// Claude Code injects various system messages as "type":"user" entries in JSONL;
// these are noise for search purposes.
func isRealPrompt(text string) bool {
	// Skip very short or whitespace-only text
	if len(strings.TrimSpace(text)) < 3 {
		return false
	}
	// System-injected messages that appear as user entries
	for _, prefix := range []string{
		"[Request interrupted",
		"<teammate-message",
		"<task-notification",
		"<local-command-caveat",
		"<bash-input>",
		"<bash-stdout>",
		"<bash-stderr>",
		"<system-reminder",
		"<tool-use-prompt",
		"<user-prompt-submit-hook",
	} {
		if strings.HasPrefix(text, prefix) {
			return false
		}
	}
	return true
}

// extractPromptText extracts the user's text from a message content field,
// which can be a JSON string or an array of content blocks.
func extractPromptText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	// Try string first (most common for user prompts)
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}

	// Try array of content blocks — skip tool_result entries
	var blocks []json.RawMessage
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	for _, block := range blocks {
		var b struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(block, &b) == nil && b.Type == "text" && b.Text != "" {
			return b.Text
		}
	}

	return ""
}

func parseMetadata(head string, session *Session) {
	var entry struct {
		CWD string `json:"cwd,omitempty"`
	}
	for _, line := range strings.Split(head, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
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
