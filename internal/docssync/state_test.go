package docssync

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// withTempHome redirects $HOME so StateFilePath resolves under a
// temporary directory. Restores the prior value on cleanup.
func withTempHome(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	prev, hadHome := os.LookupEnv("HOME")
	os.Setenv("HOME", tmp)
	t.Cleanup(func() {
		if hadHome {
			os.Setenv("HOME", prev)
		} else {
			os.Unsetenv("HOME")
		}
	})
	return tmp
}

func TestStateFilePath_Locates(t *testing.T) {
	tmp := withTempHome(t)
	got, err := StateFilePath()
	if err != nil {
		t.Fatalf("StateFilePath: %v", err)
	}
	want := filepath.Join(tmp, ".atl", "state", "docs-sync-state.json")
	if got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
}

func TestReadState_MissingFileReturnsEmpty(t *testing.T) {
	withTempHome(t)
	s, err := ReadState()
	if err != nil {
		t.Fatalf("ReadState on missing file should not error, got %v", err)
	}
	if len(s.Projects) != 0 {
		t.Errorf("expected empty Projects, got %d entries", len(s.Projects))
	}
}

func TestReadState_MalformedJSONReturnsEmpty(t *testing.T) {
	tmp := withTempHome(t)
	path := filepath.Join(tmp, ".atl", "state", "docs-sync-state.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("not json{"), 0o644); err != nil {
		t.Fatalf("write malformed: %v", err)
	}
	s, err := ReadState()
	if err != nil {
		t.Fatalf("ReadState on malformed file should not error, got %v", err)
	}
	if len(s.Projects) != 0 {
		t.Errorf("expected empty Projects on malformed input, got %d", len(s.Projects))
	}
}

func TestWriteState_RoundTrip(t *testing.T) {
	tmp := withTempHome(t)

	now := time.Now().UTC().Truncate(time.Second)
	want := State{Projects: map[string]ProjectState{
		"my-project": {
			LastFullAudit:      now.Add(-time.Hour),
			LastMarkerDrain:    now,
			ProcessedMarkers:   []string{"hash1", "hash2"},
			LastReleaseTagSeen: "v1.1.4",
			CoverageBaseline: &CoverageBaseline{
				Skills: []string{"save-learnings", "wiki"},
				Rules:  []string{"docs-sync"},
				Agents: []string{},
			},
		},
	}}

	if err := WriteState(want); err != nil {
		t.Fatalf("WriteState: %v", err)
	}
	got, err := ReadState()
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("round-trip mismatch:\n got=%+v\nwant=%+v", got, want)
	}

	// Verify file landed under $HOME/.atl/state/ (post-migration canonical).
	path := filepath.Join(tmp, ".atl", "state", "docs-sync-state.json")
	if _, err := os.Stat(path); err != nil {
		t.Errorf("state file not at expected location: %v", err)
	}
}

func TestAddProcessedMarkers_DeduplicatesAndCaps(t *testing.T) {
	withTempHome(t)
	var s State

	s.AddProcessedMarkers("p", []string{"a", "b", "a", "c"} /* duplicates */)
	got := s.Projects["p"].ProcessedMarkers
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dedup fail: got=%v, want=%v", got, want)
	}

	// Re-adding should be a no-op.
	s.AddProcessedMarkers("p", []string{"a", "c"})
	got = s.Projects["p"].ProcessedMarkers
	if !reflect.DeepEqual(got, want) {
		t.Errorf("repeat add should be no-op: got=%v, want=%v", got, want)
	}

	// FIFO cap.
	overflow := make([]string, MaxProcessedMarkers+10)
	for i := range overflow {
		overflow[i] = randomHash(i)
	}
	var fresh State
	fresh.AddProcessedMarkers("q", overflow)
	if got := len(fresh.Projects["q"].ProcessedMarkers); got != MaxProcessedMarkers {
		t.Errorf("FIFO cap fail: got=%d, want=%d", got, MaxProcessedMarkers)
	}
	// Oldest 10 should have been dropped — first remaining is overflow[10].
	first := fresh.Projects["q"].ProcessedMarkers[0]
	if first != overflow[10] {
		t.Errorf("FIFO cap dropped wrong end: first=%q, want=%q", first, overflow[10])
	}
}

func TestProcessedSet_ConstantTimeMembership(t *testing.T) {
	withTempHome(t)
	var s State
	s.AddProcessedMarkers("p", []string{"x", "y", "z"})
	set := s.ProcessedSet("p")
	for _, h := range []string{"x", "y", "z"} {
		if !set[h] {
			t.Errorf("ProcessedSet missing %q", h)
		}
	}
	if set["nope"] {
		t.Errorf("ProcessedSet false positive for unknown hash")
	}
	// Unknown project returns empty set, not panic.
	if got := s.ProcessedSet("unknown"); len(got) != 0 {
		t.Errorf("unknown project set should be empty, got %d", len(got))
	}
}

func TestStampTimestamps_OnlyAdvanceForward(t *testing.T) {
	withTempHome(t)
	var s State

	now := time.Now().UTC().Truncate(time.Second)
	earlier := now.Add(-time.Hour)
	later := now.Add(time.Hour)

	s.StampMarkerDrain("p", now)
	s.StampMarkerDrain("p", earlier) // older — must be ignored
	if got := s.Projects["p"].LastMarkerDrain; !got.Equal(now) {
		t.Errorf("StampMarkerDrain regressed: got=%v, want=%v", got, now)
	}
	s.StampMarkerDrain("p", later) // newer — must advance
	if got := s.Projects["p"].LastMarkerDrain; !got.Equal(later) {
		t.Errorf("StampMarkerDrain did not advance: got=%v, want=%v", got, later)
	}

	s.StampFullAudit("p", now)
	s.StampFullAudit("p", earlier)
	if got := s.Projects["p"].LastFullAudit; !got.Equal(now) {
		t.Errorf("StampFullAudit regressed")
	}
}

func TestSetCoverageBaseline_SortsAndCopies(t *testing.T) {
	withTempHome(t)
	var s State

	input := CoverageBaseline{
		Skills: []string{"wiki", "save-learnings", "docs-sync"},
		Rules:  []string{"docs-sync", "agent-structure"},
		Agents: nil,
	}
	s.SetCoverageBaseline("p", input)

	got := s.Projects["p"].CoverageBaseline
	if got == nil {
		t.Fatal("CoverageBaseline not set")
	}
	wantSkills := []string{"docs-sync", "save-learnings", "wiki"}
	wantRules := []string{"agent-structure", "docs-sync"}
	if !reflect.DeepEqual(got.Skills, wantSkills) {
		t.Errorf("Skills not sorted: got=%v, want=%v", got.Skills, wantSkills)
	}
	if !reflect.DeepEqual(got.Rules, wantRules) {
		t.Errorf("Rules not sorted: got=%v, want=%v", got.Rules, wantRules)
	}

	// Mutating the input slice after SetCoverageBaseline must not
	// affect the stored state — the helper copies.
	input.Skills[0] = "MUTATED"
	if got := s.Projects["p"].CoverageBaseline.Skills; strings.Contains(strings.Join(got, ","), "MUTATED") {
		t.Errorf("SetCoverageBaseline did not deep-copy")
	}
}

func TestSetLastReleaseTagSeen_Records(t *testing.T) {
	withTempHome(t)
	var s State
	s.SetLastReleaseTagSeen("p", "v1.1.4")
	if got := s.Projects["p"].LastReleaseTagSeen; got != "v1.1.4" {
		t.Errorf("got=%q, want=v1.1.4", got)
	}
	// Override is allowed (skill always writes the latest observed).
	s.SetLastReleaseTagSeen("p", "v1.1.5")
	if got := s.Projects["p"].LastReleaseTagSeen; got != "v1.1.5" {
		t.Errorf("got=%q, want=v1.1.5 after override", got)
	}
}

func randomHash(seed int) string {
	// Stable, unique-per-seed strings for the FIFO cap test. The
	// seed is rendered as a zero-padded 16-character hex value so
	// every seed produces a distinct hash (no modular collisions).
	return fmt.Sprintf("%016x", seed)
}
