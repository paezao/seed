//go:build linux

package fsx

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// The kernel writes into directories that sandboxed processes can change
// concurrently. Checking "no symlinks" and then writing would race (a
// directory swapped for a symlink in between). These helpers resolve every
// path with openat2(RESOLVE_BENEATH|RESOLVE_NO_SYMLINKS), so the kernel
// itself refuses symlinks and escapes at the moment of each operation.

const noFollow = unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS

// ErrSymlink is returned when a path component is a symlink.
var ErrSymlink = errors.New("path traverses a symlink")

func openat2(dirfd int, rel string, flags int, mode uint32) (int, error) {
	fd, err := unix.Openat2(dirfd, rel, &unix.OpenHow{Flags: uint64(flags | unix.O_CLOEXEC), Mode: uint64(mode), Resolve: noFollow})
	if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.EXDEV) {
		return -1, fmt.Errorf("%s: %w", rel, ErrSymlink)
	}
	return fd, err
}

func clean(rel string) (string, error) {
	rel = filepath.Clean(rel)
	if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") || filepath.IsAbs(rel) {
		return "", fmt.Errorf("invalid path %q", rel)
	}
	return rel, nil
}

func openRoot(root string) (int, error) {
	return unix.Open(root, unix.O_DIRECTORY|unix.O_RDONLY|unix.O_CLOEXEC, 0)
}

// WriteFileNoFollow creates parent directories and writes rel under root,
// refusing any symlink on the way.
func WriteFileNoFollow(root, rel string, data []byte, perm os.FileMode) error {
	rel, err := clean(rel)
	if err != nil {
		return err
	}
	rfd, err := openRoot(root)
	if err != nil {
		return err
	}
	defer unix.Close(rfd)
	if dir := filepath.Dir(rel); dir != "." {
		if err := mkdirAll(rfd, dir); err != nil {
			return err
		}
	}
	fd, err := openat2(rfd, rel, unix.O_WRONLY|unix.O_CREAT|unix.O_TRUNC, uint32(perm))
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), rel)
	defer f.Close()
	_, err = f.Write(data)
	return err
}

func mkdirAll(rfd int, dir string) error {
	parts := strings.Split(dir, "/")
	for i := range parts {
		prefix := strings.Join(parts[:i+1], "/")
		fd, err := openat2(rfd, prefix, unix.O_PATH|unix.O_DIRECTORY, 0)
		if err == nil {
			unix.Close(fd)
			continue
		}
		if !errors.Is(err, unix.ENOENT) {
			return err
		}
		parent := "."
		if i > 0 {
			parent = strings.Join(parts[:i], "/")
		}
		pfd, err := openat2(rfd, parent, unix.O_PATH|unix.O_DIRECTORY, 0)
		if err != nil {
			return err
		}
		err = unix.Mkdirat(pfd, parts[i], 0o755)
		unix.Close(pfd)
		if err != nil && !errors.Is(err, unix.EEXIST) {
			return err
		}
	}
	return nil
}

// ReadFileNoFollow reads rel under root, refusing any symlink on the way.
func ReadFileNoFollow(root, rel string) ([]byte, error) {
	rel, err := clean(rel)
	if err != nil {
		return nil, err
	}
	rfd, err := openRoot(root)
	if err != nil {
		return nil, err
	}
	defer unix.Close(rfd)
	fd, err := openat2(rfd, rel, unix.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), rel)
	defer f.Close()
	var buf []byte
	tmp := make([]byte, 64<<10)
	for {
		n, err := f.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			break
		}
	}
	return buf, nil
}

// RemoveAllNoFollow deletes rel under root. Its parent directories must not
// be symlinks; rel itself may be a symlink (the link is removed, never its
// target), and directory contents are removed without following links.
func RemoveAllNoFollow(root, rel string) error {
	rel, err := clean(rel)
	if err != nil {
		return err
	}
	rfd, err := openRoot(root)
	if err != nil {
		return err
	}
	defer unix.Close(rfd)
	parent, name := filepath.Dir(rel), filepath.Base(rel)
	pfd, err := openat2(rfd, parent, unix.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		return err
	}
	defer unix.Close(pfd)
	return removeAt(pfd, name)
}

func removeAt(dirfd int, name string) error {
	var st unix.Stat_t
	if err := unix.Fstatat(dirfd, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR {
		return unix.Unlinkat(dirfd, name, 0)
	}
	fd, err := unix.Openat(dirfd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	d := os.NewFile(uintptr(fd), name)
	names, err := d.Readdirnames(-1)
	if err != nil {
		d.Close()
		return err
	}
	for _, n := range names {
		if err := removeAt(fd, n); err != nil {
			d.Close()
			return err
		}
	}
	d.Close()
	return unix.Unlinkat(dirfd, name, unix.AT_REMOVEDIR)
}
