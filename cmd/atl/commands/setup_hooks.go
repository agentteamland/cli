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

// NewSetupHooks builds the `atl setup-hooks` command — writes four Claude Code
// hooks into ~/.claude/settings.json:
//
//   - SessionStart     → atl update --silent-if-clean
//     (auto-update check at every new session start)
//   - UserPromptSubmit → atl update --silent-if-clean --throttle=<duration>
//     (auto-update check per message, throttled)
//   - SessionEnd       → atl learning-capture --silent-if-empty
//     (scans transcript for <!-- learning --> markers
//     and prepares save-learnings work when found)
//   - PreCompact       → atl learning-capture --silent-if-empty
//     (same scanner, before context compaction — catches
//     learnings that would otherwise be summarized away)
//
// The merge is idempotent: re-running the command replaces only the atl-owned
// hook entries (any command starting with "atl "), preserving any other hooks
// the user has configured.
func NewSetupHooks() *cobra.Command {
	var (
		remove   bool
		throttle string
	)

	cmd := &cobra.Command{
		Use:   "setup-hooks",
		Short: "Configure Claude Code hooks for auto-update + learning capture",
		Long: `Configure atl automation via Claude Code hooks.

Installs four hooks into ~/.claude/settings.json:

  - SessionStart      → atl update --silent-if-clean
                        (auto-update every repo in ~/.claude/repos/agentteamland/)
  - UserPromptSubmit  → atl update --silent-if-clean --throttle=<duration>
                        (throttled auto-update, default 30m)
  - SessionEnd        → atl learning-capture --silent-if-empty
                        (scans transcript for <!-- learning --> markers;
                         silent when no markers present — zero cost)
  - PreCompact        → atl learning-capture --silent-if-empty
                        (same scanner, runs before context compaction
                         so markers are not lost to summarization)

When updates or markers are detected, a short report shows up in your
terminal AND in Claude's context. Empty sessions stay silent (free).

The merge is idempotent: re-running replaces only atl-owned entries.
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

// atlCmdPrefix is the broad prefix we use to identify atl-owned hook entries.
// Any hook command starting with "atl " is considered atl-owned and will be
// replaced/removed by setup-hooks. This keeps the upsert idempotent across
// multiple atl subcommands (update, learning-capture, and any future additions)
// without requiring a separate tag per command.
const atlCmdPrefix = "atl "

// atlHookEvents is the full set of Claude Code hook events atl installs into.
// Keep this list in sync with the setters in runSetupHooks; removal uses it
// to clean every possible event we could have written to.
var atlHookEvents = []string{"SessionStart", "UserPromptSubmit", "SessionEnd", "PreCompact"}

func runSetupHooks(throttle string) error {
	settingsPath := filepath.Join(config.ClaudeHome(), "settings.json")
	obj, err := loadSettingsJSON(settingsPath)
	if err != nil {
		return err
	}

	hooks := ensureMap(obj, "hooks")

	updateCmd := "atl update --silent-if-clean"
	updateThrottledCmd := fmt.Sprintf("atl update --silent-if-clean --throttle=%s", throttle)
	captureCmd := "atl learning-capture --silent-if-empty"

	upsertEventEntry(hooks, "SessionStart", updateCmd)
	upsertEventEntry(hooks, "UserPromptSubmit", updateThrottledCmd)
	upsertEventEntry(hooks, "SessionEnd", captureCmd)
	upsertEventEntry(hooks, "PreCompact", captureCmd)

	if err := writeSettingsJSON(settingsPath, obj); err != nil {
		return err
	}

	color.Green("✓ atl hooks installed")
	fmt.Println()
	fmt.Printf("  SessionStart     → %s\n", updateCmd)
	fmt.Printf("  UserPromptSubmit → %s\n", updateThrottledCmd)
	fmt.Printf("  SessionEnd       → %s\n", captureCmd)
	fmt.Printf("  PreCompact       → %s\n", captureCmd)
	fmt.Println()
	fmt.Println("Edited: " + settingsPath)
	fmt.Println("Auto-update: teams + core + brainstorm + rule + team-manager + atl binary.")
	fmt.Println("Learning capture: inline <!-- learning --> markers are collected at")
	fmt.Println("session end and before context compaction. Zero cost when no markers.")
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
	for _, event := range atlHookEvents {
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
		color.Green("✓ atl hooks removed from %s", settingsPath)
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
// prefix — our marker for "this hook was installed by atl". Any command
// beginning with "atl " (update, learning-capture, or future subcommands) is
// considered atl-owned and will be replaced/removed by setup-hooks.
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
			if cmd, ok := hMap["command"].(string); ok && strings.HasPrefix(cmd, atlCmdPrefix) {
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
