// Package config provides filesystem paths and constants shared across atl.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Version is set by ldflags at build time (see scripts/build.sh and .goreleaser.yaml).
// It is a var (not a const) so that -X ldflag can override it.
var (
	Version = "0.1.0-dev"
	Commit  = "dev"
	Date    = "unknown"
)

const (
	// RegistryRawURL is the canonical registry location.
	RegistryRawURL = "https://raw.githubusercontent.com/agentteamland/registry/main/teams.json"

	// TeamSchemaURL is the team.json JSON Schema URL (fetched from core repo).
	TeamSchemaURL = "https://raw.githubusercontent.com/agentteamland/core/main/schemas/team.schema.json"

	// RegistrySchemaURL is the registry teams.json JSON Schema URL.
	RegistrySchemaURL = "https://raw.githubusercontent.com/agentteamland/registry/main/schemas/registry.schema.json"

	// GitHubOrgPrefix is the default organization for short-name resolution fallback.
	GitHubOrgPrefix = "agentteamland"
)

// ClaudeHome returns ~/.claude/ (the Claude Code global directory).
func ClaudeHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".claude"
	}
	return filepath.Join(home, ".claude")
}

// RepoCache returns ~/.claude/repos/agentteamland/ (shared source repo cache).
func RepoCache() string {
	return filepath.Join(ClaudeHome(), "repos", "agentteamland")
}

// GlobalSkills returns ~/.claude/skills/.
func GlobalSkills() string {
	return filepath.Join(ClaudeHome(), "skills")
}

// GlobalRules returns ~/.claude/rules/.
func GlobalRules() string {
	return filepath.Join(ClaudeHome(), "rules")
}

// ProjectClaudeDir returns <cwd>/.claude/ (the project-level Claude directory).
func ProjectClaudeDir(cwd string) string {
	return filepath.Join(cwd, ".claude")
}

// TeamInstallsManifest returns the project-level .team-installs.json path.
func TeamInstallsManifest(cwd string) string {
	return filepath.Join(ProjectClaudeDir(cwd), ".team-installs.json")
}

// TeamRepoDir returns the cached source directory for a given team.
func TeamRepoDir(teamName string) string {
	return filepath.Join(RepoCache(), teamName)
}

// WriteJSONAtomic writes a value as pretty-printed JSON to path using the
// canonical temp-file + rename pattern. Crashes mid-write leave the original
// file intact (or absent), never a half-written corruption. The parent
// directory must already exist.
func WriteJSONAtomic(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".atl-write-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpPath := tmp.Name()
	// Best-effort cleanup if anything below fails before rename.
	defer func() {
		if _, statErr := os.Stat(tmpPath); statErr == nil {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}
