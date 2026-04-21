package team

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/agentteamland/cli/internal/config"
	"github.com/agentteamland/cli/internal/registry"
	"github.com/agentteamland/cli/internal/resolver"
	"github.com/fatih/color"
)

// InstallOptions controls install behavior.
type InstallOptions struct {
	CWD     string // project directory (working dir)
	Verbose bool
}

// InstallResult summarizes what Install did.
type InstallResult struct {
	TopLevelName    string
	TopLevelVersion string
	Chain           []string
	AgentsCount     int
	SkillsCount     int
	RulesCount      int
	Excluded        []string
	Status          string // e.g. "verified", "community", or ""
}

// Install resolves the given team (by name or URL), walks its extends chain,
// materializes symlinks into the project's .claude/, and writes .team-installs.json.
//
// Multi-team safe: when two installed teams declare an item with the same
// name, the most recently installed one wins (symlink overwrite) with a
// warning line. This mirrors npm / pip / gnu-stow semantics.
func Install(target string, opts InstallOptions) (*InstallResult, error) {
	resolved, regStatus, err := resolveAndSymlink(target, opts.CWD, opts.Verbose, true)
	if err != nil {
		return nil, err
	}

	// Write the manifest entry for this target (replacing any prior entry with the same name).
	if err := writeManifestEntry(opts.CWD, target, resolved, regStatus); err != nil {
		return nil, err
	}

	// Build result.
	topVersion := extractVersion(resolved.ExtendsChain)
	return &InstallResult{
		TopLevelName:    target,
		TopLevelVersion: topVersion,
		Chain:           resolved.ExtendsChain,
		AgentsCount:     len(resolved.Effective.Agents),
		SkillsCount:     len(resolved.Effective.Skills),
		RulesCount:      len(resolved.Effective.Rules),
		Excluded:        resolved.Excluded,
		Status:          regStatus,
	}, nil
}

// resolveAndSymlink does the heavy lifting shared between public Install
// and Remove's wipe-and-replay path: registry lookup, dependency resolution,
// symlink materialization (with collision warnings).
//
// When warnCollisions is true, an override of a symlink already owned by
// another installed team emits a one-line warning. Set false to suppress
// warnings during Remove's wipe-and-replay (the user didn't ask for them;
// they're expected).
func resolveAndSymlink(target, cwd string, verbose, warnCollisions bool) (*resolver.Resolved, string, error) {
	// Fetch registry (best-effort; offline fallback handled by loader).
	reg, err := registry.Fetch()
	if err != nil && verbose {
		fmt.Fprintf(os.Stderr, color.YellowString("⚠ could not fetch registry: %v\n"), err)
	}

	loader := &Loader{Registry: reg, Verbose: verbose}

	// Registry status check (community/deprecated warnings).
	regStatus := ""
	if reg != nil && !isURL(target) {
		if entry := reg.Find(target); entry != nil {
			regStatus = entry.Status
			if entry.Status == "community" {
				color.Yellow("⚠ Note: %q is a community team (not reviewed by AgentTeamLand).", target)
			} else if entry.Status == "deprecated" {
				color.Red("⚠ %q is DEPRECATED.", target)
				if entry.ReplacedBy != "" {
					fmt.Fprintf(os.Stderr, "  Consider using %q instead.\n", entry.ReplacedBy)
				}
			}
		}
	}

	// Resolve full chain.
	resolved, err := resolver.Resolve(target, loader)
	if err != nil {
		return nil, "", err
	}

	// Load existing manifest to detect collisions with previously installed teams.
	var existingOwnership map[string]ownedEntry
	if warnCollisions {
		existingOwnership = ownershipMap(cwd, target)
	}

	// Ensure target subdirectories exist.
	projectClaude := config.ProjectClaudeDir(cwd)
	for _, sub := range []string{"agents", "skills", "rules"} {
		if err := os.MkdirAll(filepath.Join(projectClaude, sub), 0o755); err != nil {
			return nil, "", fmt.Errorf("mkdir %s: %w", sub, err)
		}
	}

	// Materialize symlinks.
	for _, a := range resolved.Effective.Agents {
		src := filepath.Join(config.TeamRepoDir(a.SourceTeam), "agents", a.Name, "agent.md")
		if _, err := os.Stat(src); err != nil {
			return nil, "", fmt.Errorf("agent source missing: %s", src)
		}
		dst := filepath.Join(projectClaude, "agents", a.Name+".md")
		maybeWarnCollision(warnCollisions, existingOwnership, "agent", a.Name, target)
		_ = os.Remove(dst)
		if err := os.Symlink(src, dst); err != nil {
			return nil, "", fmt.Errorf("symlink agent %s: %w", a.Name, err)
		}
	}
	for _, s := range resolved.Effective.Skills {
		src := filepath.Join(config.TeamRepoDir(s.SourceTeam), "skills", s.Name)
		if _, err := os.Stat(src); err != nil {
			continue // skill dir may be optional
		}
		dst := filepath.Join(projectClaude, "skills", s.Name)
		maybeWarnCollision(warnCollisions, existingOwnership, "skill", s.Name, target)
		_ = os.Remove(dst)
		if err := os.Symlink(src, dst); err != nil {
			return nil, "", fmt.Errorf("symlink skill %s: %w", s.Name, err)
		}
	}
	for _, r := range resolved.Effective.Rules {
		src := filepath.Join(config.TeamRepoDir(r.SourceTeam), "rules", r.Name+".md")
		if _, err := os.Stat(src); err != nil {
			continue
		}
		dst := filepath.Join(projectClaude, "rules", r.Name+".md")
		maybeWarnCollision(warnCollisions, existingOwnership, "rule", r.Name, target)
		_ = os.Remove(dst)
		if err := os.Symlink(src, dst); err != nil {
			return nil, "", fmt.Errorf("symlink rule %s: %w", r.Name, err)
		}
	}

	return resolved, regStatus, nil
}

// ownedEntry is a reverse-lookup: which installed team currently owns a given
// item name for a given kind (agent/skill/rule).
type ownedEntry struct {
	kind     string // "agent" | "skill" | "rule"
	teamName string
}

// ownershipMap builds a name→owner lookup from the existing manifest,
// skipping entries for the team we're about to re-install (since it's
// replacing its own previous install).
func ownershipMap(cwd, excludeTeam string) map[string]ownedEntry {
	out := map[string]ownedEntry{}
	m, err := List(cwd)
	if err != nil {
		return out
	}
	for _, t := range m.Teams {
		if t.Name == excludeTeam {
			continue
		}
		for _, a := range t.Effective["agents"] {
			out["agent:"+a] = ownedEntry{kind: "agent", teamName: t.Name}
		}
		for _, s := range t.Effective["skills"] {
			out["skill:"+s] = ownedEntry{kind: "skill", teamName: t.Name}
		}
		for _, r := range t.Effective["rules"] {
			out["rule:"+r] = ownedEntry{kind: "rule", teamName: t.Name}
		}
	}
	return out
}

func maybeWarnCollision(enabled bool, owners map[string]ownedEntry, kind, name, newTeam string) {
	if !enabled || owners == nil {
		return
	}
	key := kind + ":" + name
	prev, ok := owners[key]
	if !ok {
		return
	}
	color.Yellow("⚠ overriding %s %q (was from %s, now from %s)",
		kind, name, prev.teamName, newTeam)
}

func extractVersion(chain []string) string {
	if len(chain) == 0 {
		return ""
	}
	label := chain[0]
	for i := len(label) - 1; i >= 0; i-- {
		if label[i] == '@' {
			return label[i+1:]
		}
	}
	return ""
}

// TeamInstallsManifest is the .claude/.team-installs.json shape.
type TeamInstallsManifest struct {
	Teams []InstalledTeam `json:"teams"`
}

// InstalledTeam records one installed team's effective state.
type InstalledTeam struct {
	Name         string              `json:"name"`
	Repo         string              `json:"repo"`
	Version      string              `json:"version"`
	InstalledAt  string              `json:"installedAt"`
	SourceDir    string              `json:"sourceDir"`
	ExtendsChain []string            `json:"extendsChain"`
	Effective    map[string][]string `json:"effective"`
	Status       string              `json:"status,omitempty"`
}

func writeManifestEntry(cwd, topName string, resolved *resolver.Resolved, status string) error {
	path := config.TeamInstallsManifest(cwd)

	// Load existing manifest (if any).
	var m TeamInstallsManifest
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &m)
	}

	// Preserve original installedAt for the target (if re-installing / updating)
	// so wipe-and-replay ordering in Remove stays stable.
	var preservedInstalledAt string
	filtered := m.Teams[:0]
	for _, t := range m.Teams {
		if t.Name == topName {
			preservedInstalledAt = t.InstalledAt
			continue
		}
		filtered = append(filtered, t)
	}

	installedAt := preservedInstalledAt
	if installedAt == "" {
		installedAt = time.Now().UTC().Format(time.RFC3339)
	}

	eff := map[string][]string{
		"agents": itemNames(resolved.Effective.Agents),
		"skills": itemNames(resolved.Effective.Skills),
		"rules":  itemNames(resolved.Effective.Rules),
	}

	filtered = append(filtered, InstalledTeam{
		Name:         topName,
		Repo:         "", // Populated from resolved URL in a future version.
		Version:      extractVersion(resolved.ExtendsChain),
		InstalledAt:  installedAt,
		SourceDir:    "~/.claude/repos/agentteamland/" + topName,
		ExtendsChain: resolved.ExtendsChain,
		Effective:    eff,
		Status:       status,
	})
	m.Teams = filtered

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	return nil
}

func itemNames(items []resolver.ResolvedItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Name
	}
	return out
}
