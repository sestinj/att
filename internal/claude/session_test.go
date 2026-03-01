package claude

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- JSONL fixture helpers ---

func metadataEntry(cwd string) string {
	return fmt.Sprintf(`{"type":"user","cwd":"%s","sessionId":"test-session"}`, cwd)
}

func assistantEndTurn() string {
	return `{"type":"assistant","isSidechain":false,"message":{"stop_reason":"end_turn","content":[{"type":"text"}]}}`
}

func writeJSONL(t *testing.T, path string, lines ...string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, line := range lines {
		f.WriteString(line + "\n")
	}
}

// --- parseSessionMetadata tests ---

func TestParseSessionMetadata_ExtractsWorkspace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	writeJSONL(t, path,
		metadataEntry("/Users/nate/gh/myproject"),
		assistantEndTurn(),
	)

	session, err := parseSessionMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if session.WorkspacePath != "/Users/nate/gh/myproject" {
		t.Errorf("expected workspace /Users/nate/gh/myproject, got %q", session.WorkspacePath)
	}
	if session.ProjectName != "myproject" {
		t.Errorf("expected project myproject, got %q", session.ProjectName)
	}
}

func TestParseSessionMetadata_NoAssistantEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	writeJSONL(t, path,
		metadataEntry("/tmp/proj"),
	)

	session, err := parseSessionMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if session.WorkspacePath != "/tmp/proj" {
		t.Errorf("expected workspace /tmp/proj, got %q", session.WorkspacePath)
	}
}

func TestParseSessionMetadata_LargeFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")

	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}

	f.WriteString(metadataEntry("/Users/nate/gh/bigproject") + "\n")

	filler := `{"type":"progress","data":{"type":"hook_progress"}}` + "\n"
	for i := 0; i < 500; i++ {
		f.WriteString(filler)
	}
	f.Close()

	session, err := parseSessionMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if session.WorkspacePath != "/Users/nate/gh/bigproject" {
		t.Errorf("expected workspace from head, got %q", session.WorkspacePath)
	}
}

func TestParseSessionMetadata_CustomTitle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	writeJSONL(t, path,
		metadataEntry("/tmp/proj"),
		`{"type":"custom-title","customTitle":"my named session"}`,
		assistantEndTurn(),
	)

	session, err := parseSessionMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if session.Summary != "my named session" {
		t.Errorf("expected 'my named session', got %q", session.Summary)
	}
}

// --- DiscoverSessions tests ---

func TestDiscoverSessions(t *testing.T) {
	tmpHome := t.TempDir()
	projectsDir := filepath.Join(tmpHome, ".claude", "projects", "-tmp-myproject")
	if err := os.MkdirAll(projectsDir, 0755); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(projectsDir, "session1.jsonl")
	writeJSONL(t, path,
		metadataEntry("/tmp/myproject"),
		assistantEndTurn(),
	)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	sessions, err := DiscoverSessions(24 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].WorkspacePath != "/tmp/myproject" {
		t.Errorf("expected workspace /tmp/myproject, got %q", sessions[0].WorkspacePath)
	}
}

func TestDiscoverSessions_SkipsOld(t *testing.T) {
	tmpHome := t.TempDir()
	projectsDir := filepath.Join(tmpHome, ".claude", "projects", "-tmp-old")
	if err := os.MkdirAll(projectsDir, 0755); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(projectsDir, "old.jsonl")
	writeJSONL(t, path,
		metadataEntry("/tmp/old"),
		assistantEndTurn(),
	)
	old := time.Now().Add(-48 * time.Hour)
	os.Chtimes(path, old, old)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	sessions, err := DiscoverSessions(24 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Fatalf("expected 0 sessions (old file), got %d", len(sessions))
	}
}

func TestDiscoverSessions_ReturnsAllRecent(t *testing.T) {
	tmpHome := t.TempDir()
	projectsDir := filepath.Join(tmpHome, ".claude", "projects", "-tmp-proj")
	if err := os.MkdirAll(projectsDir, 0755); err != nil {
		t.Fatal(err)
	}

	s1 := filepath.Join(projectsDir, "s1.jsonl")
	writeJSONL(t, s1,
		metadataEntry("/tmp/proj"),
		assistantEndTurn(),
	)

	s2 := filepath.Join(projectsDir, "s2.jsonl")
	writeJSONL(t, s2,
		metadataEntry("/tmp/proj"),
		assistantEndTurn(),
	)

	// Old file: should be excluded
	old := filepath.Join(projectsDir, "old.jsonl")
	writeJSONL(t, old,
		metadataEntry("/tmp/proj"),
		assistantEndTurn(),
	)
	oldTime := time.Now().Add(-48 * time.Hour)
	os.Chtimes(old, oldTime, oldTime)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	sessions, err := DiscoverSessions(24 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 recent sessions (not old one), got %d", len(sessions))
	}
}

// --- loadSessionIndex tests ---

func TestLoadSessionIndex(t *testing.T) {
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "sessions-index.json")
	os.WriteFile(indexPath, []byte(`{"entries":[
		{"sessionId":"abc-123","summary":"Fix login bug"},
		{"sessionId":"def-456","summary":"Add search feature"}
	]}`), 0644)

	m := loadSessionIndex(dir)
	if len(m) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(m))
	}
	if m["abc-123"] != "Fix login bug" {
		t.Errorf("expected 'Fix login bug', got %q", m["abc-123"])
	}
	if m["def-456"] != "Add search feature" {
		t.Errorf("expected 'Add search feature', got %q", m["def-456"])
	}
}

func TestLoadSessionIndex_MissingFile(t *testing.T) {
	m := loadSessionIndex(t.TempDir())
	if m != nil {
		t.Errorf("expected nil for missing file, got %v", m)
	}
}

func TestLoadSessionIndex_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "sessions-index.json"), []byte(`not json`), 0644)
	m := loadSessionIndex(dir)
	if m != nil {
		t.Errorf("expected nil for invalid JSON, got %v", m)
	}
}

func TestLoadSessionIndex_SkipsEmptyFields(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "sessions-index.json"), []byte(`{"entries":[
		{"sessionId":"","summary":"no id"},
		{"sessionId":"abc","summary":""},
		{"sessionId":"def","summary":"valid"}
	]}`), 0644)

	m := loadSessionIndex(dir)
	if len(m) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(m))
	}
	if m["def"] != "valid" {
		t.Errorf("expected 'valid', got %q", m["def"])
	}
}

// --- extractCustomTitle tests ---

func joinLines(lines ...string) []byte {
	return []byte(strings.Join(lines, "\n"))
}

func TestExtractCustomTitle(t *testing.T) {
	data := joinLines(
		metadataEntry("/tmp/proj"),
		assistantEndTurn(),
		`{"type":"custom-title","customTitle":"my session"}`,
	)
	title := extractCustomTitle(data)
	if title != "my session" {
		t.Errorf("expected 'my session', got %q", title)
	}
}

func TestExtractCustomTitle_UsesLast(t *testing.T) {
	data := joinLines(
		`{"type":"custom-title","customTitle":"first"}`,
		assistantEndTurn(),
		`{"type":"custom-title","customTitle":"second"}`,
	)
	title := extractCustomTitle(data)
	if title != "second" {
		t.Errorf("expected 'second', got %q", title)
	}
}

func TestExtractCustomTitle_None(t *testing.T) {
	data := joinLines(
		metadataEntry("/tmp/proj"),
		assistantEndTurn(),
	)
	title := extractCustomTitle(data)
	if title != "" {
		t.Errorf("expected empty, got %q", title)
	}
}

// --- DiscoverSessions with session index ---

func TestDiscoverSessions_SummaryFromIndex(t *testing.T) {
	tmpHome := t.TempDir()
	projectsDir := filepath.Join(tmpHome, ".claude", "projects", "-tmp-proj")
	if err := os.MkdirAll(projectsDir, 0755); err != nil {
		t.Fatal(err)
	}

	sessionPath := filepath.Join(projectsDir, "abc-123.jsonl")
	writeJSONL(t, sessionPath,
		metadataEntry("/tmp/proj"),
		assistantEndTurn(),
	)

	indexData := `{"entries":[{"sessionId":"abc-123","summary":"Fix login bug"}]}`
	os.WriteFile(filepath.Join(projectsDir, "sessions-index.json"), []byte(indexData), 0644)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	sessions, err := DiscoverSessions(24 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].Summary != "Fix login bug" {
		t.Errorf("expected 'Fix login bug', got %q", sessions[0].Summary)
	}
}

func TestDiscoverSessions_CustomTitleOverridesIndex(t *testing.T) {
	tmpHome := t.TempDir()
	projectsDir := filepath.Join(tmpHome, ".claude", "projects", "-tmp-proj")
	if err := os.MkdirAll(projectsDir, 0755); err != nil {
		t.Fatal(err)
	}

	sessionPath := filepath.Join(projectsDir, "abc-123.jsonl")
	writeJSONL(t, sessionPath,
		metadataEntry("/tmp/proj"),
		`{"type":"custom-title","customTitle":"User's title"}`,
		assistantEndTurn(),
	)

	indexData := `{"entries":[{"sessionId":"abc-123","summary":"Auto summary"}]}`
	os.WriteFile(filepath.Join(projectsDir, "sessions-index.json"), []byte(indexData), 0644)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	sessions, err := DiscoverSessions(24 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].Summary != "User's title" {
		t.Errorf("expected custom title to win, got %q", sessions[0].Summary)
	}
}

// --- Attention file tests ---

func TestAttentionRoundTrip(t *testing.T) {
	tmpHome := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	transcript := "/Users/nate/.claude/projects/-tmp-proj/abc123.jsonl"

	// Write attention file
	err := WriteAttention("abc123", AttentionInfo{
		TranscriptPath:   transcript,
		NotificationType: "notification",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Read attention set
	set := ReadAttentionSet()
	if !set[transcript] {
		t.Errorf("expected transcript %q in attention set", transcript)
	}

	// Delete attention file
	if err := DeleteAttention("abc123"); err != nil {
		t.Fatal(err)
	}

	// Should be gone
	set = ReadAttentionSet()
	if set[transcript] {
		t.Errorf("expected transcript %q removed from attention set", transcript)
	}
}

func TestDeleteAttention_Nonexistent(t *testing.T) {
	tmpHome := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	// Should not error
	if err := DeleteAttention("nonexistent"); err != nil {
		t.Errorf("expected no error for nonexistent file, got %v", err)
	}
}

func TestReadAttentionSet_EmptyDir(t *testing.T) {
	tmpHome := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	set := ReadAttentionSet()
	if len(set) != 0 {
		t.Errorf("expected empty set, got %v", set)
	}
}

func TestCleanupStaleAttention(t *testing.T) {
	tmpHome := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	// Create a transcript file that exists
	transcriptDir := filepath.Join(tmpHome, ".claude", "projects", "-tmp-proj")
	os.MkdirAll(transcriptDir, 0755)
	liveTranscript := filepath.Join(transcriptDir, "live.jsonl")
	os.WriteFile(liveTranscript, []byte(`{"type":"user"}`+"\n"), 0644)

	// Write attention for live transcript
	WriteAttention("live", AttentionInfo{
		TranscriptPath: liveTranscript,
	})

	// Write attention for dead transcript (file doesn't exist)
	WriteAttention("dead", AttentionInfo{
		TranscriptPath: "/nonexistent/path/dead.jsonl",
	})

	CleanupStaleAttention(24 * time.Hour)

	set := ReadAttentionSet()
	if !set[liveTranscript] {
		t.Error("expected live transcript to survive cleanup")
	}
	if set["/nonexistent/path/dead.jsonl"] {
		t.Error("expected dead transcript to be cleaned up")
	}
}
