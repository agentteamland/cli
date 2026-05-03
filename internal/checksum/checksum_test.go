package checksum

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileSHA256_Stable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	h1, err := FileSHA256(path)
	if err != nil {
		t.Fatalf("FileSHA256: %v", err)
	}
	h2, err := FileSHA256(path)
	if err != nil {
		t.Fatalf("FileSHA256 second call: %v", err)
	}
	if h1 != h2 {
		t.Fatalf("hash should be stable across calls: %q vs %q", h1, h2)
	}
	// SHA-256 of "hello\n" is a known value.
	const want = "5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03"
	if h1 != want {
		t.Fatalf("hash mismatch: got %q, want %q", h1, want)
	}
}

func TestFileSHA256_DistinguishesContent(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	b := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(a, []byte("alpha"), 0o644); err != nil {
		t.Fatalf("setup a: %v", err)
	}
	if err := os.WriteFile(b, []byte("beta"), 0o644); err != nil {
		t.Fatalf("setup b: %v", err)
	}
	hA, _ := FileSHA256(a)
	hB, _ := FileSHA256(b)
	if hA == hB {
		t.Fatalf("hashes should differ for different content")
	}
}

func TestFileSHA256_MissingFile(t *testing.T) {
	_, err := FileSHA256("/nonexistent/path/to/nothing")
	if err == nil {
		t.Fatalf("missing file should return an error")
	}
}

func TestDirSHA256_DeterministicOrder(t *testing.T) {
	// Two directories with the same files in different filesystem-walk
	// orders must produce the same hash, because the implementation
	// sorts by relative path before hashing.
	dirA := t.TempDir()
	dirB := t.TempDir()

	files := map[string]string{
		"alpha.txt":         "first content",
		"beta/nested.txt":   "nested content",
		"zeta.md":           "last content",
		"beta/another.json": "another",
	}
	for rel, content := range files {
		for _, root := range []string{dirA, dirB} {
			full := filepath.Join(root, rel)
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
			}
			if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
				t.Fatalf("write %s: %v", full, err)
			}
		}
	}

	hA, err := DirSHA256(dirA)
	if err != nil {
		t.Fatalf("DirSHA256 A: %v", err)
	}
	hB, err := DirSHA256(dirB)
	if err != nil {
		t.Fatalf("DirSHA256 B: %v", err)
	}
	if hA != hB {
		t.Fatalf("identical trees should produce identical hashes:\n  A=%s\n  B=%s", hA, hB)
	}
}

func TestDirSHA256_DistinguishesContent(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	if err := os.WriteFile(filepath.Join(dirA, "f.txt"), []byte("alpha"), 0o644); err != nil {
		t.Fatalf("setup A: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dirB, "f.txt"), []byte("beta"), 0o644); err != nil {
		t.Fatalf("setup B: %v", err)
	}
	hA, _ := DirSHA256(dirA)
	hB, _ := DirSHA256(dirB)
	if hA == hB {
		t.Fatalf("hashes should differ when content differs")
	}
}

func TestDirSHA256_DistinguishesPath(t *testing.T) {
	// Same content at different paths must hash differently — the
	// path is mixed into the hash to prevent collision.
	dirA := t.TempDir()
	dirB := t.TempDir()
	if err := os.WriteFile(filepath.Join(dirA, "alpha.txt"), []byte("content"), 0o644); err != nil {
		t.Fatalf("setup A: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dirB, "beta.txt"), []byte("content"), 0o644); err != nil {
		t.Fatalf("setup B: %v", err)
	}
	hA, _ := DirSHA256(dirA)
	hB, _ := DirSHA256(dirB)
	if hA == hB {
		t.Fatalf("hashes should differ when same content lives at different paths")
	}
}

func TestDirSHA256_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	h, err := DirSHA256(dir)
	if err != nil {
		t.Fatalf("empty dir should be hashable: %v", err)
	}
	// Two empty dirs should produce the same hash.
	other := t.TempDir()
	h2, _ := DirSHA256(other)
	if h != h2 {
		t.Fatalf("two empty dirs must produce identical hash")
	}
}

func TestDirSHA256_IgnoresDirectories(t *testing.T) {
	// Adding an empty subdirectory must not change the hash —
	// only file contents + paths matter.
	dirA := t.TempDir()
	dirB := t.TempDir()
	if err := os.WriteFile(filepath.Join(dirA, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("setup A: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dirB, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("setup B: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dirB, "empty/nested"), 0o755); err != nil {
		t.Fatalf("setup empty: %v", err)
	}
	hA, _ := DirSHA256(dirA)
	hB, _ := DirSHA256(dirB)
	if hA != hB {
		t.Fatalf("empty subdirectories must not affect hash:\n  A=%s\n  B=%s", hA, hB)
	}
}

func TestDirSHA256_ContentChange(t *testing.T) {
	// A change to one byte in one file should change the overall hash.
	dir := t.TempDir()
	target := filepath.Join(dir, "subdir", "file.md")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(target, []byte("original"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	h1, _ := DirSHA256(dir)

	if err := os.WriteFile(target, []byte("modified"), 0o644); err != nil {
		t.Fatalf("modify: %v", err)
	}
	h2, _ := DirSHA256(dir)

	if h1 == h2 {
		t.Fatalf("hash must change when file content changes")
	}
}

func TestDirSHA256_MissingRoot(t *testing.T) {
	_, err := DirSHA256("/nonexistent/dir/that/should/not/exist")
	if err == nil {
		t.Fatalf("missing root should error")
	}
	// Error message should at least mention the path or contain "no such file".
	if !strings.Contains(err.Error(), "nonexistent") && !strings.Contains(err.Error(), "no such") {
		t.Fatalf("error should reference the missing path: %v", err)
	}
}
