# Security

A Seed executes code that it wrote itself, and runs shell commands that a model chose. The
design assumes autonomy will grow, so trust boundaries are structural rather than advisory.

## Sandboxing

Generated code never runs on the host. The Docker sandbox driver (`kernel/sandbox/docker.go`):

- mounts only the evolution's worktree at `/workspace`;
- re-mounts every protected kernel path **read-only** (in the live sandbox, also `.git`,
  `knowledge/` and `skills/`);
- masks `.seed/` (worktrees, kernel binary, markers) with an empty tmpfs;
- runs as the host user's uid/gid, never root, with `--cap-drop ALL` and `no-new-privileges`;
- limits pids, memory and CPUs, and kills commands at a timeout from inside the container;
- uses the `seed-net` network shared only with PostgreSQL, and publishes ports on 127.0.0.1 only;
- has no Docker socket, no host home directory, and no secrets except its database URL.

The sandbox image is built from `Dockerfile` **without a build context**, so it cannot copy
repository files or secrets.

Network egress is allowed, so that `go get` and `npm install` work. Restricting egress to a package
proxy is a natural next step.

The `local` driver exists for kernel tests. It provides no isolation, and the kernel logs a warning
when it is configured.

## Permissions

Every tool call is classified before it runs (`tools.Registry.Execute`):

| level | examples | default |
|---|---|---|
| safe | read files, list, search, logs, schema, git status/diff, HTTP to the sandbox | allow |
| review | write organism/knowledge/skills, run commands in the sandbox, migrations on the scratch DB, dependency installs | allow |
| dangerous | writing or deleting any protected (kernel) path | **ask** |

Decisions are `allow`, `ask` or `deny` per level in `seed.yaml`. `ask` creates an approval and
moves the evolution to `needs_input`. The control plane shows it, and the evolution blocks until
the owner decides. Plan and apply approvals can also be required (`require_plan_approval`,
`require_apply_approval`).

## The kernel boundary

The boundary is an **allowlist**: only `organism/`, `knowledge/` and `skills/` evolve freely
(`kernel.evolvable`). Everything else is the kernel. That includes `kernel/`, `cmd/`, `control/`,
`seed.yaml`, `go.mod` and every root file, as well as *new* files such as `go.work` or `vendor/`,
which would change what `go build` compiles on the host. The boundary is guarded in four layers:

1. File tools classify writes outside the evolvable directories as dangerous, which requires
   approval by default.
2. Sandboxes mount the workspace **read-only** except for the evolvable directories, so shell
   commands cannot touch the kernel or create root files.
3. At commit time, the kernel diffs the worktree and refuses any protected change that was not
   approved during that evolution ("kernel boundary violated").
4. `seed run` builds the kernel with `GOWORK=off GOFLAGS=-mod=readonly`.

## Symlinks

Sandboxed code can create symlinks inside evolvable directories. Every host-side read or write of
repository content goes through Go's `os.Root` (`kernel/fsx`, and the file tools), including
knowledge, skills, migrations, the logo and extensions. A link such as
`knowledge/x.md → ~/.ssh/id_rsa` therefore fails instead of leaking a host file into a prompt.

Kernel evolution is therefore possible but deliberate. After an approved kernel change is applied,
the kernel restarts into its own new source.

## Data

- The shared PostgreSQL superuser has a random password, generated once and stored in
  `~/.config/seed/postgres.json` (0600). Sandboxes can reach the server, so the password must never be
  guessable or appear in a repository. Older installs with the default password are rotated
  automatically.
- The live organism uses a non-superuser role that owns only its live database. Evolutions use a
  separate role that owns only scratch databases, so an experiment can never touch live data.
  `CONNECT` is revoked from `PUBLIC` on every database.
- Model API keys are entered by the owner and stored per provider in
  `~/.config/seed/credentials.json` (0600). They never enter a repository or the environment of a
  sandbox, and the API never returns them.
- Model API keys come from the environment and are never written to the repository or to
  `seed.yaml`. Settings returned by the API omit the database URL.
- The logo is generated content. It is served with `Content-Security-Policy: sandbox` and
  rendered via `<img>`, so it cannot run script. Markdown in the control plane renders raw HTML as
  text.

## Control plane

The control plane shares an origin with the organism (`/_seed` and `/`). The organism's
JavaScript is code the Seed wrote, so the kernel does not trust it:

- **Control token.** A random token is generated each time the kernel starts. It is embedded only
  in the control-plane page and written to `.seed/control-token` (hidden from sandboxes) for the
  CLI. Every `/_seed/api` call needs it (the logo is the only exception).
- **Only for navigations.** The page carrying the token is served only for top-level navigations
  (`Sec-Fetch-Dest: document`), never to `fetch`/XHR. It cannot be framed
  (`frame-ancestors 'none'`), so the Approve button cannot be clickjacked.
- **Opener isolation.** The control plane sends `Cross-Origin-Opener-Policy: same-origin`, and the
  proxy forces `unsafe-none` on organism pages. An organism page that opens `/_seed` in a popup
  therefore cannot read it.
- **No service workers.** The organism cannot register them, because a worker at `/` would also
  control `/_seed`.
- **Other sites.** Requests for any Host other than localhost, `127.0.0.1`, `[::1]` or `*.localhost` are
  refused (`server.allowed_hosts` adds more), which defeats DNS rebinding. State-changing calls must
  be JSON and must come from the same origin.

## Known limitations (v0.1)

- **Same origin.** The mitigations above are layered, but a separate origin for the control plane
  (for example `seed.localhost:8080`) would be stronger still. It is a candidate for v0.2.
- The control plane has no authentication. It binds to 127.0.0.1 by default; do not expose it.
- Sandboxes have outbound network access.
- Rollback does not reverse database migrations.
- The shared PostgreSQL uses fixed local-development credentials.
