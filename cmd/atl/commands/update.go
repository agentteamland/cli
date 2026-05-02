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
//	atl update                       # pull global cache + auto-migrate any legacy
//	                                 # symlinks + auto-refresh unmodified copies
//	                                 # for the project at cwd. Verbose output.
//	atl update <team-name>           # update only that team's chain (legacy mode).
//	atl update --silent-if-clean     # no output unless something changed. Used by hooks.
//	atl update --check-only          # dry run: show what WOULD update, pull nothing.
//	atl update --throttle=30m        # skip entirely if last successful run was <30m ago.
//	atl update --skip-self-check     # skip the atl-release check.
//
// Default mode does three things in order, scoped to the project at cwd:
//
//  1. Pull every git repo under ~/.claude/repos/agentteamland/ (the global
//     cache: core, brainstorm, rule, team-manager, every installed team).
//  2. One-time legacy migration: scan .claude/agents/ + .claude/rules/ for
//     symlinks (pre-v1.0 install topology) and convert each to a real file
//     copy. Idempotent — a no-op once migration has run.
//  3. Auto-refresh: for each installed team's resources, compare the project
//     copy's content hash against the install-time baseline AND against the
//     freshly-pulled global-cache hash. Refresh unmodified copies silently;
//     skip modified copies and surface a per-team "use --refresh to force"
//     info line so the user can opt into discarding their local changes.
//
// The auto-refresh step is what restores the zero-effort auto-update UX
// that legacy symlinks provided for free, while protecting local
// self-updating-learning-loop mutations from being silently overwritten.
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
		Long: `Pull updates from every cached AgentTeamLand repo and apply per-project
refresh steps for the project at the current working directory.

The default mode does three things in order:

  1. Pull every git repo under ~/.claude/repos/agentteamland/ (the global
     cache: core, brainstorm, rule, team-manager, every installed team).
  2. One-time legacy migration: convert any pre-v1.0 symlinks under
     .claude/agents/ + .claude/rules/ into real file copies. Idempotent.
  3. Auto-refresh: for each installed team's resources, refresh project
     copies that still match their install-time baseline; skip copies
     with local modifications and surface a per-team "use --refresh to
     force" info line.

Common flags for hooks: --silent-if-clean (no output when nothing changed),
--throttle=30m (cooldown between hook-driven runs).

Pass a team name as the first argument for the legacy per-team mode (resolves
a single team's chain and re-runs install on it). Most users want the no-arg
form; the per-team mode is preserved for compatibility.`,
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

			// Per-project steps (Q2 migration + Q3 auto-refresh of
			// unmodified copies). SessionStart hook fires inside a project's
			// working directory, so cwd is the natural starting point. The
			// migration runs first (one-time, idempotent) so the refresh
			// step always sees real files (not lingering symlinks from the
			// legacy install topology).
			if !checkOnly {
				if root, err := updater.FindProjectRoot(); err == nil && root != "" {
					// Q2 — symlink → copy migration (legacy topology cleanup).
					if _, summary, _ := updater.MigrateProjectInstall(root); summary != "" {
						fmt.Println(summary)
					}
					// Q3 — refresh unmodified copies; skip those with local
					// mutations (preserving self-updating-learning-loop work).
					if refreshSummary, err := updater.RefreshUnmodifiedCopies(root); err == nil {
						if line := updater.FormatRefreshSummary(refreshSummary, root); line != "" {
							fmt.Print(line)
						}
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
