package fsx

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSymlinksCannotEscape(t *testing.T) {
	root := t.TempDir()
	secret := filepath.Join(t.TempDir(), "id_rsa")
	os.WriteFile(secret, []byte("PRIVATE"), 0o600)
	os.MkdirAll(filepath.Join(root, "knowledge"), 0o755)
	os.WriteFile(filepath.Join(root, "knowledge", "ok.md"), []byte("# ok"), 0o644)
	os.Symlink(secret, filepath.Join(root, "knowledge", "leak.md"))
	os.Symlink("ok.md", filepath.Join(root, "knowledge", "alias.md"))

	if b, err := ReadFile(root, "knowledge/leak.md"); err == nil {
		t.Fatalf("escaping symlink was followed: %q", b)
	}
	if b, err := ReadFile(root, "knowledge/alias.md"); err != nil || string(b) != "# ok" {
		t.Fatalf("in-root symlink should work: %q %v", b, err)
	}
	if _, err := ReadFile(root, "../outside"); err == nil {
		t.Fatal("parent traversal must fail")
	}
}
