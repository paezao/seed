# Seed

> **A Seed is a minimal, self-modifying software system containing an agent and runtime capable of growing itself into an application through conversation.**

You plant a Seed, and it starts as almost nothing: a kernel (agent, tools, sandbox, memory), a control plane, and an empty body. You tell it what to become. It plans the change, rewrites its own code in an isolated workspace, builds, tests, runs and checks the result, records what it learned, commits, and *becomes* that new version: a new **generation**. The builder never leaves. The todo app you end up with still contains the Seed, and the Seed still knows how it got there.

```
$ seed new tasks
```

That plants the Seed in `./tasks`, starts it, and opens its control plane in your browser.
If port 8080 is taken, it uses the next free one. First it asks for a mind: you
paste an OpenRouter key and pick a model. Then:

> I don't have a purpose yet.
> What should I become?

Say *"Become a todo application. I need to create, complete and delete tasks."* A few minutes later `/` is a working todo app with its own name and logo. Then say *"Tasks should have priorities: low, medium and high. Let me filter by priority."* and it changes again.

## Requirements

- Linux with Docker (generated code always runs in containers)
- Go 1.26+ (the Seed compiles its own kernel on `seed run`)
- Node.js 22+ (only to develop the control plane)
- An [OpenRouter](https://openrouter.ai/keys) API key. One key gives you any model, and you
  enter it in the control plane (stored in `~/.config/seed/credentials.json`, never in a repository).

PostgreSQL is started for you in Docker (`seed-postgres`, on 127.0.0.1:55432).

## Getting started

```bash
make build                 # control plane + template + bin/seed
make install               # copies bin/seed to ~/.local/bin
seed new myapp             # create, start, open the browser (--no-run to only create)
cd myapp && seed run --open   # later: run it again
```

CLI:

| command | |
|---|---|
| `seed new <name>` | plant a new Seed (generation 1), start it, open the control plane |
| `seed run [--open]` | build this Seed's own kernel from its source and run it |
| `seed status` | what the Seed is right now |
| `seed evolve "<intent>"` | evolve from the terminal and follow progress |
| `seed generations` | list generations (works offline from git) |
| `seed rollback <n>` | return to generation n (recorded as a new generation) |
| `seed infra up\|down` | shared PostgreSQL |

## Documentation

- [Philosophy](docs/philosophy.md): why Seed exists
- [Architecture](docs/architecture.md): kernel, control plane, organism (with diagrams)
- [Evolution lifecycle](docs/evolution.md): how software modifies itself safely
- [Skills](docs/skills.md): how a Seed acquires reusable capabilities
- [Knowledge](docs/knowledge.md): how a Seed keeps continuity of identity
- [Security](docs/security.md): sandboxing, permissions, the kernel boundary
- [Development](docs/development.md): running, testing, contributing
- [Control plane API](docs/api.md)
