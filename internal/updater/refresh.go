package updater

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/agentteamland/cli/internal/checksum"
	"github.com/agentteamland/cli/internal/config"
	"github.com/agentteamland/cli/internal/state"
)

// RefreshSummary describes what `RefreshUnmodifiedCopies` did across all
// installed teams in a project. The CLI prints one info line per team that
// either refreshed something or was skipped due to local mutations.
type RefreshSummary struct {
	// PerTeam is keyed by canonical team name.
	PerTeam map[string]TeamRefreshReport
}

// TeamRefreshReport describes the outcome for a single installed team.
type TeamRefreshReport struct {
	// RefreshedCount: project copies overwritten with the freshly-pulled
	// global-cache version.
	RefreshedCount int
	// SkippedCount: copies whose hash differs from the install-time
	// baseline (local mutations) — left untouched.
	SkippedCount int
	// MissingChecksumCount: resources whose manifest has no recorded
	// install checksum (e.g. installed by an older atl version before
	// PR-2). Conservatively skipped.
	MissingChecksumCount int
}

// RefreshUnmodifiedCopies walks `.claude/.team-installs.json` for the given
// project and, for each installed team, refreshes every resource copy whose
// content matches the install-time checksum. Modified copies (or copies
// whose checksum is unknown) are left untouched.
//
// Updates manifest with new baseline checksums for refreshed resources, so
// the next refresh sees the new content as "unmodified" too.
func RefreshUnmodifiedCopies(projectPath string) (RefreshSummary, error) {
	out := RefreshSummary{PerTeam: map[string]TeamRefreshReport{}}

	manifestPath := filepath.Join(projectPath, ".claude", ".team-installs.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return out, err
	}

	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return out, fmt.Errorf("parse manifest: %w", err)
	}

	projectClaude := filepath.Join(projectPath, ".claude")
	manifestChanged := false

	for ti := range m.Teams {
		t := &m.Teams[ti]
		report := TeamRefreshReport{}

		for _, kind := range []string{"agents", "skills", "rules"} {
			refreshKindForTeam(t, kind, projectClaude, &report, &manifestChanged)
		}
		out.PerTeam[t.Name] = report
	}

	if manifestChanged {
		if data, err := json.MarshalIndent(m, "", "  "); err == nil {
			_ = os.WriteFile(manifestPath, data, 0o644)
		}
	}

	return out, nil
}

// refreshKindForTeam processes one resource kind ("agents" | "skills" |
// "rules") for one installed team. Mutates the team's checksum map in place
// when a refresh happens; flips manifestChanged so the caller knows to
// rewrite the manifest file.
//
// Decision matrix per resource:
//
//	projectHash == cacheHash  → no-op (already current; quiet baseline drift fix)
//	projectHash == installCsm → unmodified locally, cache moved ahead → REFRESH
//	projectHash != installCsm → modified locally → SKIP (preserve user's work)
func refreshKindForTeam(t *installedTeam, kind, projectClaude string, report *TeamRefreshReport, manifestChanged *bool) {
	for _, name := range t.Effective[kind] {
		expected := ""
		if t.InstalledChecksums != nil && t.InstalledChecksums[kind] != nil {
			expected = t.InstalledChecksums[kind][name]
		}
		if expected == "" {
			report.MissingChecksumCount++
			continue
		}

		dst, srcKind, sourcePath := resolveResourcePaths(kind, name, t.Name, projectClaude)
		if sourcePath == "" {
			// Source missing in global cache — leave the project copy alone.
			report.SkippedCount++
			continue
		}

		projectHash, err := hashAt(srcKind, dst)
		if err != nil {
			// Project copy missing or unreadable — conservatively skip.
			report.SkippedCount++
			continue
		}
		cacheHash, err := hashAt(srcKind, sourcePath)
		if err != nil {
			report.SkippedCount++
			continue
		}

		// Project copy already matches cache: nothing to do. If the recorded
		// baseline drifted (older atl write, or a previously-skipped state),
		// silently realign it to current — saves missing-checksum noise on
		// future runs. No refresh count, no skip count: this is the quiet
		// happy path that keeps `atl update` silent when nothing changed.
		if projectHash == cacheHash {
			if expected != cacheHash {
				ensureChecksumMap(t, kind)
				t.InstalledChecksums[kind][name] = cacheHash
				*manifestChanged = true
			}
			continue
		}

		// Cache moved ahead. If the project copy still matches the install
		// baseline, the user hasn't touched it — safe to refresh.
		if projectHash != expected {
			// User (or self-updating-learning-loop) modified the copy.
			// Preserve their work; surface a skip line so they know.
			report.SkippedCount++
			continue
		}

		if err := overwriteFromSource(srcKind, sourcePath, dst); err != nil {
			report.SkippedCount++
			continue
		}

		ensureChecksumMap(t, kind)
		t.InstalledChecksums[kind][name] = cacheHash
		*manifestChanged = true

		report.RefreshedCount++
	}
}

func ensureChecksumMap(t *installedTeam, kind string) {
	if t.InstalledChecksums == nil {
		t.InstalledChecksums = map[string]map[string]string{}
	}
	if t.InstalledChecksums[kind] == nil {
		t.InstalledChecksums[kind] = map[string]string{}
	}
}

func resolveResourcePaths(kind, name, sourceTeam, projectClaude string) (dst string, srcKind state.ResourceKind, sourcePath string) {
	teamDir := config.TeamRepoDir(sourceTeam)
	switch kind {
	case "agents":
		dst = filepath.Join(projectClaude, "agents", name+".md")
		sourcePath = filepath.Join(teamDir, "agents", name, "agent.md")
		srcKind = state.KindFile
	case "rules":
		dst = filepath.Join(projectClaude, "rules", name+".md")
		sourcePath = filepath.Join(teamDir, "rules", name+".md")
		srcKind = state.KindFile
	case "skills":
		dst = filepath.Join(projectClaude, "skills", name)
		sourcePath = filepath.Join(teamDir, "skills", name)
		srcKind = state.KindDir
	}
	if _, err := os.Stat(sourcePath); err != nil {
		return dst, srcKind, ""
	}
	return dst, srcKind, sourcePath
}

func overwriteFromSource(kind state.ResourceKind, src, dst string) error {
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	if kind == state.KindDir {
		return copyTree(src, dst)
	}
	return copyOneFile(src, dst)
}

func copyOneFile(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
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

func copyTree(src, dst string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !srcInfo.IsDir() {
		return fmt.Errorf("copyTree expected a directory, got file: %s", src)
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
		return copyOneFile(path, target)
	})
}

func hashAt(kind state.ResourceKind, path string) (string, error) {
	if kind == state.KindDir {
		return checksum.DirSHA256(path)
	}
	return checksum.FileSHA256(path)
}

// FormatRefreshSummary produces one info line per team that did anything
// interesting. Empty when nothing happened.
//
// Skipped lines explicitly mention `atl install --refresh` so the user
// knows how to force the overwrite when they intend to discard local
// changes (PR-4 wires that flag).
func FormatRefreshSummary(s RefreshSummary, projectPath string) string {
	if len(s.PerTeam) == 0 {
		return ""
	}
	var lines []string
	for team, r := range s.PerTeam {
		if r.RefreshedCount > 0 {
			lines = append(lines,
				fmt.Sprintf("🔄 Refreshed %d resource%s in %s from %s",
					r.RefreshedCount, plural(r.RefreshedCount), projectPath, team))
		}
		if r.SkippedCount > 0 {
			lines = append(lines,
				fmt.Sprintf("🔒 %s in %s: %d resource%s have local changes — skipped refresh, use `atl install %s --refresh` to force",
					team, projectPath, r.SkippedCount, plural(r.SkippedCount), team))
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// --- internal manifest read+write types -------------------------------------
// Decoupled from internal/team to avoid cyclic imports; updater needs both
// read (modified detection) and write (baseline-checksum update) access.

type manifest struct {
	Teams []installedTeam `json:"teams"`
}

type installedTeam struct {
	Name               string                       `json:"name"`
	Repo               string                       `json:"repo,omitempty"`
	Version            string                       `json:"version,omitempty"`
	InstalledAt        string                       `json:"installedAt,omitempty"`
	SourceDir          string                       `json:"sourceDir,omitempty"`
	ExtendsChain       []string                     `json:"extendsChain,omitempty"`
	Effective          map[string][]string          `json:"effective,omitempty"`
	Status             string                       `json:"status,omitempty"`
	InstalledChecksums map[string]map[string]string `json:"installedChecksums,omitempty"`
}
