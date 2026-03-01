package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Configure Claude Code hooks for att",
	RunE:  runSetup,
}

func init() {
	rootCmd.AddCommand(setupCmd)
}

func runSetup(cmd *cobra.Command, args []string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	settingsPath := filepath.Join(home, ".claude", "settings.json")

	// Resolve the full path to the att binary
	attBin, err := os.Executable()
	if err != nil {
		attBin = filepath.Join(home, "go", "bin", "att")
	}
	hookCommand := attBin + " hook"

	// Read existing settings
	var settings map[string]interface{}
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		settings = make(map[string]interface{})
	} else {
		if err := json.Unmarshal(data, &settings); err != nil {
			return fmt.Errorf("parse %s: %w", settingsPath, err)
		}
	}

	// Get or create hooks object
	hooks, _ := settings["hooks"].(map[string]interface{})
	if hooks == nil {
		hooks = make(map[string]interface{})
	}

	for _, eventType := range []string{"Notification", "UserPromptSubmit", "SessionEnd"} {
		addHookIfAbsent(hooks, eventType, hookCommand)
	}

	settings["hooks"] = hooks

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(settingsPath), 0755); err != nil {
		return err
	}

	if err := os.WriteFile(settingsPath, append(out, '\n'), 0644); err != nil {
		return err
	}

	fmt.Println("Hooks configured in", settingsPath)
	fmt.Printf("Using binary: %s\n", attBin)
	return nil
}

func addHookIfAbsent(hooks map[string]interface{}, eventType, command string) {
	var groups []interface{}
	if existing, ok := hooks[eventType]; ok {
		if arr, ok := existing.([]interface{}); ok {
			groups = arr
		}
	}

	// Check if att hook is already configured
	for _, g := range groups {
		group, ok := g.(map[string]interface{})
		if !ok {
			continue
		}
		hookList, ok := group["hooks"].([]interface{})
		if !ok {
			continue
		}
		for _, h := range hookList {
			hook, ok := h.(map[string]interface{})
			if !ok {
				continue
			}
			if cmd, ok := hook["command"].(string); ok {
				if strings.Contains(cmd, "att hook") {
					return
				}
			}
		}
	}

	newGroup := map[string]interface{}{
		"hooks": []interface{}{
			map[string]interface{}{
				"type":    "command",
				"command": command,
			},
		},
	}
	hooks[eventType] = append(groups, newGroup)
}
