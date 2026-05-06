package commands

import (
	"fmt"
	"os"

	"github.com/agentteamland/cli/internal/atlmigrate"
	"github.com/spf13/cobra"
)

// NewMigrate builds the `atl migrate` command.
//
// Per the atl-config-system decision (workspace
// .claude/docs/atl-config-system.md § State-file migration), atl owns a
// dedicated ~/.atl/ tree. Five state files move from their legacy
// ~/.claude/ locations:
//
//	~/.claude/state/learning-capture-state.json -> ~/.atl/state/learning-capture-state.json
//	~/.claude/state/docs-sync-state.json        -> ~/.atl/state/docs-sync-state.json
//	~/.claude/atl-install-marker.json           -> ~/.atl/install-marker.json
//	~/.claude/cache/atl-last-repo-check         -> ~/.atl/cache/last-repo-check
//	~/.claude/cache/atl-last-self-check         -> ~/.atl/cache/last-self-check
//
// The migration is auto-triggered from `atl update` and `atl session-start`
// — most users never need to run this command directly. It exists for
// explicit invocation (post-incident recovery, manual resync after a
// dotfile sync conflict, dry-run inspection of what would move).
//
// Idempotent: re-running on a fully-migrated home is a no-op.
func NewMigrate() *cobra.Command {
	var verbose bool

	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Move atl state files from ~/.claude/ to ~/.atl/ (idempotent)",
		Long: `Move atl's state files from their legacy ~/.claude/ locations to the new
~/.atl/ tree. This runs automatically on every 'atl update' and 'atl
session-start' — manual invocation is rarely needed.

Five files are migrated in one pass:

  ~/.claude/state/learning-capture-state.json  ->  ~/.atl/state/learning-capture-state.json
  ~/.claude/state/docs-sync-state.json         ->  ~/.atl/state/docs-sync-state.json
  ~/.claude/atl-install-marker.json            ->  ~/.atl/install-marker.json
  ~/.claude/cache/atl-last-repo-check          ->  ~/.atl/cache/last-repo-check
  ~/.claude/cache/atl-last-self-check          ->  ~/.atl/cache/last-self-check

The migration is idempotent — files already at the new location are
skipped, files that never existed are skipped, and re-running on a
fully-migrated home prints nothing.

Decision context: workspace .claude/docs/atl-config-system.md.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			var sink = os.Stderr
			if !verbose {
				// Quiet by default: only show output when actually moving files.
				sink = os.Stderr // we still want migrate-line on stderr; keep
			}
			result, err := atlmigrate.Migrate(sink)
			if err != nil {
				// Failures already printed line-by-line via Migrate's stderr
				// writer; surface a summary with non-zero exit.
				return fmt.Errorf("atl migrate: %d file(s) failed", len(result.Failed))
			}
			if !result.HasMigrations() {
				if verbose {
					fmt.Fprintln(os.Stderr, "atl migrate: nothing to migrate (state already at ~/.atl/, or no legacy state present)")
				}
				return nil
			}
			fmt.Fprintf(os.Stderr, "atl migrate: %d file(s) moved\n", len(result.Migrated))
			return nil
		},
	}

	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Print 'nothing to migrate' on a clean home (default: silent)")
	return cmd
}
