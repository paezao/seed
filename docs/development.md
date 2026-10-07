# Development

## Setup

```bash
make up       # builds bin/seed, starts seed-postgres (127.0.0.1:55432) and the seed-net network
make build    # control plane (npm) → template snapshot → bin/seed
make test     # kernel tests (PostgreSQL required; Docker sandbox tests run when the image exists)
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

## Model configuration

A Seed's mind is chosen by its owner in the control plane. A fresh Seed always asks for it
first; nothing is picked up from the environment. v0.1 offers **OpenRouter**, where one key
gives any tool-capable model. The model list comes live from OpenRouter, filtered to models
that support tool calling.

- Keys are stored per provider in `~/.config/seed/credentials.json` (0600), shared by your Seeds.
- The per-Seed choice of provider and model is stored in that Seed's memory. It can be changed
  any time in Settings, without a restart.
- The kernel validates a choice with a tiny test call before saving it.
- More providers (Anthropic and OpenAI-compatible, both already implemented in `kernel/models`) are a
  matter of listing them in `models.Providers`.

Infrastructure overrides: `SEED_DATABASE_URL` (your own PostgreSQL), `SEED_ADDR`,
`SEED_SANDBOX_DRIVER`. The managed PostgreSQL's random superuser password lives in
`~/.config/seed/postgres.json`.

## Tests

| area | where |
|---|---|
| evolution state machine | `kernel/memory/store_test.go` |
| orchestrator: happy path, build repair, failing tests, migration failure, kernel boundary, approved kernel change, deploy failure → rollback, provider failure, rollback generations, interrupted-evolution recovery, cancel | `kernel/evolution/orchestrator_test.go` (scripted model + local sandbox + real git/PostgreSQL) |
| agent loop: terminal tools, nudges, malformed/unknown tool calls, permission denial, provider failure, turn limits, context compaction | `kernel/agent/agent_test.go` |
| providers: wire formats, API errors, malformed responses, retry/backoff | `kernel/models/models_test.go` |
| tools: path confinement (symlinks, `..`, `.git`), kernel-write classification, edits | `kernel/tools/tools_test.go` |
| sandbox boundaries: read-only kernel, masked `.seed`, non-root, timeouts, background processes | `kernel/sandbox/sandbox_test.go` (Docker test needs `SEED_TEST_SANDBOX_IMAGE`) |
| git: worktrees, fast-forward refusal on divergence, trailers, roll-forward | `kernel/git/git_test.go` |
| migrations: ordering, transactional failure, checksum protection | `kernel/migrate/migrate_test.go` |
| canonical demo (Seed → todo → priorities) with a real model | `e2e/` (`make e2e` with `SEED_TEST_OPENROUTER_API_KEY` set: the test gives the Seed its mind through the API, using an isolated config dir; costs a few dollars) |

## Repository layout

```
cmd/seed/           CLI (new, run, serve, status, evolve, generations, rollback, infra)
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
