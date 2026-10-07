// Package git wraps the git CLI. Git history is the Seed's evolutionary record:
// every generation is a commit, every evolution a branch and worktree.
package git

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type Repo struct {
	Dir string
	// Identity used for commits made by the Seed.
	AuthorName, AuthorEmail string
}

func Open(dir string) *Repo {
	return &Repo{Dir: dir, AuthorName: "Seed", AuthorEmail: "seed@localhost"}
}

// Run executes git with args in the repo directory and returns trimmed stdout.
func (r *Repo) Run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = r.Dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME="+r.AuthorName, "GIT_AUTHOR_EMAIL="+r.AuthorEmail,
		"GIT_COMMITTER_NAME="+r.AuthorName, "GIT_COMMITTER_EMAIL="+r.AuthorEmail,
		"GIT_TERMINAL_PROMPT=0", "LC_ALL=C",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return strings.TrimSpace(stdout.String()), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimRight(stdout.String(), "\n"), nil
}

func (r *Repo) Init(ctx context.Context) error {
	_, err := r.Run(ctx, "init", "-q", "-b", "main")
	return err
}

func (r *Repo) Head(ctx context.Context) (string, error) {
	return r.Run(ctx, "rev-parse", "HEAD")
}

func (r *Repo) Branch(ctx context.Context) (string, error) {
	return r.Run(ctx, "rev-parse", "--abbrev-ref", "HEAD")
}

// Status returns porcelain status (empty when clean, ignoring ignored files).
func (r *Repo) Status(ctx context.Context) (string, error) {
	return r.Run(ctx, "status", "--porcelain")
}

func (r *Repo) IsClean(ctx context.Context) (bool, error) {
	s, err := r.Status(ctx)
	return s == "", err
}

// AddWorktree creates a new branch at base and checks it out at path.
func (r *Repo) AddWorktree(ctx context.Context, path, branch, base string) error {
	_, err := r.Run(ctx, "worktree", "add", "-q", "-b", branch, path, base)
	return err
}

// RemoveWorktree removes a worktree (forcefully: build outputs are untracked).
func (r *Repo) RemoveWorktree(ctx context.Context, path string) error {
	_, err := r.Run(ctx, "worktree", "remove", "--force", path)
	if err != nil {
		// The directory may already be gone; prune stale metadata.
		_, _ = r.Run(ctx, "worktree", "prune")
	}
	return err
}

func (r *Repo) DeleteBranch(ctx context.Context, branch string) error {
	_, err := r.Run(ctx, "branch", "-D", branch)
	return err
}

// CommitAll stages everything and commits. Returns the new commit hash, or
// ErrNothingToCommit if the tree is unchanged.
func (r *Repo) CommitAll(ctx context.Context, message string) (string, error) {
	if _, err := r.Run(ctx, "add", "-A"); err != nil {
		return "", err
	}
	if out, _ := r.Run(ctx, "diff", "--cached", "--name-only"); out == "" {
		return "", ErrNothingToCommit
	}
	if _, err := r.Run(ctx, "commit", "-q", "--no-verify", "-m", message); err != nil {
		return "", err
	}
	return r.Head(ctx)
}

var ErrNothingToCommit = fmt.Errorf("nothing to commit")

// MergeFastForward advances the current branch to rev, refusing anything but a fast-forward.
func (r *Repo) MergeFastForward(ctx context.Context, rev string) error {
	_, err := r.Run(ctx, "merge", "--ff-only", "-q", rev)
	return err
}

func (r *Repo) ResetHard(ctx context.Context, rev string) error {
	_, err := r.Run(ctx, "reset", "--hard", "-q", rev)
	return err
}

func (r *Repo) Tag(ctx context.Context, name, rev string) error {
	_, err := r.Run(ctx, "tag", "-f", name, rev)
	return err
}

// ChangedFiles lists paths changed between two revisions (base..head).
func (r *Repo) ChangedFiles(ctx context.Context, base, head string) ([]string, error) {
	out, err := r.Run(ctx, "diff", "--name-only", base, head)
	if err != nil || out == "" {
		return nil, err
	}
	return strings.Split(out, "\n"), nil
}

// WorkingChanges lists paths changed in the working tree relative to HEAD,
// including untracked files.
func (r *Repo) WorkingChanges(ctx context.Context) ([]string, error) {
	out, err := r.Run(ctx, "status", "--porcelain", "-uall")
	if err != nil || out == "" {
		return nil, err
	}
	var files []string
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 4 {
			continue
		}
		p := line[3:]
		if i := strings.Index(p, " -> "); i >= 0 {
			files = append(files, p[:i])
			p = p[i+4:]
		}
		files = append(files, strings.Trim(p, `"`))
	}
	return files, nil
}

func (r *Repo) Diff(ctx context.Context, args ...string) (string, error) {
	return r.Run(ctx, append([]string{"diff"}, args...)...)
}

// DiffWorking returns the diff of the working tree (including untracked files) against HEAD.
func (r *Repo) DiffWorking(ctx context.Context, stat bool) (string, error) {
	// Intent-to-add makes untracked files show up in diff without staging content.
	_, _ = r.Run(ctx, "add", "-A", "-N")
	if stat {
		return r.Run(ctx, "diff", "--stat", "HEAD")
	}
	return r.Run(ctx, "diff", "HEAD")
}

type Commit struct {
	Hash    string `json:"hash"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
	Date    string `json:"date"`
}

// Log returns up to n commits reachable from rev (newest first).
func (r *Repo) Log(ctx context.Context, rev string, n int) ([]Commit, error) {
	out, err := r.Run(ctx, "log", fmt.Sprintf("-n%d", n), "--format=%H%x1f%s%x1f%b%x1f%cI%x1e", rev)
	if err != nil {
		return nil, err
	}
	var commits []Commit
	for _, rec := range strings.Split(out, "\x1e") {
		rec = strings.TrimSpace(rec)
		if rec == "" {
			continue
		}
		f := strings.Split(rec, "\x1f")
		if len(f) < 4 {
			continue
		}
		commits = append(commits, Commit{Hash: f[0], Subject: f[1], Body: strings.TrimSpace(f[2]), Date: f[3]})
	}
	return commits, nil
}

// Trailers parses "Key: value" trailer lines from a commit body.
func Trailers(body string) map[string]string {
	t := map[string]string{}
	for _, line := range strings.Split(body, "\n") {
		k, v, ok := strings.Cut(line, ": ")
		if ok && k != "" && !strings.Contains(k, " ") {
			t[k] = strings.TrimSpace(v)
		}
	}
	return t
}

// RestoreTree makes the working tree and index match rev's tree (used for
// roll-forward rollbacks that preserve history).
func (r *Repo) RestoreTree(ctx context.Context, rev string) error {
	if _, err := r.Run(ctx, "read-tree", "-u", "--reset", rev); err != nil {
		return err
	}
	return nil
}

// WithDir returns a Repo for another directory (e.g. a worktree) with the same identity.
func (r *Repo) WithDir(dir string) *Repo {
	c := *r
	c.Dir = dir
	return &c
}
