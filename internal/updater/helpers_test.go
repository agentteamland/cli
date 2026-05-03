package updater

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseSemver(t *testing.T) {
	cases := []struct {
		in   string
		want [3]int
	}{
		{"1.2.3", [3]int{1, 2, 3}},
		{"v1.2.3", [3]int{1, 2, 3}},
		{"0.0.1", [3]int{0, 0, 1}},
		{"10.20.30", [3]int{10, 20, 30}},
		{"1.2.3-rc1", [3]int{1, 2, 3}}, // pre-release stripped
		{"1.2.3-dev", [3]int{1, 2, 3}}, // documented behavior
		{"v1.2", [3]int{1, 2, 0}},      // missing patch defaults to 0
		{"v1", [3]int{1, 0, 0}},        // missing minor + patch
		{"", [3]int{0, 0, 0}},
		{"garbage", [3]int{0, 0, 0}},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := parseSemver(tc.in)
			if got != tc.want {
				t.Fatalf("parseSemver(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestIsNewerVersion(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"1.2.3", "1.2.2", true},
		{"1.2.3", "1.2.3", false},
		{"1.2.3", "1.2.4", false},
		{"2.0.0", "1.99.99", true},
		{"1.2.3", "1.1.99", true},
		{"v1.2.3", "1.2.2", true},     // v-prefix tolerated
		{"1.2.3", "v1.2.2", true},     // either side
		{"1.2.3-rc1", "1.2.3", false}, // pre-release stripping = equal
		{"", "1.2.3", false},          // empty latest = nothing newer
	}
	for _, tc := range cases {
		t.Run(tc.latest+"_vs_"+tc.current, func(t *testing.T) {
			got := isNewerVersion(tc.latest, tc.current)
			if got != tc.want {
				t.Fatalf("isNewerVersion(%q, %q) = %v, want %v",
					tc.latest, tc.current, got, tc.want)
			}
		})
	}
}

func TestParseDuration(t *testing.T) {
	cases := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"30m", 30 * time.Minute, false},
		{"5m", 5 * time.Minute, false},
		{"1h", 1 * time.Hour, false},
		{"24h", 24 * time.Hour, false},
		{"30s", 30 * time.Second, false},
		{"0", 0, false},
		{"", 0, false}, // empty is treated as zero duration, not an error
		{"garbage", 0, true},
		{"30x", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := ParseDuration(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseDuration(%q) should error, got %v", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseDuration(%q): unexpected err %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("ParseDuration(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestReadTeamJSONVersion(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "team.json"),
		[]byte(`{"name":"x","version":"1.2.3"}`), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	got := readTeamJSONVersion(dir)
	if got != "1.2.3" {
		t.Fatalf("readTeamJSONVersion = %q, want %q", got, "1.2.3")
	}
}

func TestReadTeamJSONVersion_MissingFile(t *testing.T) {
	got := readTeamJSONVersion(t.TempDir())
	if got != "" {
		t.Fatalf("missing team.json should yield empty string, got %q", got)
	}
}

func TestReadTeamJSONVersion_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "team.json"),
		[]byte(`{not valid`), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	got := readTeamJSONVersion(dir)
	if got != "" {
		t.Fatalf("malformed team.json should yield empty string, got %q", got)
	}
}

func TestThrottleGate(t *testing.T) {
	dir := t.TempDir()
	stamp := filepath.Join(dir, "throttle-stamp")

	// First call: stamp file missing → not throttled (proceed).
	if !throttleGate(stamp, 30*time.Minute) {
		t.Fatalf("first call (no stamp file) should NOT be throttled")
	}

	// Touch the stamp to simulate a recent successful run.
	if err := touchStamp(stamp); err != nil {
		t.Fatalf("touchStamp: %v", err)
	}

	// Second call within the throttle window should be throttled (skip).
	if throttleGate(stamp, 30*time.Minute) {
		t.Fatalf("second call within window should BE throttled")
	}

	// With a zero throttle, it should always proceed.
	if !throttleGate(stamp, 0) {
		t.Fatalf("zero throttle should always proceed")
	}

	// With a tiny throttle, after a brief sleep it should proceed again.
	if err := os.Chtimes(stamp,
		time.Now().Add(-time.Hour),
		time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	if !throttleGate(stamp, 30*time.Minute) {
		t.Fatalf("after stamp aged past window, should proceed")
	}
}

func TestTouchStamp(t *testing.T) {
	dir := t.TempDir()
	stamp := filepath.Join(dir, "subdir", "stamp")

	if err := touchStamp(stamp); err != nil {
		t.Fatalf("touchStamp: %v", err)
	}
	info, err := os.Stat(stamp)
	if err != nil {
		t.Fatalf("stamp file not created: %v", err)
	}
	if info.IsDir() {
		t.Fatalf("stamp should be a file, not a directory")
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"hello", 5, "hello"},
		{"hello world", 5, "hello…"}, // implementation appends Unicode ellipsis
		{"hi", 5, "hi"},
		{"", 5, ""},
		{"abcdef", 6, "abcdef"},
		{"abcdef", 5, "abcde…"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := truncate(tc.in, tc.n)
			if got != tc.want {
				t.Fatalf("truncate(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
			}
		})
	}
}

func TestPlural(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{0, "s"},
		{1, ""},
		{2, "s"},
	}
	for _, tc := range cases {
		t.Run("", func(t *testing.T) {
			got := plural(tc.n)
			if got != tc.want {
				t.Fatalf("plural(%d) = %q, want %q", tc.n, got, tc.want)
			}
		})
	}
}
