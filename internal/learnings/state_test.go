package learnings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMarkerHash_Stable(t *testing.T) {
	a := MarkerHash("auth-refresh", "decision", "7-day JWT")
	b := MarkerHash("auth-refresh", "decision", "7-day JWT")
	if a != b {
		t.Fatalf("hash should be deterministic: %q vs %q", a, b)
	}
	if len(a) != 16 {
		t.Fatalf("hash should be 16 hex chars: got %d (%q)", len(a), a)
	}
}

func TestMarkerHash_FieldsMatter(t *testing.T) {
	base := MarkerHash("auth-refresh", "decision", "body")
	cases := []struct {
		name              string
		topic, kind, body string
	}{
		{"different topic", "auth-token", "decision", "body"},
		{"different kind", "auth-refresh", "discovery", "body"},
		{"different body", "auth-refresh", "decision", "different"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MarkerHash(tc.topic, tc.kind, tc.body)
			if got == base {
				t.Fatalf("hashes should differ when %s differs (got %q == base)", tc.name, got)
			}
		})
	}
}

func TestMarkerHash_NoFieldBleed(t *testing.T) {
	// Topic="auth", kind="refresh|decision" should NOT collide with topic="auth-refresh", kind="decision".
	// The "|" separator in MarkerHash prevents this.
	a := MarkerHash("auth", "refresh|decision", "body")
	b := MarkerHash("auth-refresh", "decision", "body")
	if a == b {
		t.Fatalf("hashes should not collide via field-boundary bleed (got %q == %q)", a, b)
	}
}

func TestMarker_Hash_DelegatesToMarkerHash(t *testing.T) {
	m := Marker{Topic: "x", Kind: "decision", Body: "y"}
	if m.Hash() != MarkerHash("x", "decision", "y") {
		t.Fatalf("Marker.Hash should delegate to MarkerHash")
	}
}

func TestState_ProcessedSet_Empty(t *testing.T) {
	s := State{}
	set := s.ProcessedSet("any-slug")
	if set == nil {
		t.Fatalf("ProcessedSet must return a non-nil map for empty state (caller uses it without nil-check)")
	}
	if len(set) != 0 {
		t.Fatalf("expected empty set, got %d entries", len(set))
	}
}

func TestState_ProcessedSet_Populated(t *testing.T) {
	s := State{
		Projects: map[string]ProjectState{
			"slug-a": {ProcessedMarkers: []string{"hash1", "hash2", "hash3"}},
			"slug-b": {ProcessedMarkers: []string{"other"}},
		},
	}
	set := s.ProcessedSet("slug-a")
	if !set["hash1"] || !set["hash2"] || !set["hash3"] {
		t.Fatalf("expected all 3 hashes present, got %v", set)
	}
	if set["other"] {
		t.Fatalf("hash from a different slug must not appear")
	}
	if set["never-added"] {
		t.Fatalf("unknown hash must not appear")
	}
}

func TestAddProcessed_Dedup(t *testing.T) {
	s := State{}
	s.AddProcessed("slug", []string{"a", "b", "a", "c"}, time.Time{})
	got := s.Projects["slug"].ProcessedMarkers
	want := []string{"a", "b", "c"}
	if !equalSlices(got, want) {
		t.Fatalf("dedup failed: got %v, want %v", got, want)
	}
}

func TestAddProcessed_DedupAcrossCalls(t *testing.T) {
	s := State{}
	s.AddProcessed("slug", []string{"a", "b"}, time.Time{})
	s.AddProcessed("slug", []string{"b", "c"}, time.Time{})
	got := s.Projects["slug"].ProcessedMarkers
	want := []string{"a", "b", "c"}
	if !equalSlices(got, want) {
		t.Fatalf("cross-call dedup failed: got %v, want %v", got, want)
	}
}

func TestAddProcessed_FIFOCap(t *testing.T) {
	// Override cap for the test by inserting cap+5 entries and
	// verifying the first 5 are dropped. We can't change the const
	// at runtime, so use the real cap value.
	s := State{}
	hashes := make([]string, MaxProcessedMarkers+5)
	for i := range hashes {
		hashes[i] = formatHash(i)
	}
	s.AddProcessed("slug", hashes, time.Time{})

	got := s.Projects["slug"].ProcessedMarkers
	if len(got) != MaxProcessedMarkers {
		t.Fatalf("cap not applied: got len=%d, want %d", len(got), MaxProcessedMarkers)
	}
	// The first 5 should be dropped — entries 5..MaxProcessedMarkers+4 remain.
	if got[0] != formatHash(5) {
		t.Fatalf("FIFO drop failed: first remaining = %q, want %q", got[0], formatHash(5))
	}
	if got[len(got)-1] != formatHash(MaxProcessedMarkers+4) {
		t.Fatalf("last entry wrong: got %q, want %q", got[len(got)-1], formatHash(MaxProcessedMarkers+4))
	}
}

func TestAddProcessed_AdvancesLastProcessedAt_OnlyForward(t *testing.T) {
	earlier := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	later := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	s := State{}
	s.AddProcessed("slug", []string{"a"}, later)
	if !s.Projects["slug"].LastProcessedAt.Equal(later) {
		t.Fatalf("LastProcessedAt should advance to later, got %v", s.Projects["slug"].LastProcessedAt)
	}
	s.AddProcessed("slug", []string{"b"}, earlier)
	if !s.Projects["slug"].LastProcessedAt.Equal(later) {
		t.Fatalf("LastProcessedAt must not regress: got %v, want stay at %v", s.Projects["slug"].LastProcessedAt, later)
	}
}

func TestState_BackwardsCompat_ReadLegacyJSON(t *testing.T) {
	// State files written by pre-v1.1.3 atl have only lastProcessedAt, no processedMarkers.
	// ReadState must handle that without losing data, and ProcessedSet must return empty.
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.json")
	legacy := `{
  "projects": {
    "-Users-foo-projects-bar": {
      "lastProcessedAt": "2026-04-01T12:00:00Z"
    }
  }
}`
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if s.Projects["-Users-foo-projects-bar"].LastProcessedAt.IsZero() {
		t.Fatalf("LastProcessedAt should be parsed from legacy JSON")
	}
	if len(s.Projects["-Users-foo-projects-bar"].ProcessedMarkers) != 0 {
		t.Fatalf("legacy state should have empty ProcessedMarkers")
	}
	set := s.ProcessedSet("-Users-foo-projects-bar")
	if len(set) != 0 {
		t.Fatalf("ProcessedSet on legacy state should be empty")
	}
}

func TestWriteState_AtomicAndRoundtrip(t *testing.T) {
	// Override $HOME so StateFilePath resolves under t.TempDir().
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	s := State{
		Projects: map[string]ProjectState{
			"slug-x": {
				LastProcessedAt:  time.Date(2026, 5, 2, 14, 0, 0, 0, time.UTC),
				ProcessedMarkers: []string{"abc123def4567890", "deadbeefcafe1234"},
			},
		},
	}
	if err := WriteState(s); err != nil {
		t.Fatalf("WriteState: %v", err)
	}

	// Roundtrip: read it back and verify.
	got, err := ReadState()
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if len(got.Projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(got.Projects))
	}
	if !got.Projects["slug-x"].LastProcessedAt.Equal(s.Projects["slug-x"].LastProcessedAt) {
		t.Fatalf("LastProcessedAt did not roundtrip: got %v, want %v",
			got.Projects["slug-x"].LastProcessedAt,
			s.Projects["slug-x"].LastProcessedAt)
	}
	if !equalSlices(got.Projects["slug-x"].ProcessedMarkers, s.Projects["slug-x"].ProcessedMarkers) {
		t.Fatalf("ProcessedMarkers did not roundtrip: got %v", got.Projects["slug-x"].ProcessedMarkers)
	}

	// No leftover temp file in the state directory (atomic rename cleaned up).
	stateDir := filepath.Join(tmp, ".atl", "state")
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		t.Fatalf("read state dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".atl-write-") {
			t.Fatalf("leftover temp file from atomic write: %s", e.Name())
		}
	}
}

func TestReadState_MissingFile_ReturnsEmpty(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	s, err := ReadState()
	if err != nil {
		t.Fatalf("missing state file should not error: %v", err)
	}
	if s.Projects == nil {
		t.Fatalf("Projects must be non-nil even on missing file")
	}
	if len(s.Projects) != 0 {
		t.Fatalf("expected empty Projects, got %d entries", len(s.Projects))
	}
}

func TestReadState_MalformedJSON_ReturnsEmpty(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	statePath := filepath.Join(tmp, ".atl", "state", "learning-capture-state.json")
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(statePath, []byte("{not valid json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	s, err := ReadState()
	if err != nil {
		t.Fatalf("malformed JSON should not error (corruption-tolerant): %v", err)
	}
	if s.Projects == nil {
		t.Fatalf("Projects must be non-nil even on malformed file")
	}
	if len(s.Projects) != 0 {
		t.Fatalf("malformed should yield empty state, got %d projects", len(s.Projects))
	}
}

// --- helpers --------------------------------------------------------

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func formatHash(i int) string {
	// 16-hex-char placeholder; real hashes are sha256-truncated.
	return MarkerHash("topic", "decision", strings.Repeat("x", i+1))
}
