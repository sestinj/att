package claude

import (
	"bytes"
	"encoding/json"
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
