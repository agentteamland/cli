package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/agentteamland/cli/internal/docssync"
	"github.com/agentteamland/cli/internal/learnings"
	"github.com/spf13/cobra"
)

// NewDocsSync builds the `atl docs-sync` command — state-file maintenance
// for the /docs-sync skill's auto-trigger loop.
//
// In the Phase 3 design, the heavy lifting (marker drain, drift-detector,
// parity-checker) lives in the skill (~/.claude/repos/agentteamland/core/
// skills/docs-sync/). The CLI's job is narrow: persist what the skill
// processed so the next session does not redo it.
//
// Currently the command supports a single mode:
//
//	atl docs-sync --commit-from-state \
//	  --marker-hashes h1,h2,h3 \
//	  [--marker-drain-stamp] \
//	  [--full-audit-stamp] \
//	  [--release-tag-seen v1.1.4] \
//	  [--coverage-baseline-json /path/to/baseline.json]
//
// The skill calls this on Phase 6 success. Each flag is independent —
// the skill writes only the fields it advanced this run. State writes
// are atomic (config.WriteJSONAtomic via WriteState). On any failure,
// the command logs to stderr and exits 0; the skill already finished
// its real work, and a re-report on the next session is the only cost.
//
// Future modes (full audit, marker drain, etc.) live entirely in the
// skill. The CLI surface intentionally stays small: state file I/O
// only, no business logic. This mirrors the `atl learning-capture
// --commit-from-transcripts` pattern.
func NewDocsSync() *cobra.Command {
	var (
		commitFromState     bool
		markerHashes        string
		markerDrainStamp    bool
		fullAuditStamp      bool
		releaseTagSeen      string
		coverageBaselineRaw string
	)

	cmd := &cobra.Command{
		Use:   "docs-sync",
		Short: "State-file maintenance for the /docs-sync skill's auto-trigger loop",
		Long: `State-file maintenance for the /docs-sync skill (Phase 3 of the
docs-sync-automation brainstorm).

The skill (lives at ~/.claude/repos/agentteamland/core/skills/docs-sync/)
runs the actual marker drain, drift-detector, and parity-checker logic.
This CLI command persists what the skill processed:

  atl docs-sync --commit-from-state \
    --marker-hashes h1,h2,h3                  # FIFO-add to processedMarkers
    [--marker-drain-stamp]                    # advance lastMarkerDrain to now
    [--full-audit-stamp]                      # advance lastFullAudit to now
    [--release-tag-seen v1.1.4]               # record latest cli release seen
    [--coverage-baseline-json /path/to.json]  # replace coverageBaseline snapshot

Each flag is independent — the skill passes only the fields it advanced.

State file: ~/.claude/state/docs-sync-state.json
Schema documented at internal/docssync/state.go`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !commitFromState {
				return fmt.Errorf("docs-sync: --commit-from-state is required (no other modes implemented)")
			}
			return runDocsSyncCommitFromState(commitFromStateArgs{
				MarkerHashes:        markerHashes,
				MarkerDrainStamp:    markerDrainStamp,
				FullAuditStamp:      fullAuditStamp,
				ReleaseTagSeen:      releaseTagSeen,
				CoverageBaselineRaw: coverageBaselineRaw,
			})
		},
	}

	cmd.Flags().BoolVar(&commitFromState, "commit-from-state", false, "Commit processed-state values to ~/.claude/state/docs-sync-state.json")
	cmd.Flags().StringVar(&markerHashes, "marker-hashes", "", "Comma-separated marker hashes to add to processedMarkers (FIFO-capped)")
	cmd.Flags().BoolVar(&markerDrainStamp, "marker-drain-stamp", false, "Advance lastMarkerDrain to now (UTC)")
	cmd.Flags().BoolVar(&fullAuditStamp, "full-audit-stamp", false, "Advance lastFullAudit to now (UTC)")
	cmd.Flags().StringVar(&releaseTagSeen, "release-tag-seen", "", "Record the most-recent cli release tag observed (e.g., v1.1.4)")
	cmd.Flags().StringVar(&coverageBaselineRaw, "coverage-baseline-json", "", "Path to a JSON file holding {skills:[],rules:[],agents:[]}; replaces the coverage baseline snapshot")
	return cmd
}

// commitFromStateArgs bundles the flag values for runDocsSyncCommitFromState.
// Keeping the parsed values in a struct keeps the runner small and testable.
type commitFromStateArgs struct {
	MarkerHashes        string
	MarkerDrainStamp    bool
	FullAuditStamp      bool
	ReleaseTagSeen      string
	CoverageBaselineRaw string
}

func runDocsSyncCommitFromState(a commitFromStateArgs) error {
	root, err := learnings.ResolveProjectRoot()
	if err != nil || root == "" {
		fmt.Fprintln(os.Stderr, "docs-sync: cannot resolve project root for state-file write")
		return nil
	}
	slug := learnings.SlugForPath(root)

	state, _ := docssync.ReadState()

	// 1. Marker hashes — FIFO-add (deduplicated).
	hashes := splitCSV(a.MarkerHashes)
	if len(hashes) > 0 {
		state.AddProcessedMarkers(slug, hashes)
	}

	// 2. Marker-drain timestamp.
	now := time.Now().UTC()
	if a.MarkerDrainStamp {
		state.StampMarkerDrain(slug, now)
	}

	// 3. Full-audit timestamp.
	if a.FullAuditStamp {
		state.StampFullAudit(slug, now)
	}

	// 4. Release tag.
	if a.ReleaseTagSeen != "" {
		state.SetLastReleaseTagSeen(slug, a.ReleaseTagSeen)
	}

	// 5. Coverage baseline — read JSON file from disk.
	if a.CoverageBaselineRaw != "" {
		baseline, err := readCoverageBaselineFile(a.CoverageBaselineRaw)
		if err != nil {
			fmt.Fprintf(os.Stderr, "docs-sync: coverage-baseline-json read failed: %v (skipping)\n", err)
		} else {
			state.SetCoverageBaseline(slug, baseline)
		}
	}

	if err := docssync.WriteState(state); err != nil {
		fmt.Fprintf(os.Stderr, "docs-sync: state write failed: %v\n", err)
		return nil
	}

	// Compact summary, mirroring `atl learning-capture --commit-from-transcripts`.
	parts := []string{}
	if len(hashes) > 0 {
		parts = append(parts, fmt.Sprintf("%d marker hash%s",
			len(hashes), pluralEs(len(hashes))))
	}
	if a.MarkerDrainStamp {
		parts = append(parts, "marker-drain stamp")
	}
	if a.FullAuditStamp {
		parts = append(parts, "full-audit stamp")
	}
	if a.ReleaseTagSeen != "" {
		parts = append(parts, fmt.Sprintf("release-tag=%s", a.ReleaseTagSeen))
	}
	if a.CoverageBaselineRaw != "" && state.Projects[slug].CoverageBaseline != nil {
		cb := state.Projects[slug].CoverageBaseline
		parts = append(parts, fmt.Sprintf("coverage-baseline=%d skills/%d rules/%d agents",
			len(cb.Skills), len(cb.Rules), len(cb.Agents)))
	}
	if len(parts) == 0 {
		parts = append(parts, "no fields advanced")
	}
	fmt.Printf("📝 docs-sync: committed [%s] for %s\n", strings.Join(parts, ", "), slug)
	return nil
}

// readCoverageBaselineFile loads {skills, rules, agents} from a JSON file.
// Each field is optional — missing fields are treated as empty arrays.
func readCoverageBaselineFile(path string) (docssync.CoverageBaseline, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return docssync.CoverageBaseline{}, err
	}
	var cb docssync.CoverageBaseline
	if err := json.Unmarshal(data, &cb); err != nil {
		return docssync.CoverageBaseline{}, fmt.Errorf("malformed coverage baseline JSON: %w", err)
	}
	return cb, nil
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}
