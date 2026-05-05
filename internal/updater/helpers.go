package updater

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/agentteamland/cli/internal/config"
)

// readTeamJSONVersion tries to read <dir>/team.json and return its "version"
// field. Returns "" on any failure (no file, bad JSON, missing field).
// Used so we can show "1.1.1 → 1.1.2" style summaries when updates land.
func readTeamJSONVersion(dir string) string {
	path := filepath.Join(dir, "team.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var m struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return ""
	}
	return m.Version
}

// --- atl self-check ---

const (
	githubReleaseURL = "https://api.github.com/repos/agentteamland/cli/releases/latest"
	httpTimeout      = 5 * time.Second
)

// checkLatestRelease queries GitHub for the latest atl release tag.
//
// Returns (latestVersion, upgradeCommand, err). latestVersion has the leading
// 'v' stripped so it compares cleanly with config.Version.
//
// If the request fails (offline, rate limited, transient) the returned error
// is non-nil and latestVersion is empty. Callers should treat this as
// non-fatal — the caller wants to skip the self-check on error, not crash.
func checkLatestRelease() (string, string, error) {
	client := &http.Client{Timeout: httpTimeout}
	req, err := http.NewRequest("GET", githubReleaseURL, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "atl-cli/"+config.Version)

	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// 403 = rate limited unauthenticated. 404 = no releases yet.
		// Neither is fatal; we just skip.
		body, _ := io.ReadAll(resp.Body)
		return "", "", fmt.Errorf("releases API %d: %s", resp.StatusCode, truncate(string(body), 100))
	}

	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", "", err
	}

	latest := strings.TrimPrefix(payload.TagName, "v")
	return latest, detectUpgradeCommand(), nil
}

// detectUpgradeCommand returns a platform-appropriate upgrade command based on
// how the user likely installed atl. Best-effort — if detection is ambiguous,
// defaults to the one-liner curl install.
func detectUpgradeCommand() string {
	// Check if we're running from brew's cellar / prefix.
	exe, err := os.Executable()
	if err != nil {
		return defaultUpgradeCommand()
	}
	exeAbs, err := filepath.EvalSymlinks(exe)
	if err != nil {
		exeAbs = exe
	}

	// Homebrew install paths (macOS + Linuxbrew).
	if strings.Contains(exeAbs, "/Cellar/") ||
		strings.Contains(exeAbs, "/opt/homebrew/") ||
		strings.Contains(exeAbs, "/usr/local/Cellar/") ||
		strings.Contains(exeAbs, "/home/linuxbrew/") {
		return "brew upgrade atl"
	}

	// Scoop install paths (Windows).
	if strings.Contains(strings.ToLower(exeAbs), "\\scoop\\") ||
		strings.Contains(strings.ToLower(exeAbs), "/scoop/") {
		return "scoop update atl"
	}

	return defaultUpgradeCommand()
}

func defaultUpgradeCommand() string {
	// On Windows without a scoop signal, default to scoop (most common).
	if runtime.GOOS == "windows" {
		return "scoop update atl"
	}
	// Otherwise point at our install.sh one-liner.
	return "curl -fsSL https://raw.githubusercontent.com/agentteamland/cli/main/scripts/install.sh | sh"
}

// --- version comparison ---

// isNewerVersion reports whether 'latest' > 'current' using SemVer lexical
// compare. Strips leading 'v' and trailing -dev / -rc etc. Best-effort.
func isNewerVersion(latest, current string) bool {
	la := parseSemver(latest)
	cu := parseSemver(current)
	for i := 0; i < 3; i++ {
		if la[i] > cu[i] {
			return true
		}
		if la[i] < cu[i] {
			return false
		}
	}
	return false
}

// parseSemver returns [major, minor, patch] as ints. Missing / unparseable
// components become 0. Handles "1.2.3", "v1.2.3", "1.2.3-dev", "1.2".
func parseSemver(s string) [3]int {
	s = strings.TrimPrefix(s, "v")
	// Strip any pre-release / build metadata.
	for _, sep := range []string{"-", "+"} {
		if idx := strings.Index(s, sep); idx >= 0 {
			s = s[:idx]
		}
	}
	parts := strings.Split(s, ".")
	var out [3]int
	for i := 0; i < 3 && i < len(parts); i++ {
		if n, err := strconv.Atoi(parts[i]); err == nil {
			out[i] = n
		}
	}
	return out
}

// --- small utils ---

// ParseDuration is a friendly wrapper around time.ParseDuration for the
// --throttle=<dur> flag. Accepts "30m", "1h", "1h30m", "90s", etc.
func ParseDuration(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	return time.ParseDuration(s)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
