package template

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"seed/kernel/fsx"
	"seed/kernel/git"
	"seed/kernel/knowledge"
)

// ErrNothingToRollBack: my kernel files are the same as the last good ones,
// so whatever stopped me is not a kernel change.
var ErrNothingToRollBack = errors.New("my kernel is the last one that ran well")

// RollbackResult describes a kernel rollback.
type RollbackResult struct {
	From, To   string // kernel versions
	Generation int
	Commit     string
	Files      []string
}

// RollbackKernel restores the kernel files of the last good commit (one
// whose kernel ran healthily), keeping everything the Seed made since
// (organism, knowledge, skills), as a new generation. The boot script runs
// it, with the last good kernel binary, when a new kernel fails to build or
// crashes right after starting.
func RollbackKernel(ctx context.Context, dir, good, reason string) (*RollbackResult, error) {
	repo := git.Open(dir)
	repo.AuthorName = knowledge.Name(dir)
	if _, err := repo.Run(ctx, "cat-file", "-e", good+"^{commit}"); err != nil {
		return nil, fmt.Errorf("last good commit %s: %w", good, err)
	}
	// Uncommitted work in progress belongs to nobody now; the tree must be
	// clean to commit the rollback alone.
	if status, err := repo.Status(ctx); err != nil {
		return nil, err
	} else if status != "" {
		if _, err := repo.Run(ctx, "stash", "push", "--include-untracked", "-m", "before kernel rollback"); err != nil {
			return nil, err
		}
	}
	changed, err := repo.ChangedFiles(ctx, good, "HEAD")
	if err != nil {
		return nil, err
	}
	var kernelFiles []string
	for _, f := range changed {
		if !isEvolvable(f) {
			kernelFiles = append(kernelFiles, f)
		}
	}
	if len(kernelFiles) == 0 {
		return nil, ErrNothingToRollBack
	}
	res := &RollbackResult{From: SeedVersion(dir), Files: kernelFiles}
	for _, f := range kernelFiles {
		if _, err := repo.Run(ctx, "cat-file", "-e", good+":"+f); err == nil {
			if _, err := repo.Run(ctx, "checkout", good, "--", f); err != nil {
				return nil, err
			}
		} else if err := fsx.RemoveAllNoFollow(dir, f); err != nil {
			return nil, err
		}
	}
	res.To = SeedVersion(dir)
	maxGen := 0
	if commits, err := repo.Log(ctx, "HEAD", 10000); err == nil {
		for _, c := range commits {
			if m := genRe.FindStringSubmatch(git.Trailers(c.Body)["Generation"]); m != nil {
				var n int
				fmt.Sscan(m[1], &n)
				if n > maxGen {
					maxGen = n
				}
			}
		}
	}
	res.Generation = maxGen + 1
	msg := fmt.Sprintf("rollback: kernel %s -> %s\n\nMy new kernel %s, so I went back to the last kernel that ran well.\nMy organism, knowledge, data and skills are unchanged.\n\nGeneration: %d -> %d\nKernel: %s\n",
		res.From, res.To, strings.TrimSuffix(reason, "."), maxGen, res.Generation, res.To)
	commit, err := repo.CommitAll(ctx, msg)
	if err != nil {
		return nil, err
	}
	res.Commit = commit
	_ = repo.Tag(ctx, fmt.Sprintf("seed/gen-%d", res.Generation), commit)
	return res, nil
}
