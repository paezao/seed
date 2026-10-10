# Seed

> **A Seed is a minimal, self-modifying software system containing an agent and runtime capable of growing itself into an application through conversation.**

**Website:** [paezao.github.io/seed](https://paezao.github.io/seed/)

**Status: early and experimental.** Seed runs code that a language model writes. It does so in
sandboxes, behind an owner sign-in, with every change tested and reversible (see
[security](docs/security.md)), but treat it as the young project it is.

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

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/paezao/seed/main/install.sh | sh
```

That puts the `seed` CLI in `~/.local/bin` (Linux and macOS; on Windows, run it inside WSL2:
see [Windows](docs/windows.md)),
after checking it against the release's checksums. `SEED_BIN_DIR` picks another folder, and
`SEED_VERSION=2026.10.10` installs a specific release.
You also need:

- Docker (Docker Desktop on macOS and Windows), or Podman with `SEED_CONTAINER_ENGINE=podman`
- An [OpenRouter](https://openrouter.ai/keys) API key (one key, any model). You pass it when you
  start a Seed: `seed new tasks -e OPENROUTER_API_KEY`. Seeds never store credentials.

To build from source instead you need Go (see `go.mod`) and Node 24: `make build && make install`.

**A Seed is one folder.** It runs in one container, its body: its kernel, its own private
PostgreSQL (data in `.seed/postgres` inside the folder), the Go and Node toolchains it builds
itself with, and bubblewrap to sandbox its experiments. Nothing is shared between Seeds and
nothing else runs beside them. Only one port is exposed, the Seed's own. Copy, move or back up
the folder, and `seed run` brings it back to life. The same image is what you deploy.

## Getting started

```bash
seed new myapp -e OPENROUTER_API_KEY      # create, start, open the browser
cd myapp && seed run -e OPENROUTER_API_KEY --open   # later: start it again
```

## What a Seed can do

- **Grow by conversation.** Every change is planned, built in an isolated workspace, tested,
  checked against the running app and committed as a new **generation**. You try it on a copy of
  your data before it goes live, then apply it or ask for changes. You can roll back
  to any of them, with the data of then too: it copies the app's data before every change goes
  live and once a day ([backups](docs/backups.md)). It asks before guessing, and it breaks big
  goals into stages.
- **Point at what to change.** On the app's own pages, hover the seed, pick **Change something
  here**, drag a box around part of the page and say what should change; it lands in chat with
  a picture of that area, for you to send.
- **Answer about itself and its data.** Its chat can query the app's data (read-only) and call
  its API. Changing data waits for your approval.
- **Look after itself.** It notices errors, crashes and failing jobs in its app, restarts what
  crashed, investigates the cause and fixes it when you say so, with a test that proves it
  ([health](docs/health.md)).
- **Keep track of what it costs.** Every model call is counted, by what it was for, with an
  optional monthly budget that pauses what it does on its own ([spending](docs/spending.md)).
- **Do things on a schedule.** [Routines](docs/routines.md) are tasks it runs on its own and
  reports on, and jobs its app runs on a schedule.
- **Reach the outside world, when you allow it.** Its app has no network except HTTPS to hosts
  you approve, with the secrets you grant ([outbound access](docs/security.md#outbound-access)).
- **Tell you when it needs you.** Notifications in your browsers and phone when a change is
  ready to try, it has a question, or it found a problem ([notifications](docs/notifications.md)).
- **Stay yours.** Owner sign-in, a control plane only you can open, and a little seed on the
  app's pages that takes you back to it.
- **Update itself.** New kernels arrive as signed releases you install from its control
  plane, and it rolls back on its own if one fails to start ([updates](docs/updates.md)).
- **Move out.** The same image runs anywhere Docker does, with a volume and optionally an
  external PostgreSQL ([hosting](docs/hosting.md)).

CLI:

| command | |
|---|---|
| `seed new <name>` | plant a new Seed (generation 1), start it, open the control plane |
| `seed run [--open] [--detach]` | start the Seed in its container (it builds its own kernel from its own source) |
| `seed status` | what the Seed is right now |
| `seed evolve "<intent>"` | evolve from the terminal and follow progress |
| `seed generations` | list generations |
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
- [Kernel updates](docs/updates.md): signed releases, updating from the control plane, automatic rollback
- [Windows (WSL2)](docs/windows.md): running Seed on Windows
- [Hosting](docs/hosting.md): deploying a Seed with a volume and an external PostgreSQL
- [Health](docs/health.md): noticing, diagnosing and fixing what goes wrong in the app
- [Routines](docs/routines.md): what a Seed does on a schedule (agent routines and jobs)
- [Spending](docs/spending.md): what its model calls cost, and a monthly budget
- [Backups](docs/backups.md): copies of the app's data, restoring them, rolling back with data
- [Notifications](docs/notifications.md): Web Push to the owner's browsers when something needs them
- [Security](docs/security.md): sandboxing, permissions, the kernel boundary
- [Development](docs/development.md): running, testing, contributing
- [Releasing](docs/releasing.md): how a version is checked, signed and published
- [Control plane API](docs/api.md)

## Your app is yours

What your Seed grows (its app in `organism/`, its `knowledge/` and `skills/`) belongs to you:
use it, sell it, license it however you like. The starter app it grows from is MIT
([organism/LICENSE](organism/LICENSE)).

## License

Seed itself (the kernel, CLI and control plane) is licensed under the
[Mozilla Public License 2.0](LICENSE). You can use it, change it, and host what you build with
it, commercially too. If you distribute a modified Seed, its changed files stay available under
the MPL. Running your own Seed, even with a kernel it changed itself, asks nothing of you. The
name and logo: see [TRADEMARKS.md](TRADEMARKS.md).

[Contributing](CONTRIBUTING.md) · [Security](SECURITY.md) · [Code of conduct](CODE_OF_CONDUCT.md)
