package learnings

import "regexp"

// markerBlockPattern matches a complete <!-- learning ... --> HTML
// comment, multi-line, non-greedy. Same regex as the single-transcript
// scanner in cmd/atl/commands/learning_capture.go — pattern is
// duplicated rather than imported to avoid a cyclic dependency
// (commands → internal/learnings → commands).
var markerBlockPattern = regexp.MustCompile(`(?s)<!--\s*learning\b(.*?)-->`)
