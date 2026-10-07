// Package fsx reads repository content without following symlinks out of it.
//
// Evolvable directories are written by sandboxed code, which can create
// symlinks. Anything the kernel reads from them on the host (knowledge,
// skills, migrations, the logo) must go through these helpers so a link like
// knowledge/x.md -> ~/.ssh/id_rsa cannot leak host files.
package fsx

import (
	"io/fs"
	"os"
	"path/filepath"
)

// ReadFile reads rel (slash-separated, relative to root) confined to root.
func ReadFile(root, rel string) ([]byte, error) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return fs.ReadFile(r.FS(), filepath.ToSlash(filepath.Clean(rel)))
}

// FS returns a filesystem rooted at dir that refuses to escape it. The
// returned close function releases it.
func FS(dir string) (fs.FS, func(), error) {
	r, err := os.OpenRoot(dir)
	if err != nil {
		return nil, func() {}, err
	}
	return r.FS(), func() { r.Close() }, nil
}

// ReadDir lists rel within root (confined).
func ReadDir(root, rel string) ([]fs.DirEntry, error) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return fs.ReadDir(r.FS(), filepath.ToSlash(filepath.Clean(rel)))
}
