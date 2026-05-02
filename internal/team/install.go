package team

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/agentteamland/cli/internal/checksum"
	"github.com/agentteamland/cli/internal/config"
	"github.com/agentteamland/cli/internal/registry"
	"github.com/agentteamland/cli/internal/resolver"
	"github.com/agentteamland/cli/internal/state"
	"github.com/fatih/color"
)

// InstallOptions controls install behavior.
type InstallOptions struct {
	CWD     string // project directory (working dir)
	Verbose bool

	// Refresh forces overwrite of an already-installed team. Without it,
	// `atl install <team>` is idempotent: it returns InstallStatusNoOp when
	// the team is already present, leaving project copies untouched. With
	// Refresh, the install proceeds and overwrites every resource — local
	// modifications (e.g. self-updating-learning-loop mutations) are
	// announced as "discarding local changes" before overwrite.
	Refresh bool
}

// InstallStatus indicates how Install handled the request.
type InstallStatus string

const (
	// InstallStatusInstalled is returned for a normal (first-time or
	// --refresh) install where copies were materialized.
	InstallStatusInstalled InstallStatus = "installed"

	// InstallStatusNoOp is returned when the team was already installed
	// and Refresh was not requested. Project copies were not touched.
	InstallStatusNoOp InstallStatus = "no-op"
)

// InstallResult summarizes what Install did.
type InstallResult struct {
	TopLevelName    string
	TopLevelVersion string
	Chain           []string
	AgentsCount     int
	SkillsCount     int
	RulesCount      int
	Excluded        []string
	Status          string // registry status: "verified", "community", or ""

	// Op describes the action Install performed (installed vs. no-op).
	// Set to InstallStatusNoOp only when the team was already present and
	// Refresh was not requested.
	Op InstallStatus

	// ModifiedResourceCount reports how many copies showed local changes
	// at the moment of refresh (the "discarding local changes" count).
	// Zero on first-time install. Populated only when Op == "installed"
	// AND Refresh == true.
	ModifiedResourceCount int
}

// Install resolves the given team (by name or URL), walks its extends chain,
// materializes copies into the project's .claude/, and writes .team-installs.json.
//
// Idempotent by default: if the team is already installed (matching by
// canonical name or slug), Install returns InstallStatusNoOp without
// touching project copies. Pass InstallOptions.Refresh = true to force
// overwrite — local changes (self-updating-learning-loop mutations or
// hand edits) will be reported and discarded.
//
// Multi-team safe: when two installed teams declare an item with the same
// name, the most recently installed one wins (overwrite) with a warning
// line. This mirrors npm / pip / gnu-stow semantics.
//
// `target` may be a short name, owner/repo shorthand, or a full URL. The
// manifest entry records the team's canonical name (from team.json), not
// the user's input form, so subsequent commands like `atl remove <name>`
// and `atl update <name>` work with the canonical name.
func Install(target string, opts InstallOptions) (*InstallResult, error) {
	// Idempotency check: did the user already install this team?
	if existingName, found := matchInstalledTeam(opts.CWD, target); found {
		if !opts.Refresh {
			return &InstallResult{
				TopLevelName: existingName,
				Op:           InstallStatusNoOp,
			}, nil
		}
		// Refresh requested. Surface what's about to be discarded so the
		// user sees their intent confirmed (or aborts via Ctrl+C).
		modified := countLocalModifications(opts.CWD, existingName)
		if modified > 0 {
			fmt.Fprintf(os.Stderr,
				color.YellowString("⚠ Discarding local changes (%d resource%s modified) in %s\n"),
				modified, plural(modified), existingName)
		}
	}

	resolved, regStatus, err := resolveAndSymlink(target, opts.CWD, opts.Verbose, true)
	if err != nil {
		return nil, err
	}

	// Canonical team name comes from team.json (preserved in extendsChain[0] as "name@version").
	canonicalName := canonicalNameFromChain(resolved.ExtendsChain)

	// Modified count for the result — only meaningful when Refresh was used.
	modifiedCount := 0
	if opts.Refresh {
		modifiedCount = countLocalModifications(opts.CWD, canonicalName)
	}

	// Write the manifest entry under the canonical name (replacing any prior entry with the same name).
	if err := writeManifestEntry(opts.CWD, canonicalName, resolved, regStatus); err != nil {
		return nil, err
	}

	// Build result.
	topVersion := extractVersion(resolved.ExtendsChain)
	return &InstallResult{
		TopLevelName:          canonicalName,
		TopLevelVersion:       topVersion,
		Chain:                 resolved.ExtendsChain,
		AgentsCount:           len(resolved.Effective.Agents),
		SkillsCount:           len(resolved.Effective.Skills),
		RulesCount:            len(resolved.Effective.Rules),
		Excluded:              resolved.Excluded,
		Status:                regStatus,
		Op:                    InstallStatusInstalled,
		ModifiedResourceCount: modifiedCount,
	}, nil
}

// matchInstalledTeam reports whether the given target (which may be a short
// name, owner/repo shorthand, or URL) corresponds to a team already in the
// project's manifest. Returns the canonical team name if matched.
func matchInstalledTeam(cwd, target string) (string, bool) {
	m, err := List(cwd)
	if err != nil {
		return "", false
	}
	for _, t := range m.Teams {
		if t.Name == target {
			return t.Name, true
		}
	}
	// Try slug derivation: take the last "/"-separated segment, strip
	// ".git" suffix. Catches "owner/repo" and full git URLs.
	slug := slugFromTarget(target)
	if slug == "" {
		return "", false
	}
	for _, t := range m.Teams {
		if t.Name == slug {
			return t.Name, true
		}
	}
	return "", false
}

// slugFromTarget extracts the likely team name from inputs like
// "agentteamland/design-system-team", "https://github.com/x/y.git", etc.
// Returns the empty string for inputs that already look like bare names.
func slugFromTarget(target string) string {
	last := target
	for i := len(target) - 1; i >= 0; i-- {
		if target[i] == '/' {
			last = target[i+1:]
			break
		}
	}
	if last == target {
		return ""
	}
	if strings.HasSuffix(last, ".git") {
		last = strings.TrimSuffix(last, ".git")
	}
	return last
}

// countLocalModifications walks the given installed team's resources and
// returns how many project copies have content hashes that diverge from
// the manifest's installed-time baseline. Used to surface the
// "discarding local changes" message before a --refresh overwrite.
func countLocalModifications(cwd, teamName string) int {
	m, err := List(cwd)
	if err != nil {
		return 0
	}
	var t *InstalledTeam
	for i := range m.Teams {
		if m.Teams[i].Name == teamName {
			t = &m.Teams[i]
			break
		}
	}
	if t == nil || t.InstalledChecksums == nil {
		return 0
	}
	projectClaude := config.ProjectClaudeDir(cwd)
	count := 0
	for kind, names := range t.Effective {
		for _, name := range names {
			expected := ""
			if t.InstalledChecksums[kind] != nil {
				expected = t.InstalledChecksums[kind][name]
			}
			if expected == "" {
				continue
			}
			path, srcKind := resourcePath(kind, name, projectClaude)
			if path == "" {
				continue
			}
			modified, err := state.IsResourceModified(path, expected, srcKind)
			if err == nil && modified {
				count++
			}
		}
	}
	return count
}

func resourcePath(kind, name, projectClaude string) (string, state.ResourceKind) {
	switch kind {
	case "agents":
		return filepath.Join(projectClaude, "agents", name+".md"), state.KindFile
	case "rules":
		return filepath.Join(projectClaude, "rules", name+".md"), state.KindFile
	case "skills":
		return filepath.Join(projectClaude, "skills", name), state.KindDir
	}
	return "", state.KindFile
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// canonicalNameFromChain returns the team's canonical name from its extends
// chain. The chain is "name@version" formatted, child-first, so element 0 is
// the top-level team. Falls back to empty string if chain is empty.
func canonicalNameFromChain(chain []string) string {
	if len(chain) == 0 {
		return ""
	}
	label := chain[0]
	for i := 0; i < len(label); i++ {
		if label[i] == '@' {
			return label[:i]
		}
	}
	return label
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

	// Materialize project copies.
	//
	// All three resource kinds (agents, skills, rules) are now COPIED into the
	// project's .claude/ tree. This unified copy paradigm replaces the earlier
	// mixed model (skills copied since v0.3.0, agents + rules symlinked).
	//
	// Why fully self-contained: when self-updating-learning-loop ships,
	// /save-learnings can mutate agent + rule files (auto-grown children/,
	// learnings/, KB section rebuilds, identity proposals). Symlinks would
	// route those writes back to the global cache at
	// ~/.claude/repos/agentteamland/{team}/, where the user has no push
	// permission and the next `atl update` would collide on git pull.
	// Project-local copies isolate mutations cleanly. See:
	// .claude/docs/install-mechanism-redesign.md (Q1).
	//
	// Trade-off: copies become stale relative to the global cache. Compensated
	// by `atl update`'s auto-refresh-of-unmodified-projects step (Q3),
	// shipping in a follow-up PR. Until then, users on this PR refresh
	// manually with `atl install <team> --refresh` (after Q4 ships) or by
	// re-running `atl install <team>` (current behavior).
	for _, a := range resolved.Effective.Agents {
		src := filepath.Join(config.TeamRepoDir(a.SourceTeam), "agents", a.Name, "agent.md")
		if _, err := os.Stat(src); err != nil {
			return nil, "", fmt.Errorf("agent source missing: %s", src)
		}
		dst := filepath.Join(projectClaude, "agents", a.Name+".md")
		maybeWarnCollision(warnCollisions, existingOwnership, "agent", a.Name, target)
		// RemoveAll handles prior symlinks (legacy install), prior copies
		// (re-install), and any stray empties — keeps the operation idempotent.
		_ = os.RemoveAll(dst)
		if err := copyFile(src, dst); err != nil {
			return nil, "", fmt.Errorf("copy agent %s: %w", a.Name, err)
		}
	}
	for _, s := range resolved.Effective.Skills {
		src := filepath.Join(config.TeamRepoDir(s.SourceTeam), "skills", s.Name)
		if _, err := os.Stat(src); err != nil {
			continue // skill dir may be optional
		}
		dst := filepath.Join(projectClaude, "skills", s.Name)
		maybeWarnCollision(warnCollisions, existingOwnership, "skill", s.Name, target)
		_ = os.RemoveAll(dst)
		if err := copySkillDir(src, dst); err != nil {
			return nil, "", fmt.Errorf("copy skill %s: %w", s.Name, err)
		}
	}
	for _, r := range resolved.Effective.Rules {
		src := filepath.Join(config.TeamRepoDir(r.SourceTeam), "rules", r.Name+".md")
		if _, err := os.Stat(src); err != nil {
			continue
		}
		dst := filepath.Join(projectClaude, "rules", r.Name+".md")
		maybeWarnCollision(warnCollisions, existingOwnership, "rule", r.Name, target)
		_ = os.RemoveAll(dst)
		if err := copyFile(src, dst); err != nil {
			return nil, "", fmt.Errorf("copy rule %s: %w", r.Name, err)
		}
	}

	return resolved, regStatus, nil
}

// copyFile copies a single regular file from src to dst, preserving the
// source file's permission bits. Used for agent and rule install (single-file
// resources). Skills use copySkillDir (recursive walk) since they are
// directory trees.
func copyFile(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("copyFile expected a regular file, got directory: %s", src)
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// copySkillDir does a recursive file copy from src to dst. Used for skill
// directory installation (Claude Code skill loader doesn't follow symlinks
// under .claude/skills/, so we materialize real files).
//
// Skills are small (typically a single skill.md, occasionally with a few
// supporting files), so a simple synchronous walk is appropriate. We do not
// preserve symlinks within the source tree — every path inside src is
// resolved and copied as a real file.
func copySkillDir(src, dst string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !srcInfo.IsDir() {
		return fmt.Errorf("skill source is not a directory: %s", src)
	}
	if err := os.MkdirAll(dst, srcInfo.Mode().Perm()); err != nil {
		return err
	}
	return filepath.Walk(src, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			if rel == "." {
				return nil
			}
			return os.MkdirAll(target, info.Mode().Perm())
		}
		// Regular file (or symlink — resolved by os.Open below).
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
		if err != nil {
			return err
		}
		defer out.Close()
		if _, err := io.Copy(out, in); err != nil {
			return err
		}
		return nil
	})
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
//
// InstalledChecksums is keyed by resource kind ("agents" / "skills" /
// "rules"), then by item name; values are hex-encoded SHA-256 hashes of the
// content as installed. Used by `atl update` to detect modified copies
// (skip refresh) vs. unmodified copies (safe to overwrite with the fresh
// global-cache version). The field is `omitempty` for backwards compatibility
// with manifests written by older atl versions; missing values are treated
// as "unknown — don't auto-refresh", protecting against accidental data loss.
type InstalledTeam struct {
	Name               string                       `json:"name"`
	Repo               string                       `json:"repo"`
	Version            string                       `json:"version"`
	InstalledAt        string                       `json:"installedAt"`
	SourceDir          string                       `json:"sourceDir"`
	ExtendsChain       []string                     `json:"extendsChain"`
	Effective          map[string][]string          `json:"effective"`
	Status             string                       `json:"status,omitempty"`
	InstalledChecksums map[string]map[string]string `json:"installedChecksums,omitempty"`
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

	// Record install-time content checksums for each resource. These drive
	// the modified-detection used by `atl update` to decide refresh vs.
	// skip per project copy. Hashing the source (global cache) is
	// equivalent to hashing the just-written destination because copy is
	// byte-identical; source is more convenient since the path is already
	// derivable from `resolved`.
	checksums := computeInstallChecksums(resolved)

	filtered = append(filtered, InstalledTeam{
		Name:               topName,
		Repo:               "", // Populated from resolved URL in a future version.
		Version:            extractVersion(resolved.ExtendsChain),
		InstalledAt:        installedAt,
		SourceDir:          "~/.claude/repos/agentteamland/" + topName,
		ExtendsChain:       resolved.ExtendsChain,
		Effective:          eff,
		Status:             status,
		InstalledChecksums: checksums,
	})
	m.Teams = filtered

	if err := config.WriteJSONAtomic(path, m); err != nil {
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

// computeInstallChecksums hashes every resolved resource (agent, skill, rule)
// from its global-cache source path. The result is keyed by kind, then by
// item name, with hex-encoded SHA-256 values. Failures (unreadable files,
// missing source) cause the affected entry to be omitted; the user later
// sees "unknown checksum" treated as "modified" by the auto-refresh path,
// which conservatively skips refresh rather than risk overwriting.
func computeInstallChecksums(resolved *resolver.Resolved) map[string]map[string]string {
	out := map[string]map[string]string{
		"agents": {},
		"skills": {},
		"rules":  {},
	}
	for _, a := range resolved.Effective.Agents {
		src := filepath.Join(config.TeamRepoDir(a.SourceTeam), "agents", a.Name, "agent.md")
		if h, err := checksum.FileSHA256(src); err == nil {
			out["agents"][a.Name] = h
		}
	}
	for _, s := range resolved.Effective.Skills {
		src := filepath.Join(config.TeamRepoDir(s.SourceTeam), "skills", s.Name)
		if h, err := checksum.DirSHA256(src); err == nil {
			out["skills"][s.Name] = h
		}
	}
	for _, r := range resolved.Effective.Rules {
		src := filepath.Join(config.TeamRepoDir(r.SourceTeam), "rules", r.Name+".md")
		if h, err := checksum.FileSHA256(src); err == nil {
			out["rules"][r.Name] = h
		}
	}
	return out
}
