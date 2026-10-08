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

func TestRollbackKernelKeepsTheSeedsWork(t *testing.T) {
	if !Available() {
		t.Skip("binary built without a template")
	}
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "tasks")
	if err := Create(ctx, dir, "tasks"); err != nil {
		t.Fatal(err)
	}
	repo := git.Open(dir)
	good, _ := repo.Head(ctx)
	write := func(rel, s string) {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o755)
		os.WriteFile(filepath.Join(dir, rel), []byte(s), 0o644)
	}
	// After the good kernel: an evolution of the organism, then a kernel
	// update that breaks (changes a file, adds one, removes one).
	write("organism/backend/feature.go", "package main // kept\n")
	if _, err := repo.CommitAll(ctx, "evolve: feature\n\nGeneration: 1 -> 2"); err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(filepath.Join(dir, "kernel", "agent", "agent.go"))
	write("kernel/agent/agent.go", "package agent // broken\n")
	write("kernel/agent/new.go", "package agent\n")
	os.Remove(filepath.Join(dir, "kernel", "VERSION"))
	write("kernel/VERSION", "broken-1\n")
	if _, err := repo.CommitAll(ctx, "upgrade: kernel x -> broken-1\n\nGeneration: 2 -> 3\nKernel: broken-1"); err != nil {
		t.Fatal(err)
	}
	res, err := RollbackKernel(ctx, dir, good, "didn't build")
	if err != nil {
		t.Fatal(err)
	}
	if res.From != "broken-1" || res.Generation != 4 {
		t.Fatalf("%+v", res)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "kernel", "agent", "agent.go")); string(b) != string(original) {
		t.Fatal("kernel files come back from the good commit")
	}
	if _, err := os.Stat(filepath.Join(dir, "kernel", "agent", "new.go")); !os.IsNotExist(err) {
		t.Fatal("kernel files the good kernel didn't have are removed")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "organism", "backend", "feature.go")); !strings.Contains(string(b), "kept") {
		t.Fatal("the Seed's own work since the good kernel is kept")
	}
	log, _ := repo.Log(ctx, "HEAD", 1)
	if tr := git.Trailers(log[0].Body); tr["Generation"] != "3 -> 4" || tr["Kernel"] == "" {
		t.Fatalf("rollback is a generation: %v", tr)
	}
	if _, err := RollbackKernel(ctx, dir, good, "again"); !errors.Is(err, ErrNothingToRollBack) {
		t.Fatalf("nothing left to roll back: %v", err)
	}
}
