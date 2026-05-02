package learnings

import (
	"os"
	"path/filepath"
	"testing"
)

// Each fixture line below is one Claude Code transcript JSONL record.
// The cases together cover the four shapes the scanner used to over-
// count: a real assistant-emitted marker, a Read tool result quoting a
// marker template, an inline assistant prose mention, and a Bash tool
// input parameter quoting the format spec.
//
// Expectation after the fix: only the first record yields a marker.
var fixture = []string{
	// 1. Real marker — assistant text content, full block.
	`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Decided on JWT.\n\n<!-- learning\ntopic: auth-refresh\nkind: decision\ndoc-impact: readme\nbody: 7-day refresh chosen.\n-->"}]}}`,

	// 2. Read tool result — marker quoted from a rule file. Lives in a
	// user-role message under tool_result content; should NOT count.
	`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"x","content":"# learning-capture rule\n\n<!-- learning\ntopic: example-topic\nkind: pattern\ndoc-impact: docs\nbody: This is the rule's example.\n-->"}]}}`,

	// 3. Bash tool input — marker template embedded in a command param.
	// Tool inputs land in assistant content as type=tool_use, which has
	// no `text` field, so it's filtered out.
	`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"echo '<!-- learning\\ntopic: foo\\nkind: pattern\\nbody: bar\\n-->'"}}]}}`,

	// 4. Assistant inline prose — describes the format without emitting
	// a complete block. The regex won't match (no closing -->), so this
	// is filtered by the regex itself; included to confirm the scanner
	// doesn't trip on stray substring mentions.
	`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Drop a <!-- learning marker when something interesting happens."}]}}`,

	// 5. Partial marker — assistant text contains the block, but the
	// inner body has no topic field. Filtered by IsValid.
	`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"<!-- learning\nkind: pattern\nbody: missing topic.\n-->"}]}}`,

	// 5b. Inline prose with ellipsis-style placeholders. Survives the
	// role and regex filters but the topic value is non-kebab-case
	// (spaces, dots), so IsValid drops it.
	`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Drop a <!-- learning\ntopic: ... doc-impact: docs ...\n--> block when something interesting happens."}]}}`,

	// 6. String-shaped content (legacy assistant message shape) with a
	// real marker — should count.
	`{"type":"assistant","message":{"role":"assistant","content":"<!-- learning\ntopic: legacy-shape\nkind: discovery\nbody: content was a JSON string, not an array.\n-->"}}`,

	// 7. Summary event — different top-level type, no message.role.
	// Should be skipped by the role check.
	`{"type":"summary","summary":"earlier session","leafUuid":"abc"}`,
}

func TestScanFile_FiltersToAssistantTextOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fixture.jsonl")

	var content string
	for _, line := range fixture {
		content += line + "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got, err := scanFile(path)
	if err != nil {
		t.Fatalf("scanFile: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("want 2 markers (auth-refresh, legacy-shape), got %d:\n%+v", len(got), got)
	}
	if got[0].Topic != "auth-refresh" {
		t.Errorf("marker[0].Topic: want auth-refresh, got %q", got[0].Topic)
	}
	if got[0].Kind != "decision" {
		t.Errorf("marker[0].Kind: want decision, got %q", got[0].Kind)
	}
	if got[0].DocImpact != "readme" {
		t.Errorf("marker[0].DocImpact: want readme, got %q", got[0].DocImpact)
	}
	if got[1].Topic != "legacy-shape" {
		t.Errorf("marker[1].Topic: want legacy-shape, got %q", got[1].Topic)
	}
}

func TestExtractAssistantText_Shapes(t *testing.T) {
	cases := []struct {
		name string
		line string
		want string
	}{
		{
			name: "assistant array text",
			line: `{"message":{"role":"assistant","content":[{"type":"text","text":"hello"}]}}`,
			want: "hello",
		},
		{
			name: "assistant array multi-text-parts",
			line: `{"message":{"role":"assistant","content":[{"type":"text","text":"first"},{"type":"text","text":"second"}]}}`,
			want: "first\nsecond",
		},
		{
			name: "assistant string content",
			line: `{"message":{"role":"assistant","content":"hello"}}`,
			want: "hello",
		},
		{
			name: "assistant tool_use only",
			line: `{"message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{}}]}}`,
			want: "",
		},
		{
			name: "user role tool_result",
			line: `{"message":{"role":"user","content":[{"type":"tool_result","content":"output"}]}}`,
			want: "",
		},
		{
			name: "user role text",
			line: `{"message":{"role":"user","content":"hi"}}`,
			want: "",
		},
		{
			name: "summary event no message",
			line: `{"type":"summary","summary":"earlier"}`,
			want: "",
		},
		{
			name: "malformed json",
			line: `{not-json`,
			want: "",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := extractAssistantText([]byte(c.line))
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestMarkerIsValid(t *testing.T) {
	cases := []struct {
		name string
		m    Marker
		want bool
	}{
		{"single letter", Marker{Topic: "x"}, true},
		{"kebab-case", Marker{Topic: "auth-refresh"}, true},
		{"with digits", Marker{Topic: "v1-cold-build"}, true},
		{"version-dotted", Marker{Topic: "software-project-team-v1.1.4-cold-build-fixes"}, true},
		{"topic + kind", Marker{Topic: "x", Kind: "pattern"}, true},
		{"empty", Marker{}, false},
		{"kind without topic", Marker{Kind: "pattern"}, false},
		{"uppercase rejected", Marker{Topic: "Auth-Refresh"}, false},
		{"underscore rejected", Marker{Topic: "auth_refresh"}, false},
		{"space rejected", Marker{Topic: "auth refresh"}, false},
		{"ellipsis prose rejected", Marker{Topic: "... doc-impact: docs ..."}, false},
		{"trailing hyphen rejected", Marker{Topic: "auth-"}, false},
		{"double separator rejected", Marker{Topic: "auth--refresh"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.m.IsValid() != c.want {
				t.Errorf("got %v, want %v", c.m.IsValid(), c.want)
			}
		})
	}
}
