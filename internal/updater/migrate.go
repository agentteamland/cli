package updater

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// MigrateProjectInstall scans a project's .claude/agents/ and .claude/rules/
// directories, detects symlinks (legacy install topology before atl v1.0),
// and converts them to plain file copies in place. Idempotent: no work is
// done if no symlinks exist.
//
// This is the one-time bridge from the old symlink-to-global-cache model to
// the new self-contained-project model. It runs as a step inside `atl update`
// (which itself is auto-triggered by the SessionStart hook), so the user
// experiences zero manual action — symlinks turn into copies on the first
// session after upgrading atl.
//
// Returns:
//   - migrated: total number of items converted (agents + rules)
//   - summary: a one-line info string for atl update output, empty when nothing was migrated
//   - err: a non-fatal error if any individual conversion failed; partial
//     migration is allowed (each kind is best-effort), and the summary still
//     reflects what succeeded
func MigrateProjectInstall(projectPath string) (migrated int, summary string, err error) {
	claudeDir := filepath.Join(projectPath, ".claude")
	if _, statErr := os.Stat(claudeDir); os.IsNotExist(statErr) {
		return 0, "", nil
	}

	agents, agentErr := migrateSymlinksInDir(filepath.Join(claudeDir, "agents"))
	rules, ruleErr := migrateSymlinksInDir(filepath.Join(claudeDir, "rules"))

	total := agents + rules
	if total == 0 {
		return 0, "", nil
	}

	summary = fmt.Sprintf(
		"🔄 Migrated %d agent%s + %d rule%s to project-local copies (one-time topology upgrade)",
		agents, plural(agents), rules, plural(rules),
	)

	if agentErr != nil || ruleErr != nil {
		err = fmt.Errorf("migration partial: agents=%v rules=%v", agentErr, ruleErr)
	}
	return total, summary, err
}

// migrateSymlinksInDir walks a single directory, finds symlinks, and replaces
// each with a real copy of its target's content. Returns the conversion count
// for this directory.
func migrateSymlinksInDir(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	count := 0
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		info, lstatErr := os.Lstat(path)
		if lstatErr != nil {
			continue
		}
		if info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		target, readlinkErr := os.Readlink(path)
		if readlinkErr != nil {
			continue
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(dir, target)
		}
		targetInfo, statErr := os.Stat(target)
		if statErr != nil {
			// Broken symlink — leave it for the user to clean up; don't silently delete.
			continue
		}
		if err := replaceSymlinkWithCopy(path, target, targetInfo.Mode().Perm()); err != nil {
			continue
		}
		count++
	}
	return count, nil
}

// replaceSymlinkWithCopy removes a symlink at symlinkPath and writes a real
// file copy of targetPath's content in its place, with the given mode.
func replaceSymlinkWithCopy(symlinkPath, targetPath string, mode os.FileMode) error {
	if err := os.Remove(symlinkPath); err != nil {
		return err
	}
	in, err := os.Open(targetPath)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(symlinkPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// FindProjectRoot walks upward from the current working directory until it
// finds a directory containing .claude/.team-installs.json (the marker of an
// atl-managed project). Returns the project root path, or empty string if no
// project is found within the directory chain up to root.
//
// Used by `atl update` to know which project to migrate + refresh. The
// SessionStart hook fires inside a project's working directory, so cwd is
// the natural starting point.
func FindProjectRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		marker := filepath.Join(cwd, ".claude", ".team-installs.json")
		if _, err := os.Stat(marker); err == nil {
			return cwd, nil
		}
		parent := filepath.Dir(cwd)
		if parent == cwd {
			return "", nil
		}
		cwd = parent
	}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
