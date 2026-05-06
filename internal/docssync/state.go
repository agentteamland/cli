// Package docssync provides on-disk state management for the
// `/docs-sync` skill's auto-trigger loop.
//
// The skill drains marker queues, runs drift-detector audits, and
// regenerates bilingual mirrors. After successful processing, the
// skill calls `atl docs-sync --commit-from-state` to persist what
// was processed — marker hashes (FIFO-capped), audit timestamps,
// the coverage baseline, and the latest release tag observed.
//
// State writes are performed only via the CLI's commit subcommand,
// preserving the invariant "state advances iff processing succeeded."
// A crash mid-skill leaves the previous state file intact (atomic
// tmp + rename via config.WriteJSONAtomic). The next session simply
// re-reports the same markers — no work is lost, no work is hidden.
//
// State file location: ~/.atl/state/docs-sync-state.json
// (legacy ~/.claude/state/docs-sync-state.json is read as fallback during
// the migration window, per the atl-config-system decision).
// Schema:
//
//	{
//	  "projects": {
//	    "<slug>": {
//	      "lastFullAudit":   "RFC3339 timestamp",
//	      "lastMarkerDrain": "RFC3339 timestamp",
//	      "processedMarkers": ["hash1", "hash2", ...],
//	      "coverageBaseline": {
//	        "skills": ["save-learnings", "wiki", ...],
//	        "rules":  ["docs-sync", "learning-capture", ...],
//	        "agents": []
//	      },
//	      "lastReleaseTagSeen": "v1.1.4"
//	    }
//	  }
//	}
//
// Slug convention matches the learnings package — the cwd path with
// path separators replaced by hyphens, so the same project's state
// is uniformly addressable across the CLI's two state files.
package docssync

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/agentteamland/cli/internal/atlmigrate"
	"github.com/agentteamland/cli/internal/config"
)

// MaxProcessedMarkers caps the per-project processedMarkers list. Same
// rationale as learnings.MaxProcessedMarkers — past this size, oldest
// hashes are dropped (FIFO). 5000 hashes × 16 hex chars ≈ 80 KB on disk
// per project. At a sustained 50 doc-impact markers/session × 200
// sessions/year, the cap holds for ~6 months before any drop.
const MaxProcessedMarkers = 5000

// State is the on-disk shape of the docs-sync state file.
type State struct {
	Projects map[string]ProjectState `json:"projects"`
}

// ProjectState records per-project docs-sync progress.
type ProjectState struct {
	// LastFullAudit is the timestamp of the last successful comparison-
	// driven audit (manual `/docs-sync --audit` or default sweep). The
	// skill uses this for the "recent audit warning" — < 7 days warns
	// and prompts unless --force.
	LastFullAudit time.Time `json:"lastFullAudit,omitempty"`

	// LastMarkerDrain is the timestamp of the last successful marker
	// drain (Phase 1 of the skill). Tracked separately from full audit
	// because marker drain runs on every /create-pr Step 4.5 invocation
	// while full audits are periodic.
	LastMarkerDrain time.Time `json:"lastMarkerDrain,omitempty"`

	// ProcessedMarkers is a FIFO-capped list of marker hashes that
	// have already been processed by /docs-sync (Phase 1c → 1d → 5).
	// The skill uses this as a precise filter when extracting markers
	// from transcripts, even if the transcript file's modtime advanced
	// since the prior drain.
	ProcessedMarkers []string `json:"processedMarkers,omitempty"`

	// CoverageBaseline is the inventory of skills / rules / agents
	// observed by the kapsama-denetleyicisi sub-check on its last run.
	// The next run reads this and skips entirely when the inventory
	// has not changed (incremental optimization).
	CoverageBaseline *CoverageBaseline `json:"coverageBaseline,omitempty"`

	// LastReleaseTagSeen is the most-recent cli release tag observed
	// by the skill (e.g., "v1.1.4"). Used by the version-pin sweep to
	// skip when no new release has shipped since the last audit.
	LastReleaseTagSeen string `json:"lastReleaseTagSeen,omitempty"`
}

// CoverageBaseline is the skills/rules/agents inventory snapshot.
// Sorted alphabetically for deterministic comparison + diff-ability.
type CoverageBaseline struct {
	Skills []string `json:"skills"`
	Rules  []string `json:"rules"`
	Agents []string `json:"agents"`
}

// StateFilePath returns the canonical (write) location of the state file
// at the new ~/.atl/state/ location. Reads should go through the
// migration-window-aware Resolve in ReadState.
// Resolves $HOME at call time; never caches.
func StateFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".atl", "state", "docs-sync-state.json"), nil
}

// legacyStateFilePath returns the pre-atl-config-system location
// (~/.claude/state/docs-sync-state.json). Read sites use Resolve over
// the legacy + canonical pair so unmigrated installs keep working until
// atlmigrate.Migrate has been auto-triggered.
func legacyStateFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "state", "docs-sync-state.json"), nil
}

// ReadState loads the state file from disk. Missing file is treated
// as empty state (not an error) — first-run case. Malformed JSON is
// also treated as empty (we don't fail the user's session for a
// corrupt state file; worst case we re-process some markers).
func ReadState() (State, error) {
	newPath, err := StateFilePath()
	if err != nil {
		return emptyState(), err
	}
	oldPath, err := legacyStateFilePath()
	if err != nil {
		return emptyState(), err
	}
	data, err := os.ReadFile(atlmigrate.Resolve(oldPath, newPath))
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

// ProcessedSet returns a set of marker hashes already processed for
// this project. O(1) lookup. Empty (non-nil) map when the project is
// unknown.
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

// AddProcessedMarkers appends new marker hashes to the project's
// ProcessedMarkers list, deduplicating against existing entries and
// applying the FIFO cap. Mutates s in place; caller writes via
// WriteState.
func (s *State) AddProcessedMarkers(slug string, hashes []string) {
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
	s.Projects[slug] = p
}

// StampMarkerDrain advances LastMarkerDrain to the provided timestamp
// when it is newer than the current value. Callers pass time.Now().UTC()
// after a successful /docs-sync Phase 1.
func (s *State) StampMarkerDrain(slug string, at time.Time) {
	if s.Projects == nil {
		s.Projects = map[string]ProjectState{}
	}
	p := s.Projects[slug]
	if at.After(p.LastMarkerDrain) {
		p.LastMarkerDrain = at
	}
	s.Projects[slug] = p
}

// StampFullAudit advances LastFullAudit to the provided timestamp
// when it is newer than the current value. Callers pass time.Now().UTC()
// after a successful /docs-sync Phase 3 (comparison-driven audit).
func (s *State) StampFullAudit(slug string, at time.Time) {
	if s.Projects == nil {
		s.Projects = map[string]ProjectState{}
	}
	p := s.Projects[slug]
	if at.After(p.LastFullAudit) {
		p.LastFullAudit = at
	}
	s.Projects[slug] = p
}

// SetCoverageBaseline replaces the project's coverage baseline with
// the provided inventory. Each list is sorted in place for stable
// disk representation.
func (s *State) SetCoverageBaseline(slug string, baseline CoverageBaseline) {
	if s.Projects == nil {
		s.Projects = map[string]ProjectState{}
	}
	p := s.Projects[slug]
	cb := CoverageBaseline{
		Skills: append([]string(nil), baseline.Skills...),
		Rules:  append([]string(nil), baseline.Rules...),
		Agents: append([]string(nil), baseline.Agents...),
	}
	sort.Strings(cb.Skills)
	sort.Strings(cb.Rules)
	sort.Strings(cb.Agents)
	p.CoverageBaseline = &cb
	s.Projects[slug] = p
}

// SetLastReleaseTagSeen records the most-recent cli release tag the
// skill observed during this run. The kapsama-denetleyicisi /
// versiyon-referansı-tarayıcı sub-check uses this on subsequent runs
// to skip when no new release has shipped.
func (s *State) SetLastReleaseTagSeen(slug, tag string) {
	if s.Projects == nil {
		s.Projects = map[string]ProjectState{}
	}
	p := s.Projects[slug]
	p.LastReleaseTagSeen = tag
	s.Projects[slug] = p
}
