//go:build !linux

package fsx

import (
	"errors"
	"os"
	"path/filepath"
)

// Non-Linux fallback: os.Root confines operations to root but follows
// symlinks inside it, so callers must also check for symlinks first (racy).
// Seed targets Linux, where nofollow_linux.go is race-free.

var ErrSymlink = errors.New("path traverses a symlink")

func WriteFileNoFollow(root, rel string, data []byte, perm os.FileMode) error {
	r, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer r.Close()
	if err := r.MkdirAll(filepath.Dir(rel), 0o755); err != nil {
		return err
	}
	return r.WriteFile(rel, data, perm)
}

func ReadFileNoFollow(root, rel string) ([]byte, error) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return r.ReadFile(rel)
}

func RemoveAllNoFollow(root, rel string) error {
	r, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer r.Close()
	return r.RemoveAll(rel)
}
