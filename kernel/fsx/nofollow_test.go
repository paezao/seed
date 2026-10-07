//go:build linux

package fsx

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestNoFollowOperations(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "kernel"), 0o755)
	os.WriteFile(filepath.Join(root, "kernel", "core.go"), []byte("package kernel"), 0o644)
	os.MkdirAll(filepath.Join(root, "organism"), 0o755)
	os.Symlink("../kernel", filepath.Join(root, "organism", "link"))
	outside := t.TempDir()
	os.Symlink(outside, filepath.Join(root, "organism", "out"))

	if err := WriteFileNoFollow(root, "organism/a/b/c.txt", []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, err := ReadFileNoFollow(root, "organism/a/b/c.txt"); err != nil || string(b) != "ok" {
		t.Fatalf("read back: %q %v", b, err)
	}
	for _, p := range []string{"organism/link/core.go", "organism/link/new.go", "organism/out/x", "organism/link/sub/x"} {
		if err := WriteFileNoFollow(root, p, []byte("pwned"), 0o644); !errors.Is(err, ErrSymlink) {
			t.Errorf("write %s: expected ErrSymlink, got %v", p, err)
		}
	}
	if _, err := ReadFileNoFollow(root, "organism/link/core.go"); !errors.Is(err, ErrSymlink) {
		t.Errorf("read via link: expected ErrSymlink, got %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "kernel", "core.go")); string(b) != "package kernel" {
		t.Fatal("kernel modified")
	}
	// Removing the link removes only the link; removing a tree never follows links inside it.
	os.Symlink("../../kernel", filepath.Join(root, "organism", "a", "inner"))
	if err := RemoveAllNoFollow(root, "organism/a"); err != nil {
		t.Fatal(err)
	}
	if err := RemoveAllNoFollow(root, "organism/link"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "kernel", "core.go")); err != nil {
		t.Fatal("link target was deleted")
	}
	if err := RemoveAllNoFollow(root, "organism/link/core.go"); err == nil {
		t.Fatal("removing through a (now missing) link should fail")
	}
}
