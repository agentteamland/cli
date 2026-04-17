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
func Install(target string, opts InstallOptions) (*InstallResult, error) {
	// Fetch registry (best-effort; offline fallback handled by loader).
	reg, err := registry.Fetch()
	if err != nil && opts.Verbose {
		fmt.Fprintf(os.Stderr, color.YellowString("⚠ could not fetch registry: %v\n"), err)
	}

	loader := &Loader{Registry: reg, Verbose: opts.Verbose}

	// If input matches a registry entry with status=community, show a warning.
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
		return nil, err
	}

	// Materialize symlinks into project .claude/.
	projectClaude := config.ProjectClaudeDir(opts.CWD)
	if err := os.MkdirAll(filepath.Join(projectClaude, "agents"), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir agents: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(projectClaude, "skills"), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir skills: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(projectClaude, "rules"), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir rules: %w", err)
	}

	for _, a := range resolved.Effective.Agents {
		src := filepath.Join(config.TeamRepoDir(a.SourceTeam), "agents", a.Name, "agent.md")
		if _, err := os.Stat(src); err != nil {
			return nil, fmt.Errorf("agent source missing: %s", src)
		}
		dst := filepath.Join(projectClaude, "agents", a.Name+".md")
		_ = os.Remove(dst)
		if err := os.Symlink(src, dst); err != nil {
			return nil, fmt.Errorf("symlink agent %s: %w", a.Name, err)
		}
	}
	for _, s := range resolved.Effective.Skills {
		src := filepath.Join(config.TeamRepoDir(s.SourceTeam), "skills", s.Name)
		if _, err := os.Stat(src); err != nil {
			continue // skill dir may be optional
		}
		dst := filepath.Join(projectClaude, "skills", s.Name)
		_ = os.Remove(dst)
		if err := os.Symlink(src, dst); err != nil {
			return nil, fmt.Errorf("symlink skill %s: %w", s.Name, err)
		}
	}
	for _, r := range resolved.Effective.Rules {
		src := filepath.Join(config.TeamRepoDir(r.SourceTeam), "rules", r.Name+".md")
		if _, err := os.Stat(src); err != nil {
			continue
		}
		dst := filepath.Join(projectClaude, "rules", r.Name+".md")
		_ = os.Remove(dst)
		if err := os.Symlink(src, dst); err != nil {
			return nil, fmt.Errorf("symlink rule %s: %w", r.Name, err)
		}
	}

	// Write .team-installs.json (replace any prior entry for the same team).
	if err := writeManifest(opts.CWD, target, resolved, regStatus); err != nil {
		return nil, err
	}

	// Build result.
	topVersion := ""
	if len(resolved.ExtendsChain) > 0 {
		// "name@version" → take the "version" after the first '@'.
		label := resolved.ExtendsChain[0]
		for i := len(label) - 1; i >= 0; i-- {
			if label[i] == '@' {
				topVersion = label[i+1:]
				break
			}
		}
	}

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

func writeManifest(cwd, topName string, resolved *resolver.Resolved, status string) error {
	path := config.TeamInstallsManifest(cwd)

	// Load existing manifest (if any).
	var m TeamInstallsManifest
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &m)
	}

	// Remove any entry with the same top-level name.
	filtered := m.Teams[:0]
	for _, t := range m.Teams {
		if t.Name != topName {
			filtered = append(filtered, t)
		}
	}

	// Build effective lists.
	eff := map[string][]string{
		"agents": itemNames(resolved.Effective.Agents),
		"skills": itemNames(resolved.Effective.Skills),
		"rules":  itemNames(resolved.Effective.Rules),
	}

	version := ""
	if len(resolved.ExtendsChain) > 0 {
		label := resolved.ExtendsChain[0]
		for i := len(label) - 1; i >= 0; i-- {
			if label[i] == '@' {
				version = label[i+1:]
				break
			}
		}
	}

	filtered = append(filtered, InstalledTeam{
		Name:         topName,
		Repo:         "", // We could populate this from the resolved URL; left blank for v0.1.0 simplicity.
		Version:      version,
		InstalledAt:  time.Now().UTC().Format(time.RFC3339),
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
