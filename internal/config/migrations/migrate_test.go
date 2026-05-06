package migrations

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeMigrator is a test double that bumps schemaVersion + sets a marker
// field. It pretends to migrate from `from` to `from+1`.
type fakeMigrator struct {
	from   int
	marker string
}

func (f fakeMigrator) FromVersion() int { return f.from }
func (f fakeMigrator) Apply(in map[string]any) (map[string]any, error) {
	out := make(map[string]any, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	out["schemaVersion"] = float64(f.from + 1)
	out[f.marker] = true
	return out, nil
}

// brokenMigrator returns the wrong output schemaVersion.
type brokenMigrator struct {
	from int
}

func (b brokenMigrator) FromVersion() int { return b.from }
func (b brokenMigrator) Apply(in map[string]any) (map[string]any, error) {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	out["schemaVersion"] = float64(99) // wrong
	return out, nil
}

// erroringMigrator just returns an error.
type erroringMigrator struct {
	from int
}

func (e erroringMigrator) FromVersion() int { return e.from }
func (e erroringMigrator) Apply(in map[string]any) (map[string]any, error) {
	return nil, errors.New("boom")
}

// --- ApplyChain ---

func TestApplyChain_alreadyAtTarget(t *testing.T) {
	in := map[string]any{"schemaVersion": float64(1)}
	out, version, migrated, err := ApplyChain(in, 1, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if migrated {
		t.Error("expected migrated=false when already at target")
	}
	if version != 1 {
		t.Errorf("version = %d, want 1", version)
	}
	if out["schemaVersion"].(float64) != 1 {
		t.Error("schemaVersion changed by no-op chain")
	}
}

func TestApplyChain_singleStep(t *testing.T) {
	in := map[string]any{"schemaVersion": float64(1)}
	chain := []Migrator{fakeMigrator{from: 1, marker: "m_v2"}}

	out, version, migrated, err := ApplyChain(in, 2, chain)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !migrated {
		t.Error("expected migrated=true")
	}
	if version != 2 {
		t.Errorf("version = %d, want 2", version)
	}
	if out["schemaVersion"].(float64) != 2 {
		t.Error("schemaVersion not bumped to 2")
	}
	if out["m_v2"] != true {
		t.Error("v2 marker missing in output")
	}
}

func TestApplyChain_multiStep(t *testing.T) {
	in := map[string]any{"schemaVersion": float64(1)}
	chain := []Migrator{
		fakeMigrator{from: 1, marker: "m_v2"},
		fakeMigrator{from: 2, marker: "m_v3"},
		fakeMigrator{from: 3, marker: "m_v4"},
	}

	out, version, migrated, err := ApplyChain(in, 4, chain)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !migrated {
		t.Error("expected migrated=true")
	}
	if version != 4 {
		t.Errorf("version = %d, want 4", version)
	}
	for _, m := range []string{"m_v2", "m_v3", "m_v4"} {
		if out[m] != true {
			t.Errorf("marker %q missing in output (chain didn't reach this step)", m)
		}
	}
}

func TestApplyChain_skipFromMidVersion(t *testing.T) {
	// Config already at v2 — only v2->v3 step needs to apply for target=3.
	in := map[string]any{"schemaVersion": float64(2)}
	chain := []Migrator{
		fakeMigrator{from: 1, marker: "m_v2"},
		fakeMigrator{from: 2, marker: "m_v3"},
	}

	out, version, _, err := ApplyChain(in, 3, chain)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if version != 3 {
		t.Errorf("version = %d, want 3", version)
	}
	if out["m_v2"] != nil {
		t.Error("v2 marker should not be present (started at v2 already)")
	}
	if out["m_v3"] != true {
		t.Error("v3 marker should be present")
	}
}

func TestApplyChain_missingStep(t *testing.T) {
	in := map[string]any{"schemaVersion": float64(1)}
	// chain has v1->v2 but not v2->v3
	chain := []Migrator{fakeMigrator{from: 1, marker: "m_v2"}}

	_, _, _, err := ApplyChain(in, 3, chain)
	if err == nil {
		t.Fatal("expected error for missing chain step")
	}
	var noStep *ErrNoChainStep
	if !errors.As(err, &noStep) {
		t.Fatalf("expected *ErrNoChainStep, got %T: %v", err, err)
	}
	if noStep.From != 2 {
		t.Errorf("ErrNoChainStep.From = %d, want 2", noStep.From)
	}
	if !strings.Contains(err.Error(), "upgrade atl") {
		t.Errorf("error %q should hint at upgrade atl", err)
	}
}

func TestApplyChain_tooNew(t *testing.T) {
	in := map[string]any{"schemaVersion": float64(5)}
	_, _, _, err := ApplyChain(in, 1, nil)
	if err == nil {
		t.Fatal("expected ErrTooNew")
	}
	var tooNew *ErrTooNew
	if !errors.As(err, &tooNew) {
		t.Fatalf("expected *ErrTooNew, got %T: %v", err, err)
	}
	if tooNew.Found != 5 || tooNew.Target != 1 {
		t.Errorf("ErrTooNew: Found=%d Target=%d, want Found=5 Target=1", tooNew.Found, tooNew.Target)
	}
	if !strings.Contains(err.Error(), "Upgrade atl") {
		t.Errorf("error %q should hint at Upgrade atl", err)
	}
}

func TestApplyChain_brokenMigratorOutput(t *testing.T) {
	in := map[string]any{"schemaVersion": float64(1)}
	chain := []Migrator{brokenMigrator{from: 1}}

	_, _, _, err := ApplyChain(in, 2, chain)
	if err == nil {
		t.Fatal("expected error from migrator that outputs wrong schemaVersion")
	}
	if !strings.Contains(err.Error(), "schemaVersion") {
		t.Errorf("error %q should mention schemaVersion", err)
	}
}

func TestApplyChain_migratorError(t *testing.T) {
	in := map[string]any{"schemaVersion": float64(1)}
	chain := []Migrator{erroringMigrator{from: 1}}

	_, _, _, err := ApplyChain(in, 2, chain)
	if err == nil {
		t.Fatal("expected error from erroring migrator")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error %q should propagate migrator error", err)
	}
}

func TestApplyChain_noMutation(t *testing.T) {
	in := map[string]any{"schemaVersion": float64(1), "untouched": "yes"}
	chain := []Migrator{fakeMigrator{from: 1, marker: "m_v2"}}

	_, _, _, err := ApplyChain(in, 2, chain)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if in["schemaVersion"].(float64) != 1 {
		t.Error("input map's schemaVersion mutated")
	}
	if _, has := in["m_v2"]; has {
		t.Error("input map gained a marker (mutation)")
	}
}

func TestApplyChain_missingSchemaVersion(t *testing.T) {
	in := map[string]any{"foo": "bar"}
	_, _, _, err := ApplyChain(in, 1, nil)
	if err == nil {
		t.Fatal("expected error for missing schemaVersion")
	}
}

// --- MigrateFile ---

func TestMigrateFile_alreadyAtTarget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	mustWriteJSON(t, path, map[string]any{"schemaVersion": 1})

	if err := MigrateFile(path, 1, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// No backup expected.
	if _, err := os.Stat(path + ".bak.v1"); !os.IsNotExist(err) {
		t.Error("backup file should not exist when no migration ran")
	}
}

func TestMigrateFile_missing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "config.json")
	if err := MigrateFile(missing, 1, nil); err != nil {
		t.Fatalf("expected no error for missing file, got %v", err)
	}
}

func TestMigrateFile_writesBackupAndMigrates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	mustWriteJSON(t, path, map[string]any{"schemaVersion": 1, "preserved": "yes"})

	// Swap Default for a known chain just for this test.
	prev := Default
	t.Cleanup(func() { Default = prev })
	Default = []Migrator{fakeMigrator{from: 1, marker: "added_by_v2"}}

	var buf bytes.Buffer
	if err := MigrateFile(path, 2, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Backup at v1.
	backupBytes, err := os.ReadFile(path + ".bak.v1")
	if err != nil {
		t.Fatalf("backup file missing: %v", err)
	}
	var backup map[string]any
	mustUnmarshal(t, backupBytes, &backup)
	if backup["schemaVersion"].(float64) != 1 {
		t.Errorf("backup schemaVersion = %v, want 1", backup["schemaVersion"])
	}
	if backup["preserved"] != "yes" {
		t.Error("backup did not preserve original fields")
	}

	// Live file at v2.
	liveBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("live file missing: %v", err)
	}
	var live map[string]any
	mustUnmarshal(t, liveBytes, &live)
	if live["schemaVersion"].(float64) != 2 {
		t.Errorf("live schemaVersion = %v, want 2", live["schemaVersion"])
	}
	if live["added_by_v2"] != true {
		t.Error("v2 marker missing in live file")
	}
	if live["preserved"] != "yes" {
		t.Error("live file lost preserved field")
	}

	// Stderr report.
	out := buf.String()
	for _, want := range []string{"v1", "v2", "backup"} {
		if !strings.Contains(out, want) {
			t.Errorf("stderr %q missing %q", out, want)
		}
	}
}

func TestMigrateFile_malformed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("{ truncated"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	err := MigrateFile(path, 1, nil)
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
	if !strings.Contains(err.Error(), "parse") {
		t.Errorf("error %q should mention parse", err)
	}
}

func TestMigrateFile_tooNew(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	mustWriteJSON(t, path, map[string]any{"schemaVersion": 99})

	err := MigrateFile(path, 1, nil)
	if err == nil {
		t.Fatal("expected ErrTooNew")
	}
	var tooNew *ErrTooNew
	if !errors.As(err, &tooNew) {
		t.Fatalf("expected *ErrTooNew, got %T", err)
	}
	// Original file untouched.
	got, _ := os.ReadFile(path)
	var live map[string]any
	mustUnmarshal(t, got, &live)
	if live["schemaVersion"].(float64) != 99 {
		t.Errorf("original file modified despite ErrTooNew: schemaVersion = %v", live["schemaVersion"])
	}
}

func TestMigrateFile_missingChainStep(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	mustWriteJSON(t, path, map[string]any{"schemaVersion": 1})

	prev := Default
	t.Cleanup(func() { Default = prev })
	Default = nil // empty chain

	err := MigrateFile(path, 2, nil)
	if err == nil {
		t.Fatal("expected ErrNoChainStep")
	}
	var noStep *ErrNoChainStep
	if !errors.As(err, &noStep) {
		t.Fatalf("expected *ErrNoChainStep, got %T", err)
	}
}

// --- helpers ---

func mustWriteJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mustUnmarshal(t *testing.T, b []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, string(b))
	}
}

// Sanity check that the package compiles even without a real fmt import
// in this file (it's used transitively via MigrateFile's stderr formatting).
var _ = fmt.Sprintf
