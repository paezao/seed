// Package template creates new Seeds.
//
// A new Seed is a copy of a pristine Seed repository (kernel, control plane,
// empty organism, initial knowledge and skills), packed into the seed binary
// at build time by `make template`. The copy gets its own git history whose
// first commit is generation 1.
package template

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"seed/kernel/git"
)

//go:embed assets
var assets embed.FS

const archive = "assets/template.tar.gz"

// Available reports whether this binary carries a template.
func Available() bool {
	_, err := fs.Stat(assets, archive)
	return err == nil
}

var nameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,40}$`)

// Create makes a new Seed named name in dir and commits generation 1.
func Create(ctx context.Context, dir, name string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("name %q: use lowercase letters, digits and dashes, starting with a letter", name)
	}
	data, err := assets.ReadFile(archive)
	if err != nil {
		return errors.New("this seed binary was built without a template (build it with `make build`)")
	}
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		return fmt.Errorf("%s already exists and is not empty", dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := extract(data, dir); err != nil {
		return err
	}
	if err := personalize(dir, name); err != nil {
		return err
	}
	repo := git.Open(dir)
	if err := repo.Init(ctx); err != nil {
		return err
	}
	msg := "seed: initial seed\n\nA Seed with no purpose yet: kernel, control plane and an empty organism.\n\nGeneration: 1\n"
	commit, err := repo.CommitAll(ctx, msg)
	if err != nil {
		return err
	}
	return repo.Tag(ctx, "seed/gen-1", commit)
}

func extract(data []byte, dir string) error {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		clean := filepath.Clean(h.Name)
		if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
			return fmt.Errorf("template entry %q escapes the destination", h.Name)
		}
		dst := filepath.Join(dir, clean)
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(0o644)
			if h.Mode&0o111 != 0 {
				mode = 0o755
			}
			f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		}
	}
}

// personalize sets the Seed's technical name. Its display identity stays
// "Seed" until it becomes something.
func personalize(dir, name string) error {
	replace := func(rel string, re *regexp.Regexp, repl string) error {
		p := filepath.Join(dir, rel)
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(p, re.ReplaceAll(b, []byte(repl)), 0o644)
	}
	if err := replace("seed.yaml", regexp.MustCompile(`(?m)^name:.*$`), "name: "+name); err != nil {
		return err
	}
	return replace("knowledge/self.yaml", regexp.MustCompile(`(?m)^  slug:.*$`), "  slug: "+name)
}
