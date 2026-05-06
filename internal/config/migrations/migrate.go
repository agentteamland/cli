// Package migrations implements the atl-config schema-version migration
// chain. Each schemaVersion bump ships a Migrator that transforms a
// configuration map from version N to N+1; the chain is applied step by
// step so users who skip several intermediate versions still arrive at
// the current shape.
//
// As of atl v1.x.y (the atl-config-system rollout) the only schemaVersion
// in production is 1. The migrator chain is empty — the framework is in
// place so that a future v2 (and beyond) can register a Migrator in a
// dedicated source file (migrate_v1_to_v2.go) without cross-cutting
// changes to load logic.
//
// Decision context: workspace .atl/docs/atl-config-system.md
// § Schema versioning.
package migrations

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
)

// Migrator transforms a config map from FromVersion() to FromVersion()+1.
//
// Apply MUST NOT mutate its input map. The output map MUST set
// schemaVersion = FromVersion()+1.
type Migrator interface {
	FromVersion() int
	Apply(data map[string]any) (map[string]any, error)
}

// Default is the registered chain. Populated by init() in
// per-version source files (e.g. migrate_v1_to_v2.go) once those exist.
//
// Always nil-safe: an empty chain is a no-op when current == target.
var Default []Migrator

// Register appends a Migrator to the Default chain. Intended to be called
// from per-version source files' init() functions.
func Register(m Migrator) {
	Default = append(Default, m)
}

// ErrNoChainStep is returned when ApplyChain needs a migrator from
// schemaVersion N to N+1 but none is registered.
type ErrNoChainStep struct {
	From int
}

func (e *ErrNoChainStep) Error() string {
	return fmt.Sprintf("no migrator registered for schemaVersion %d -> %d (upgrade atl)", e.From, e.From+1)
}

// ErrTooNew is returned when ApplyChain is asked to bring data DOWN to a
// lower target than its current schemaVersion. This indicates the config
// was written by a newer atl binary than the one running.
type ErrTooNew struct {
	Found  int
	Target int
}

func (e *ErrTooNew) Error() string {
	return fmt.Sprintf("schemaVersion %d is newer than this binary supports (target %d). Upgrade atl: brew upgrade agentteamland/tap/atl (or scoop update atl)", e.Found, e.Target)
}

// ApplyChain runs migrators against data to bring its schemaVersion up to
// target. Returns the migrated map, its final schemaVersion, and a flag
// indicating whether any migration was actually applied.
//
// The input map is not mutated; a shallow-cloned working map is passed
// to each migrator.
func ApplyChain(data map[string]any, target int, chain []Migrator) (map[string]any, int, bool, error) {
	current, err := readSchemaVersion(data)
	if err != nil {
		return nil, 0, false, err
	}
	if current > target {
		return data, current, false, &ErrTooNew{Found: current, Target: target}
	}
	if current == target {
		return data, current, false, nil
	}

	work := cloneMap(data)
	migrated := false
	for current < target {
		step := findMigrator(chain, current)
		if step == nil {
			return nil, current, migrated, &ErrNoChainStep{From: current}
		}
		next, err := step.Apply(work)
		if err != nil {
			return nil, current, migrated, fmt.Errorf("apply migrator v%d->v%d: %w", current, current+1, err)
		}
		nv, err := readSchemaVersion(next)
		if err != nil {
			return nil, current, migrated, fmt.Errorf("migrator v%d->v%d output: %w", current, current+1, err)
		}
		if nv != current+1 {
			return nil, current, migrated, fmt.Errorf("migrator v%d->v%d output declared schemaVersion %d, want %d", current, current+1, nv, current+1)
		}
		work = next
		current = nv
		migrated = true
	}
	return work, current, migrated, nil
}

// MigrateFile applies the Default chain to a config file in-place. Writes
// a backup at <path>.bak.v<oldVersion> before rewriting the live file.
//
// Behavior:
//   - File missing: returns nil (no migration needed).
//   - File at target schemaVersion: returns nil (idempotent, no backup).
//   - schemaVersion newer than target: returns *ErrTooNew (no file change).
//   - Missing chain step: returns *ErrNoChainStep (no file change).
//   - Malformed JSON: returns a parse error (no file change).
//   - Migration error: returns the error; backup is not written.
//
// On success, stderr (when non-nil) receives one line:
//
//	config migrated v<oldVersion> -> v<newVersion> (backup: <path>.bak.v<oldVersion>)
func MigrateFile(path string, target int, stderr io.Writer) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read %s: %w", path, err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}

	migrated, finalVersion, didMigrate, err := ApplyChain(data, target, Default)
	if err != nil {
		return err
	}
	if !didMigrate {
		return nil
	}

	oldVersion, _ := readSchemaVersion(data)
	backupPath := fmt.Sprintf("%s.bak.v%d", path, oldVersion)
	if err := os.WriteFile(backupPath, raw, 0o600); err != nil {
		return fmt.Errorf("write backup %s: %w", backupPath, err)
	}

	out, err := json.MarshalIndent(migrated, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal migrated config: %w", err)
	}
	out = append(out, '\n')
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	if stderr != nil {
		fmt.Fprintf(stderr, "config migrated v%d -> v%d (backup: %s)\n", oldVersion, finalVersion, backupPath)
	}
	return nil
}

func readSchemaVersion(m map[string]any) (int, error) {
	raw, ok := m["schemaVersion"]
	if !ok {
		return 0, errors.New("missing schemaVersion")
	}
	f, ok := raw.(float64)
	if !ok {
		return 0, fmt.Errorf("schemaVersion must be a number, got %T", raw)
	}
	if float64(int(f)) != f {
		return 0, fmt.Errorf("schemaVersion must be a whole number, got %v", raw)
	}
	return int(f), nil
}

func cloneMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func findMigrator(chain []Migrator, from int) Migrator {
	for _, m := range chain {
		if m.FromVersion() == from {
			return m
		}
	}
	return nil
}
