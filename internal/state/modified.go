// Package state provides helpers that operate on .claude/.team-installs.json,
// the manifest of installed teams in a project. The package surfaces the
// "is this resource modified?" question that drives auto-refresh decisions
// in `atl update` (Q3 of install-mechanism-redesign) and confirm prompts in
// `atl remove` (Q7).
package state

import (
	"os"

	"github.com/agentteamland/cli/internal/checksum"
)

// ResourceKind tells IsResourceModified whether to use file or directory
// hashing. Agents and rules are single .md files; skills are directory trees.
type ResourceKind string

const (
	KindFile ResourceKind = "file"
	KindDir  ResourceKind = "dir"
)

// IsResourceModified returns true if the given path's current content does
// not match the expected (install-time) checksum. Used by `atl update` to
// decide between auto-refresh (unmodified — safe to overwrite with the
// fresh global-cache version) and skip (modified — preserve user's local
// mutations).
//
// Returns:
//   - (true, nil) if the path is missing — treat as modified, don't silently
//     restore (might mask intentional deletion)
//   - (true, err) if hashing failed — fall through to the modified branch so
//     we don't blindly overwrite content we couldn't verify
//   - (true, nil) if the actual hash differs from expected — local change
//     present, keep it
//   - (false, nil) only when actual hash matches expected — safe to refresh
func IsResourceModified(path, expectedChecksum string, kind ResourceKind) (bool, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return true, err
	}

	var actual string
	var err error
	switch kind {
	case KindDir:
		actual, err = checksum.DirSHA256(path)
	default:
		actual, err = checksum.FileSHA256(path)
	}
	if err != nil {
		return true, err
	}
	return actual != expectedChecksum, nil
}
