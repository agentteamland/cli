// Package atlmigrate moves atl's per-user state files from their legacy
// location under ~/.claude/ to ~/.atl/, the new dedicated atl directory.
//
// Five files migrate (decision: workspace .atl/docs/atl-config-system.md
// § State-file migration):
//
//	~/.claude/state/learning-capture-state.json -> ~/.atl/state/learning-capture-state.json
//	~/.claude/state/docs-sync-state.json        -> ~/.atl/state/docs-sync-state.json
//	~/.claude/atl-install-marker.json           -> ~/.atl/install-marker.json
//	~/.claude/cache/atl-last-repo-check         -> ~/.atl/cache/last-repo-check
//	~/.claude/cache/atl-last-self-check         -> ~/.atl/cache/last-self-check
//
// The atl- prefix is dropped under .atl/ (redundant). Move semantics:
// atomic os.Rename when possible; cross-disk fallback to copy+remove.
// No backup file is created — state is regenerable.
//
// The migration runs idempotently:
//   - If newPath already exists, the pair is skipped (treat as already migrated).
//   - If oldPath does not exist, the pair is skipped (nothing to migrate).
//   - Otherwise the file is moved.
//
// Auto-triggered from `atl update` and `atl session-start`; manual entry
// point is `atl migrate`. Existing read sites use Resolve(old, new) so
// the migration window (5 minor versions per the brainstorm) is seamless.
package atlmigrate

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// PathPair represents a single state file's old and new locations.
type PathPair struct {
	// Old is the legacy ~/.claude/... path.
	Old string
	// New is the canonical ~/.atl/... path.
	New string
	// Label is a short human-readable name (used in migration log lines).
	Label string
}

// PathPairs returns the 5 canonical state-file migrations with $HOME
// resolved at call time. Returns nil when $HOME is unresolvable —
// callers treat nil as "nothing to migrate".
func PathPairs() []PathPair {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	claudeHome := filepath.Join(home, ".claude")
	atlHome := filepath.Join(home, ".atl")
	return []PathPair{
		{
			Old:   filepath.Join(claudeHome, "state", "learning-capture-state.json"),
			New:   filepath.Join(atlHome, "state", "learning-capture-state.json"),
			Label: "learning-capture state",
		},
		{
			Old:   filepath.Join(claudeHome, "state", "docs-sync-state.json"),
			New:   filepath.Join(atlHome, "state", "docs-sync-state.json"),
			Label: "docs-sync state",
		},
		{
			Old:   filepath.Join(claudeHome, "atl-install-marker.json"),
			New:   filepath.Join(atlHome, "install-marker.json"),
			Label: "install marker",
		},
		{
			Old:   filepath.Join(claudeHome, "cache", "atl-last-repo-check"),
			New:   filepath.Join(atlHome, "cache", "last-repo-check"),
			Label: "repo cache stamp",
		},
		{
			Old:   filepath.Join(claudeHome, "cache", "atl-last-self-check"),
			New:   filepath.Join(atlHome, "cache", "last-self-check"),
			Label: "self-check cache stamp",
		},
	}
}

// Convenience accessors for the new (canonical) paths. Read sites that
// want old-path fallback should wrap these with Resolve.

// LearningCaptureStatePath returns ~/.atl/state/learning-capture-state.json.
func LearningCaptureStatePath() string {
	return findPair("learning-capture state").New
}

// DocsSyncStatePath returns ~/.atl/state/docs-sync-state.json.
func DocsSyncStatePath() string {
	return findPair("docs-sync state").New
}

// InstallMarkerPath returns ~/.atl/install-marker.json.
func InstallMarkerPath() string {
	return findPair("install marker").New
}

// RepoStampPath returns ~/.atl/cache/last-repo-check.
func RepoStampPath() string {
	return findPair("repo cache stamp").New
}

// SelfStampPath returns ~/.atl/cache/last-self-check.
func SelfStampPath() string {
	return findPair("self-check cache stamp").New
}

func findPair(label string) PathPair {
	for _, p := range PathPairs() {
		if p.Label == label {
			return p
		}
	}
	return PathPair{}
}

// Resolve returns the path that should be READ. New wins if it exists,
// old wins as fallback if new does not exist, and new is returned as the
// canonical destination when neither exists (writers go to new).
//
// This is the migration-window helper: existing read sites call
// Resolve(oldPath, newPath) so behavior is identical pre- and post-migration.
func Resolve(oldPath, newPath string) string {
	if _, err := os.Stat(newPath); err == nil {
		return newPath
	}
	if _, err := os.Stat(oldPath); err == nil {
		return oldPath
	}
	return newPath
}

// Result reports the outcome of a Migrate call.
type Result struct {
	// Migrated holds the labels of pairs that were actually moved.
	Migrated []string
	// Skipped holds the labels of pairs that did not need moving (old
	// absent, or new already present).
	Skipped []string
	// Failed holds per-pair errors for any rename that did not complete.
	Failed []FailedFile
}

// FailedFile pairs a label with the error that prevented its migration.
type FailedFile struct {
	Label string
	Err   error
}

// HasMigrations reports whether any file actually moved during the call.
// Useful to suppress noise when nothing happened (auto-triggered calls).
func (r Result) HasMigrations() bool { return len(r.Migrated) > 0 }

// Migrate moves the 5 state files from their legacy ~/.claude/ paths to
// the new ~/.atl/ paths. Idempotent: re-running the migration when nothing
// is left to move returns a Result with empty Migrated.
//
// Per-file failures do NOT abort the pass — every pair is attempted, and
// the returned error wraps every failure via errors.Join. Callers may
// inspect Result.Failed for per-file detail.
//
// stderr (when non-nil) receives one line per migrated file:
//
//	atlmigrate: <label>: <oldPath> -> <newPath>
func Migrate(stderr io.Writer) (Result, error) {
	pairs := PathPairs()
	var result Result

	for _, p := range pairs {
		moved, err := movePair(p)
		switch {
		case err != nil:
			result.Failed = append(result.Failed, FailedFile{Label: p.Label, Err: err})
		case moved:
			result.Migrated = append(result.Migrated, p.Label)
			if stderr != nil {
				fmt.Fprintf(stderr, "atlmigrate: %s: %s -> %s\n", p.Label, p.Old, p.New)
			}
		default:
			result.Skipped = append(result.Skipped, p.Label)
		}
	}

	if len(result.Failed) == 0 {
		return result, nil
	}
	errs := make([]error, 0, len(result.Failed))
	for _, f := range result.Failed {
		errs = append(errs, fmt.Errorf("%s: %w", f.Label, f.Err))
	}
	return result, errors.Join(errs...)
}

// movePair handles a single PathPair. Returns (moved=true, nil) when the
// rename completed, (moved=false, nil) when the pair was a no-op
// (idempotent or nothing to move), or (_, err) on a real failure.
func movePair(p PathPair) (bool, error) {
	if _, err := os.Stat(p.New); err == nil {
		return false, nil // already migrated
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("stat new %s: %w", p.New, err)
	}

	info, err := os.Stat(p.Old)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil // nothing to migrate
		}
		return false, fmt.Errorf("stat old %s: %w", p.Old, err)
	}
	if info.IsDir() {
		return false, fmt.Errorf("old path is a directory, expected file: %s", p.Old)
	}

	if err := os.MkdirAll(filepath.Dir(p.New), 0o755); err != nil {
		return false, fmt.Errorf("mkdir %s: %w", filepath.Dir(p.New), err)
	}

	if err := os.Rename(p.Old, p.New); err == nil {
		return true, nil
	}
	// os.Rename failed (commonly EXDEV on a cross-filesystem boundary).
	// Fall back to copy + verify + remove.
	if err := copyAndRemove(p.Old, p.New, info.Mode().Perm()); err != nil {
		return false, err
	}
	return true, nil
}

// copyAndRemove replicates a cross-disk move: copy oldPath into a temp
// file beside newPath, fsync + close, atomic-rename temp into newPath,
// then remove the original. On any failure before the final rename, the
// temp is cleaned up; the original remains intact.
func copyAndRemove(oldPath, newPath string, mode os.FileMode) error {
	src, err := os.Open(oldPath)
	if err != nil {
		return fmt.Errorf("open old %s: %w", oldPath, err)
	}
	defer src.Close()

	tmp := newPath + ".atlmigrate.tmp"
	dst, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("create temp %s: %w", tmp, err)
	}

	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("copy %s -> %s: %w", oldPath, tmp, err)
	}
	if err := dst.Sync(); err != nil {
		dst.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("fsync %s: %w", tmp, err)
	}
	if err := dst.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close temp %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, newPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename %s -> %s: %w", tmp, newPath, err)
	}
	if err := os.Remove(oldPath); err != nil {
		// New file is in place; old file failed to delete. Treat as
		// success — next migration run will skip (new exists). User may
		// manually clean up the old file.
		return nil
	}
	return nil
}
