package cmd

import (
	"encoding/json"
	"io"
	"os"
	"time"

	"github.com/sestinj/att/internal/claude"
	"github.com/spf13/cobra"
)

var hookCmd = &cobra.Command{
	Use:          "hook",
	Short:        "Handle Claude Code hook events (called by Claude Code, not directly)",
	SilenceUsage: true,
	RunE:         runHook,
}

func init() {
	rootCmd.AddCommand(hookCmd)
}

type hookInput struct {
	HookEventName string `json:"hook_event_name"`
	SessionID     string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
}

func runHook(cmd *cobra.Command, args []string) error {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil // never block Claude Code
	}

	var input hookInput
	if err := json.Unmarshal(data, &input); err != nil {
		return nil
	}

	if input.SessionID == "" {
		return nil
	}

	switch input.HookEventName {
	case "Notification":
		claude.WriteAttention(input.SessionID, claude.AttentionInfo{
			TranscriptPath:   input.TranscriptPath,
			NotificationType: "notification",
			Timestamp:        time.Now(),
		})
	case "UserPromptSubmit":
		claude.DeleteAttention(input.SessionID)
	case "SessionEnd":
		claude.DeleteAttention(input.SessionID)
	}

	return nil
}
