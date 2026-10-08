# Seed

> **A Seed is a minimal, self-modifying software system containing an agent and runtime capable of growing itself into an application through conversation.**

You plant a Seed, and it starts as almost nothing: a kernel (agent, tools, sandbox, memory), a control plane, and an empty body. You tell it what to become. It plans the change, rewrites its own code in an isolated workspace, builds, tests, runs and checks the result, records what it learned, commits, and *becomes* that new version: a new **generation**. The builder never leaves. The todo app you end up with still contains the Seed, and the Seed still knows how it got there.

```
$ export OPENROUTER_API_KEY=sk-or-…      # or keep it in your shell profile
$ seed new tasks -e OPENROUTER_API_KEY
```

That plants the Seed in `./tasks`, starts it, and opens its control plane in your browser, signed in. Only you can use it: anyone else gets a private page.
If port 8080 is taken, it uses the next free one. Then:

> I don't have a purpose yet.
> What should I become?

Say *"Become a todo application. I need to create, complete and delete tasks."* A few minutes later `/` is a working todo app with its own name and logo. Then say *"Tasks should have priorities: low, medium and high. Let me filter by priority."* and it changes again.

## Requirements

- Docker (Docker Desktop on macOS and Windows), or Podman with `SEED_CONTAINER_ENGINE=podman`
- Go 1.27+ and Node.js 22+ only to build the `seed` CLI from source
- An [OpenRouter](https://openrouter.ai/keys) API key (one key, any model). You pass it when you
  start a Seed: `seed new tasks -e OPENROUTER_API_KEY`. Seeds never store credentials.

**A Seed is one folder.** It runs in one container, its body: its kernel, its own private
PostgreSQL (data in `.seed/postgres` inside the folder), the Go and Node toolchains it builds
itself with, and bubblewrap to sandbox its experiments. Nothing is shared between Seeds and
nothing else runs beside them. Only one port is exposed, the Seed's own. Copy, move or back up
the folder, and `seed run` brings it back to life. The same image is what you deploy.

## Getting started

```bash
make build                 # control plane + template + bin/seed
make install               # copies bin/seed to ~/.local/bin
seed new myapp -e OPENROUTER_API_KEY      # create, start, open the browser
cd myapp && seed run -e OPENROUTER_API_KEY --open   # later: start it again
```

CLI:

| command | |
|---|---|
| `seed new <name>` | plant a new Seed (generation 1), start it, open the control plane |
| `seed run [--open] [--detach]` | start the Seed in its container (it builds its own kernel from its own source) |
| `seed status` | what the Seed is right now |
| `seed evolve "<intent>"` | evolve from the terminal and follow progress |
| `seed generations` | list generations (works offline from git) |
| `seed rollback <n>` | return to generation n (recorded as a new generation) |
| `seed login` | sign a browser in to the control plane (`--print` for the link instead) |
| `seed stop` | stop the Seed (its folder keeps everything) |
| `seed upgrade` | give a stopped Seed the kernel of your `seed` CLI, as a new generation |

Signing in: locally, `seed new` and `seed run --open` sign your browser in for you, and
`seed login` makes a new one-time link. To sign in with a username and password (for example
when the Seed is hosted), pass them at start like any secret:

```bash
seed run -e OPENROUTER_API_KEY -e SEED_OWNER_USER=me -e SEED_OWNER_PASSWORD   # at least 12 characters
```

## Documentation

- [Philosophy](docs/philosophy.md): why Seed exists
- [Architecture](docs/architecture.md): kernel, control plane, organism (with diagrams)
- [Evolution lifecycle](docs/evolution.md): how software modifies itself safely
- [Skills](docs/skills.md): how a Seed acquires reusable capabilities
- [Knowledge](docs/knowledge.md): how a Seed keeps continuity of identity
- [Hosting](docs/hosting.md): deploying a Seed with a volume and an external PostgreSQL
- [Routines](docs/routines.md): what a Seed does on a schedule (agent routines and jobs)
- [Security](docs/security.md): sandboxing, permissions, the kernel boundary
- [Development](docs/development.md): running, testing, contributing
- [Control plane API](docs/api.md)
