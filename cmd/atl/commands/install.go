package commands

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/agentteamland/cli/internal/atlmigrate"
	"github.com/agentteamland/cli/internal/config"
	"github.com/agentteamland/cli/internal/configui"
	"github.com/agentteamland/cli/internal/team"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// NewInstall builds the `atl install` command.
func NewInstall() *cobra.Command {
	var (
		verbose bool
		refresh bool
	)

	cmd := &cobra.Command{
		Use:   "install <team-name | git-url | owner/repo>",
		Short: "Install a team into this project's .claude/",
		Long: `Install a team into the current project.

Accepts three forms:

  atl install software-project-team             # registry lookup by short name
  atl install agentteamland/starter-extended    # owner/repo shorthand (GitHub)
  atl install https://github.com/you/team.git   # direct git URL

Idempotent by default: if the team is already installed, the command exits
with an info message and the project copies are left untouched. Pass
--refresh to force overwrite — local changes (self-updating-learning-loop
mutations or hand edits) are reported and discarded.

If the team has an 'extends' declaration, its parent is installed recursively.
Agents/skills/rules are merged with child-overrides-parent semantics; any names
listed in 'excludes' are dropped from the final set.

Routine cache updates (when the global cache pulls a newer version) are
applied automatically by 'atl update' for unmodified copies — you don't
need to re-install. Use --refresh only when you want to wipe your local
modifications and start over from the cache.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}

			target := args[0]
			color.Cyan("→ installing %s ...", target)

			result, err := team.Install(target, team.InstallOptions{
				CWD:     cwd,
				Verbose: verbose,
				Refresh: refresh,
			})
			if err != nil {
				return err
			}

			// No-op path: team already installed, --refresh not requested.
			if result.Op == team.InstallStatusNoOp {
				fmt.Println()
				color.Green("✓ %s is already installed (no-op)", result.TopLevelName)
				fmt.Println("   Use --refresh to force overwrite, or rely on `atl update` to refresh unmodified copies automatically.")
				return nil
			}

			// Installed path.
			fmt.Println()
			color.Green("✓ installed: %s@%s", result.TopLevelName, result.TopLevelVersion)
			if len(result.Chain) > 1 {
				fmt.Printf("   chain:     %s\n", joinChain(result.Chain))
			}
			fmt.Printf("   effective: %d agents, %d skills, %d rules\n",
				result.AgentsCount, result.SkillsCount, result.RulesCount)
			if len(result.Excluded) > 0 {
				fmt.Printf("   excluded:  %s\n", joinStrings(result.Excluded))
			}
			if result.Status == "community" {
				color.Yellow("   status:    community (not reviewed)")
			}

			// First-install opt-in for auto-update hooks.
			maybeFirstInstallFlow()
			return nil
		},
	}

	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Print git operations and resolution details")
	cmd.Flags().BoolVar(&refresh, "refresh", false, "Force overwrite of an already-installed team (discards local changes)")
	return cmd
}

// maybeFirstInstallFlow runs the one-shot first-install onboarding after a
// successful `atl install`. Two prompts in sequence (per the
// atl-config-system decision, workspace .claude/docs/atl-config-system.md):
//
//  1. atl config init — Bubbletea Q&A populating ~/.atl/config.json with
//     the 9 user-tunable keys. Cancelled flow leaves config absent.
//  2. atl setup-hooks prompt — single y/N asking whether to wire the
//     SessionStart + UserPromptSubmit hooks in Claude Code's settings.json.
//
// Gated by ~/.atl/install-marker.json (legacy ~/.claude/atl-install-marker.json
// read as fallback). The marker is written at the end regardless of the user's
// choices — "user's choice respected; not nagged." Cancelled welcome still
// writes the marker.
//
// Non-interactive stdin (pipe / CI) → skip both prompts silently and write
// the marker so future runs don't spam.
//
// This runs AFTER the install succeeds so users only see it when their first
// install actually worked.
func maybeFirstInstallFlow() {
	markerWrite := filepath.Join(config.AtlHome(), "install-marker.json")
	markerLegacy := filepath.Join(config.ClaudeHome(), "atl-install-marker.json")
	markerRead := atlmigrate.Resolve(markerLegacy, markerWrite)
	if _, err := os.Stat(markerRead); err == nil {
		// Not first install — already prompted at some point.
		return
	}

	// Non-interactive stdin (pipe / CI) → skip both prompts silently and record
	// the marker so we don't spam later.
	if !isTerminal(os.Stdin) {
		_ = writeMarker(markerWrite, "non-interactive-skip")
		return
	}

	configOutcome := runFirstInstallConfigInit()
	hooksOutcome := runFirstInstallHookSetup()

	_ = writeMarker(markerWrite, fmt.Sprintf("config:%s,hooks:%s", configOutcome, hooksOutcome))
}

// runFirstInstallConfigInit runs the atl config init Q&A flow. Returns a
// short outcome label embedded in the install marker for telemetry / debug.
//
// If ~/.atl/config.json already exists, the Q&A is skipped (we don't
// overwrite a config the user (or a previous install) already wrote).
func runFirstInstallConfigInit() string {
	configPath := config.GlobalAtlConfigPath()
	if _, err := os.Stat(configPath); err == nil {
		// Config already exists — don't auto-overwrite.
		return "preexisting"
	}

	fmt.Println()
	color.Cyan("First time using atl on this machine? Let's set up your config.")
	fmt.Println("  9 short questions populate ~/.atl/config.json with your preferences.")
	fmt.Println("  Defaults are sensible — press Enter on each to keep them.")
	fmt.Println("  You can re-run anytime with `atl config edit`.")
	fmt.Println()

	result, err := configui.Run(configui.ModeInit, config.DefaultAtlConfig(), configPath)
	if err != nil {
		color.Yellow("⚠ config Q&A failed: %v", err)
		color.Yellow("  You can retry later with `atl config init`.")
		return "qa-error"
	}
	if !result.Saved {
		fmt.Println("  Config setup cancelled — no file written. Run `atl config init` later if you change your mind.")
		return "cancelled"
	}
	if err := config.WriteAtlConfigFile(configPath, result.Cfg); err != nil {
		color.Yellow("⚠ could not write %s: %v", configPath, err)
		return "write-error"
	}
	color.Green("✓ wrote %s", configPath)
	return "saved"
}

// runFirstInstallHookSetup prompts the user once to enable the
// SessionStart + UserPromptSubmit hooks in Claude Code's settings.json
// via atl setup-hooks. The throttle parameter pinned at 30m matches the
// brainstorm's recommended default; users can re-tune later via
// `atl config edit` (autoUpdate.throttleMinutes).
func runFirstInstallHookSetup() string {
	fmt.Println()
	color.Cyan("Enable Claude Code auto-update hooks?")
	fmt.Println("  Wires SessionStart + UserPromptSubmit hooks in ~/.claude/settings.json")
	fmt.Println("  so atl pulls cache updates + scans transcripts for learning markers")
	fmt.Println("  automatically. Throttled to once per 30m on prompt-submit.")
	fmt.Println()
	fmt.Println("  You can opt in / out any time with: `atl setup-hooks` / `atl setup-hooks --remove`")
	fmt.Println()
	fmt.Print("Enable auto-update hooks now? [Y/n]: ")

	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "no-response"
	}
	answer := strings.TrimSpace(strings.ToLower(line))
	if answer == "" || strings.HasPrefix(answer, "y") {
		if err := runSetupHooks("30m"); err != nil {
			color.Yellow("⚠ could not install hooks: %v", err)
			color.Yellow("  You can retry later with `atl setup-hooks`.")
			return "install-failed"
		}
		return "installed"
	}

	fmt.Println("  Skipped. Run `atl setup-hooks` whenever you want to enable this.")
	return "declined"
}

// writeMarker is tolerant — failure to write the marker just means we'll
// prompt again on the next install, which is mildly annoying but not fatal.
func writeMarker(path, outcome string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	payload, _ := json.MarshalIndent(map[string]string{
		"outcome": outcome,
		"version": config.Version,
	}, "", "  ")
	return os.WriteFile(path, append(payload, '\n'), 0o644)
}

// isTerminal reports whether stdin is attached to a terminal — used to skip
// the hook-setup prompt in CI / piped invocations.
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

func joinChain(chain []string) string {
	// child → ... → root (reverse the ExtendsChain which is child-first)
	out := ""
	for i := 0; i < len(chain); i++ {
		if i > 0 {
			out += " → "
		}
		out += chain[i]
	}
	return out
}

func joinStrings(xs []string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += ", "
		}
		out += x
	}
	return out
}
