package commands

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
)

// NewLearningCapture builds the `atl learning-capture` command — scans the
// current session transcript for inline `<!-- learning ... -->` markers and
// reports what was found.
//
// It is driven by Claude Code hooks (SessionEnd, PreCompact) installed by
// `atl setup-hooks`. Those hooks deliver the transcript path via a JSON blob
// on stdin:
//
//	{"session_id": "...", "transcript_path": "/path/to/transcript.jsonl", ...}
//
// When markers are found, the command prints a short report to stdout. The
// harness injects that report into Claude's next-turn context; the
// `learning-capture` rule + `/save-learnings --from-markers` skill pick it up
// and perform the actual save work (wiki updates, memory append, doc drafts).
//
// When no markers are found and --silent-if-empty is passed, the command
// exits silently — zero cost for boring sessions.
func NewLearningCapture() *cobra.Command {
	var (
		silentIfEmpty  bool
		transcriptPath string
	)

	cmd := &cobra.Command{
		Use:   "learning-capture",
		Short: "Scan session transcript for <!-- learning --> markers (driven by hooks)",
		Long: `Scan the current Claude Code session transcript for inline
<!-- learning ... --> markers and report what was found.

Invocation:

  atl learning-capture                      # reads hook JSON from stdin
  atl learning-capture --silent-if-empty    # no output when 0 markers (hook default)
  atl learning-capture --transcript-path X  # explicit path (for manual testing)

Marker format (in any assistant message within the transcript):

  <!-- learning
  topic: auth-refresh
  kind: decision
  doc-impact: readme
  body: 7-day JWT refresh chosen because we want long sessions.
  -->

When markers are found, the command prints a short report that Claude picks up
on the next turn and processes via /save-learnings --from-markers. When no
markers are found with --silent-if-empty, the command exits silently.

This is the "capture half" of the learning-capture rule pair (see
~/.claude/repos/agentteamland/core/rules/learning-capture.md). The
"processing half" is the /save-learnings skill.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLearningCapture(silentIfEmpty, transcriptPath)
		},
	}

	cmd.Flags().BoolVar(&silentIfEmpty, "silent-if-empty", false, "Produce no output when no markers found (for hooks)")
	cmd.Flags().StringVar(&transcriptPath, "transcript-path", "", "Explicit transcript path (bypasses stdin)")
	return cmd
}

// Marker is a parsed <!-- learning ... --> block extracted from the transcript.
type Marker struct {
	Topic      string
	Kind       string
	DocImpact  string
	Body       string
}

// runLearningCapture is the command entry point. Resolves the transcript path
// (from the --transcript-path flag or from hook JSON on stdin), scans it, and
// prints a report.
func runLearningCapture(silentIfEmpty bool, explicitPath string) error {
	path := explicitPath
	if path == "" {
		// Try stdin (Claude Code hook provides JSON with transcript_path).
		hookInput, err := readHookInput()
		if err == nil && hookInput.TranscriptPath != "" {
			path = hookInput.TranscriptPath
		}
	}

	if path == "" {
		// No transcript available — nothing to do.
		if silentIfEmpty {
			return nil
		}
		fmt.Fprintln(os.Stderr, "learning-capture: no transcript path (pass --transcript-path or pipe hook JSON to stdin)")
		return nil
	}

	markers, err := scanMarkers(path)
	if err != nil {
		// Non-fatal — hooks should never block Claude. Report to stderr and exit 0.
		if !silentIfEmpty {
			fmt.Fprintf(os.Stderr, "learning-capture: failed to scan transcript: %v\n", err)
		}
		return nil
	}

	if len(markers) == 0 {
		if silentIfEmpty {
			return nil
		}
		fmt.Println("📝 learning-capture: no markers found in this session (0 cost)")
		return nil
	}

	fmt.Print(formatReport(markers))
	return nil
}

// --- hook input (stdin JSON from Claude Code) -------------------------------

type hookInput struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	CWD            string `json:"cwd"`
}

func readHookInput() (hookInput, error) {
	var in hookInput
	// Only read stdin if it has content (not attached to a TTY).
	stat, err := os.Stdin.Stat()
	if err != nil {
		return in, err
	}
	if (stat.Mode() & os.ModeCharDevice) != 0 {
		// Interactive stdin — no hook input.
		return in, fmt.Errorf("no stdin input")
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return in, err
	}
	if len(data) == 0 {
		return in, fmt.Errorf("empty stdin")
	}
	if err := json.Unmarshal(data, &in); err != nil {
		return in, fmt.Errorf("parse hook input: %w", err)
	}
	return in, nil
}

// --- transcript scanning ----------------------------------------------------

// markerBlockPattern matches a complete <!-- learning ... --> HTML comment,
// multi-line, non-greedy. We use re.FindAllStringSubmatch to pull them from
// whatever text we find in the transcript.
var markerBlockPattern = regexp.MustCompile(`(?s)<!--\s*learning\b(.*?)-->`)

// scanMarkers walks the JSONL transcript and returns every <!-- learning -->
// block found in any text field (assistant messages, tool results, or user
// messages — we don't filter by role because markers are legal anywhere).
func scanMarkers(path string) ([]Marker, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var markers []Marker
	scanner := bufio.NewScanner(f)
	// Allow large JSON lines (transcripts can have multi-KB messages).
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 8*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		// Each line is a JSON record. We grep through the raw JSON for speed;
		// the regex operates on the escaped form which is still valid text.
		matches := markerBlockPattern.FindAllSubmatch(line, -1)
		for _, m := range matches {
			if len(m) < 2 {
				continue
			}
			raw := string(m[1])
			// JSON-escaped newlines appear as \n in the raw bytes; normalize.
			raw = strings.ReplaceAll(raw, `\n`, "\n")
			raw = strings.ReplaceAll(raw, `\"`, `"`)
			markers = append(markers, parseMarker(raw))
		}
	}
	if err := scanner.Err(); err != nil {
		return markers, err
	}
	return markers, nil
}

// parseMarker extracts topic / kind / doc-impact / body from the inner text
// of a marker block. The body of a marker is in loose YAML:
//
//	topic: auth-refresh
//	kind: decision
//	doc-impact: readme
//	body: 7-day JWT refresh chosen...
//
// Unknown fields are ignored. Missing fields become empty strings.
func parseMarker(inner string) Marker {
	m := Marker{}
	lines := strings.Split(inner, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		colon := strings.Index(line, ":")
		if colon < 0 {
			continue
		}
		key := strings.TrimSpace(line[:colon])
		value := strings.TrimSpace(line[colon+1:])
		switch strings.ToLower(key) {
		case "topic":
			m.Topic = value
		case "kind":
			m.Kind = value
		case "doc-impact", "doc_impact", "docimpact":
			m.DocImpact = value
		case "body":
			m.Body = value
		}
	}
	return m
}

// --- report formatting ------------------------------------------------------

func formatReport(markers []Marker) string {
	var b strings.Builder
	fmt.Fprintf(&b, "📝 learning-capture: %d marker%s detected\n", len(markers), plural(len(markers)))

	docImpactCount := 0
	for i, m := range markers {
		impact := m.DocImpact
		if impact == "" {
			impact = "none"
		}
		if impact != "none" {
			docImpactCount++
		}
		kind := m.Kind
		if kind == "" {
			kind = "?"
		}
		topic := m.Topic
		if topic == "" {
			topic = "(untitled)"
		}
		fmt.Fprintf(&b, "  %d. [%s] %s (doc-impact: %s)\n", i+1, kind, topic, impact)
	}

	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "→ Run /save-learnings --from-markers to process these into wiki + memory.")
	if docImpactCount > 0 {
		fmt.Fprintf(&b, "  %d marker%s require doc drafts (README / doc site) — see docs-sync rule.\n", docImpactCount, plural(docImpactCount))
	}
	return b.String()
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
