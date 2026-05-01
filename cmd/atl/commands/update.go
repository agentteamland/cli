package commands

import (
	"fmt"
	"os"
	"time"

	"github.com/agentteamland/cli/internal/team"
	"github.com/agentteamland/cli/internal/updater"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// NewUpdate builds the `atl update` command.
//
// Modes:
//
//	atl update                       # update every repo in ~/.claude/repos/agentteamland/
//	                                 # plus a self-release check. Verbose output.
//	atl update <team-name>           # update only that team's chain (legacy behavior).
//	atl update --silent-if-clean     # no output unless something changed. Used by hooks.
//	atl update --check-only          # dry run: show what WOULD update, pull nothing.
//	atl update --throttle=30m        # skip entirely if last successful run was <30m ago.
//	atl update --skip-self-check     # skip the atl-release check.
//
// When invoked with no team name, `update` pulls every git repo under
// ~/.claude/repos/agentteamland/ — that's teams AND global repos (core,
// brainstorm, rule, team-manager, etc.). They all live in the same cache and
// share the same pull mechanism.
func NewUpdate() *cobra.Command {
	var (
		verbose       bool
		silentIfClean bool
		checkOnly     bool
		throttle      string
		skipSelf      bool
	)

	cmd := &cobra.Command{
		Use:   "update [team-name]",
		Short: "Pull updates for installed teams (and all cached agentteamland repos)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// ---- legacy per-team mode (user passed a name) ---------------
			if len(args) == 1 {
				return runLegacyUpdate(args[0], verbose)
			}

			// ---- modern full-cache mode -----------------------------------
			throttleDur := time.Duration(0)
			if throttle != "" {
				d, err := updater.ParseDuration(throttle)
				if err != nil {
					return fmt.Errorf("--throttle: %w", err)
				}
				throttleDur = d
			}

			// Self-check throttle defaults to 24h regardless of --throttle
			// (binary releases are hourly at most; checking every message
			// would spam GitHub).
			selfThrottle := 24 * time.Hour

			res := updater.Run(updater.Options{
				RepoThrottle:  throttleDur,
				SelfThrottle:  selfThrottle,
				SkipSelfCheck: skipSelf,
				CheckOnly:     checkOnly,
				Verbose:       verbose,
			})

			// Per-project legacy-symlink migration (one-time, idempotent).
			// SessionStart hook fires inside a project's working directory, so
			// cwd is the natural starting point. We walk upward to find the
			// project root (.claude/.team-installs.json marker), then convert
			// any agents/rules symlinks found there to plain copies. Future
			// PRs add the auto-refresh-of-unmodified-projects step (Q3) at
			// the same point. See .claude/docs/install-mechanism-redesign.md.
			if !checkOnly {
				if root, err := updater.FindProjectRoot(); err == nil && root != "" {
					if _, summary, _ := updater.MigrateProjectInstall(root); summary != "" {
						fmt.Println(summary)
					}
				}
			}

			summary := res.FormatSummary(silentIfClean)
			if summary != "" {
				fmt.Print(summary)
			}

			// Any per-repo error → non-zero exit? No — errors are surfaced in
			// the summary already. Exit 0 keeps hooks non-blocking even when
			// one repo is offline / behind auth.
			return nil
		},
	}

	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Print git commands and per-step progress")
	cmd.Flags().BoolVar(&silentIfClean, "silent-if-clean", false, "Produce no output if nothing changed (for hooks)")
	cmd.Flags().BoolVar(&checkOnly, "check-only", false, "Report what would update; do not pull")
	cmd.Flags().StringVar(&throttle, "throttle", "", "Skip if last successful run was within this duration (e.g., 30m, 1h)")
	cmd.Flags().BoolVar(&skipSelf, "skip-self-check", false, "Do not check for a newer atl release")
	return cmd
}

// runLegacyUpdate keeps the original `atl update <team-name>` behavior: resolve
// installed teams from .team-installs.json, re-run team.Install for the given
// team. Not affected by the cache-wide throttle because the user asked for a
// specific team by name — explicit intent, honor it.
func runLegacyUpdate(name string, verbose bool) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	m, err := team.List(cwd)
	if err != nil {
		return err
	}
	if len(m.Teams) == 0 {
		fmt.Println("No teams installed in this project.")
		return nil
	}

	found := false
	for _, t := range m.Teams {
		if t.Name == name {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("%q is not installed in this project", name)
	}

	color.Cyan("→ updating %s ...", name)
	if _, err := team.Install(name, team.InstallOptions{CWD: cwd, Verbose: verbose}); err != nil {
		color.Red("  failed: %v", err)
		return err
	}
	color.Green("  ✓ updated")
	return nil
}
