// Package updater refreshes local git-repo caches for every installed
// agentteamland repo (teams AND global repos — core, brainstorm, rule,
// team-manager, etc.) plus optionally checks for a newer `atl` binary
// release.
//
// Designed to be the backend for `atl update [--silent-if-clean]
// [--throttle=30m] [--check-only]` and for Claude Code hooks
// (SessionStart / UserPromptSubmit).
package updater

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/agentteamland/cli/internal/config"
)

// RepoUpdate describes the result of a single repo's fetch+pull attempt.
type RepoUpdate struct {
	Name    string // basename of the cached repo dir
	OldRev  string // short SHA before (possibly empty if unknown)
	NewRev  string // short SHA after (== OldRev if no change)
	Changed bool   // true if new commits were pulled
	OldVer  string // parsed from team.json if present; else ""
	NewVer  string // same after pull
	Err     error  // set if fetch/pull failed; Changed stays false
	Skipped bool   // set if the repo had no remote / not a clone
}

// Result aggregates all updates from a single `atl update` invocation.
type Result struct {
	Repos          []RepoUpdate
	SelfOutdated   bool   // true if a newer atl release is available
	SelfCurrent    string // current atl version
	SelfLatest     string // latest release version found
	SelfUpgradeCmd string // platform-appropriate upgrade command (e.g., "brew upgrade atl")
	SelfErr        error  // set if the self-check failed (network, rate limit, etc.) — non-fatal

	ThrottledRepos bool // true if the repo check was skipped due to throttling
	ThrottledSelf  bool // true if the self-check was skipped due to throttling
}

// Options controls what a single Run does.
type Options struct {
	// RepoThrottle — if non-zero and the last repo check happened within this
	// duration, skip the repo scan entirely. Timestamp is kept at
	// ~/.claude/cache/atl-last-repo-check.
	RepoThrottle time.Duration

	// SelfThrottle — same, for the atl binary self-check. Timestamp at
	// ~/.claude/cache/atl-last-self-check. Usually much longer than RepoThrottle
	// (self-releases are less frequent than team updates).
	SelfThrottle time.Duration

	// SkipSelfCheck — bypass self-check entirely (e.g., during unit tests or
	// from a hook that wants to be extra-light).
	SkipSelfCheck bool

	// CheckOnly — fetch but do NOT pull. Reports what WOULD be pulled.
	CheckOnly bool

	// Verbose — print git commands and per-step progress to stderr.
	Verbose bool
}

// Run executes a single update pass per Options and returns the aggregate
// Result. Never errors at the top level — per-repo and self-check failures
// surface inside the Result for the caller to format.
func Run(opts Options) *Result {
	res := &Result{
		SelfCurrent: config.Version,
	}

	// Repo updates (throttled).
	if throttleGate(repoStampPath(), opts.RepoThrottle) {
		res.Repos = updateAllRepos(opts.CheckOnly, opts.Verbose)
		if !opts.CheckOnly {
			_ = touchStamp(repoStampPath())
		}
	} else {
		res.ThrottledRepos = true
	}

	// Self-check (independently throttled, larger default).
	if !opts.SkipSelfCheck {
		if throttleGate(selfStampPath(), opts.SelfThrottle) {
			latest, upgradeCmd, err := checkLatestRelease()
			res.SelfLatest = latest
			res.SelfUpgradeCmd = upgradeCmd
			res.SelfErr = err
			if err == nil && latest != "" && isNewerVersion(latest, config.Version) {
				res.SelfOutdated = true
			}
			if err == nil {
				_ = touchStamp(selfStampPath())
			}
		} else {
			res.ThrottledSelf = true
		}
	}

	return res
}

// FormatSummary produces the human-readable multi-line summary the caller
// prints to stdout. In --silent-if-clean mode, empty string means "nothing to
// say."
func (r *Result) FormatSummary(silentIfClean bool) string {
	var b strings.Builder
	anyChange := false

	if r.SelfOutdated {
		fmt.Fprintf(&b, "⬆  atl %s → %s available — run: %s\n", r.SelfCurrent, r.SelfLatest, r.SelfUpgradeCmd)
		anyChange = true
	}

	for _, u := range r.Repos {
		switch {
		case u.Err != nil:
			if !silentIfClean {
				fmt.Fprintf(&b, "⚠  %s: %v\n", u.Name, u.Err)
			}
		case u.Skipped:
			// quiet
		case u.Changed:
			old := u.OldVer
			new_ := u.NewVer
			switch {
			case old != "" && new_ != "" && old != new_:
				fmt.Fprintf(&b, "🔄 %s %s → %s (auto-updated)\n", u.Name, old, new_)
			case u.OldRev != "" && u.NewRev != "":
				fmt.Fprintf(&b, "🔄 %s %s → %s (auto-updated)\n", u.Name, u.OldRev, u.NewRev)
			default:
				fmt.Fprintf(&b, "🔄 %s (auto-updated)\n", u.Name)
			}
			anyChange = true
		}
	}

	if !anyChange && silentIfClean {
		return ""
	}
	return b.String()
}

// HasChanges reports whether the result contains any repo changes or a
// self-upgrade prompt — useful for exit-code-only callers.
func (r *Result) HasChanges() bool {
	if r.SelfOutdated {
		return true
	}
	for _, u := range r.Repos {
		if u.Changed {
			return true
		}
	}
	return false
}

// updateAllRepos walks ~/.claude/repos/agentteamland/* and fetch-pulls each
// one that has a .git directory. Parallelized (one goroutine per repo) so
// the total wall-time is roughly one git-fetch roundtrip.
func updateAllRepos(checkOnly, verbose bool) []RepoUpdate {
	cacheDir := config.RepoCache()
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		// Cache dir missing = nothing to update yet.
		return nil
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	results := make([]RepoUpdate, 0, len(entries))

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		repoDir := filepath.Join(cacheDir, entry.Name())
		gitDir := filepath.Join(repoDir, ".git")
		if _, err := os.Stat(gitDir); err != nil {
			// Not a git clone — skip silently.
			continue
		}

		wg.Add(1)
		go func(name, dir string) {
			defer wg.Done()
			u := fetchAndPull(name, dir, checkOnly, verbose)
			mu.Lock()
			results = append(results, u)
			mu.Unlock()
		}(entry.Name(), repoDir)
	}
	wg.Wait()
	return results
}

// fetchAndPull does git fetch + (optional) fast-forward pull on a single repo.
// Captures before/after version info from team.json if that file exists.
func fetchAndPull(name, dir string, checkOnly, verbose bool) RepoUpdate {
	u := RepoUpdate{Name: name}

	u.OldRev = shortHead(dir)
	u.OldVer = readTeamJSONVersion(dir)

	// Fetch (always safe, never destructive).
	if out, err := runGit(dir, verbose, "fetch", "--quiet", "origin"); err != nil {
		u.Err = fmt.Errorf("fetch: %v (%s)", err, strings.TrimSpace(string(out)))
		return u
	}

	// Determine local vs remote.
	localSHA, err := rev(dir, "HEAD")
	if err != nil {
		u.Err = fmt.Errorf("rev-parse HEAD: %v", err)
		return u
	}

	// Try origin/main, fall back to origin/master.
	remoteSHA, err := rev(dir, "origin/main")
	if err != nil {
		remoteSHA, err = rev(dir, "origin/master")
		if err != nil {
			// No standard default branch — skip without error (probably bare checkout).
			u.Skipped = true
			return u
		}
	}

	if localSHA == remoteSHA {
		// Already current.
		u.NewRev = u.OldRev
		u.NewVer = u.OldVer
		return u
	}

	if checkOnly {
		// Would update but caller asked dry run.
		u.Changed = true
		u.NewRev = short(remoteSHA)
		return u
	}

	// Fast-forward pull.
	if out, err := runGit(dir, verbose, "pull", "--ff-only", "--quiet"); err != nil {
		u.Err = fmt.Errorf("pull: %v (%s)", err, strings.TrimSpace(string(out)))
		return u
	}

	u.Changed = true
	u.NewRev = shortHead(dir)
	u.NewVer = readTeamJSONVersion(dir)
	return u
}

// --- helpers ---

func runGit(dir string, verbose bool, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if verbose {
		fmt.Fprintf(os.Stderr, "  git -C %s %s\n", dir, strings.Join(args, " "))
	}
	return cmd.CombinedOutput()
}

func rev(dir, ref string) (string, error) {
	out, err := runGit(dir, false, "rev-parse", ref)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func shortHead(dir string) string {
	out, err := runGit(dir, false, "rev-parse", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// throttleGate reads the timestamp file; returns TRUE if the caller should
// proceed (throttle expired), FALSE if the caller should skip.
//
// If throttle is zero, always proceeds (no throttling).
func throttleGate(stampPath string, throttle time.Duration) bool {
	if throttle <= 0 {
		return true
	}
	info, err := os.Stat(stampPath)
	if err != nil {
		return true // no prior timestamp → proceed
	}
	return time.Since(info.ModTime()) >= throttle
}

func touchStamp(stampPath string) error {
	if err := os.MkdirAll(filepath.Dir(stampPath), 0o755); err != nil {
		return err
	}
	f, err := os.Create(stampPath)
	if err != nil {
		return err
	}
	return f.Close()
}

func repoStampPath() string {
	return filepath.Join(config.ClaudeHome(), "cache", "atl-last-repo-check")
}

func selfStampPath() string {
	return filepath.Join(config.ClaudeHome(), "cache", "atl-last-self-check")
}
