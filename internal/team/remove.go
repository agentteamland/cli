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

	// Wipe every atl-managed resource under .claude/{agents,skills,rules}/.
	// We use a name allowlist (the resources currently in the manifest,
	// across all teams) so user-authored project-local agents/rules/skills
	// — files NOT registered with atl — are left untouched.
	projectClaude := config.ProjectClaudeDir(cwd)
	if err := wipeAtlManagedResources(projectClaude, m); err != nil {
		return fmt.Errorf("wipe managed resources: %w", err)
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

// wipeAtlManagedResources removes everything under .claude/{agents,rules,skills}/
// that the manifest identifies as atl-managed. User-authored project-local
// resources (files / directories not registered with any installed team) are
// left untouched.
//
// Resources are matched by name against the union of every installed team's
// effective set:
//   - agents/{name}.md
//   - rules/{name}.md
//   - skills/{name}/  (directory)
//
// This works for both legacy symlinks (still possible during the brief
// migration window between PR-1 ship and the user's first `atl update`) and
// PR-1's project-local copies. RemoveAll handles both transparently.
func wipeAtlManagedResources(projectClaude string, m *TeamInstallsManifest) error {
	managed := managedNames(m)
	for sub, names := range managed {
		dir := filepath.Join(projectClaude, sub)
		var pattern func(name string) string
		switch sub {
		case "agents", "rules":
			pattern = func(name string) string { return filepath.Join(dir, name+".md") }
		case "skills":
			pattern = func(name string) string { return filepath.Join(dir, name) }
		default:
			continue
		}
		for name := range names {
			_ = os.RemoveAll(pattern(name))
		}
	}
	return nil
}

// managedNames flattens every installed team's effective set into a set
// keyed by kind ("agents" / "rules" / "skills") then by name. Names from
// multiple teams collapse into a single entry, which is what we want — we
// remove the resource from disk regardless of which team owned the
// "winning" copy.
func managedNames(m *TeamInstallsManifest) map[string]map[string]struct{} {
	out := map[string]map[string]struct{}{
		"agents": {},
		"rules":  {},
		"skills": {},
	}
	if m == nil {
		return out
	}
	for _, t := range m.Teams {
		for _, kind := range []string{"agents", "rules", "skills"} {
			for _, name := range t.Effective[kind] {
				out[kind][name] = struct{}{}
			}
		}
	}
	return out
}

// CountLocalModifications walks the given installed team's resources and
// reports how many project copies have content hashes that diverge from
// the manifest's installed-time baseline. Used by `atl remove` to surface
// the "discarding local changes" confirm prompt before the destructive op.
//
// This is the exported sibling of install.go's countLocalModifications;
// they share semantics but install computes during install flow (where the
// internal helper has direct access to canonicalName), and remove needs it
// from the CLI command dispatch.
func CountLocalModifications(cwd, teamName string) int {
	return countLocalModifications(cwd, teamName)
}

func writeManifestFile(cwd string, m *TeamInstallsManifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(config.TeamInstallsManifest(cwd), data, 0o644)
}
