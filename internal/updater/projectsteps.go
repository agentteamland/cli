package updater

import "fmt"

// RunPerProjectSteps performs the post-cache-pull project-local steps:
//
//  1. One-time legacy symlink → copy migration (Q2 of install-mechanism-redesign)
//  2. Auto-refresh of unmodified resource copies (Q3)
//
// Both are no-ops when the current directory is not inside an atl-managed
// project (FindProjectRoot returns ""). Both are idempotent — safe to run
// repeatedly. Output is printed directly to stdout for the caller's user
// to see (atl update or atl session-start).
//
// checkOnly mirrors `atl update --check-only` semantics: skip migration
// and refresh writes; only the cache pull happened upstream of this call.
//
// Extracted from cmd/atl/commands/update.go in 2026-05-02 so that
// `atl session-start` (the new boot-time composite command) and
// `atl update` (the existing routine update command) share the same
// per-project-step logic. Adding new boot-time steps becomes a
// single-place edit.
func RunPerProjectSteps(checkOnly bool) {
	if checkOnly {
		return
	}

	root, err := FindProjectRoot()
	if err != nil || root == "" {
		return
	}

	// Q2 — symlink → copy migration (legacy topology cleanup).
	if _, summary, _ := MigrateProjectInstall(root); summary != "" {
		fmt.Println(summary)
	}

	// Q3 — refresh unmodified copies; skip those with local mutations
	// (preserving self-updating-learning-loop work and any hand edits).
	if refreshSummary, err := RefreshUnmodifiedCopies(root); err == nil {
		if line := FormatRefreshSummary(refreshSummary, root); line != "" {
			fmt.Print(line)
		}
	}
}
