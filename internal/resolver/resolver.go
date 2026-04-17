// Package resolver resolves a team's full inheritance chain, applies override
// and excludes semantics, and produces the final effective set of agents/skills/rules
// to materialize as symlinks.
//
// See agentteamland/team-manager/skill/skill.md for the canonical semantics —
// this implementation mirrors those rules.
package resolver

import (
	"fmt"
	"strings"

	"github.com/agentteamland/cli/internal/config"
	"github.com/agentteamland/cli/internal/manifest"
)

// Resolved is the outcome of resolving an install request.
type Resolved struct {
	// ExtendsChain lists the effective install order, child-first (index 0) → root ancestor (last).
	// Each element is formatted "name@version".
	ExtendsChain []string

	// Effective is the final merged set of items to symlink into the project.
	Effective Effective

	// Excluded names (from any level) that were dropped.
	Excluded []string

	// Repos is the set of cached repo directories involved (for cleanup, status, etc.).
	Repos []string
}

// Effective captures the final, post-override, post-excludes set of items.
type Effective struct {
	Agents []ResolvedItem
	Skills []ResolvedItem
	Rules  []ResolvedItem
}

// ResolvedItem is one named item with its winning source team.
type ResolvedItem struct {
	Name       string
	SourceTeam string // team repo directory name where this item lives
	Desc       string
}

// Loader provides the teams needed during resolution. It must clone and read
// a team by its short name. The resolver does not perform I/O itself.
type Loader interface {
	// Load returns the manifest and repo dir for a team, handling clone/pull as needed.
	// name may be a short name (registry lookup) or an arbitrary URL fallback.
	// The constraint is the version range requested (e.g. "^1.0.0") — may be empty for top-level.
	Load(name, constraint string) (*manifest.TeamManifest, string, error)
}

// Resolve walks the extends chain starting from the top-level team and builds
// the effective set. Circular dependencies are detected and reported.
func Resolve(topName string, loader Loader) (*Resolved, error) {
	// visited tracks install state for cycle detection:
	// state "installing" → currently in the chain (seeing again = cycle)
	// state "done" → already fully resolved in this session
	visited := map[string]string{}

	// Build chain root-to-child by walking extends upward, then reverse.
	// We collect the chain first, then apply merge semantics in a separate pass.
	chainChildFirst := []*manifest.TeamManifest{}
	chainRepos := []string{}
	chainLabels := []string{}

	currentName := topName
	currentConstraint := ""

	for {
		if state, seen := visited[currentName]; seen {
			if state == "installing" {
				// Cycle. Build a helpful error message.
				cycle := []string{currentName}
				for _, m := range chainChildFirst {
					cycle = append(cycle, m.Name)
				}
				cycle = append(cycle, currentName)
				return nil, fmt.Errorf("circular dependency detected:\n  %s\n\nFix: break the cycle by removing or relocating the 'extends' field in one of the teams above",
					strings.Join(cycle, " → "))
			}
			// "done" — already resolved in a prior branch (not possible with single inheritance,
			// but guard anyway).
			break
		}
		visited[currentName] = "installing"

		m, repoDir, err := loader.Load(currentName, currentConstraint)
		if err != nil {
			return nil, fmt.Errorf("load %q: %w", currentName, err)
		}
		chainChildFirst = append(chainChildFirst, m)
		chainRepos = append(chainRepos, repoDir)
		chainLabels = append(chainLabels, m.Name+"@"+m.Version)

		parentName, parentConstraint, hasParent := m.ParseExtends()
		if !hasParent {
			break
		}
		currentName = parentName
		currentConstraint = parentConstraint
	}

	// Mark all as done.
	for _, m := range chainChildFirst {
		visited[m.Name] = "done"
	}

	// Merge parent-first (so child overrides). Reverse the chain.
	// Also, excludes accumulate across the whole chain.
	agents := map[string]ResolvedItem{}
	skills := map[string]ResolvedItem{}
	rules := map[string]ResolvedItem{}
	excludes := map[string]struct{}{}

	for i := len(chainChildFirst) - 1; i >= 0; i-- {
		m := chainChildFirst[i]
		repoDir := chainRepos[i]
		sourceKey := pathBase(repoDir)

		for _, x := range m.Excludes {
			excludes[x] = struct{}{}
		}
		for _, a := range m.Agents {
			agents[a.Name] = ResolvedItem{Name: a.Name, SourceTeam: sourceKey, Desc: a.Description}
		}
		for _, s := range m.Skills {
			skills[s.Name] = ResolvedItem{Name: s.Name, SourceTeam: sourceKey, Desc: s.Description}
		}
		for _, r := range m.Rules {
			rules[r.Name] = ResolvedItem{Name: r.Name, SourceTeam: sourceKey, Desc: r.Description}
		}
	}

	// Apply excludes (drop from all three buckets).
	excludedList := make([]string, 0, len(excludes))
	for x := range excludes {
		excludedList = append(excludedList, x)
		delete(agents, x)
		delete(skills, x)
		delete(rules, x)
	}

	effective := Effective{
		Agents: sortedValues(agents),
		Skills: sortedValues(skills),
		Rules:  sortedValues(rules),
	}

	return &Resolved{
		ExtendsChain: chainLabels,
		Effective:    effective,
		Excluded:     excludedList,
		Repos:        chainRepos,
	}, nil
}

// RepoDirFor returns the expected cached-repo path for a team by short name.
func RepoDirFor(name string) string {
	return config.TeamRepoDir(name)
}

// --- helpers ---

func pathBase(dir string) string {
	// Last path segment.
	for i := len(dir) - 1; i >= 0; i-- {
		if dir[i] == '/' {
			return dir[i+1:]
		}
	}
	return dir
}

func sortedValues(m map[string]ResolvedItem) []ResolvedItem {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Simple alphabetical sort.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	out := make([]ResolvedItem, len(keys))
	for i, k := range keys {
		out[i] = m[k]
	}
	return out
}
