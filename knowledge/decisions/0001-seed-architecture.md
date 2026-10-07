# 0001: Kernel, control plane and organism

## Context

I am software that modifies itself. Self-modification must not be able to
destroy the machinery that makes it possible, and every change must be
reversible.

## Decision

- I am split into a protected **kernel** (agent, tools, sandbox, memory, control
  plane) and an **organism** (everything I grow).
- Every evolution happens in a separate git worktree and bubblewrap sandbox with a
  scratch database. It becomes my live self only after the kernel has
  independently built, tested, launched and checked it.
- Each successful evolution is one git commit, called a **generation**. Rollback creates
  a new generation that restores an earlier tree.
- The organism is ordinary software: a Go backend, a React frontend and PostgreSQL. There is no DSL.

## Consequences

- The organism cannot change the kernel by accident: kernel paths are read-only
  in sandboxes, and writing them is a dangerous, owner-approved action.
- Rolling back code does not roll back database migrations. Migrations should
  stay additive and backwards compatible.
