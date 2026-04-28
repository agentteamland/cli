package team

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/agentteamland/cli/internal/config"
)

// Remove deletes the named team from the project and replays all remaining
// installed teams' symlinks from scratch (wipe-and-replay pattern).
//
// This design removes the need to track per-symlink ownership: symlinks are
// the deterministic output of "apply every installed team in installedAt
// order." Remove simply filters out the target team and re-runs that algorithm.
//
// Net effect:
//   - All of the target team's symlinks are gone.
//   - Items the target team was "winning" (via collision overwrite of a
//     previously-installed team's same-named item) correctly fall back to
//     the original owner.
//   - Cached source repos under ~/.claude/repos/agentteamland/ are left
//     intact (they're shared across projects).
//
// Performance: symlink creation is ~5-10ms per item; no git / network work.
func Remove(name, cwd string) error {
	m, err := List(cwd)
	if err != nil {
		return err
	}

	// Locate the target.
	idx := -1
	for i := range m.Teams {
		if m.Teams[i].Name == name {
			idx = i
			break
		}
	}
	if idx == -1 {
		return fmt.Errorf("team %q is not installed in this project", name)
	}

	// Build the list of remaining teams, sorted by original installedAt so
	// replay reproduces the same last-wins outcome for collisions.
	remaining := make([]InstalledTeam, 0, len(m.Teams)-1)
	for i, t := range m.Teams {
		if i == idx {
			continue
		}
		remaining = append(remaining, t)
	}
	sort.SliceStable(remaining, func(i, j int) bool {
		return remaining[i].InstalledAt < remaining[j].InstalledAt
	})

	// Wipe every symlink under .claude/{agents,skills,rules}/.
	// Only symlinks are removed; any real files (e.g. project-local agents)
	// are preserved.
	projectClaude := config.ProjectClaudeDir(cwd)
	if err := wipeSymlinks(projectClaude); err != nil {
		return fmt.Errorf("wipe symlinks: %w", err)
	}

	// Write the reduced manifest BEFORE replay. That way, the replay's
	// collision-detection logic sees the correct "already installed" state
	// at each step (no stale entry for the team being removed).
	m.Teams = remaining
	if err := writeManifestFile(cwd, m); err != nil {
		return err
	}

	// Replay remaining teams in install-order. Collision warnings are
	// suppressed here — they were already shown when each team was
	// originally installed; replaying doesn't warrant re-surfacing them.
	for _, t := range remaining {
		if _, _, err := resolveAndSymlink(t.Name, cwd, false, false); err != nil {
			return fmt.Errorf("replay %s: %w", t.Name, err)
		}
	}

	return nil
}

// wipeSymlinks removes atl-managed entries under .claude/{agents,skills,rules}/
// in the given project directory. Real files NOT created by atl are left alone.
//
// For agents/ and rules/: only symlinks are removed (matches the install
// pattern — agents and rules are installed as symlinks).
//
// For skills/: BOTH symlinks AND directories containing skill.md are removed.
// Reason: install switched from symlink to copy in v0.3.0+ (workaround for
// Claude Code's skill-discovery symlink limitation, see install.go), so we
// must clean up real directories too. The "directory containing skill.md"
// heuristic distinguishes atl-managed copies from any user-authored
// project-local skills the user may have placed under .claude/skills/.
func wipeSymlinks(projectClaude string) error {
	for _, sub := range []string{"agents", "skills", "rules"} {
		dir := filepath.Join(projectClaude, sub)
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		for _, e := range entries {
			p := filepath.Join(dir, e.Name())
			info, err := os.Lstat(p)
			if err != nil {
				continue
			}
			// Symlinks: always remove (atl-installed convention).
			if info.Mode()&os.ModeSymlink != 0 {
				_ = os.Remove(p)
				continue
			}
			// Skills directory: also wipe real directories that contain
			// skill.md (atl-installed copies post-v0.3.0). User-authored
			// skills at this path get the same treatment — but the
			// reinstall-after-wipe pattern means atl-managed ones come
			// back. Project-local skills not registered with atl will be
			// lost in remove + reinstall flows; document this as a known
			// trade-off in atl docs.
			if sub == "skills" && info.IsDir() {
				skillManifest := filepath.Join(p, "skill.md")
				if _, err := os.Stat(skillManifest); err == nil {
					_ = os.RemoveAll(p)
				}
			}
		}
	}
	return nil
}

func writeManifestFile(cwd string, m *TeamInstallsManifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(config.TeamInstallsManifest(cwd), data, 0o644)
}
