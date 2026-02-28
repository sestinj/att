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

func assistantEntry(stopReason string, sidechain bool, blocks ...contentBlock) string {
	sr := "null"
	if stopReason != "" {
		sr = fmt.Sprintf(`"%s"`, stopReason)
	}
	var parts []string
	for _, b := range blocks {
		if b.Name != "" {
			parts = append(parts, fmt.Sprintf(`{"type":"%s","name":"%s"}`, b.Type, b.Name))
		} else {
			parts = append(parts, fmt.Sprintf(`{"type":"%s"}`, b.Type))
		}
	}
	content := "[" + strings.Join(parts, ",") + "]"
	return fmt.Sprintf(`{"type":"assistant","isSidechain":%v,"message":{"stop_reason":%s,"content":%s}}`,
		sidechain, sr, content)
}

func userToolResult() string {
	return `{"type":"user","isSidechain":false,"message":{"role":"user","content":[{"type":"tool_result","content":"ok"}]}}`
}

func userText(text string) string {
	return fmt.Sprintf(`{"type":"user","isSidechain":false,"message":{"role":"user","content":"%s"}}`, text)
}

func progressEntry(dataType string) string {
	return fmt.Sprintf(`{"type":"progress","data":{"type":"%s"}}`, dataType)
}

func systemEntry(subtype string) string {
	return fmt.Sprintf(`{"type":"system","subtype":"%s"}`, subtype)
}

func metadataEntry(cwd string) string {
	return fmt.Sprintf(`{"type":"user","cwd":"%s","sessionId":"test-session"}`, cwd)
}

func text() contentBlock     { return contentBlock{Type: "text"} }
func thinking() contentBlock { return contentBlock{Type: "thinking"} }
func tool(name string) contentBlock {
	return contentBlock{Type: "tool_use", Name: name}
}

func mainAssistant(stopReason string, blocks ...contentBlock) string {
	return assistantEntry(stopReason, false, blocks...)
}
func sideAssistant(stopReason string, blocks ...contentBlock) string {
	return assistantEntry(stopReason, true, blocks...)
}

func joinLines(lines ...string) []byte {
	return []byte(strings.Join(lines, "\n"))
}

// --- ClassifySessionState tests (pure function) ---

func TestClassifySessionState(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
		want  SessionState
	}{
		// === StateIdle ===
		{
			name: "idle: end_turn with text",
			lines: []string{
				mainAssistant("end_turn", text()),
			},
			want: StateIdle,
		},
		{
			name: "idle: end_turn after tool loop",
			lines: []string{
				mainAssistant("tool_use", tool("Bash")),
				progressEntry("bash_progress"),
				userToolResult(),
				mainAssistant("tool_use", tool("Read")),
				progressEntry("hook_progress"),
				userToolResult(),
				mainAssistant("end_turn", text()),
			},
			want: StateIdle,
		},
		{
			name: "idle: stop_sequence",
			lines: []string{
				mainAssistant("stop_sequence", text()),
			},
			want: StateIdle,
		},
		{
			name: "idle: old version null stop_reason with text",
			lines: []string{
				mainAssistant("", text()),
			},
			want: StateIdle,
		},
		{
			name: "idle: end_turn followed by system entries",
			lines: []string{
				mainAssistant("end_turn", text()),
				progressEntry("hook_progress"),
				progressEntry("hook_progress"),
				systemEntry("stop_hook_summary"),
				systemEntry("turn_duration"),
			},
			want: StateIdle,
		},

		// === StateWorking ===
		{
			name: "working: tool executing with bash_progress",
			lines: []string{
				mainAssistant("tool_use", tool("Bash")),
				progressEntry("hook_progress"),
				progressEntry("bash_progress"),
			},
			want: StateWorking,
		},
		{
			name: "working: tool executing with mcp_progress",
			lines: []string{
				mainAssistant("tool_use", tool("mcp__posthog__query-run")),
				progressEntry("mcp_progress"),
			},
			want: StateWorking,
		},
		{
			name: "working: subagent running with agent_progress",
			lines: []string{
				mainAssistant("tool_use", tool("Task")),
				progressEntry("agent_progress"),
				progressEntry("agent_progress"),
			},
			want: StateWorking,
		},
		{
			name: "working: tool result came back, Claude processing",
			lines: []string{
				mainAssistant("tool_use", tool("Bash")),
				progressEntry("bash_progress"),
				userToolResult(),
			},
			want: StateWorking,
		},
		{
			name: "working: user sent new message after idle",
			lines: []string{
				mainAssistant("end_turn", text()),
				userText("do something else"),
			},
			want: StateWorking,
		},
		{
			name: "working: thinking streaming partial",
			lines: []string{
				userToolResult(),
				mainAssistant("", thinking()),
			},
			want: StateWorking,
		},
		{
			name: "working: empty content null stop_reason",
			lines: []string{
				mainAssistant("end_turn", text()),
				mainAssistant(""),
			},
			want: StateWorking,
		},
		{
			name: "working: old version tool with bash_progress",
			lines: []string{
				mainAssistant("", tool("Bash")),
				progressEntry("bash_progress"),
			},
			want: StateWorking,
		},

		// === StateAsking ===
		{
			name: "asking: AskUserQuestion with tool_use stop_reason",
			lines: []string{
				mainAssistant("tool_use", tool("AskUserQuestion")),
			},
			want: StateAsking,
		},
		{
			name: "asking: AskUserQuestion with null stop_reason (old version)",
			lines: []string{
				mainAssistant("", tool("AskUserQuestion")),
			},
			want: StateAsking,
		},
		{
			name: "asking: AskUserQuestion after text",
			lines: []string{
				mainAssistant("", text()),
				mainAssistant("tool_use", text(), tool("AskUserQuestion")),
			},
			want: StateAsking,
		},

		// === StatePlanMode ===
		{
			name: "plan: ExitPlanMode with tool_use stop_reason",
			lines: []string{
				mainAssistant("tool_use", tool("ExitPlanMode")),
			},
			want: StatePlanMode,
		},
		{
			name: "plan: ExitPlanMode with null stop_reason",
			lines: []string{
				mainAssistant("", tool("ExitPlanMode")),
			},
			want: StatePlanMode,
		},

		// === StateToolPermission ===
		{
			name: "permission: bare tool_use with no progress",
			lines: []string{
				mainAssistant("tool_use", tool("Bash")),
			},
			want: StateToolPermission,
		},
		{
			name: "permission: tool_use with only hook_progress (pre-tool hooks)",
			lines: []string{
				mainAssistant("tool_use", tool("Edit")),
				progressEntry("hook_progress"),
			},
			want: StateToolPermission,
		},
		{
			name: "permission: old version tool_use null stop_reason, no progress",
			lines: []string{
				mainAssistant("", tool("Write")),
			},
			want: StateToolPermission,
		},

		// === StateUnknown ===
		{
			name: "unknown: empty file",
			lines: []string{},
			want: StateUnknown,
		},
		{
			name: "unknown: only metadata",
			lines: []string{
				metadataEntry("/tmp/proj"),
			},
			want: StateUnknown,
		},
		{
			name: "unknown: only system entries",
			lines: []string{
				systemEntry("turn_duration"),
				systemEntry("stop_hook_summary"),
			},
			want: StateUnknown,
		},

		// === Sidechain filtering ===
		{
			name: "sidechain: skipped, main idle wins",
			lines: []string{
				mainAssistant("end_turn", text()),
				sideAssistant("tool_use", tool("Bash")),
				sideAssistant("tool_use", tool("AskUserQuestion")),
			},
			want: StateIdle,
		},
		{
			name: "sidechain: only sidechain entries = unknown",
			lines: []string{
				sideAssistant("end_turn", text()),
				sideAssistant("tool_use", tool("Bash")),
			},
			want: StateUnknown,
		},

		// === Multi-turn scenarios ===
		{
			name: "multi-turn: ask answered then idle",
			lines: []string{
				mainAssistant("tool_use", tool("AskUserQuestion")),
				userToolResult(),
				mainAssistant("tool_use", tool("Bash")),
				progressEntry("bash_progress"),
				userToolResult(),
				mainAssistant("end_turn", text()),
			},
			want: StateIdle,
		},
		{
			name: "multi-turn: plan approved then working",
			lines: []string{
				mainAssistant("tool_use", tool("ExitPlanMode")),
				userToolResult(),
				userText("looks good, proceed"),
				mainAssistant("tool_use", tool("Edit")),
				progressEntry("hook_progress"),
				userToolResult(),
				mainAssistant("tool_use", tool("Bash")),
				progressEntry("bash_progress"),
			},
			want: StateWorking,
		},
		{
			name: "multi-turn: several tools then permission wait",
			lines: []string{
				mainAssistant("tool_use", tool("Glob")),
				progressEntry("hook_progress"),
				userToolResult(),
				mainAssistant("tool_use", tool("Read")),
				progressEntry("hook_progress"),
				userToolResult(),
				mainAssistant("tool_use", tool("Bash")),
			},
			want: StateToolPermission,
		},
		{
			name: "multi-turn: working then idle then asking",
			lines: []string{
				mainAssistant("tool_use", tool("Bash")),
				progressEntry("bash_progress"),
				userToolResult(),
				mainAssistant("end_turn", text()),
				userText("now help me with this"),
				mainAssistant("", thinking()),
				mainAssistant("tool_use", tool("AskUserQuestion")),
			},
			want: StateAsking,
		},

		// === Edge cases ===
		{
			name: "edge: assistant with no message field",
			lines: []string{
				`{"type":"assistant","isSidechain":false}`,
			},
			want: StateUnknown,
		},
		{
			name: "edge: malformed JSON lines skipped",
			lines: []string{
				"this is not json",
				mainAssistant("end_turn", text()),
				"{incomplete json",
			},
			want: StateIdle,
		},
		{
			name: "edge: user with string content (old format)",
			lines: []string{
				mainAssistant("end_turn", text()),
				`{"type":"user","isSidechain":false,"message":{"role":"user","content":"hello"}}`,
			},
			want: StateWorking,
		},
		{
			name: "edge: progress without data field",
			lines: []string{
				mainAssistant("tool_use", tool("Bash")),
				`{"type":"progress"}`,
			},
			want: StateToolPermission,
		},
		{
			name: "edge: mixed sidechain progress doesn't affect state",
			lines: []string{
				mainAssistant("tool_use", tool("Bash")),
				// Sidechain progress from a subagent should not upgrade main state
				`{"type":"progress","isSidechain":true,"data":{"type":"bash_progress"}}`,
			},
			// Note: progress entries don't have isSidechain in practice,
			// but the parser checks entry.IsSidechain on all types
			want: StateToolPermission,
		},
		{
			name: "edge: very long line (>256KB) does not block parsing",
			lines: []string{
				mainAssistant("tool_use", tool("Bash")),
				// Simulate a huge tool result (>256KB) that would break bufio.Scanner
				`{"type":"user","isSidechain":false,"message":{"role":"user","content":[{"type":"tool_result","content":"` + strings.Repeat("x", 300000) + `"}]}}`,
				mainAssistant("end_turn", text()),
			},
			want: StateIdle,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := joinLines(tt.lines...)
			got := ClassifySessionState(data)
			if got != tt.want {
				t.Errorf("ClassifySessionState() = %v, want %v", got, tt.want)
			}
		})
	}
}

// --- NeedsAttention tests ---

func TestNeedsAttention(t *testing.T) {
	tests := []struct {
		state SessionState
		want  bool
	}{
		{StateUnknown, false},
		{StateWorking, false},
		{StateIdle, true},
		{StateAsking, true},
		{StatePlanMode, true},
		{StateToolPermission, true},
	}

	for _, tt := range tests {
		t.Run(tt.state.String(), func(t *testing.T) {
			s := Session{State: tt.state}
			if got := s.NeedsAttention(); got != tt.want {
				t.Errorf("NeedsAttention() = %v, want %v", got, tt.want)
			}
		})
	}
}

// --- String tests ---

func TestSessionState_String(t *testing.T) {
	tests := []struct {
		state SessionState
		want  string
	}{
		{StateUnknown, "Unknown"},
		{StateWorking, "Working"},
		{StateIdle, "Idle"},
		{StateAsking, "Asking"},
		{StatePlanMode, "Plan"},
		{StateToolPermission, "Permission"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.state.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- parseSessionState integration tests ---

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

func TestParseSessionState_Idle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	writeJSONL(t, path,
		metadataEntry("/Users/nate/gh/myproject"),
		mainAssistant("end_turn", text()),
	)

	session, err := parseSessionState(path)
	if err != nil {
		t.Fatal(err)
	}
	if session.State != StateIdle {
		t.Errorf("expected StateIdle, got %v", session.State)
	}
	if session.WorkspacePath != "/Users/nate/gh/myproject" {
		t.Errorf("expected workspace /Users/nate/gh/myproject, got %q", session.WorkspacePath)
	}
	if session.ProjectName != "myproject" {
		t.Errorf("expected project myproject, got %q", session.ProjectName)
	}
	if !session.NeedsAttention() {
		t.Error("idle session should need attention")
	}
}

func TestParseSessionState_ToolPermission(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	writeJSONL(t, path,
		metadataEntry("/Users/nate/gh/myproject"),
		mainAssistant("tool_use", tool("Bash")),
	)

	session, err := parseSessionState(path)
	if err != nil {
		t.Fatal(err)
	}
	if session.State != StateToolPermission {
		t.Errorf("expected StateToolPermission, got %v", session.State)
	}
	if !session.NeedsAttention() {
		t.Error("tool permission session should need attention")
	}
}

func TestParseSessionState_Working(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	writeJSONL(t, path,
		metadataEntry("/Users/nate/gh/myproject"),
		mainAssistant("tool_use", tool("Bash")),
		progressEntry("bash_progress"),
	)

	session, err := parseSessionState(path)
	if err != nil {
		t.Fatal(err)
	}
	if session.State != StateWorking {
		t.Errorf("expected StateWorking, got %v", session.State)
	}
	if session.NeedsAttention() {
		t.Error("working session should not need attention")
	}
}

func TestParseSessionState_Asking(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	writeJSONL(t, path,
		metadataEntry("/tmp/proj"),
		mainAssistant("tool_use", text(), tool("AskUserQuestion")),
	)

	session, err := parseSessionState(path)
	if err != nil {
		t.Fatal(err)
	}
	if session.State != StateAsking {
		t.Errorf("expected StateAsking, got %v", session.State)
	}
}

func TestParseSessionState_PlanMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	writeJSONL(t, path,
		metadataEntry("/tmp/proj"),
		mainAssistant("tool_use", tool("ExitPlanMode")),
	)

	session, err := parseSessionState(path)
	if err != nil {
		t.Fatal(err)
	}
	if session.State != StatePlanMode {
		t.Errorf("expected StatePlanMode, got %v", session.State)
	}
}

func TestParseSessionState_SkipsSidechain(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	writeJSONL(t, path,
		metadataEntry("/tmp/proj"),
		mainAssistant("end_turn", text()),
		sideAssistant("tool_use", tool("Bash")),
	)

	session, err := parseSessionState(path)
	if err != nil {
		t.Fatal(err)
	}
	if session.State != StateIdle {
		t.Errorf("expected StateIdle (skipping sidechain), got %v", session.State)
	}
}

func TestParseSessionState_NoAssistantEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	writeJSONL(t, path,
		metadataEntry("/tmp/proj"),
	)

	session, err := parseSessionState(path)
	if err != nil {
		t.Fatal(err)
	}
	if session.State != StateUnknown {
		t.Errorf("expected StateUnknown, got %v", session.State)
	}
}

func TestParseSessionState_LargeFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")

	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}

	f.WriteString(metadataEntry("/Users/nate/gh/bigproject") + "\n")

	// Write enough filler to push past any tail window
	filler := `{"type":"progress","data":{"type":"hook_progress"}}` + "\n"
	for i := 0; i < 500; i++ {
		f.WriteString(filler)
	}

	f.WriteString(mainAssistant("tool_use", tool("AskUserQuestion")) + "\n")
	f.Close()

	session, err := parseSessionState(path)
	if err != nil {
		t.Fatal(err)
	}
	if session.State != StateAsking {
		t.Errorf("expected StateAsking, got %v", session.State)
	}
	if session.WorkspacePath != "/Users/nate/gh/bigproject" {
		t.Errorf("expected workspace from head, got %q", session.WorkspacePath)
	}
}

func TestDiscoverSessions(t *testing.T) {
	tmpHome := t.TempDir()
	projectsDir := filepath.Join(tmpHome, ".claude", "projects", "-tmp-myproject")
	if err := os.MkdirAll(projectsDir, 0755); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(projectsDir, "session1.jsonl")
	writeJSONL(t, path,
		metadataEntry("/tmp/myproject"),
		mainAssistant("end_turn", text()),
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
	if sessions[0].State != StateIdle {
		t.Errorf("expected StateIdle, got %v", sessions[0].State)
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
		mainAssistant("end_turn"),
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

	// Two recent sessions at the same path: one working, one idle
	working := filepath.Join(projectsDir, "working.jsonl")
	writeJSONL(t, working,
		metadataEntry("/tmp/proj"),
		mainAssistant("tool_use", tool("Bash")),
		progressEntry("bash_progress"),
	)

	idle := filepath.Join(projectsDir, "idle.jsonl")
	writeJSONL(t, idle,
		metadataEntry("/tmp/proj"),
		mainAssistant("end_turn", text()),
	)

	// Old file: should be excluded
	old := filepath.Join(projectsDir, "old.jsonl")
	writeJSONL(t, old,
		metadataEntry("/tmp/proj"),
		mainAssistant("tool_use", tool("AskUserQuestion")),
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

	// Verify both states are present
	states := map[SessionState]bool{}
	for _, s := range sessions {
		states[s.State] = true
	}
	if !states[StateWorking] {
		t.Error("expected a Working session")
	}
	if !states[StateIdle] {
		t.Error("expected an Idle session")
	}
}
