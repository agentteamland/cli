package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/agentteamland/cli/internal/config"
	"github.com/agentteamland/cli/internal/updater"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// NewSetupHooks builds the `atl setup-hooks` command — writes the Claude Code
// SessionStart + UserPromptSubmit hooks into ~/.claude/settings.json so that
// `atl update --silent-if-clean` runs automatically:
//
//   - at every new session start (immediately catches updates)
//   - on every user prompt (throttled to once per <duration>)
//
// The merge is idempotent: re-running the command replaces only the atl-owned
// hook entries, preserving any other hooks the user has configured.
func NewSetupHooks() *cobra.Command {
	var (
		remove   bool
		throttle string
	)

	cmd := &cobra.Command{
		Use:   "setup-hooks",
		Short: "Configure Claude Code hooks to auto-check for updates on every session + every message (throttled)",
		Long: `Configure automatic update checks via Claude Code hooks.

Installs two hooks into ~/.claude/settings.json:

  - SessionStart      → atl update --silent-if-clean
                        (runs once every time a Claude Code session starts)
  - UserPromptSubmit  → atl update --silent-if-clean --throttle=<duration>
                        (runs on every message, but throttled — default 30m —
                         so cost per message is a microsecond unless the
                         throttle has expired)

When a new team / core / atl release is available, a single line like
  🔄 software-project-team 1.1.1 → 1.1.2 (auto-updated)
shows up in your terminal AND in Claude's context. Sessions stay fresh
without you having to run 'atl update' manually.

The merge is idempotent: re-running replaces only the atl-owned entries.
Other hooks you have are preserved. Pass --remove to uninstall.

The file edited is ~/.claude/settings.json.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if remove {
				return runRemoveHooks()
			}

			dur := "30m"
			if throttle != "" {
				if _, err := updater.ParseDuration(throttle); err != nil {
					return fmt.Errorf("--throttle: %w", err)
				}
				dur = throttle
			}
			return runSetupHooks(dur)
		},
	}

	cmd.Flags().BoolVar(&remove, "remove", false, "Remove atl hooks from settings.json")
	cmd.Flags().StringVar(&throttle, "throttle", "30m", "UserPromptSubmit throttle duration (e.g., 5m, 30m, 1h)")
	return cmd
}

// atlCmdTag is the unique prefix we use to identify atl-owned hook entries so
// we can safely update/remove them without touching the user's other hooks.
const atlCmdTag = "atl update --silent-if-clean"

func runSetupHooks(throttle string) error {
	settingsPath := filepath.Join(config.ClaudeHome(), "settings.json")
	obj, err := loadSettingsJSON(settingsPath)
	if err != nil {
		return err
	}

	hooks := ensureMap(obj, "hooks")

	setSessionStart(hooks, "atl update --silent-if-clean")
	setUserPromptSubmit(hooks, fmt.Sprintf("atl update --silent-if-clean --throttle=%s", throttle))

	if err := writeSettingsJSON(settingsPath, obj); err != nil {
		return err
	}

	color.Green("✓ atl auto-update hooks installed")
	fmt.Println()
	fmt.Printf("  SessionStart     → %s\n", "atl update --silent-if-clean")
	fmt.Printf("  UserPromptSubmit → atl update --silent-if-clean --throttle=%s\n", throttle)
	fmt.Println()
	fmt.Println("Edited: " + settingsPath)
	fmt.Println("Teams + core + brainstorm + rule + team-manager + atl binary are now auto-checked.")
	fmt.Println("Remove at any time with: atl setup-hooks --remove")
	return nil
}

func runRemoveHooks() error {
	settingsPath := filepath.Join(config.ClaudeHome(), "settings.json")
	if _, err := os.Stat(settingsPath); os.IsNotExist(err) {
		fmt.Println("~/.claude/settings.json does not exist; nothing to remove.")
		return nil
	}

	obj, err := loadSettingsJSON(settingsPath)
	if err != nil {
		return err
	}
	hooks, ok := obj["hooks"].(map[string]interface{})
	if !ok {
		fmt.Println("No hooks configured; nothing to remove.")
		return nil
	}

	removed := false
	for _, event := range []string{"SessionStart", "UserPromptSubmit"} {
		if cleanEventEntries(hooks, event) {
			removed = true
		}
	}

	// If hooks map is now empty, drop it entirely.
	if len(hooks) == 0 {
		delete(obj, "hooks")
	}

	if err := writeSettingsJSON(settingsPath, obj); err != nil {
		return err
	}

	if removed {
		color.Green("✓ atl auto-update hooks removed from %s", settingsPath)
	} else {
		fmt.Println("No atl-owned hooks found; settings.json unchanged.")
	}
	return nil
}

// --- settings.json load / save ---

func loadSettingsJSON(path string) (map[string]interface{}, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]interface{}{}, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return map[string]interface{}{}, nil
	}
	var obj map[string]interface{}
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if obj == nil {
		obj = map[string]interface{}{}
	}
	return obj, nil
}

func writeSettingsJSON(path string, obj map[string]interface{}) error {
	data, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return err
	}
	// Atomic write (tmp + rename) so a crash mid-write doesn't corrupt.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// --- hook-entry shape helpers ---

// Hooks in settings.json look like:
//
//	{
//	  "hooks": {
//	    "SessionStart": [
//	      { "hooks": [ { "type": "command", "command": "atl update --silent-if-clean" } ] }
//	    ]
//	  }
//	}
//
// Each event (SessionStart, UserPromptSubmit, …) holds an array of matcher
// groups. Each group has a "hooks" array containing command entries. For our
// purposes we treat our auto-update check as its own matcher group.

func setSessionStart(hooks map[string]interface{}, command string) {
	upsertEventEntry(hooks, "SessionStart", command)
}

func setUserPromptSubmit(hooks map[string]interface{}, command string) {
	upsertEventEntry(hooks, "UserPromptSubmit", command)
}

// upsertEventEntry adds or replaces the atl-owned matcher group in hooks[event].
// Preserves all other matcher groups in that event.
func upsertEventEntry(hooks map[string]interface{}, event, command string) {
	groups := asArray(hooks[event])

	// Drop any existing atl-owned group (command starts with atlCmdTag).
	filtered := groups[:0]
	for _, g := range groups {
		if !isAtlGroup(g) {
			filtered = append(filtered, g)
		}
	}

	// Append our group at the end.
	atlGroup := map[string]interface{}{
		"hooks": []interface{}{
			map[string]interface{}{
				"type":    "command",
				"command": command,
			},
		},
	}
	filtered = append(filtered, atlGroup)
	hooks[event] = filtered
}

// cleanEventEntries drops atl-owned groups from hooks[event]. Returns true if
// anything was removed. If the event list ends up empty, the event key itself
// is removed.
func cleanEventEntries(hooks map[string]interface{}, event string) bool {
	groups := asArray(hooks[event])
	if len(groups) == 0 {
		return false
	}
	filtered := groups[:0]
	removed := false
	for _, g := range groups {
		if isAtlGroup(g) {
			removed = true
			continue
		}
		filtered = append(filtered, g)
	}
	if len(filtered) == 0 {
		delete(hooks, event)
	} else {
		hooks[event] = filtered
	}
	return removed
}

// isAtlGroup reports whether a matcher group's inner command starts with our
// tag — our marker for "this hook was installed by atl".
func isAtlGroup(g interface{}) bool {
	grpMap, ok := g.(map[string]interface{})
	if !ok {
		return false
	}
	inner := asArray(grpMap["hooks"])
	for _, h := range inner {
		hMap, ok := h.(map[string]interface{})
		if !ok {
			continue
		}
		if t, ok := hMap["type"].(string); ok && t == "command" {
			if cmd, ok := hMap["command"].(string); ok && strings.HasPrefix(cmd, atlCmdTag) {
				return true
			}
		}
	}
	return false
}

// --- small JSON helpers ---

func ensureMap(parent map[string]interface{}, key string) map[string]interface{} {
	if v, ok := parent[key].(map[string]interface{}); ok {
		return v
	}
	m := map[string]interface{}{}
	parent[key] = m
	return m
}

func asArray(v interface{}) []interface{} {
	if arr, ok := v.([]interface{}); ok {
		return arr
	}
	return []interface{}{}
}
