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

`kernel/`, `cmd/`, `control/`, `go.mod`, `go.sum`, `seed.yaml`, `Dockerfile`, `Makefile` and
`docker-compose.yml` are protected (`kernel.protected`). They are guarded in three layers:

1. File tools classify protected writes as dangerous, which requires approval by default.
2. Sandboxes mount them read-only, so shell commands cannot modify them.
3. At commit time, the kernel diffs the worktree and refuses any protected change that was not
   approved during that evolution ("kernel boundary violated").

Kernel evolution is therefore possible but deliberate. After an approved kernel change is applied,
the kernel restarts into its own new source.

## Data

- Each Seed's organism uses a non-superuser role that owns only its live and scratch databases.
  `CONNECT` is revoked from `PUBLIC`, so it cannot read kernel memory or other Seeds' data.
- Model API keys come from the environment and are never written to the repository or to
  `seed.yaml`. Settings returned by the API omit the database URL.
- The logo is generated content. It is served with `Content-Security-Policy: sandbox` and
  rendered via `<img>`, so it cannot run script. Markdown in the control plane renders raw HTML as
  text.

## Control plane API

State-changing `/_seed/api` requests must be `application/json`, and are refused when the browser
reports `Sec-Fetch-Site: cross-site`. Other websites therefore cannot drive the kernel. A plain
form post or `fetch` without a CORS preflight fails, and the kernel never grants preflights.

## Known limitations (v0.1)

- **Same origin.** The control plane (`/_seed`) and the organism (`/`) share one origin, as the
  spec requires. JavaScript the Seed generates for its organism runs in the owner's browser on
  that origin, so it could in principle call the kernel API, for example to approve its own
  kernel change. Sandboxes and the commit-time boundary check protect against generated code at
  *build/run* time, but not this browser path. The robust fix is to serve the control plane
  from its own origin (for example `seed.localhost:8080` or a second port), with `/_seed`
  redirecting there.
- The control plane has no authentication. It binds to 127.0.0.1 by default; do not expose it.
- Sandboxes have outbound network access.
- Rollback does not reverse database migrations.
- The shared PostgreSQL uses fixed local-development credentials.
