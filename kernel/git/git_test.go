package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func newRepo(t *testing.T) *Repo {
	t.Helper()
	dir := t.TempDir()
	r := Open(dir)
	ctx := context.Background()
	if err := r.Init(ctx); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "README.md", "hello\n")
	if _, err := r.CommitAll(ctx, "init\n\nGeneration: 1"); err != nil {
		t.Fatal(err)
	}
	return r
}

func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWorktreeLifecycle(t *testing.T) {
	ctx := context.Background()
	r := newRepo(t)
	base, _ := r.Head(ctx)
	wt := filepath.Join(t.TempDir(), "evo")
	if err := r.AddWorktree(ctx, wt, "seed/evo_1", base); err != nil {
		t.Fatal(err)
	}
	w := Open(wt)
	write(t, wt, "organism/app.go", "package main\n")
	files, err := w.WorkingChanges(ctx)
	if err != nil || len(files) != 1 || files[0] != "organism/app.go" {
		t.Fatalf("working changes: %v %v", files, err)
	}
	diff, _ := w.DiffWorking(ctx, false)
	if diff == "" {
		t.Fatal("expected diff including untracked file")
	}
	commit, err := w.CommitAll(ctx, "evolve: add app\n\nEvolution: evo_1\nGeneration: 1 -> 2")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.MergeFastForward(ctx, commit); err != nil {
		t.Fatal(err)
	}
	head, _ := r.Head(ctx)
	if head != commit {
		t.Fatalf("main not advanced: %s != %s", head, commit)
	}
	changed, _ := r.ChangedFiles(ctx, base, head)
	if len(changed) != 1 {
		t.Fatalf("changed files: %v", changed)
	}
	if err := r.RemoveWorktree(ctx, wt); err != nil {
		t.Fatal(err)
	}
	if err := r.DeleteBranch(ctx, "seed/evo_1"); err != nil {
		t.Fatal(err)
	}
	log, _ := r.Log(ctx, "HEAD", 10)
	if len(log) != 2 || Trailers(log[0].Body)["Evolution"] != "evo_1" {
		t.Fatalf("log/trailers: %+v", log)
	}
}

func TestNothingToCommit(t *testing.T) {
	r := newRepo(t)
	if _, err := r.CommitAll(context.Background(), "x"); !errors.Is(err, ErrNothingToCommit) {
		t.Fatalf("expected ErrNothingToCommit, got %v", err)
	}
}

func TestFastForwardRefusesDivergence(t *testing.T) {
	ctx := context.Background()
	r := newRepo(t)
	base, _ := r.Head(ctx)
	wt := filepath.Join(t.TempDir(), "evo")
	_ = r.AddWorktree(ctx, wt, "seed/evo_2", base)
	write(t, wt, "a.txt", "from evolution\n")
	evoCommit, _ := Open(wt).CommitAll(ctx, "evo")
	// main moves on independently -> conflict-prone divergence
	write(t, r.Dir, "a.txt", "from main\n")
	if _, err := r.CommitAll(ctx, "main change"); err != nil {
		t.Fatal(err)
	}
	if err := r.MergeFastForward(ctx, evoCommit); err == nil {
		t.Fatal("expected fast-forward to be refused for diverged history")
	}
}

func TestRestoreTreeRollForward(t *testing.T) {
	ctx := context.Background()
	r := newRepo(t)
	gen1, _ := r.Head(ctx)
	write(t, r.Dir, "feature.txt", "v2\n")
	_, _ = r.CommitAll(ctx, "gen2")
	if err := r.RestoreTree(ctx, gen1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(r.Dir, "feature.txt")); !os.IsNotExist(err) {
		t.Fatal("feature.txt should be removed by restore")
	}
	c, err := r.CommitAll(ctx, "rollback to gen1")
	if err != nil {
		t.Fatal(err)
	}
	if d, _ := r.Diff(ctx, gen1, c); d != "" {
		t.Fatalf("tree should equal gen1, diff: %s", d)
	}
}
