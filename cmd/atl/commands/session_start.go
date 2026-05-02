package commands

import (
	"fmt"
	"time"

	"github.com/agentteamland/cli/internal/updater"
	"github.com/spf13/cobra"
)

// NewSessionStart builds the `atl session-start` command — a single composite
// entry point for everything that should run at the start of a Claude Code
// session.
//
// Today this is:
//
//  1. atl update --silent-if-clean   (cache pull)
//  2. per-project steps              (migration + auto-refresh)
//
// Future steps (per .claude/docs/self-updating-learning-loop.md Q1):
//
//  3. atl learning-capture --previous-transcripts (PR 2A.2)
//
// And anything else that becomes a boot-time concern.
//
// Why a wrapper instead of registering each step as its own hook command:
// `atl setup-hooks` only needs to know one command name. New boot-time
// steps land here without touching hook config — no setup-hooks rerun
// required for users who already opted in. This is what Q1.5 of the
// install-mechanism-redesign brainstorm decided.
//
// Hooks call this with `--silent-if-clean` (no output unless something
// changed). The /refresh skill (PR 2A.4) calls it without any flag for
// verbose mid-session refresh.
func NewSessionStart() *cobra.Command {
	var (
		silentIfClean bool
		verbose       bool
	)

	cmd := &cobra.Command{
		Use:   "session-start",
		Short: "Composite boot-time tasks (cache pull + migration + auto-refresh)",
		Long: `Run every "fresh-session boot" task in sequence:

  1. Pull the global cache (atl update equivalent — every git repo under
     ~/.claude/repos/agentteamland/).
  2. Per-project steps for the project at the current working directory:
     • One-time legacy symlink → copy migration (idempotent)
     • Auto-refresh of unmodified resource copies (skips locally-modified
       copies and surfaces a per-team "use --refresh to force" hint)
  3. (Future) Process pending learning markers from previous sessions.

This command is what 'atl setup-hooks' registers on the SessionStart hook.
Adding new boot-time tasks here means hook config does NOT need to be
re-run — users who already enabled hooks pick the new tasks up on their
next Claude Code session.

For routine "I want a fresh refresh in this conversation" use cases, see
the /refresh skill (PR 2A.4) which calls this command with verbose output.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Step 1: cache pull. Self-check stays at 24h regardless of
			// throttle (binary releases are weekly at most; checking
			// every session start would spam GitHub).
			res := updater.Run(updater.Options{
				RepoThrottle:  0,
				SelfThrottle:  24 * time.Hour,
				SkipSelfCheck: false,
				CheckOnly:     false,
				Verbose:       verbose,
			})

			// Step 2: project-local migration + auto-refresh. Same code
			// path as `atl update` — see internal/updater/projectsteps.go.
			updater.RunPerProjectSteps(false)

			// Step 3 (PR 2A.2): scan unprocessed transcripts for inline
			// <!-- learning --> markers. The output appears in Claude's
			// additionalContext (via SessionStart hook injection) so the
			// next-turn assistant call can act on it via /save-learnings
			// --from-markers --transcripts ...
			//
			// Best-effort: any failure inside runFromPreviousTranscripts is
			// swallowed (it returns nil even on error to keep hooks
			// non-blocking) and prints to stderr at most. The cache pull
			// summary is unaffected.
			_ = runFromPreviousTranscripts(silentIfClean)

			// Cache-pull summary printed last so per-project + learning
			// lines stay near the top of the output.
			summary := res.FormatSummary(silentIfClean)
			if summary != "" {
				fmt.Print(summary)
			}

			return nil
		},
	}

	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Print git operations and per-step progress")
	cmd.Flags().BoolVar(&silentIfClean, "silent-if-clean", false, "Produce no output if nothing changed (for hooks)")
	return cmd
}
