package commands

import (
	"testing"
)

// helper: count atl-owned groups in an event array.
func countAtlGroups(hooks map[string]interface{}, event string) int {
	groups := asArray(hooks[event])
	n := 0
	for _, g := range groups {
		if isAtlGroup(g) {
			n++
		}
	}
	return n
}

// helper: count non-atl groups (preserve check).
func countNonAtlGroups(hooks map[string]interface{}, event string) int {
	groups := asArray(hooks[event])
	n := 0
	for _, g := range groups {
		if !isAtlGroup(g) {
			n++
		}
	}
	return n
}

func TestUpsertEventEntry_FreshInstall(t *testing.T) {
	hooks := map[string]interface{}{}
	upsertEventEntry(hooks, "SessionStart", "atl session-start --silent-if-clean")

	if countAtlGroups(hooks, "SessionStart") != 1 {
		t.Fatalf("expected exactly 1 atl group after fresh upsert, got %d",
			countAtlGroups(hooks, "SessionStart"))
	}
}

func TestUpsertEventEntry_Idempotent(t *testing.T) {
	hooks := map[string]interface{}{}
	for i := 0; i < 5; i++ {
		upsertEventEntry(hooks, "SessionStart", "atl session-start --silent-if-clean")
	}
	if countAtlGroups(hooks, "SessionStart") != 1 {
		t.Fatalf("re-upsert should remain 1 atl group, got %d (the second call must REPLACE the first, not append)",
			countAtlGroups(hooks, "SessionStart"))
	}
}

func TestUpsertEventEntry_PreservesUserGroups(t *testing.T) {
	// User has their own SessionStart hook. atl install must not touch it.
	userGroup := map[string]interface{}{
		"hooks": []interface{}{
			map[string]interface{}{
				"type":    "command",
				"command": "/path/to/user-script.sh",
			},
		},
	}
	hooks := map[string]interface{}{
		"SessionStart": []interface{}{userGroup},
	}

	upsertEventEntry(hooks, "SessionStart", "atl session-start --silent-if-clean")

	if countAtlGroups(hooks, "SessionStart") != 1 {
		t.Fatalf("expected 1 atl group, got %d", countAtlGroups(hooks, "SessionStart"))
	}
	if countNonAtlGroups(hooks, "SessionStart") != 1 {
		t.Fatalf("user group must be preserved (got %d non-atl groups, want 1)",
			countNonAtlGroups(hooks, "SessionStart"))
	}
}

func TestUpsertEventEntry_ReplacesStaleAtlGroup(t *testing.T) {
	// Simulate a v0.2.0 install (atl update --silent-if-clean was the
	// SessionStart command). The new shape (v1.1.0+) is atl session-start.
	// upsertEventEntry should drop the v0.2.0 group and write the new one.
	hooks := map[string]interface{}{}
	upsertEventEntry(hooks, "SessionStart", "atl update --silent-if-clean") // legacy
	upsertEventEntry(hooks, "SessionStart", "atl session-start --silent-if-clean")

	if countAtlGroups(hooks, "SessionStart") != 1 {
		t.Fatalf("legacy atl group should be replaced, not coexist (got %d)",
			countAtlGroups(hooks, "SessionStart"))
	}

	// Verify the *current* command is the new one.
	groups := asArray(hooks["SessionStart"])
	for _, g := range groups {
		if !isAtlGroup(g) {
			continue
		}
		grpMap := g.(map[string]interface{})
		inner := asArray(grpMap["hooks"])
		for _, h := range inner {
			hMap := h.(map[string]interface{})
			cmd, _ := hMap["command"].(string)
			if cmd != "atl session-start --silent-if-clean" {
				t.Fatalf("after replace, command should be the new one, got %q", cmd)
			}
		}
	}
}

func TestCleanEventEntries_RemovesAtlOnly(t *testing.T) {
	userGroup := map[string]interface{}{
		"hooks": []interface{}{
			map[string]interface{}{
				"type":    "command",
				"command": "user-script.sh",
			},
		},
	}
	atlGroup := map[string]interface{}{
		"hooks": []interface{}{
			map[string]interface{}{
				"type":    "command",
				"command": "atl session-start --silent-if-clean",
			},
		},
	}
	hooks := map[string]interface{}{
		"SessionStart": []interface{}{userGroup, atlGroup},
	}

	removed := cleanEventEntries(hooks, "SessionStart")
	if !removed {
		t.Fatalf("cleanEventEntries should report something was removed")
	}

	if countAtlGroups(hooks, "SessionStart") != 0 {
		t.Fatalf("atl groups must all be cleaned, got %d remaining",
			countAtlGroups(hooks, "SessionStart"))
	}
	if countNonAtlGroups(hooks, "SessionStart") != 1 {
		t.Fatalf("user group must survive cleanup (got %d non-atl groups, want 1)",
			countNonAtlGroups(hooks, "SessionStart"))
	}
}

func TestCleanEventEntries_DeletesEmptyEvent(t *testing.T) {
	// If only atl groups existed under SessionStart, the entire key
	// should be deleted from hooks (not left as an empty array).
	atlGroup := map[string]interface{}{
		"hooks": []interface{}{
			map[string]interface{}{
				"type":    "command",
				"command": "atl session-start --silent-if-clean",
			},
		},
	}
	hooks := map[string]interface{}{
		"SessionStart": []interface{}{atlGroup},
	}

	cleanEventEntries(hooks, "SessionStart")

	if _, present := hooks["SessionStart"]; present {
		t.Fatalf("event key should be deleted when no non-atl groups remain")
	}
}

func TestCleanEventEntries_NoOpOnMissingEvent(t *testing.T) {
	hooks := map[string]interface{}{}
	removed := cleanEventEntries(hooks, "SessionStart")
	if removed {
		t.Fatalf("cleanEventEntries on missing event must report no removal")
	}
}

func TestCleanEventEntries_NoOpOnAllUserGroups(t *testing.T) {
	userGroup := map[string]interface{}{
		"hooks": []interface{}{
			map[string]interface{}{
				"type":    "command",
				"command": "/path/to/user-script.sh",
			},
		},
	}
	hooks := map[string]interface{}{
		"SessionStart": []interface{}{userGroup},
	}

	removed := cleanEventEntries(hooks, "SessionStart")
	if removed {
		t.Fatalf("cleanEventEntries on event with no atl groups must report no removal")
	}
	if countNonAtlGroups(hooks, "SessionStart") != 1 {
		t.Fatalf("user group must be preserved untouched")
	}
}

func TestIsAtlGroup_TrueForAtlCommand(t *testing.T) {
	cases := []string{
		"atl update --silent-if-clean",
		"atl session-start --silent-if-clean",
		"atl learning-capture --silent-if-empty",
		"atl session-start",
		"atl update --silent-if-clean --throttle=30m",
	}
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			g := map[string]interface{}{
				"hooks": []interface{}{
					map[string]interface{}{
						"type":    "command",
						"command": cmd,
					},
				},
			}
			if !isAtlGroup(g) {
				t.Fatalf("%q should be recognized as an atl-owned group", cmd)
			}
		})
	}
}

func TestIsAtlGroup_FalseForUserCommand(t *testing.T) {
	cases := []string{
		"echo hello",
		"/path/to/user-script.sh",
		"npm test",
		"atld",      // looks like atl but isn't (no space)
		"atlas",     // also not atl
	}
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			g := map[string]interface{}{
				"hooks": []interface{}{
					map[string]interface{}{
						"type":    "command",
						"command": cmd,
					},
				},
			}
			if isAtlGroup(g) {
				t.Fatalf("%q should NOT be recognized as atl-owned", cmd)
			}
		})
	}
}

func TestIsAtlGroup_FalseForMalformed(t *testing.T) {
	// nil, non-map values, missing "hooks" key — all should return false
	// (defensive — settings.json could contain anything).
	cases := []interface{}{
		nil,
		"not a map",
		map[string]interface{}{}, // no "hooks" key
		map[string]interface{}{"hooks": "not an array"},
		map[string]interface{}{"hooks": []interface{}{}}, // empty
	}
	for i, g := range cases {
		t.Run("", func(t *testing.T) {
			if isAtlGroup(g) {
				t.Fatalf("malformed group %d should not be atl-owned: %v", i, g)
			}
		})
	}
}

func TestEnsureMap_CreatesMissing(t *testing.T) {
	parent := map[string]interface{}{}
	child := ensureMap(parent, "hooks")
	if child == nil {
		t.Fatalf("ensureMap should return a non-nil map")
	}
	child["x"] = 1
	if parent["hooks"].(map[string]interface{})["x"] != 1 {
		t.Fatalf("ensureMap result must be the same map reference stored in parent")
	}
}

func TestEnsureMap_PreservesExisting(t *testing.T) {
	existing := map[string]interface{}{"existing-key": "existing-value"}
	parent := map[string]interface{}{"hooks": existing}

	got := ensureMap(parent, "hooks")
	if got["existing-key"] != "existing-value" {
		t.Fatalf("ensureMap must preserve existing content, got %v", got)
	}
}

func TestEnsureMap_ReplacesNonMap(t *testing.T) {
	// If "hooks" is the wrong type (string instead of map), ensureMap
	// should replace it with a fresh empty map. Defensive against
	// hand-edited settings.json with the wrong shape.
	parent := map[string]interface{}{"hooks": "wrong type"}
	got := ensureMap(parent, "hooks")
	if got == nil {
		t.Fatalf("ensureMap must return a fresh map when existing is wrong type")
	}
	if _, ok := parent["hooks"].(map[string]interface{}); !ok {
		t.Fatalf("parent[hooks] should now be a map, got %T", parent["hooks"])
	}
}

func TestAsArray_Variants(t *testing.T) {
	cases := []struct {
		name   string
		in     interface{}
		wantLen int
	}{
		{"nil", nil, 0},
		{"empty array", []interface{}{}, 0},
		{"two-element array", []interface{}{1, 2}, 2},
		{"non-array", "string", 0},
		{"map", map[string]interface{}{}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := asArray(tc.in)
			if len(got) != tc.wantLen {
				t.Fatalf("asArray(%v) len = %d, want %d", tc.in, len(got), tc.wantLen)
			}
		})
	}
}
