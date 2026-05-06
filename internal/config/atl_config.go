// Package config — atl-config-system layer (PR 2 of 7).
//
// This file owns the user/project-tunable configuration loaded from
// ~/.atl/config.json (global) and <project>/.atl/config.json. The schema
// source-of-truth is agentteamland/core's schemas/atl-config.schema.json
// (added in core@1.14.0). Decision context: workspace
// .claude/docs/atl-config-system.md (decided 2026-05-06).
//
// PR 2 (this) provides load + merge + validate for schemaVersion 1 only.
// Migration of older schema versions to v1 lands in PR 3 (atl migrate).
// The Bubbletea Q&A flow that writes config.json lives in PR 4.

package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// CurrentAtlConfigSchemaVersion is the schemaVersion this binary writes
// and consumes. Bumped when the schema changes (any field add / remove /
// rename / type change / enum change / default semantic change). Migration
// from older versions is registered in cli/internal/config/migrations/
// (PR 3 of the atl-config-system rollout).
const CurrentAtlConfigSchemaVersion = 1

// AtlConfig is the typed view of an atl config.json (global or project).
//
// Field defaults match the JSON Schema's `default` values; see
// DefaultAtlConfig for the canonical population.
type AtlConfig struct {
	SchemaVersion   int                   `json:"schemaVersion"`
	CLI             CLIConfig             `json:"cli"`
	AutoUpdate      AutoUpdateConfig      `json:"autoUpdate"`
	LearningCapture LearningCaptureConfig `json:"learningCapture"`
	Brainstorm      BrainstormConfig      `json:"brainstorm"`
}

// CLIConfig holds CLI-behavior keys.
type CLIConfig struct {
	// Locale is one of "en" or "tr". v1 only behaves as English; "tr" is
	// accepted in the schema for forward-compatibility but currently
	// behaves as "en" until the cli-localization brainstorm ships.
	Locale string `json:"locale"`
}

// AutoUpdateConfig holds auto-update behavior keys.
type AutoUpdateConfig struct {
	SessionStartEnabled bool `json:"sessionStartEnabled"`
	PromptSubmitEnabled bool `json:"promptSubmitEnabled"`
	ThrottleMinutes     int  `json:"throttleMinutes"`
	SelfCheckEnabled    bool `json:"selfCheckEnabled"`
	SelfCheckHours      int  `json:"selfCheckHours"`
}

// LearningCaptureConfig holds learning-capture (transcript marker scanning) keys.
type LearningCaptureConfig struct {
	AutoScanEnabled      bool `json:"autoScanEnabled"`
	FirstRunLookbackDays int  `json:"firstRunLookbackDays"`
}

// BrainstormConfig holds brainstorm-skill keys.
type BrainstormConfig struct {
	MarkerBulletCap int `json:"markerBulletCap"`
}

// DefaultAtlConfig returns the schema-defined defaults at
// CurrentAtlConfigSchemaVersion. This is the bottom layer of the
// effective-config merge stack.
func DefaultAtlConfig() AtlConfig {
	return AtlConfig{
		SchemaVersion: CurrentAtlConfigSchemaVersion,
		CLI: CLIConfig{
			Locale: "en",
		},
		AutoUpdate: AutoUpdateConfig{
			SessionStartEnabled: true,
			PromptSubmitEnabled: true,
			ThrottleMinutes:     30,
			SelfCheckEnabled:    true,
			SelfCheckHours:      24,
		},
		LearningCapture: LearningCaptureConfig{
			AutoScanEnabled:      true,
			FirstRunLookbackDays: 7,
		},
		Brainstorm: BrainstormConfig{
			MarkerBulletCap: 8,
		},
	}
}

// AtlHome returns ~/.atl/. Falls back to ".atl" relative to cwd if
// UserHomeDir fails (rare; only on misconfigured systems).
func AtlHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".atl"
	}
	return filepath.Join(home, ".atl")
}

// GlobalAtlConfigPath returns ~/.atl/config.json.
func GlobalAtlConfigPath() string {
	return filepath.Join(AtlHome(), "config.json")
}

// ProjectAtlConfigPath returns <projectRoot>/.atl/config.json.
func ProjectAtlConfigPath(projectRoot string) string {
	return filepath.Join(projectRoot, ".atl", "config.json")
}

// FindProjectRoot walks up from cwd looking for a .atl/ directory.
// Returns ("", false, nil) when no project .atl/ is found before the
// filesystem root or $HOME is reached.
//
// Special case: a match at $HOME is treated as global (since ~/.atl/
// IS the global config home), so this function will NOT report $HOME
// as a project root.
func FindProjectRoot(cwd string) (string, bool, error) {
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return "", false, fmt.Errorf("get cwd: %w", err)
		}
	}

	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", false, fmt.Errorf("resolve cwd: %w", err)
	}
	abs = filepath.Clean(abs)

	home, _ := os.UserHomeDir()
	if home != "" {
		home = filepath.Clean(home)
	}

	for {
		if home != "" && abs == home {
			return "", false, nil
		}

		atl := filepath.Join(abs, ".atl")
		info, err := os.Stat(atl)
		if err == nil && info.IsDir() {
			return abs, true, nil
		}
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", false, fmt.Errorf("stat %s: %w", atl, err)
		}

		parent := filepath.Dir(abs)
		if parent == abs {
			return "", false, nil
		}
		abs = parent
	}
}

// SchemaVersionMismatchError signals a config.json with a schemaVersion
// that this binary cannot directly consume. Callers may surface a hint
// to run `atl migrate` (older) or `brew upgrade atl` (newer).
type SchemaVersionMismatchError struct {
	Path   string
	Found  int
	Wanted int
}

// Error implements the error interface with a hint matching the version
// gap direction (older = migrate, newer = upgrade).
func (e *SchemaVersionMismatchError) Error() string {
	if e.Found > e.Wanted {
		return fmt.Sprintf(
			"%s: schemaVersion %d is newer than this atl binary supports (max %d). Upgrade atl: brew upgrade agentteamland/tap/atl (or scoop update atl)",
			e.Path, e.Found, e.Wanted,
		)
	}
	return fmt.Sprintf(
		"%s: schemaVersion %d is older than current (%d). Run: atl migrate",
		e.Path, e.Found, e.Wanted,
	)
}

// LoadAtlConfigFile reads a single config.json and returns its contents
// as an unflattened map[string]any (preserves "field present" vs "field
// absent" — required for the deep-merge to do the right thing on partial
// hand-edited files).
//
// Returns (nil, false, nil) when the file does not exist.
// Returns an error for malformed JSON, unknown top-level / nested fields,
// or schema-version mismatch (wrapped as *SchemaVersionMismatchError).
func LoadAtlConfigFile(path string) (map[string]any, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("read %s: %w", path, err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, true, fmt.Errorf("parse %s: %w", path, err)
	}

	if err := validateAtlConfigShape(raw, path); err != nil {
		return nil, true, err
	}

	if err := checkAtlConfigSchemaVersion(raw, path); err != nil {
		return nil, true, err
	}

	return raw, true, nil
}

// LoadEffectiveAtlConfig returns defaults <- global <- project, merged at
// the field level (deep merge). projectRoot may be empty to skip the
// project layer. Missing files (global or project) are silently treated
// as empty overlays.
//
// The returned config is also Validate'd before return — range / enum
// errors surface as a single error covering all violations.
func LoadEffectiveAtlConfig(projectRoot string) (AtlConfig, error) {
	merged, err := defaultsAsMap()
	if err != nil {
		return AtlConfig{}, err
	}

	if g, ok, err := LoadAtlConfigFile(GlobalAtlConfigPath()); err != nil {
		return AtlConfig{}, err
	} else if ok {
		merged = deepMergeMaps(merged, g)
	}

	if projectRoot != "" {
		if p, ok, err := LoadAtlConfigFile(ProjectAtlConfigPath(projectRoot)); err != nil {
			return AtlConfig{}, err
		} else if ok {
			merged = deepMergeMaps(merged, p)
		}
	}

	cfg, err := mapToAtlConfig(merged)
	if err != nil {
		return AtlConfig{}, err
	}

	if err := cfg.Validate(); err != nil {
		return AtlConfig{}, err
	}

	return cfg, nil
}

// Validate checks the config against the schema's range / enum constraints.
// Returns a single error joining all violations (errors.Join). The
// schemaVersion / unknown-field checks happen at load time inside
// LoadAtlConfigFile; Validate covers what's left.
func (c AtlConfig) Validate() error {
	var errs []error

	if c.SchemaVersion != CurrentAtlConfigSchemaVersion {
		errs = append(errs, fmt.Errorf("schemaVersion: must be %d, got %d", CurrentAtlConfigSchemaVersion, c.SchemaVersion))
	}

	switch c.CLI.Locale {
	case "en", "tr":
		// ok
	default:
		errs = append(errs, fmt.Errorf("cli.locale: must be one of [en tr], got %q", c.CLI.Locale))
	}

	if c.AutoUpdate.ThrottleMinutes < 1 || c.AutoUpdate.ThrottleMinutes > 1440 {
		errs = append(errs, fmt.Errorf("autoUpdate.throttleMinutes: must be in [1, 1440], got %d", c.AutoUpdate.ThrottleMinutes))
	}
	if c.AutoUpdate.SelfCheckHours < 1 || c.AutoUpdate.SelfCheckHours > 168 {
		errs = append(errs, fmt.Errorf("autoUpdate.selfCheckHours: must be in [1, 168], got %d", c.AutoUpdate.SelfCheckHours))
	}
	if c.LearningCapture.FirstRunLookbackDays < 1 || c.LearningCapture.FirstRunLookbackDays > 365 {
		errs = append(errs, fmt.Errorf("learningCapture.firstRunLookbackDays: must be in [1, 365], got %d", c.LearningCapture.FirstRunLookbackDays))
	}
	if c.Brainstorm.MarkerBulletCap < 1 || c.Brainstorm.MarkerBulletCap > 50 {
		errs = append(errs, fmt.Errorf("brainstorm.markerBulletCap: must be in [1, 50], got %d", c.Brainstorm.MarkerBulletCap))
	}

	return errors.Join(errs...)
}

// allowedAtlConfigFields is the canonical list of permitted top-level keys
// and their nested children. Matches schemas/atl-config.schema.json.
var allowedAtlConfigFields = map[string]map[string]struct{}{
	"schemaVersion": nil,
	"cli": {
		"locale": {},
	},
	"autoUpdate": {
		"sessionStartEnabled": {},
		"promptSubmitEnabled": {},
		"throttleMinutes":     {},
		"selfCheckEnabled":    {},
		"selfCheckHours":      {},
	},
	"learningCapture": {
		"autoScanEnabled":      {},
		"firstRunLookbackDays": {},
	},
	"brainstorm": {
		"markerBulletCap": {},
	},
}

// validateAtlConfigShape enforces additionalProperties:false at every level.
// It also validates that group keys are objects (not primitives), since the
// schema declares `type: object` on each group.
func validateAtlConfigShape(raw map[string]any, path string) error {
	var errs []error

	for k, v := range raw {
		nested, known := allowedAtlConfigFields[k]
		if !known {
			errs = append(errs, fmt.Errorf("%s: unknown top-level field %q (allowed: %s)", path, k, sortedKeys(allowedAtlConfigFields)))
			continue
		}
		if nested == nil {
			continue // primitive top-level field (schemaVersion)
		}
		group, ok := v.(map[string]any)
		if !ok {
			errs = append(errs, fmt.Errorf("%s: %s must be an object, got %T", path, k, v))
			continue
		}
		for nk := range group {
			if _, allowed := nested[nk]; !allowed {
				errs = append(errs, fmt.Errorf("%s: unknown field %s.%s (allowed: %s)", path, k, nk, sortedKeysInner(nested)))
			}
		}
	}

	return errors.Join(errs...)
}

func checkAtlConfigSchemaVersion(raw map[string]any, path string) error {
	v, ok := raw["schemaVersion"]
	if !ok {
		return fmt.Errorf("%s: missing required field 'schemaVersion' (expected %d)", path, CurrentAtlConfigSchemaVersion)
	}
	// JSON numbers parse to float64 by default.
	f, ok := v.(float64)
	if !ok {
		return fmt.Errorf("%s: schemaVersion must be an integer, got %T", path, v)
	}
	n := int(f)
	if float64(n) != f {
		return fmt.Errorf("%s: schemaVersion must be a whole number, got %v", path, v)
	}
	if n != CurrentAtlConfigSchemaVersion {
		return &SchemaVersionMismatchError{Path: path, Found: n, Wanted: CurrentAtlConfigSchemaVersion}
	}
	return nil
}

// defaultsAsMap returns DefaultAtlConfig serialized as map[string]any.
// Used as the bottom layer of the effective-config merge stack.
func defaultsAsMap() (map[string]any, error) {
	b, err := json.Marshal(DefaultAtlConfig())
	if err != nil {
		return nil, fmt.Errorf("marshal defaults: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("unmarshal defaults: %w", err)
	}
	return m, nil
}

// mapToAtlConfig decodes a fully-merged map back to the typed struct.
func mapToAtlConfig(m map[string]any) (AtlConfig, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return AtlConfig{}, fmt.Errorf("re-marshal merged config: %w", err)
	}
	var cfg AtlConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		return AtlConfig{}, fmt.Errorf("decode merged config: %w", err)
	}
	return cfg, nil
}

// deepMergeMaps overlays b on a recursively. Maps merge field-by-field;
// non-map values (primitives, arrays) from b override a's.
//
// Inputs are never mutated; a fresh map is returned.
func deepMergeMaps(a, b map[string]any) map[string]any {
	out := make(map[string]any, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		if existing, ok := out[k]; ok {
			if em, eok := existing.(map[string]any); eok {
				if vm, vok := v.(map[string]any); vok {
					out[k] = deepMergeMaps(em, vm)
					continue
				}
			}
		}
		out[k] = v
	}
	return out
}

func sortedKeys(m map[string]map[string]struct{}) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

func sortedKeysInner(m map[string]struct{}) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}
