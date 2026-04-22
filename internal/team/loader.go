// Package team coordinates install, list, remove, update, and search.
package team

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/agentteamland/cli/internal/config"
	"github.com/agentteamland/cli/internal/manifest"
	"github.com/agentteamland/cli/internal/registry"
)

// Loader implements resolver.Loader: given a team short name (or URL) it
// clones/pulls the repo into the cache and reads its team.json.
type Loader struct {
	Registry *registry.Registry // may be nil if offline; short-name lookups will fail
	Verbose  bool
}

// Load satisfies the resolver.Loader interface.
func (l *Loader) Load(name, constraint string) (*manifest.TeamManifest, string, error) {
	// Resolve to a clone URL.
	cloneURL, err := l.resolveURL(name)
	if err != nil {
		return nil, "", err
	}

	// Derive a cache directory slug. For URLs / owner/repo forms, this extracts
	// the team's repo name (last path segment, sans .git). For plain short names
	// it returns the name as-is.
	slug := slugFromInput(name)
	cacheDir := config.TeamRepoDir(slug)

	// If cached, git pull to stay current. If not, git clone.
	if _, err := os.Stat(cacheDir); err == nil {
		if l.Verbose {
			fmt.Fprintf(os.Stderr, "  pulling %s...\n", slug)
		}
		cmd := exec.Command("git", "-C", cacheDir, "pull", "--quiet")
		if out, err := cmd.CombinedOutput(); err != nil {
			return nil, "", fmt.Errorf("git pull %s: %w (%s)", cacheDir, err, string(out))
		}
	} else {
		if l.Verbose {
			fmt.Fprintf(os.Stderr, "  cloning %s from %s...\n", slug, cloneURL)
		}
		if err := os.MkdirAll(config.RepoCache(), 0o755); err != nil {
			return nil, "", fmt.Errorf("mkdir cache: %w", err)
		}
		cmd := exec.Command("git", "clone", "--quiet", cloneURL, cacheDir)
		if out, err := cmd.CombinedOutput(); err != nil {
			return nil, "", fmt.Errorf("git clone %s: %w (%s)", cloneURL, err, string(out))
		}
	}

	// Read manifest.
	m, err := manifest.ReadFromRepo(cacheDir)
	if err != nil {
		return nil, "", err
	}

	// Sanity: manifest name should match the requested name when user typed a short name.
	// For URL / owner-repo inputs, we skip — the manifest's name is canonical and may
	// differ from the URL slug (rare but legitimate).
	if !isURL(name) && !strings.Contains(name, "/") && m.Name != name {
		return nil, "", fmt.Errorf("manifest name %q in %s does not match requested %q", m.Name, cacheDir, name)
	}

	// TODO: enforce constraint (semver match). For v0.1.0 we accept whatever is at HEAD.
	_ = constraint

	return m, cacheDir, nil
}

// slugFromInput extracts the team slug from any input form atl install accepts:
//
//	"design-system-team"                                              → "design-system-team"
//	"agentteamland/design-system-team"                                → "design-system-team"
//	"https://github.com/agentteamland/design-system-team.git"         → "design-system-team"
//	"https://github.com/agentteamland/design-system-team"             → "design-system-team"
//	"git@github.com:agentteamland/design-system-team.git"             → "design-system-team"
//	"ssh://git@github.com/agentteamland/design-system-team.git"       → "design-system-team"
//
// The slug is used as the cache directory name under ~/.claude/repos/agentteamland/.
func slugFromInput(input string) string {
	s := strings.TrimSpace(input)
	s = strings.TrimSuffix(s, ".git")
	s = strings.TrimRight(s, "/")
	if i := strings.LastIndexAny(s, "/:"); i >= 0 {
		s = s[i+1:]
	}
	return s
}

// resolveURL turns a short name into a git URL. It tries:
// 1. If the input is a URL, use it directly.
// 2. If the input is "owner/repo", expand to GitHub.
// 3. Otherwise look up in the registry; if missing, fall back to "agentteamland/<name>".
func (l *Loader) resolveURL(input string) (string, error) {
	if isURL(input) {
		return input, nil
	}
	if strings.Count(input, "/") == 1 && !strings.ContainsAny(input, " @") {
		return "https://github.com/" + input + ".git", nil
	}

	// Short name → registry lookup.
	if l.Registry != nil {
		if entry := l.Registry.Find(input); entry != nil {
			return entry.CloneURL(), nil
		}
	}

	// Fallback: assume agentteamland org.
	fallback := fmt.Sprintf("https://github.com/%s/%s.git", config.GitHubOrgPrefix, input)
	return fallback, nil
}

func isURL(s string) bool {
	return strings.HasPrefix(s, "https://") ||
		strings.HasPrefix(s, "http://") ||
		strings.HasPrefix(s, "git@") ||
		strings.HasPrefix(s, "ssh://")
}
