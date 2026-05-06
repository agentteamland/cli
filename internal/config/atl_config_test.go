package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDefaultAtlConfig(t *testing.T) {
	c := DefaultAtlConfig()

	if c.SchemaVersion != CurrentAtlConfigSchemaVersion {
		t.Errorf("schemaVersion = %d, want %d", c.SchemaVersion, CurrentAtlConfigSchemaVersion)
	}
	if c.CLI.Locale != "en" {
		t.Errorf("cli.locale = %q, want %q", c.CLI.Locale, "en")
	}
	if !c.AutoUpdate.SessionStartEnabled {
		t.Error("autoUpdate.sessionStartEnabled: want true")
	}
	if c.AutoUpdate.ThrottleMinutes != 30 {
		t.Errorf("autoUpdate.throttleMinutes = %d, want 30", c.AutoUpdate.ThrottleMinutes)
	}
	if c.LearningCapture.FirstRunLookbackDays != 7 {
		t.Errorf("learningCapture.firstRunLookbackDays = %d, want 7", c.LearningCapture.FirstRunLookbackDays)
	}
	if c.Brainstorm.MarkerBulletCap != 8 {
		t.Errorf("brainstorm.markerBulletCap = %d, want 8", c.Brainstorm.MarkerBulletCap)
	}

	if err := c.Validate(); err != nil {
		t.Errorf("default config failed Validate: %v", err)
	}
}

func TestFindProjectRoot_walksUp(t *testing.T) {
	// Layout:
	//   <tempDir>/
	//     .atl/                 (project marker)
	//     a/
	//       b/
	//         c/                (cwd)
	//
	// FindProjectRoot from c/ should resolve to <tempDir>.
	tempHome := t.TempDir()
	setHome(t, tempHome)

	projectRoot := t.TempDir()
	mustMkdir(t, filepath.Join(projectRoot, ".atl"))
	cwd := filepath.Join(projectRoot, "a", "b", "c")
	mustMkdir(t, cwd)

	root, ok, err := FindProjectRoot(cwd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatalf("expected to find project root, got ok=false")
	}
	if got, want := filepath.Clean(root), filepath.Clean(projectRoot); got != want {
		t.Errorf("project root = %q, want %q", got, want)
	}
}

func TestFindProjectRoot_homeIsGlobal(t *testing.T) {
	// $HOME with a .atl/ directory in it should NOT be reported as a project.
	tempHome := t.TempDir()
	mustMkdir(t, filepath.Join(tempHome, ".atl"))
	setHome(t, tempHome)

	cwd := filepath.Join(tempHome, "some", "subdir")
	mustMkdir(t, cwd)

	_, ok, err := FindProjectRoot(cwd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected $HOME to be treated as global, got project root reported")
	}
}

func TestFindProjectRoot_noMatch(t *testing.T) {
	tempHome := t.TempDir()
	setHome(t, tempHome)

	cwd := t.TempDir() // separate from tempHome, no .atl/ anywhere
	_, ok, err := FindProjectRoot(cwd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected ok=false when no .atl/ exists, got ok=true")
	}
}

func TestLoadAtlConfigFile_missing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "config.json")
	m, ok, err := LoadAtlConfigFile(missing)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected ok=false for missing file")
	}
	if m != nil {
		t.Error("expected nil map for missing file")
	}
}

func TestLoadAtlConfigFile_full(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	mustWrite(t, path, `{
  "schemaVersion": 1,
  "cli": {"locale": "tr"},
  "autoUpdate": {
    "sessionStartEnabled": false,
    "promptSubmitEnabled": false,
    "throttleMinutes": 60,
    "selfCheckEnabled": false,
    "selfCheckHours": 12
  },
  "learningCapture": {"autoScanEnabled": false, "firstRunLookbackDays": 14},
  "brainstorm": {"markerBulletCap": 5}
}`)

	m, ok, err := LoadAtlConfigFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true")
	}
	if m["schemaVersion"].(float64) != 1 {
		t.Errorf("schemaVersion in map = %v, want 1", m["schemaVersion"])
	}
	if m["cli"].(map[string]any)["locale"].(string) != "tr" {
		t.Errorf("cli.locale = %v, want \"tr\"", m["cli"])
	}
}

func TestLoadAtlConfigFile_partial(t *testing.T) {
	// Partial file (only schemaVersion + cli.locale) should load cleanly;
	// the merge layer fills in defaults for everything else.
	path := filepath.Join(t.TempDir(), "config.json")
	mustWrite(t, path, `{"schemaVersion": 1, "cli": {"locale": "tr"}}`)

	m, ok, err := LoadAtlConfigFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true")
	}
	if _, has := m["autoUpdate"]; has {
		t.Error("expected autoUpdate absent in raw map (partial file)")
	}
}

func TestLoadAtlConfigFile_unknownTopLevel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	mustWrite(t, path, `{"schemaVersion": 1, "bogus": true}`)

	_, _, err := LoadAtlConfigFile(path)
	if err == nil {
		t.Fatal("expected error for unknown top-level field")
	}
	if !strings.Contains(err.Error(), "unknown top-level field") {
		t.Errorf("error %q does not mention 'unknown top-level field'", err)
	}
	if !strings.Contains(err.Error(), `"bogus"`) {
		t.Errorf("error %q should name the offending field", err)
	}
}

func TestLoadAtlConfigFile_unknownNested(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	mustWrite(t, path, `{"schemaVersion": 1, "cli": {"locale": "en", "themer": "dark"}}`)

	_, _, err := LoadAtlConfigFile(path)
	if err == nil {
		t.Fatal("expected error for unknown nested field")
	}
	if !strings.Contains(err.Error(), "cli.themer") {
		t.Errorf("error %q should mention cli.themer", err)
	}
}

func TestLoadAtlConfigFile_oldSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	mustWrite(t, path, `{"schemaVersion": 0}`)

	_, _, err := LoadAtlConfigFile(path)
	if err == nil {
		t.Fatal("expected error for old schemaVersion")
	}
	var sv *SchemaVersionMismatchError
	if !errors.As(err, &sv) {
		t.Fatalf("expected *SchemaVersionMismatchError, got %T: %v", err, err)
	}
	if sv.Found != 0 || sv.Wanted != 1 {
		t.Errorf("Found=%d Wanted=%d, want Found=0 Wanted=1", sv.Found, sv.Wanted)
	}
	if !strings.Contains(err.Error(), "atl migrate") {
		t.Errorf("old-schema error %q should hint at atl migrate", err)
	}
}

func TestLoadAtlConfigFile_newerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	mustWrite(t, path, `{"schemaVersion": 99}`)

	_, _, err := LoadAtlConfigFile(path)
	if err == nil {
		t.Fatal("expected error for newer schemaVersion")
	}
	if !strings.Contains(err.Error(), "Upgrade atl") {
		t.Errorf("newer-schema error %q should hint at upgrading atl", err)
	}
}

func TestLoadAtlConfigFile_missingSchemaVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	mustWrite(t, path, `{"cli": {"locale": "en"}}`)

	_, _, err := LoadAtlConfigFile(path)
	if err == nil {
		t.Fatal("expected error for missing schemaVersion")
	}
	if !strings.Contains(err.Error(), "schemaVersion") {
		t.Errorf("error %q should mention schemaVersion", err)
	}
}

func TestLoadAtlConfigFile_malformed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	mustWrite(t, path, `{ "schemaVersion": 1, "cli": `) // truncated

	_, _, err := LoadAtlConfigFile(path)
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
	if !strings.Contains(err.Error(), "parse") {
		t.Errorf("error %q should mention parse", err)
	}
}

func TestDeepMergeMaps_simple(t *testing.T) {
	a := map[string]any{"x": 1, "y": 2}
	b := map[string]any{"y": 99, "z": 3}
	got := deepMergeMaps(a, b)

	want := map[string]any{"x": 1, "y": 99, "z": 3}
	if !mapsEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	// Inputs unchanged.
	if a["y"] != 2 {
		t.Error("input map a was mutated")
	}
}

func TestDeepMergeMaps_nested(t *testing.T) {
	a := map[string]any{
		"group": map[string]any{"keep": 1, "override": 10},
		"top":   "untouched",
	}
	b := map[string]any{
		"group": map[string]any{"override": 99, "added": 7},
	}
	got := deepMergeMaps(a, b)

	g := got["group"].(map[string]any)
	if g["keep"] != 1 {
		t.Errorf("group.keep = %v, want 1", g["keep"])
	}
	if g["override"] != 99 {
		t.Errorf("group.override = %v, want 99", g["override"])
	}
	if g["added"] != 7 {
		t.Errorf("group.added = %v, want 7", g["added"])
	}
	if got["top"] != "untouched" {
		t.Errorf("top = %v, want \"untouched\"", got["top"])
	}
}

func TestLoadEffectiveAtlConfig_defaultsOnly(t *testing.T) {
	tempHome := t.TempDir()
	setHome(t, tempHome) // ~/.atl/config.json absent

	cfg, err := LoadEffectiveAtlConfig("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := DefaultAtlConfig()
	if cfg != want {
		t.Errorf("got %+v, want %+v", cfg, want)
	}
}

func TestLoadEffectiveAtlConfig_globalOnly(t *testing.T) {
	tempHome := t.TempDir()
	setHome(t, tempHome)
	mustMkdir(t, filepath.Join(tempHome, ".atl"))
	mustWrite(t, filepath.Join(tempHome, ".atl", "config.json"),
		`{"schemaVersion": 1, "cli": {"locale": "tr"}, "autoUpdate": {"throttleMinutes": 60}}`)

	cfg, err := LoadEffectiveAtlConfig("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.CLI.Locale != "tr" {
		t.Errorf("cli.locale = %q, want %q (global override)", cfg.CLI.Locale, "tr")
	}
	if cfg.AutoUpdate.ThrottleMinutes != 60 {
		t.Errorf("autoUpdate.throttleMinutes = %d, want 60", cfg.AutoUpdate.ThrottleMinutes)
	}
	// Untouched fields fall through to default.
	if !cfg.AutoUpdate.SessionStartEnabled {
		t.Error("autoUpdate.sessionStartEnabled: want default true")
	}
	if cfg.LearningCapture.FirstRunLookbackDays != 7 {
		t.Errorf("learningCapture.firstRunLookbackDays = %d, want default 7", cfg.LearningCapture.FirstRunLookbackDays)
	}
}

func TestLoadEffectiveAtlConfig_projectOverridesGlobal(t *testing.T) {
	tempHome := t.TempDir()
	setHome(t, tempHome)
	mustMkdir(t, filepath.Join(tempHome, ".atl"))
	mustWrite(t, filepath.Join(tempHome, ".atl", "config.json"),
		`{"schemaVersion": 1, "autoUpdate": {"throttleMinutes": 60, "selfCheckEnabled": true}}`)

	projectRoot := t.TempDir()
	mustMkdir(t, filepath.Join(projectRoot, ".atl"))
	mustWrite(t, filepath.Join(projectRoot, ".atl", "config.json"),
		`{"schemaVersion": 1, "autoUpdate": {"selfCheckEnabled": false}}`)

	cfg, err := LoadEffectiveAtlConfig(projectRoot)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Project overrides selfCheckEnabled.
	if cfg.AutoUpdate.SelfCheckEnabled {
		t.Error("autoUpdate.selfCheckEnabled: want false (project override)")
	}
	// Global's throttleMinutes survives because project doesn't touch it
	// (deep merge at field level — verifies the brainstorm's intent).
	if cfg.AutoUpdate.ThrottleMinutes != 60 {
		t.Errorf("autoUpdate.throttleMinutes = %d, want 60 (global preserved)", cfg.AutoUpdate.ThrottleMinutes)
	}
}

func TestLoadEffectiveAtlConfig_validateFires(t *testing.T) {
	tempHome := t.TempDir()
	setHome(t, tempHome)
	mustMkdir(t, filepath.Join(tempHome, ".atl"))
	// Out-of-range throttle.
	mustWrite(t, filepath.Join(tempHome, ".atl", "config.json"),
		`{"schemaVersion": 1, "autoUpdate": {"throttleMinutes": 0}}`)

	_, err := LoadEffectiveAtlConfig("")
	if err == nil {
		t.Fatal("expected validation error for throttleMinutes=0")
	}
	if !strings.Contains(err.Error(), "throttleMinutes") {
		t.Errorf("error %q should mention throttleMinutes", err)
	}
}

func TestValidate_invalidLocale(t *testing.T) {
	c := DefaultAtlConfig()
	c.CLI.Locale = "fr"

	err := c.Validate()
	if err == nil {
		t.Fatal("expected error for locale=fr")
	}
	if !strings.Contains(err.Error(), "cli.locale") {
		t.Errorf("error %q should mention cli.locale", err)
	}
}

func TestValidate_outOfRange(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*AtlConfig)
		wantSub string
	}{
		{"throttleMinutes=0", func(c *AtlConfig) { c.AutoUpdate.ThrottleMinutes = 0 }, "throttleMinutes"},
		{"throttleMinutes=10000", func(c *AtlConfig) { c.AutoUpdate.ThrottleMinutes = 10000 }, "throttleMinutes"},
		{"selfCheckHours=0", func(c *AtlConfig) { c.AutoUpdate.SelfCheckHours = 0 }, "selfCheckHours"},
		{"selfCheckHours=999", func(c *AtlConfig) { c.AutoUpdate.SelfCheckHours = 999 }, "selfCheckHours"},
		{"firstRunLookbackDays=0", func(c *AtlConfig) { c.LearningCapture.FirstRunLookbackDays = 0 }, "firstRunLookbackDays"},
		{"firstRunLookbackDays=400", func(c *AtlConfig) { c.LearningCapture.FirstRunLookbackDays = 400 }, "firstRunLookbackDays"},
		{"markerBulletCap=0", func(c *AtlConfig) { c.Brainstorm.MarkerBulletCap = 0 }, "markerBulletCap"},
		{"markerBulletCap=99", func(c *AtlConfig) { c.Brainstorm.MarkerBulletCap = 99 }, "markerBulletCap"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := DefaultAtlConfig()
			tc.mutate(&c)
			err := c.Validate()
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error %q should mention %q", err, tc.wantSub)
			}
		})
	}
}

func TestPathHelpers(t *testing.T) {
	tempHome := t.TempDir()
	setHome(t, tempHome)

	if got := AtlHome(); got != filepath.Join(tempHome, ".atl") {
		t.Errorf("AtlHome() = %q, want %q", got, filepath.Join(tempHome, ".atl"))
	}
	if got := GlobalAtlConfigPath(); got != filepath.Join(tempHome, ".atl", "config.json") {
		t.Errorf("GlobalAtlConfigPath() = %q, want %q", got, filepath.Join(tempHome, ".atl", "config.json"))
	}
	if got := ProjectAtlConfigPath("/tmp/proj"); got != filepath.Join("/tmp/proj", ".atl", "config.json") {
		t.Errorf("ProjectAtlConfigPath unexpected: %q", got)
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
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

func mapsEqual(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
