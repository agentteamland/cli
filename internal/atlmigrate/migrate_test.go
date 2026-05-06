package atlmigrate

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPathPairs_resolved(t *testing.T) {
	tempHome := t.TempDir()
	setHome(t, tempHome)

	pairs := PathPairs()
	if len(pairs) != 5 {
		t.Fatalf("PathPairs() returned %d pairs, want 5", len(pairs))
	}
	for _, p := range pairs {
		if !strings.HasPrefix(p.Old, tempHome) {
			t.Errorf("Old path %q not under tempHome %q", p.Old, tempHome)
		}
		if !strings.HasPrefix(p.New, tempHome) {
			t.Errorf("New path %q not under tempHome %q", p.New, tempHome)
		}
		if !strings.Contains(p.Old, ".claude") {
			t.Errorf("Old path %q should reference .claude", p.Old)
		}
		if !strings.Contains(p.New, ".atl") {
			t.Errorf("New path %q should reference .atl", p.New)
		}
		if p.Label == "" {
			t.Error("PathPair has empty Label")
		}
	}
}

func TestConvenienceAccessors(t *testing.T) {
	tempHome := t.TempDir()
	setHome(t, tempHome)

	wantAtl := filepath.Join(tempHome, ".atl")
	cases := []struct {
		fn       func() string
		wantPath string
	}{
		{LearningCaptureStatePath, filepath.Join(wantAtl, "state", "learning-capture-state.json")},
		{DocsSyncStatePath, filepath.Join(wantAtl, "state", "docs-sync-state.json")},
		{InstallMarkerPath, filepath.Join(wantAtl, "install-marker.json")},
		{RepoStampPath, filepath.Join(wantAtl, "cache", "last-repo-check")},
		{SelfStampPath, filepath.Join(wantAtl, "cache", "last-self-check")},
	}
	for _, tc := range cases {
		if got := tc.fn(); got != tc.wantPath {
			t.Errorf("got %q, want %q", got, tc.wantPath)
		}
	}
}

func TestResolve_newWins(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old.txt")
	new := filepath.Join(dir, "new.txt")
	mustWrite(t, old, "old")
	mustWrite(t, new, "new")

	if got := Resolve(old, new); got != new {
		t.Errorf("Resolve = %q, want new path %q (new exists, should win)", got, new)
	}
}

func TestResolve_oldFallback(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old.txt")
	new := filepath.Join(dir, "new.txt")
	mustWrite(t, old, "old")

	if got := Resolve(old, new); got != old {
		t.Errorf("Resolve = %q, want old path %q (only old exists)", got, old)
	}
}

func TestResolve_neitherExists(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old.txt")
	new := filepath.Join(dir, "new.txt")

	if got := Resolve(old, new); got != new {
		t.Errorf("Resolve = %q, want new path %q (neither exists, writers go to new)", got, new)
	}
}

func TestMigrate_movesFiles(t *testing.T) {
	tempHome := t.TempDir()
	setHome(t, tempHome)

	// Pre-populate every old path with distinct content.
	pairs := PathPairs()
	for i, p := range pairs {
		mustMkdir(t, filepath.Dir(p.Old))
		mustWrite(t, p.Old, "content-"+string(rune('a'+i)))
	}

	var buf bytes.Buffer
	result, err := Migrate(&buf)
	if err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}
	if len(result.Migrated) != 5 {
		t.Errorf("Migrated count = %d, want 5", len(result.Migrated))
	}
	if len(result.Skipped) != 0 || len(result.Failed) != 0 {
		t.Errorf("expected no skipped/failed, got skipped=%d failed=%d", len(result.Skipped), len(result.Failed))
	}

	for i, p := range pairs {
		if exists(p.Old) {
			t.Errorf("old path %q still exists after migration", p.Old)
		}
		if !exists(p.New) {
			t.Errorf("new path %q missing after migration", p.New)
		}
		got, err := os.ReadFile(p.New)
		if err != nil {
			t.Errorf("read new path %q: %v", p.New, err)
			continue
		}
		want := "content-" + string(rune('a'+i))
		if string(got) != want {
			t.Errorf("new path %q content = %q, want %q", p.New, string(got), want)
		}
	}

	// Verify stderr output names every migrated label.
	output := buf.String()
	for _, label := range result.Migrated {
		if !strings.Contains(output, label) {
			t.Errorf("stderr output missing label %q: %s", label, output)
		}
	}
}

func TestMigrate_idempotent(t *testing.T) {
	tempHome := t.TempDir()
	setHome(t, tempHome)

	// Already migrated state: only new paths exist.
	for _, p := range PathPairs() {
		mustMkdir(t, filepath.Dir(p.New))
		mustWrite(t, p.New, "already-migrated")
	}

	result, err := Migrate(nil)
	if err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}
	if result.HasMigrations() {
		t.Errorf("expected HasMigrations=false on idempotent re-run, got Migrated=%v", result.Migrated)
	}
	if len(result.Skipped) != 5 {
		t.Errorf("Skipped count = %d, want 5 (all already migrated)", len(result.Skipped))
	}
}

func TestMigrate_noOldFiles(t *testing.T) {
	tempHome := t.TempDir()
	setHome(t, tempHome)

	// Empty $HOME — no old or new state files.
	result, err := Migrate(nil)
	if err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}
	if result.HasMigrations() {
		t.Errorf("expected HasMigrations=false on empty home, got Migrated=%v", result.Migrated)
	}
	if len(result.Skipped) != 5 {
		t.Errorf("Skipped count = %d, want 5", len(result.Skipped))
	}
}

func TestMigrate_partialState(t *testing.T) {
	tempHome := t.TempDir()
	setHome(t, tempHome)

	pairs := PathPairs()
	// Migrate only the first two pairs (new exists), populate the rest only in old.
	for i, p := range pairs {
		if i < 2 {
			mustMkdir(t, filepath.Dir(p.New))
			mustWrite(t, p.New, "already")
		} else {
			mustMkdir(t, filepath.Dir(p.Old))
			mustWrite(t, p.Old, "to-migrate")
		}
	}

	result, err := Migrate(nil)
	if err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}
	if len(result.Migrated) != 3 {
		t.Errorf("Migrated count = %d, want 3", len(result.Migrated))
	}
	if len(result.Skipped) != 2 {
		t.Errorf("Skipped count = %d, want 2", len(result.Skipped))
	}
	if len(result.Failed) != 0 {
		t.Errorf("Failed count = %d, want 0", len(result.Failed))
	}
}

func TestMigrate_oldIsDir(t *testing.T) {
	tempHome := t.TempDir()
	setHome(t, tempHome)

	// Wedge: replace one of the old paths with a directory.
	pairs := PathPairs()
	target := pairs[0]
	mustMkdir(t, target.Old)

	result, err := Migrate(nil)
	if err == nil {
		t.Fatal("expected error when old path is a directory")
	}
	if len(result.Failed) != 1 {
		t.Errorf("Failed count = %d, want 1", len(result.Failed))
	}
	if !strings.Contains(err.Error(), "directory") {
		t.Errorf("error %q should mention directory", err)
	}
}

func TestMigrate_continuesAfterFailure(t *testing.T) {
	tempHome := t.TempDir()
	setHome(t, tempHome)

	pairs := PathPairs()
	// First pair: directory wedge (will fail).
	mustMkdir(t, pairs[0].Old)
	// Remaining pairs: normal old-file content.
	for _, p := range pairs[1:] {
		mustMkdir(t, filepath.Dir(p.Old))
		mustWrite(t, p.Old, "ok")
	}

	result, _ := Migrate(nil)
	if len(result.Failed) != 1 {
		t.Errorf("Failed count = %d, want 1", len(result.Failed))
	}
	if len(result.Migrated) != 4 {
		t.Errorf("Migrated count = %d, want 4 (continue past failure)", len(result.Migrated))
	}
}

// --- helpers ---

func setHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", dir)
	}
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", p, err)
	}
}

func mustWrite(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir for write %s: %v", p, err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
