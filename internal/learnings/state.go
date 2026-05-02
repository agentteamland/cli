// Package learnings provides helpers for the inline `<!-- learning -->`
// marker capture flow: locating Claude Code transcript files for the
// current project, scanning them for unprocessed markers, and reading
// the per-project state file that records what has already been
// processed by /save-learnings.
//
// State writes are performed only when /save-learnings explicitly
// commits processed markers via `atl learning-capture --commit-from-
// transcripts`. The CLI never advances state speculatively — that
// preserves the invariant "state advances iff processing succeeded."
//
// State file location: ~/.claude/state/learning-capture-state.json
// Schema:
//
//	{
//	  "projects": {
//	    "<slug>": {
//	      "lastProcessedAt": "RFC3339 timestamp",
//	      "processedMarkers": ["hash1", "hash2", ...]
//	    },
//	    ...
//	  }
//	}
//
// where <slug> is the Claude Code session-directory name (cwd with
// path separators replaced by hyphens), and processedMarkers is a
// FIFO-capped list of marker hashes (Marker.Hash) that have already
// been processed by /save-learnings.
//
// Backwards compatibility: state files written by pre-v1.1.3 atl
// have no processedMarkers field. ReadState treats missing as empty
// (no markers known-processed; the lastProcessedAt cutoff still
// filters by file modtime as before).
package learnings

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/agentteamland/cli/internal/config"
)

// MaxProcessedMarkers caps the per-project processedMarkers list.
// Past this size, oldest hashes are dropped (FIFO). 5000 hashes ×
// 16 hex chars per hash ≈ 80 KB on disk per project — the budget.
// At a sustained rate of 50 markers/session × 200 sessions/year
// the cap holds for ~6 months before any drop happens.
const MaxProcessedMarkers = 5000

// State is the on-disk shape of the learning-capture state file.
type State struct {
	Projects map[string]ProjectState `json:"projects"`
}

// ProjectState records per-project capture progress.
type ProjectState struct {
	// LastProcessedAt is the timestamp before which a transcript file
	// is considered fully-processed. The CLI uses this as a coarse
	// modtime filter when enumerating transcripts (cheap; skips files
	// that haven't been touched since the last commit).
	LastProcessedAt time.Time `json:"lastProcessedAt"`

	// ProcessedMarkers is a FIFO-capped list of Marker.Hash values
	// that have already been processed by /save-learnings. The CLI
	// uses this as a precise filter when reporting "unprocessed"
	// markers — even if a transcript file's modtime advanced (e.g.,
	// the long-running session kept appending after the prior
	// /save-learnings completed), markers with a known hash are
	// excluded from the report.
	ProcessedMarkers []string `json:"processedMarkers,omitempty"`
}

// StateFilePath returns the canonical location of the state file.
// Resolves $HOME at call time; never caches.
func StateFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "state", "learning-capture-state.json"), nil
}

// ReadState loads the state file from disk. Missing file is treated
// as empty state (not an error) — first-run case. Malformed JSON is
// also treated as empty (we don't fail the user's session for a
// corrupt state file; worst case we re-process some markers).
func ReadState() (State, error) {
	path, err := StateFilePath()
	if err != nil {
		return emptyState(), err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return emptyState(), nil
		}
		return emptyState(), err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return emptyState(), nil
	}
	if s.Projects == nil {
		s.Projects = map[string]ProjectState{}
	}
	return s, nil
}

// WriteState writes the state file atomically (tmp + rename via
// config.WriteJSONAtomic). Creates the parent directory if needed.
// A crash mid-write leaves the previous state file intact.
func WriteState(s State) error {
	path, err := StateFilePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if s.Projects == nil {
		s.Projects = map[string]ProjectState{}
	}
	return config.WriteJSONAtomic(path, s)
}

func emptyState() State {
	return State{Projects: map[string]ProjectState{}}
}

// LastProcessedAtFor returns the recorded last-processed-at timestamp
// for the given project slug, or the zero time if no record exists.
// The zero time means "no markers from this project have been
// processed yet" — caller applies the first-run cap.
func (s State) LastProcessedAtFor(slug string) time.Time {
	if s.Projects == nil {
		return time.Time{}
	}
	return s.Projects[slug].LastProcessedAt
}

// ProcessedSet returns a set (map[hash]bool) of marker hashes that
// have already been processed for this project. O(1) lookup; the
// caller iterates fresh markers and excludes those whose hash is
// in this set. Empty (non-nil) map when the project is unknown or
// has no recorded markers — safe to use without nil-checking.
func (s State) ProcessedSet(slug string) map[string]bool {
	set := make(map[string]bool)
	if s.Projects == nil {
		return set
	}
	for _, h := range s.Projects[slug].ProcessedMarkers {
		set[h] = true
	}
	return set
}

// AddProcessed appends new marker hashes to the project's
// ProcessedMarkers list, deduplicating against existing entries
// and applying the FIFO cap (oldest dropped first when the cap
// is exceeded). Also advances LastProcessedAt to the provided
// timestamp if it is newer than the current value.
//
// Mutates s in place. Caller writes the result via WriteState.
func (s *State) AddProcessed(slug string, hashes []string, processedAt time.Time) {
	if s.Projects == nil {
		s.Projects = map[string]ProjectState{}
	}
	p := s.Projects[slug]

	seen := make(map[string]bool, len(p.ProcessedMarkers))
	for _, h := range p.ProcessedMarkers {
		seen[h] = true
	}
	for _, h := range hashes {
		if !seen[h] {
			p.ProcessedMarkers = append(p.ProcessedMarkers, h)
			seen[h] = true
		}
	}
	if len(p.ProcessedMarkers) > MaxProcessedMarkers {
		drop := len(p.ProcessedMarkers) - MaxProcessedMarkers
		p.ProcessedMarkers = p.ProcessedMarkers[drop:]
	}
	if processedAt.After(p.LastProcessedAt) {
		p.LastProcessedAt = processedAt
	}
	s.Projects[slug] = p
}

// MarkerHash returns the canonical 16-hex-character hash for a
// marker, identifying it stably across re-scans. Composed of
// (topic + "|" + kind + "|" + body) hashed via SHA-256 truncated
// to 8 bytes. 8 bytes is collision-resistant for the realistic
// upper bound (~10⁵ distinct markers per project lifetime) per
// birthday-paradox math (~10⁹ space for first collision at 50%).
func MarkerHash(topic, kind, body string) string {
	h := sha256.Sum256([]byte(topic + "|" + kind + "|" + body))
	return hex.EncodeToString(h[:8])
}

// Hash returns the canonical hash of m. Convenience wrapper around
// MarkerHash so callers don't have to thread the three fields.
func (m Marker) Hash() string {
	return MarkerHash(m.Topic, m.Kind, m.Body)
}
