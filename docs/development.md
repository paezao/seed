# Development

## Setup

```bash
make up       # builds bin/seed and the runtime image (a Seed's body)
make build    # control plane (npm) → template snapshot → bin/seed
make test     # kernel tests, run inside the runtime image (private PostgreSQL, real bubblewrap)
make lint     # go vet, gofmt, control-plane typecheck, organism vet
```

Run a Seed from a checkout:

```bash
bin/seed new /tmp/demo                     # creates, starts, opens the browser
cd /tmp/demo && ~/path/to/bin/seed status  # CLI talks to the running kernel
```

The repository itself is a pristine Seed (`seed.yaml` name `seed`). `seed new` copies it
through the template snapshot, which `make template` builds from tracked and untracked
non-ignored files.

## Secrets and model

A Seed stores **no credentials**. Pass them each time you start it:

```bash
seed run -e OPENROUTER_API_KEY              # value from your shell
seed run -e GITHUB_TOKEN=ghp_…               # explicit value (lands in shell history)
seed run --env-file ~/.seed.env              # KEY=VALUE lines, kept outside any Seed
```

The launcher passes the names on the container's command line and the values through the
container engine's environment. The kernel reads them once at boot into memory
(`kernel/runtime/secrets.go`) and removes them from its environment, so PostgreSQL, git and
sandboxes never inherit them. A deployed Seed gets the same variables from its platform.

The model is chosen in the control plane (Settings → Mind) or with `-e SEED_MODEL=…`; the
choice is not a secret and is kept in the Seed's memory. Without a key, the control plane
explains how to start with one and can borrow a key **for the current session only**.
v0.1 offers OpenRouter (one key, any tool-capable model).

Overrides: `SEED_DATABASE_URL` (an external PostgreSQL instead of the private one),
`SEED_SANDBOX_DRIVER`, `SEED_CONTAINER_ENGINE=podman`.

`seed run --native` runs the kernel directly on your machine instead of in its container (for
kernel development). It needs bubblewrap and PostgreSQL server binaries installed.

## Upgrading a Seed's kernel

A Seed builds its kernel from its own source, so a newer `seed` CLI does not change existing
Seeds by itself. `seed run` tells you when your CLI carries a newer kernel. To upgrade:

```bash
seed stop && seed upgrade && seed run -e OPENROUTER_API_KEY
```

`seed upgrade` replaces the Seed's kernel files (`kernel/`, `cmd/`, `control/`, `docs/`, root
files; `seed.yaml` keeps the Seed's name) with the CLI's, removes kernel files the new kernel
no longer has, and adds new starter skills. It never touches the organism, knowledge, data or
skills the Seed already has. It commits all of this as a **new generation** (with a `Kernel:`
trailer), so it shows in the Seed's history and can be rolled back.
- If the Seed has changed its own kernel (an approved kernel evolution) since its kernel was
  last set, the upgrade refuses and lists those files; `--force` replaces them.
- Each kernel's version (date and commit of the Seed repository) is stamped into
  `kernel/VERSION` by `make template`.

## Tests

| area | where |
|---|---|
| evolution state machine | `kernel/memory/store_test.go` |
| orchestrator: happy path, build repair, failing tests, migration failure, kernel boundary, approved kernel change, deploy failure → rollback, provider failure, rollback generations, interrupted-evolution recovery, cancel | `kernel/evolution/orchestrator_test.go` (scripted model + local sandbox + real git/PostgreSQL) |
| agent loop: terminal tools, nudges, malformed/unknown tool calls, permission denial, provider failure, turn limits, context compaction | `kernel/agent/agent_test.go` |
| providers: wire formats, API errors, malformed responses, retry/backoff | `kernel/models/models_test.go` |
| tools: path confinement (symlinks, `..`, `.git`), kernel-write classification, edits | `kernel/tools/tools_test.go` |
| sandbox boundaries (bubblewrap): read-only workspace except evolvable dirs, masked `.seed` and host paths, clean env, timeouts, background processes | `kernel/sandbox/sandbox_test.go` |
| private PostgreSQL: init, restart with data, password auth | `kernel/pg/pg_test.go` |
| git: worktrees, fast-forward refusal on divergence, trailers, roll-forward | `kernel/git/git_test.go` |
| migrations: ordering, transactional failure, checksum protection | `kernel/migrate/migrate_test.go` |
| canonical demo (Seed → todo → priorities) with a real model | `e2e/` (`make e2e` with `SEED_TEST_OPENROUTER_API_KEY` set, passed to the Seed with `-e` like an owner would; `SEED_E2E_MODEL` picks the model; costs a few dollars) |

## Repository layout

```
cmd/seed/           CLI (new, run, stop, status, evolve, generations, rollback) and the container launcher
Dockerfile          the runtime image: a Seed's body
kernel/             the kernel (see docs/architecture.md)
control/web/        control plane (React); dist/ is committed and embedded
organism/           the empty organism a new Seed starts with
knowledge/ skills/  initial self model and skills
docs/               documentation
e2e/                end-to-end test of the canonical demo
```

## Contributing

- Keep the kernel small and legible. The Seed has to understand its own implementation, and
  every new kernel concept costs context in every evolution.
- Prefer giving the Seed the ability to create something over building it in.
- Behavior changes to the agent usually belong in `kernel/agent/prompts/*.md` or in a skill.
- Run `make lint test` before sending changes.
