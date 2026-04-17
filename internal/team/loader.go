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

	// Derive a cache directory name from the team name (same as registry 'name' field).
	cacheDir := config.TeamRepoDir(name)

	// If cached, git pull to stay current. If not, git clone.
	if _, err := os.Stat(cacheDir); err == nil {
		if l.Verbose {
			fmt.Fprintf(os.Stderr, "  pulling %s...\n", name)
		}
		cmd := exec.Command("git", "-C", cacheDir, "pull", "--quiet")
		if out, err := cmd.CombinedOutput(); err != nil {
			return nil, "", fmt.Errorf("git pull %s: %w (%s)", cacheDir, err, string(out))
		}
	} else {
		if l.Verbose {
			fmt.Fprintf(os.Stderr, "  cloning %s from %s...\n", name, cloneURL)
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

	// Sanity: manifest name should match the requested name (unless we followed a URL).
	if !isURL(name) && m.Name != name {
		return nil, "", fmt.Errorf("manifest name %q in %s does not match requested %q", m.Name, cacheDir, name)
	}

	// TODO: enforce constraint (semver match). For v0.1.0 we accept whatever is at HEAD.
	_ = constraint

	return m, cacheDir, nil
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
