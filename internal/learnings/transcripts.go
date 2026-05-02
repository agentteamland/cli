package learnings

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// FirstRunCap is how far back to scan when no LastProcessedAt is
// recorded for a project. Prevents a fresh atl install on a workspace
// with months of history from re-processing every marker ever seen.
// Brainstorm Q1.2 decided this default.
const FirstRunCap = 7 * 24 * time.Hour

// SlugForPath converts a project absolute path to the Claude Code
// session-directory slug — replace every "/" with "-" (and on Windows
// every "\" with "-"). Matches Claude Code's own naming convention
// for ~/.claude/projects/<slug>/.
func SlugForPath(absPath string) string {
	s := filepath.ToSlash(absPath)
	return strings.ReplaceAll(s, "/", "-")
}

// ResolveProjectRoot returns the project root path that learning-capture
// should use. Differs from updater.FindProjectRoot (which requires
// .claude/.team-installs.json — atl-managed projects only): learning-
// capture also runs in atl-source repos and the maintainer workspace
// where no team has been installed but Claude Code session transcripts
// still accumulate under ~/.claude/projects/<slug>/.
//
// Resolution rules, in order:
//
//  1. cwd has .claude/ directory → use cwd (we're at a project root that
//     Claude Code knows about)
//  2. cwd ancestor has .claude/ → use that ancestor (we're in a subdir)
//  3. otherwise → return cwd anyway (worst case the slug points at a
//     non-existent ~/.claude/projects/<slug>/ and FindUnprocessedTranscripts
//     returns nil silently)
//
// The `.claude/` heuristic is intentionally loose. Claude Code creates
// ~/.claude/projects/<slug>/ for any directory it opens; we don't need
// the project to be atl-managed, only Claude-Code-aware.
func ResolveProjectRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	current := cwd
	for {
		if _, err := os.Stat(filepath.Join(current, ".claude")); err == nil {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return cwd, nil
}

// ProjectTranscriptDir returns the absolute path to the Claude Code
// transcript directory for the given project root path. Returns the
// empty string if $HOME cannot be resolved.
func ProjectTranscriptDir(projectRoot string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	slug := SlugForPath(projectRoot)
	return filepath.Join(home, ".claude", "projects", slug), nil
}

// FindUnprocessedTranscripts returns the .jsonl transcript files in
// the project's Claude Code session directory that were modified
// after `since`. If `since` is the zero time, the FirstRunCap is
// applied (relative to time.Now()).
//
// Returned paths are sorted by modification time ascending (oldest
// first), so consumers can preserve causal order when displaying
// markers.
//
// Returns (nil, nil) if the transcript directory does not exist
// (e.g., a project that has never been opened in Claude Code).
func FindUnprocessedTranscripts(projectRoot string, since time.Time) ([]string, error) {
	dir, err := ProjectTranscriptDir(projectRoot)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	cutoff := since
	if cutoff.IsZero() {
		cutoff = time.Now().Add(-FirstRunCap)
	}

	type entry struct {
		path    string
		modTime time.Time
	}
	var matched []entry
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		// Modtime > cutoff means "modified after the cutoff". Strict
		// inequality avoids re-processing the exact transcript that
		// /save-learnings just stamped (which uses the modtime of the
		// last line as the new LastProcessedAt).
		if !info.ModTime().After(cutoff) {
			continue
		}
		matched = append(matched, entry{
			path:    filepath.Join(dir, e.Name()),
			modTime: info.ModTime(),
		})
	}
	sort.Slice(matched, func(i, j int) bool {
		return matched[i].modTime.Before(matched[j].modTime)
	})
	out := make([]string, len(matched))
	for i, m := range matched {
		out[i] = m.path
	}
	return out, nil
}

// ScanMarkersInTranscripts walks each transcript file and returns
// every <!-- learning ... --> block found, in the order they appear
// in their respective transcripts (and across transcripts in the
// passed-in order). Existing scanner logic is reused via the
// transcript-path entry point of `atl learning-capture`'s manual
// mode; this helper is the multi-file equivalent.
//
// Each marker is parsed into the same Marker shape as the existing
// single-transcript scanner (topic, kind, doc-impact, body).
type Marker struct {
	Topic     string
	Kind      string
	DocImpact string
	Body      string
}

func ScanMarkersInTranscripts(paths []string) ([]Marker, error) {
	var all []Marker
	for _, p := range paths {
		ms, err := scanFile(p)
		if err != nil {
			// Best-effort: skip the unreadable file, continue.
			continue
		}
		all = append(all, ms...)
	}
	return all, nil
}

func scanFile(path string) ([]Marker, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var markers []Marker
	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 8*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		matches := markerBlockPattern.FindAllSubmatch(line, -1)
		for _, m := range matches {
			if len(m) < 2 {
				continue
			}
			raw := string(m[1])
			raw = strings.ReplaceAll(raw, `\n`, "\n")
			raw = strings.ReplaceAll(raw, `\"`, `"`)
			markers = append(markers, parseMarker(raw))
		}
	}
	return markers, nil
}

func parseMarker(inner string) Marker {
	m := Marker{}
	for _, line := range strings.Split(inner, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		colon := strings.Index(line, ":")
		if colon < 0 {
			continue
		}
		key := strings.TrimSpace(line[:colon])
		value := strings.TrimSpace(line[colon+1:])
		switch strings.ToLower(key) {
		case "topic":
			m.Topic = value
		case "kind":
			m.Kind = value
		case "doc-impact", "doc_impact", "docimpact":
			m.DocImpact = value
		case "body":
			m.Body = value
		}
	}
	return m
}
