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

// NewSetupHooks builds the `atl setup-hooks` command — writes Claude Code
// hooks into ~/.claude/settings.json:
//
//   - SessionStart: atl session-start --silent-if-clean
//     (composite boot-time tasks — cache pull, per-project migration,
//     per-project auto-refresh, transcript marker scan; output reaches
//     Claude's additionalContext on the first turn)
//
//   - UserPromptSubmit: atl update --silent-if-clean --throttle=<duration>
//     (throttled cache-pull check per message)
//
// SessionEnd and PreCompact registrations were removed in PR 2A.3
// (atl v1.1.0). Their stdout never reached Claude's context per Claude
// Code v2.1.x docs — only SessionStart / UserPromptSubmit / UserPromptExpansion
// deliver additionalContext. Marker capture moved to SessionStart via the
// session-start wrapper, where the report is actually visible to Claude.
// See .atl/wiki/claude-code-hook-output-events.md.
//
// The merge is idempotent: re-running replaces only atl-owned hook entries
// (any command starting with "atl "), preserving any other hooks the user
// has configured. Re-running ALSO removes legacy SessionEnd / PreCompact
// atl entries from previous installs — silent migration on first
// session-start of v1.1.0+.
func NewSetupHooks() *cobra.Command {
	var (
		remove   bool
		throttle string
	)

	cmd := &cobra.Command{
		Use:   "setup-hooks",
		Short: "Configure Claude Code hooks for auto-update + learning capture",
		Long: `Configure atl automation via Claude Code hooks.

Installs hooks into ~/.claude/settings.json:

  - SessionStart      → atl session-start --silent-if-clean
                        (composite boot-time tasks: cache pull + per-project
                         migration + auto-refresh + transcript marker scan;
                         output reaches Claude's additionalContext)
  - UserPromptSubmit  → atl update --silent-if-clean --throttle=<duration>
                        (throttled cache-pull check per message, default 30m)

When updates or markers are detected, a short report appears in your
terminal AND in Claude's context. Empty sessions stay silent (zero cost).

Legacy hook migration: previous atl versions also registered SessionEnd
and PreCompact for marker scanning. Those events do not deliver stdout
to Claude (per Claude Code docs), so the marker reports were silently
lost. v1.1.0+ removes those registrations on next setup-hooks run; the
marker scan now runs inside session-start where it actually surfaces.

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

// atlHookEvents is the full set of Claude Code hook events atl could have
// written to (current + legacy). Used by --remove for cleanup, AND by the
// install path's migration step to clean v1.0.x SessionEnd + PreCompact
// entries before installing the v1.1.0+ shape.
//
// Keep this list as a superset of every event atl has ever installed into.
// The active subset (currently SessionStart + UserPromptSubmit) is implied
// by the upsertEventEntry calls in runSetupHooks; everything else here is
// kept solely for cleanup of legacy installs.
var atlHookEvents = []string{"SessionStart", "UserPromptSubmit", "SessionEnd", "PreCompact"}

func runSetupHooks(throttle string) error {
	settingsPath := filepath.Join(config.ClaudeHome(), "settings.json")
	obj, err := loadSettingsJSON(settingsPath)
	if err != nil {
		return err
	}

	hooks := ensureMap(obj, "hooks")

	sessionStartCmd := "atl session-start --silent-if-clean"
	updateThrottledCmd := fmt.Sprintf("atl update --silent-if-clean --throttle=%s", throttle)

	// Install / replace active hooks.
	upsertEventEntry(hooks, "SessionStart", sessionStartCmd)
	upsertEventEntry(hooks, "UserPromptSubmit", updateThrottledCmd)

	// Silent legacy migration: clean any v1.0.x atl entries from events
	// we no longer use. Returns whether anything was removed; we surface
	// this to the user so they understand why settings.json shrunk.
	migratedSessionEnd := cleanEventEntries(hooks, "SessionEnd")
	migratedPreCompact := cleanEventEntries(hooks, "PreCompact")
	migrated := migratedSessionEnd || migratedPreCompact

	if err := writeSettingsJSON(settingsPath, obj); err != nil {
		return err
	}

	color.Green("✓ atl hooks installed")
	fmt.Println()
	fmt.Printf("  SessionStart     → %s\n", sessionStartCmd)
	fmt.Printf("  UserPromptSubmit → %s\n", updateThrottledCmd)
	if migrated {
		fmt.Println()
		fmt.Println("ℹ Removed legacy SessionEnd / PreCompact registrations from a previous")
		fmt.Println("  atl version. Their stdout never reached Claude's context (Claude Code")
		fmt.Println("  docs); marker scanning now runs inside session-start instead.")
	}
	fmt.Println()
	fmt.Println("Edited: " + settingsPath)
	fmt.Println("Auto-update: teams + core + brainstorm + rule + team-manager + atl binary.")
	fmt.Println("Learning capture: inline <!-- learning --> markers from previous transcripts")
	fmt.Println("are scanned at session start. Zero cost when no markers.")
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
