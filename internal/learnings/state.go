// Package learnings provides helpers for the inline `<!-- learning -->`
// marker capture flow: locating Claude Code transcript files for the
// current project, scanning them for unprocessed markers, and reading
// the per-project state file that records what has already been
// processed by /save-learnings.
//
// State writes are NOT performed by this package. /save-learnings owns
// the write semantic (it knows when processing actually completed
// successfully). This package is read-only at the state-file boundary.
//
// State file location: ~/.claude/state/learning-capture-state.json
// Schema:
//
//	{
//	  "projects": {
//	    "<slug>": { "lastProcessedAt": "RFC3339 timestamp" },
//	    ...
//	  }
//	}
//
// where <slug> is the Claude Code session-directory name (cwd with
// path separators replaced by hyphens).
package learnings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// State is the on-disk shape of the learning-capture state file.
type State struct {
	Projects map[string]ProjectState `json:"projects"`
}

// ProjectState records per-project capture progress.
type ProjectState struct {
	// LastProcessedAt is the timestamp after which a marker is
	// considered unprocessed. /save-learnings updates this when its
	// processing completes; learning-capture only reads it.
	LastProcessedAt time.Time `json:"lastProcessedAt"`
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
