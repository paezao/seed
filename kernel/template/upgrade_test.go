package template

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"seed/kernel/git"
)

func plant(t *testing.T) (string, *git.Repo) {
	t.Helper()
	if !Available() {
		t.Skip("binary built without a template (make build)")
	}
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "tasks")
	if err := Create(ctx, dir, "tasks"); err != nil {
		t.Fatal(err)
	}
	repo := git.Open(dir)
	// Pretend it was planted by an older kernel: different version, a file
	// that kernel had and the new one doesn't, and an old seed.yaml.
	write := func(rel, s string) {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o755)
		os.WriteFile(filepath.Join(dir, rel), []byte(s), 0o644)
	}
	write(VersionPath, "old\n")
	write("kernel/sandbox/docker.go", "package sandbox // removed in newer kernels\n")
	write("seed.yaml", "name: tasks\nsandbox:\n  driver: docker\n")
	if _, err := repo.Run(ctx, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Run(ctx, "commit", "-q", "--amend", "--no-edit"); err != nil {
		t.Fatal(err)
	}
	// It has evolved since: generation 2 grew its organism and knowledge.
	write("organism/backend/tasks.go", "package main // mine\n")
	write("knowledge/product.md", "# Product\n\nI am Tasklet.\n")
	if _, err := repo.CommitAll(ctx, "evolve(tasks): become Tasklet\n\nEvolution: evo_x\nGeneration: 1 -> 2"); err != nil {
		t.Fatal(err)
	}
	return dir, repo
}

func TestUpgradeReplacesKernelKeepsTheSeed(t *testing.T) {
	ctx := context.Background()
	dir, repo := plant(t)
	res, err := Upgrade(ctx, dir, false)
	if err != nil {
		t.Fatal(err)
	}
	latest, _ := Version()
	if res.From != "old" || res.To != latest || res.Generation != 3 {
		t.Fatalf("unexpected result: %+v", res)
	}
	if SeedVersion(dir) != latest {
		t.Fatal("kernel version not updated")
	}
	if _, err := os.Stat(filepath.Join(dir, "kernel/sandbox/docker.go")); !os.IsNotExist(err) {
		t.Fatal("files the new kernel no longer has must be removed")
	}
	cfg, _ := os.ReadFile(filepath.Join(dir, "seed.yaml"))
	if !strings.Contains(string(cfg), "name: tasks") || strings.Contains(string(cfg), "driver: docker") {
		t.Fatalf("seed.yaml should be the new kernel's, keeping my name:\n%s", cfg)
	}
	for rel, want := range map[string]string{"organism/backend/tasks.go": "mine", "knowledge/product.md": "Tasklet"} {
		if b, _ := os.ReadFile(filepath.Join(dir, rel)); !strings.Contains(string(b), want) {
			t.Fatalf("%s must be untouched", rel)
		}
	}
	log, _ := repo.Log(ctx, "HEAD", 1)
	tr := git.Trailers(log[0].Body)
	if tr["Generation"] != "2 -> 3" || tr["Kernel"] != latest {
		t.Fatalf("upgrade commit metadata: %v", tr)
	}
	if st, _ := repo.Status(ctx); st != "" {
		t.Fatalf("tree should be clean after upgrade: %s", st)
	}
	// Already up to date.
	if res, err := Upgrade(ctx, dir, false); err != nil || !res.UpToDate {
		t.Fatalf("second upgrade should be a no-op: %+v %v", res, err)
	}
}

func TestUpgradeRefusesToDiscardKernelChanges(t *testing.T) {
	ctx := context.Background()
	dir, repo := plant(t)
	os.WriteFile(filepath.Join(dir, "kernel", "agent", "mine.go"), []byte("package agent // approved kernel evolution\n"), 0o644)
	if _, err := repo.CommitAll(ctx, "evolve(kernel): improve my agent\n\nEvolution: evo_y\nGeneration: 2 -> 3"); err != nil {
		t.Fatal(err)
	}
	_, err := Upgrade(ctx, dir, false)
	var km *ErrKernelModified
	if !errors.As(err, &km) || !strings.Contains(strings.Join(km.Files, ","), "kernel/agent/mine.go") {
		t.Fatalf("expected ErrKernelModified naming the change, got %v", err)
	}
	if res, err := Upgrade(ctx, dir, true); err != nil || res.Generation != 4 {
		t.Fatalf("--force should upgrade: %+v %v", res, err)
	}
}

func TestUpgradeNeedsCleanTree(t *testing.T) {
	dir, _ := plant(t)
	os.WriteFile(filepath.Join(dir, "organism", "wip.go"), []byte("x"), 0o644)
	if _, err := Upgrade(context.Background(), dir, false); err == nil {
		t.Fatal("uncommitted changes must block the upgrade")
	}
}
