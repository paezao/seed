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
bin/seed new /tmp/demo && cd /tmp/demo
OPENROUTER_API_KEY=… ~/path/to/bin/seed run --addr 127.0.0.1:8090
```

The repository itself is a pristine Seed (`seed.yaml` name `seed`). `seed new` copies it
through the template snapshot, which `make template` builds from tracked and untracked
non-ignored files.

## Model configuration

`seed.yaml` → `model`, or the environment:

| variable | |
|---|---|
| `ANTHROPIC_API_KEY` / `OPENROUTER_API_KEY` / `OPENAI_API_KEY` | first one found selects the provider when `provider: auto` |
| `SEED_PROVIDER` | `anthropic`, `openrouter`, `openai`, `openai-compatible` |
| `SEED_MODEL` | model name (defaults: `claude-sonnet-5-5`, `anthropic/claude-sonnet-5.5`, `gpt-5.5`) |
| `SEED_DATABASE_URL`, `SEED_ADDR`, `SEED_SANDBOX_DRIVER` | infrastructure overrides |

`model.base_url` points the OpenAI-compatible provider at a local server (vLLM, Ollama, …).
New providers implement `models.Model` (one method).

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
| canonical demo (Seed → todo → priorities) with a real model | `e2e/` (`make e2e`, needs a model key; costs a few dollars) |

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
