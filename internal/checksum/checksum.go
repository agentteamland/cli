// Package checksum provides content-hash helpers for atl resources (agents,
// rules, skills). Used to detect whether a project's installed copy has been
// modified since install — e.g. by self-updating-learning-loop, by hand
// edits, or by /save-learnings auto-grown children/learnings files.
//
// All hashes are SHA-256, hex-encoded. Directory hashes are computed
// deterministically over sorted relative paths, so two identical trees
// produce the same hash regardless of OS-specific filesystem walk order.
package checksum

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// FileSHA256 returns the hex-encoded SHA-256 hash of a single file's content.
// Symlinks are followed (Open dereferences); the hash is over the underlying
// file content.
func FileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// DirSHA256 returns a single hex-encoded SHA-256 hash that covers every
// regular file under root. Files are processed in deterministic order
// (sorted by relative path). Each file contributes its relative path plus
// its content into the hash, so two trees with identical file contents at
// different layouts produce different hashes.
//
// Used for skills, which install as directory trees rather than single
// files. Symlinks within the tree are followed; broken symlinks cause an
// error.
func DirSHA256(root string) (string, error) {
	var rels []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rels = append(rels, rel)
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(rels)

	h := sha256.New()
	for _, rel := range rels {
		// Mix the path into the hash before the content. Without this,
		// identical content at different paths would collide.
		h.Write([]byte(rel))
		h.Write([]byte{0}) // separator between path and content

		f, err := os.Open(filepath.Join(root, rel))
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(h, f); err != nil {
			f.Close()
			return "", err
		}
		f.Close()
		h.Write([]byte{0}) // separator between files
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
