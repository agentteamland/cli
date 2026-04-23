package commands

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/agentteamland/cli/internal/config"
	"github.com/agentteamland/cli/internal/team"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// NewInstall builds the `atl install` command.
func NewInstall() *cobra.Command {
	var verbose bool

	cmd := &cobra.Command{
		Use:   "install <team-name | git-url | owner/repo>",
		Short: "Install a team into this project's .claude/",
		Long: `Install a team into the current project.

Accepts three forms:

  atl install software-project-team             # registry lookup by short name
  atl install agentteamland/starter-extended    # owner/repo shorthand (GitHub)
  atl install https://github.com/you/team.git   # direct git URL

If the team has an 'extends' declaration, its parent is installed recursively.
Agents/skills/rules are merged with child-overrides-parent semantics; any names
listed in 'excludes' are dropped from the final set.`,
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
			})
			if err != nil {
				return err
			}

			// Summary.
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
			maybeOfferHookSetup()
			return nil
		},
	}

	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Print git operations and resolution details")
	return cmd
}

// maybeOfferHookSetup is a one-time prompt on the first successful atl install:
// asks the user whether they want automatic update checks on Claude Code
// session start + every user prompt (throttled). Writes the answer to
// ~/.claude/atl-install-marker.json so we never prompt again.
//
// This runs AFTER the install succeeds so users only see it when their first
// install actually worked.
func maybeOfferHookSetup() {
	markerPath := filepath.Join(config.ClaudeHome(), "atl-install-marker.json")
	if _, err := os.Stat(markerPath); err == nil {
		// Not first install — already prompted at some point.
		return
	}

	// Non-interactive stdin (pipe / CI) → skip the prompt silently and record
	// the marker so we don't spam later.
	if !isTerminal(os.Stdin) {
		_ = writeMarker(markerPath, "non-interactive-skip")
		return
	}

	fmt.Println()
	color.Cyan("First time using atl on this machine? Want automatic update checks?")
	fmt.Println("  Claude Code will run `atl update --silent-if-clean` on:")
	fmt.Println("    • every session start (instant, always fresh)")
	fmt.Println("    • every user message (throttled to once per 30m)")
	fmt.Println("  Teams, core, global skills, and the atl binary all stay current.")
	fmt.Println()
	fmt.Println("  You can opt in / out any time with: `atl setup-hooks` / `atl setup-hooks --remove`")
	fmt.Println()
	fmt.Print("Enable auto-update hooks now? [Y/n]: ")

	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		_ = writeMarker(markerPath, "no-response")
		return
	}
	answer := strings.TrimSpace(strings.ToLower(line))
	if answer == "" || strings.HasPrefix(answer, "y") {
		if err := runSetupHooks("30m"); err != nil {
			color.Yellow("⚠ could not install hooks: %v", err)
			color.Yellow("  You can retry later with `atl setup-hooks`.")
			_ = writeMarker(markerPath, "install-failed")
			return
		}
		_ = writeMarker(markerPath, "hooks-installed")
		return
	}

	fmt.Println("  Skipped. Run `atl setup-hooks` whenever you want to enable this.")
	_ = writeMarker(markerPath, "declined")
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
