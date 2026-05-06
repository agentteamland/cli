package commands

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/agentteamland/cli/internal/learnings"
	"github.com/spf13/cobra"
)

// NewLearningCapture builds the `atl learning-capture` command — scans
// Claude Code transcripts for inline `<!-- learning ... -->` markers and
// reports what was found, in a form that Claude on the next turn can act on
// via `/save-learnings --from-markers --transcripts ...`.
//
// Two modes:
//
//  1. Single-transcript (legacy / explicit): read transcript path from hook
//     JSON on stdin OR --transcript-path. Used by tests and by the older
//     SessionEnd / PreCompact hook registrations (which DO NOT inject
//     output into Claude's context — see [claude-code-hook-output-events.md]).
//
//  2. --previous-transcripts (new in PR 2A.2): scan the cwd-resolved
//     project's Claude Code session directory for transcript files
//     modified since the last successful /save-learnings run (or, on
//     first use of this project, within the last 7 days). Produces a
//     report that lists transcript paths so /save-learnings can pull
//     markers from each.
//
// Marker format (anywhere in any transcript message):
//
//	<!-- learning
//	topic: auth-refresh
//	kind: decision
//	doc-impact: readme
//	body: 7-day JWT refresh chosen because we want long sessions.
//	-->
//
// In every mode, --silent-if-empty produces no output when no markers
// are found, which keeps boring sessions free at the SessionStart hook.
func NewLearningCapture() *cobra.Command {
	var (
		silentIfEmpty         bool
		transcriptPath        string
		previousTranscripts   bool
		commitFromTranscripts bool
		commitTranscriptList  string
	)

	cmd := &cobra.Command{
		Use:   "learning-capture",
		Short: "Scan Claude Code transcripts for <!-- learning --> markers",
		Long: `Scan Claude Code transcripts for inline <!-- learning ... --> markers
and report what was found, in a form Claude on the next turn can process
via /save-learnings --from-markers --transcripts ...

Invocation:

  atl learning-capture --previous-transcripts
                                            # scan all jsonl files for the
                                            # cwd's project that were
                                            # modified since the last
                                            # successful save-learnings run
                                            # (or last 7 days on first use)
  atl learning-capture --silent-if-empty    # combine with above for hooks
  atl learning-capture                      # legacy: read hook JSON from stdin
  atl learning-capture --transcript-path X  # legacy: explicit single transcript
  atl learning-capture --commit-from-transcripts --transcripts a.jsonl,b.jsonl,...
                                            # /save-learnings calls this after
                                            # successful processing — adds the
                                            # marker hashes to state so the next
                                            # SessionStart does not re-report them

Marker format (in any assistant message within a transcript):

  <!-- learning
  topic: auth-refresh
  kind: decision
  doc-impact: readme
  body: 7-day JWT refresh chosen because we want long sessions.
  -->

The --previous-transcripts mode is what 'atl session-start' calls on
every Claude Code session start. The output appears in Claude's
additionalContext, prompting /save-learnings --from-markers --transcripts
... auto-application.

State file (~/.atl/state/learning-capture-state.json; legacy ~/.claude/state/learning-capture-state.json read as fallback) records the
last successful save-learnings run per project AND the FIFO-capped set
of marker hashes that have already been processed. The CLI writes the
state ONLY via --commit-from-transcripts (called by /save-learnings on
success), preserving "state advances iff processing succeeded."`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if commitFromTranscripts {
				return runCommitFromTranscripts(commitTranscriptList)
			}
			if previousTranscripts {
				return runFromPreviousTranscripts(silentIfEmpty)
			}
			return runLearningCapture(silentIfEmpty, transcriptPath)
		},
	}

	cmd.Flags().BoolVar(&silentIfEmpty, "silent-if-empty", false, "Produce no output when no markers found (for hooks)")
	cmd.Flags().StringVar(&transcriptPath, "transcript-path", "", "Explicit transcript path (bypasses stdin) — single-file mode")
	cmd.Flags().BoolVar(&previousTranscripts, "previous-transcripts", false, "Scan cwd's Claude Code session transcripts since last save-learnings run (multi-file mode)")
	cmd.Flags().BoolVar(&commitFromTranscripts, "commit-from-transcripts", false, "Commit processed-marker hashes from --transcripts to state (called by /save-learnings on success)")
	cmd.Flags().StringVar(&commitTranscriptList, "transcripts", "", "Comma-separated transcript paths to commit (used with --commit-from-transcripts)")
	return cmd
}

// Marker is a parsed <!-- learning ... --> block extracted from the transcript.
type Marker struct {
	Topic     string
	Kind      string
	DocImpact string
	Body      string
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

// topicPattern enforces the spec from learning-capture.md ("topic:
// kebab-case, one concept"). Periods are tolerated as segment
// separators so version-tagged topics survive. Drops prose ellipses
// like `topic: ... doc-impact: docs ...` that slip through the regex.
var topicPattern = regexp.MustCompile(`^[a-z0-9]+([-.][a-z0-9]+)*$`)

// scanMarkers walks the JSONL transcript and returns every <!-- learning -->
// block found in assistant-role text content. Markers in tool inputs/outputs,
// user messages, and other shapes are documentation quotes / pasted prose,
// not real emissions, and are filtered out at extraction time. Markers
// missing a topic are dropped as partial captures.
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
		text := extractAssistantText(scanner.Bytes())
		if text == "" {
			continue
		}
		for _, sub := range markerBlockPattern.FindAllStringSubmatch(text, -1) {
			if len(sub) < 2 {
				continue
			}
			m := parseMarker(sub[1])
			if !topicPattern.MatchString(m.Topic) {
				continue
			}
			markers = append(markers, m)
		}
	}
	return markers, scanner.Err()
}

// transcriptEvent / extractAssistantText: see internal/learnings.transcripts.go
// for the rationale. Mirrored here rather than imported to keep this single-
// transcript scanner self-contained (the file already duplicates the marker
// regex and parseMarker for the same reason).
type transcriptEvent struct {
	Message struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

func extractAssistantText(line []byte) string {
	var ev transcriptEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return ""
	}
	if ev.Message.Role != "assistant" || len(ev.Message.Content) == 0 {
		return ""
	}
	var asString string
	if err := json.Unmarshal(ev.Message.Content, &asString); err == nil {
		return asString
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(ev.Message.Content, &parts); err != nil {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		if p.Type != "text" || p.Text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(p.Text)
	}
	return b.String()
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

// runFromPreviousTranscripts is the --previous-transcripts entry point.
// It locates the cwd's project root, resolves the Claude Code transcript
// directory, reads the state file to find LastProcessedAt for this project
// (or applies the 7-day first-run cap if no record exists), enumerates
// transcripts modified after that cutoff, and scans them for markers.
//
// Output (when markers are found) lists the transcript paths so the
// `/save-learnings --from-markers --transcripts ...` invocation has them.
//
// The project-root resolver is `learnings.ResolveProjectRoot` (which
// only requires `.claude/`, not `.team-installs.json`) rather than
// `updater.FindProjectRoot`. Marker-bearing transcripts can come from
// any Claude-Code-aware project, including the maintainer workspace
// itself which is not an atl-managed install target.
func runFromPreviousTranscripts(silentIfEmpty bool) error {
	root, err := learnings.ResolveProjectRoot()
	if err != nil || root == "" {
		return nil
	}

	state, _ := learnings.ReadState()
	slug := learnings.SlugForPath(root)
	since := state.LastProcessedAtFor(slug)

	transcripts, err := learnings.FindUnprocessedTranscripts(root, since)
	if err != nil {
		// Best-effort: don't block hooks on a state-file or filesystem hiccup.
		if !silentIfEmpty {
			fmt.Fprintf(os.Stderr, "learning-capture: transcript discovery failed: %v\n", err)
		}
		return nil
	}
	if len(transcripts) == 0 {
		if silentIfEmpty {
			return nil
		}
		fmt.Println("📝 learning-capture: no new transcripts since last save-learnings run (0 cost)")
		return nil
	}

	markers, scanErr := learnings.ScanMarkersInTranscripts(transcripts)
	if scanErr != nil {
		fmt.Fprintf(os.Stderr, "📝 learning-capture: scan error: %v (results may be partial)\n", scanErr)
	}

	// Filter out markers whose hash is already in state.processedMarkers.
	// This is the precise filter that handles the long-running-session
	// case: even if a transcript file's modtime advanced past the
	// lastProcessedAt cutoff (because the same session kept appending
	// after /save-learnings ran), markers with a known hash do not
	// re-report on the next SessionStart.
	processed := state.ProcessedSet(slug)
	if len(processed) > 0 {
		filtered := markers[:0]
		for _, m := range markers {
			if !processed[m.Hash()] {
				filtered = append(filtered, m)
			}
		}
		markers = filtered
	}

	if len(markers) == 0 {
		if silentIfEmpty {
			return nil
		}
		fmt.Printf("📝 learning-capture: scanned %d transcript%s, no unprocessed markers (all already in state)\n",
			len(transcripts), plural(len(transcripts)))
		return nil
	}

	fmt.Print(formatPreviousTranscriptsReport(markers, transcripts))
	return nil
}

// runCommitFromTranscripts is invoked by /save-learnings after it has
// successfully processed markers from the named transcripts. This
// command re-scans the same transcripts, computes the canonical
// Marker.Hash for each marker found, and adds those hashes to the
// project's processedMarkers list in state. Subsequent SessionStart
// runs of `atl learning-capture --previous-transcripts` exclude any
// marker whose hash is in that list — which fixes the long-running-
// session re-report bug.
//
// Idempotent: re-committing the same transcripts re-adds the same
// hashes, which the AddProcessed dedup-on-insert logic absorbs as
// no-ops.
//
// State writes are atomic (config.WriteJSONAtomic via WriteState).
// On any failure, stdout reports the error to stderr and exits 0 —
// /save-learnings already finished its real work; failing here only
// costs us a re-report on the next session, not lost user work.
func runCommitFromTranscripts(transcriptList string) error {
	if transcriptList == "" {
		fmt.Fprintln(os.Stderr, "learning-capture: --commit-from-transcripts requires --transcripts <comma-list>")
		return nil
	}
	transcripts := strings.Split(transcriptList, ",")
	for i, p := range transcripts {
		transcripts[i] = strings.TrimSpace(p)
	}

	root, err := learnings.ResolveProjectRoot()
	if err != nil || root == "" {
		fmt.Fprintln(os.Stderr, "learning-capture: cannot resolve project root for state-file write")
		return nil
	}
	slug := learnings.SlugForPath(root)

	markers, scanErr := learnings.ScanMarkersInTranscripts(transcripts)
	if scanErr != nil {
		fmt.Fprintf(os.Stderr, "learning-capture: commit scan error: %v (results may be partial)\n", scanErr)
	}

	hashes := make([]string, 0, len(markers))
	seen := make(map[string]bool, len(markers))
	for _, m := range markers {
		h := m.Hash()
		if !seen[h] {
			hashes = append(hashes, h)
			seen[h] = true
		}
	}

	// Stamp lastProcessedAt to the most-recent transcript modtime.
	// This advances the coarse modtime filter as well, so future
	// scans skip files entirely when nothing has changed.
	stamp := mostRecentModTime(transcripts)

	state, _ := learnings.ReadState()
	state.AddProcessed(slug, hashes, stamp)
	if err := learnings.WriteState(state); err != nil {
		fmt.Fprintf(os.Stderr, "learning-capture: state write failed: %v\n", err)
		return nil
	}

	fmt.Printf("📝 learning-capture: committed %d marker hash%s for %s (state.processedMarkers now has %d entr%s)\n",
		len(hashes), pluralEs(len(hashes)), slug,
		len(state.Projects[slug].ProcessedMarkers), pluralEsY(len(state.Projects[slug].ProcessedMarkers)))
	return nil
}

func mostRecentModTime(paths []string) time.Time {
	var t time.Time
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		if info.ModTime().After(t) {
			t = info.ModTime()
		}
	}
	if t.IsZero() {
		return time.Now().UTC()
	}
	return t.UTC()
}

func pluralEs(n int) string {
	if n == 1 {
		return ""
	}
	return "es"
}

func pluralEsY(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

// formatPreviousTranscriptsReport renders the multi-transcript scan
// result as a compact summary suitable for SessionStart hook injection
// (Claude Code's additionalContext is capped at ~10KB).
//
// Output format:
//
//	🧠 learning-capture: <N> unprocessed markers across <M> transcripts
//	   <breakdown by kind>
//	   <doc-impact summary if any>
//
//	→ Run: /save-learnings --from-markers --transcripts <path1>,<path2>,...
//
// Detailed marker listing is intentionally omitted — /save-learnings re-
// scans the transcripts using its own (richer) classifier. Hook output
// stays compact regardless of marker count.
func formatPreviousTranscriptsReport(markers []learnings.Marker, transcripts []string) string {
	var b strings.Builder
	count := len(markers)

	fmt.Fprintf(&b, "🧠 learning-capture: %d unprocessed marker%s across %d transcript%s\n",
		count, plural(count), len(transcripts), plural(len(transcripts)))

	// Aggregate by kind (decision / pattern / discovery / bug-fix / ...)
	// and by doc-impact (readme / docs / both / breaking / none).
	byKind := map[string]int{}
	docImpact := 0
	for _, m := range markers {
		kind := m.Kind
		if kind == "" {
			kind = "?"
		}
		byKind[kind]++
		if m.DocImpact != "" && m.DocImpact != "none" {
			docImpact++
		}
	}
	if len(byKind) > 0 {
		var parts []string
		for _, kind := range sortedKeys(byKind) {
			parts = append(parts, fmt.Sprintf("%d %s", byKind[kind], kind))
		}
		fmt.Fprintf(&b, "  by kind: %s\n", strings.Join(parts, ", "))
	}
	if docImpact > 0 {
		fmt.Fprintf(&b, "  %d marker%s require doc drafts (README / doc site) — see docs-sync rule\n",
			docImpact, plural(docImpact))
	}

	// Transcript path list — Claude needs these to invoke save-learnings.
	pathList := strings.Join(quotePaths(transcripts), ",")
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "→ Run: /save-learnings --from-markers --transcripts %s\n", pathList)
	return b.String()
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// quotePaths escapes any spaces in transcript paths so the invocation
// hint stays a copy-pasteable single line. Most transcript paths live
// under ~/.claude/projects/<slug>/ which is space-free, so this is
// usually a no-op — but defensive against unusual setups.
func quotePaths(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		if strings.ContainsAny(p, " \t") {
			out[i] = `"` + filepath.ToSlash(p) + `"`
		} else {
			out[i] = filepath.ToSlash(p)
		}
	}
	return out
}
