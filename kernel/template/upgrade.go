package template

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"seed/kernel/fsx"
	"seed/kernel/git"
	"seed/kernel/knowledge"
)

// A Seed builds its kernel from its own source, so fixing the kernel of an
// existing Seed means replacing that source. Upgrade does it as a new
// generation: the Seed's kernel files are replaced with the ones in this
// binary's template; its organism, knowledge, data and skills are kept.

// VersionPath holds a kernel's version (stamped by `make template`).
const VersionPath = "kernel/VERSION"

// evolvable directories belong to the Seed and are never replaced.
var evolvable = []string{"organism/", "knowledge/", "skills/"}

// managedDirs are kernel directories: files there that the new kernel no
// longer has are removed.
var managedDirs = []string{"kernel/", "cmd/", "control/", "docs/", "e2e/", "deploy/"}

// Version returns the kernel version this binary's template carries.
func Version() (string, error) {
	data, err := Archive()
	if err != nil {
		return "", err
	}
	files, err := templateFiles(data)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(files[VersionPath].data)), nil
}

// SeedVersion returns the kernel version of the Seed in dir ("unknown" for
// Seeds planted before kernels were versioned).
func SeedVersion(dir string) string {
	b, err := fsx.ReadFile(dir, VersionPath) // never follows a symlink out of the Seed
	if len(b) > 64 {
		b = b[:64]
	}
	if err != nil || strings.TrimSpace(string(b)) == "" {
		return "unknown"
	}
	return strings.TrimSpace(string(b))
}

type tfile struct {
	data []byte
	mode os.FileMode
}

// Archive returns this binary's template (a .tar.gz of a pristine Seed).
func Archive() ([]byte, error) {
	data, err := assets.ReadFile(archive)
	if err != nil {
		return nil, errors.New("this seed binary was built without a template (build it with `make build`)")
	}
	return data, nil
}

// File returns one file of a template archive.
func File(archive []byte, name string) ([]byte, error) {
	files, err := templateFiles(archive)
	if err != nil {
		return nil, err
	}
	f, ok := files[name]
	if !ok {
		return nil, fmt.Errorf("%s not in template", name)
	}
	return f.data, nil
}

func templateFiles(data []byte) (map[string]tfile, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	files := map[string]tfile{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return files, nil
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		name := filepath.ToSlash(filepath.Clean(h.Name))
		if strings.HasPrefix(name, "..") || filepath.IsAbs(name) {
			return nil, fmt.Errorf("template entry %q escapes the destination", h.Name)
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			return nil, err
		}
		mode := os.FileMode(0o644)
		if h.Mode&0o111 != 0 {
			mode = 0o755
		}
		files[name] = tfile{data: b, mode: mode}
	}
}

func isEvolvable(p string) bool {
	for _, e := range evolvable {
		if strings.HasPrefix(p, e) {
			return true
		}
	}
	return false
}

// UpgradeResult describes an upgrade.
type UpgradeResult struct {
	From, To   string
	Changed    []string
	Removed    []string
	NewSkills  []string
	Generation int
	Commit     string
	UpToDate   bool
}

// ErrKernelModified is returned when the Seed changed its own kernel since
// its kernel was last set; upgrading would discard those changes.
type ErrKernelModified struct{ Files []string }

func (e *ErrKernelModified) Error() string {
	return fmt.Sprintf("this Seed changed its own kernel (%s); upgrading would replace those changes (use --force to upgrade anyway)", strings.Join(e.Files, ", "))
}

var genRe = regexp.MustCompile(`^(?:\d+\s*->\s*)?(\d+)$`)

// Upgrade replaces the kernel of the (stopped) Seed in dir with the one in
// archive (a template .tar.gz) and commits it as a new generation.
//
// It runs git in the Seed's folder and writes into it, so the `seed` CLI
// runs it inside a throwaway Seed container (never on the host): the
// folder's own git config and symlinks can then only affect the container.
// Writes never follow symlinks either way.
func Upgrade(ctx context.Context, dir string, force bool, archive []byte) (*UpgradeResult, error) {
	files, err := templateFiles(archive)
	if err != nil {
		return nil, err
	}
	repo := git.Open(dir)
	// The upgrade is authored by who the Seed is (its name, not "Seed").
	repo.AuthorName = knowledge.Name(dir)
	if status, err := repo.Status(ctx); err != nil {
		return nil, err
	} else if status != "" {
		return nil, fmt.Errorf("my working tree has uncommitted changes; commit or discard them first:\n%s", status)
	}
	res := &UpgradeResult{From: SeedVersion(dir), To: strings.TrimSpace(string(files[VersionPath].data))}
	if res.From == res.To && !force {
		res.UpToDate = true
		return res, nil
	}

	// Did the Seed change its own kernel since the kernel was last set (by
	// planting or the previous upgrade)? Those changes would be lost.
	commits, err := repo.Log(ctx, "HEAD", 10000)
	if err != nil {
		return nil, err
	}
	base, maxGen := "", 0
	for _, c := range commits {
		t := git.Trailers(c.Body)
		if m := genRe.FindStringSubmatch(t["Generation"]); m != nil {
			if n, _ := strconv.Atoi(m[1]); n > maxGen {
				maxGen = n
			}
		}
		if base == "" && (t["Kernel"] != "" || strings.HasPrefix(c.Subject, "seed: initial seed")) {
			base = c.Hash
		}
	}
	if base == "" && len(commits) > 0 {
		base = commits[len(commits)-1].Hash
	}
	if !force && base != "" {
		changed, err := repo.ChangedFiles(ctx, base, "HEAD")
		if err != nil {
			return nil, err
		}
		var kernel []string
		for _, f := range changed {
			if !isEvolvable(f) {
				kernel = append(kernel, f)
			}
		}
		if len(kernel) > 0 {
			return nil, &ErrKernelModified{Files: kernel}
		}
	}

	// Replace kernel files.
	for name, f := range files {
		if isEvolvable(name) || name == "seed.yaml" {
			continue
		}
		if old, err := fsx.ReadFileNoFollow(dir, name); err == nil && bytes.Equal(old, f.data) {
			continue
		}
		if err := fsx.WriteFileNoFollow(dir, name, f.data, f.mode); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		res.Changed = append(res.Changed, name)
	}
	// Remove kernel files the new kernel no longer has.
	tracked, err := repo.Run(ctx, "ls-files")
	if err != nil {
		return nil, err
	}
	for _, name := range strings.Split(tracked, "\n") {
		if name == "" {
			continue
		}
		managed := false
		for _, d := range managedDirs {
			if strings.HasPrefix(name, d) {
				managed = true
			}
		}
		if _, ok := files[name]; managed && !ok {
			if err := fsx.RemoveAllNoFollow(dir, name); err == nil {
				res.Removed = append(res.Removed, name)
			}
		}
	}
	// seed.yaml: the new kernel's configuration, keeping my name.
	if f, ok := files["seed.yaml"]; ok {
		cfg, _ := fsx.ReadFileNoFollow(dir, "seed.yaml")
		name := "seed"
		if m := regexp.MustCompile(`(?m)^name:\s*(\S+)`).FindSubmatch(cfg); m != nil {
			name = string(m[1])
		}
		updated := regexp.MustCompile(`(?m)^name:.*$`).ReplaceAll(f.data, []byte("name: "+name))
		if !bytes.Equal(cfg, updated) {
			if err := fsx.WriteFileNoFollow(dir, "seed.yaml", updated, 0o644); err != nil {
				return nil, err
			}
			res.Changed = append(res.Changed, "seed.yaml")
		}
	}
	// Skills: add the kernel's starter skills I don't have; never overwrite
	// skills I have written or improved myself.
	for name, f := range files {
		if !strings.HasPrefix(name, "skills/") {
			continue
		}
		parts := strings.SplitN(name, "/", 3)
		if len(parts) < 3 {
			continue
		}
		// Lstat: a symlink where a skill would go counts as "present" and is left alone.
		if _, err := os.Lstat(filepath.Join(dir, "skills", parts[1])); err == nil && !contains(res.NewSkills, parts[1]) {
			continue
		}
		if err := fsx.WriteFileNoFollow(dir, name, f.data, f.mode); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if !contains(res.NewSkills, parts[1]) {
			res.NewSkills = append(res.NewSkills, parts[1])
		}
	}
	sort.Strings(res.Changed)
	sort.Strings(res.Removed)
	sort.Strings(res.NewSkills)

	res.Generation = maxGen + 1
	msg := fmt.Sprintf("upgrade: kernel %s -> %s\n\nReplaces my kernel with a newer one: %d file(s) changed, %d removed.\nMy organism, knowledge, data and skills are unchanged.\n\nGeneration: %d -> %d\nKernel: %s\n",
		res.From, res.To, len(res.Changed), len(res.Removed), maxGen, res.Generation, res.To)
	if len(res.NewSkills) > 0 {
		msg = strings.Replace(msg, "\n\nGeneration:", fmt.Sprintf("\nNew starter skills: %s.\n\nGeneration:", strings.Join(res.NewSkills, ", ")), 1)
	}
	commit, err := repo.CommitAll(ctx, msg)
	if errors.Is(err, git.ErrNothingToCommit) {
		res.UpToDate = true // forced, but the kernel was already identical
		return res, nil
	}
	if err != nil {
		return nil, err
	}
	res.Commit = commit
	_ = repo.Tag(ctx, fmt.Sprintf("seed/gen-%d", res.Generation), commit)
	return res, nil
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
