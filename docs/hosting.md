# Hosting a Seed

The quickest way: [host on a server](vps.md), one command on any VPS. This
page covers the image itself, for other platforms.

A Seed is a folder: its code, its git history, its knowledge and (by default)
its database. To host one, put that folder on a volume and run the deploy
image. Everything else is environment variables.

## The deploy image

Each release publishes it as `ghcr.io/paezao/seed:<version>` (and `:latest`),
for amd64 and arm64, signed with cosign ([releasing](releasing.md)):

```bash
docker pull ghcr.io/paezao/seed:latest
```

It is `Dockerfile.deploy` (generated from `Dockerfile` by `make
deploy-dockerfile`): the runtime image plus this repository as a template. To
build it yourself, use the repository as the context:

```bash
docker build -f Dockerfile.deploy --build-arg SEED_VERSION=$(git describe --always) -t seed .
```

On boot, if the volume at `/seed` is empty (a `lost+found` is fine), it
**plants a new Seed** named `$SEED_NAME` (default `seed`). It never plants
over files that aren't a Seed. Then it builds the Seed's kernel from the Seed's
own source and starts it, as `seed run` does locally.

The image runs as user `1000:1000`. The volume must be writable by that user:
Docker does it for an empty volume, and on Kubernetes use `fsGroup: 1000`.

## Environment

| variable | |
|---|---|
| `DATABASE_URL` | an external PostgreSQL (see below). Without it, the Seed runs its own PostgreSQL inside the volume. Either way, [backups](backups.md) of the app's data are kept in the volume (`.seed/backups`). |
| `OPENROUTER_API_KEY` | the Seed's mind |
| `SEED_OWNER_PASSWORD`, `SEED_OWNER_USER` | owner sign-in (password at least 12 characters; user defaults to `owner`) |
| `SEED_NAME` | the name of a newly planted Seed |
| `SEED_ALLOWED_HOSTS` | your domain(s), comma-separated: requests for other hosts are refused |
| `SEED_TRUSTED_PROXIES` | `1` behind a platform's ingress (for rate limiting by client address) |
| `PORT` | where to listen (default 8080) |
| `SEED_SECRETS` | names of extra secrets to treat like the ones above (removed from the environment, grantable to the app) |

Health check: `GET /_seed/healthz` answers `ok`, for any Host.

## External PostgreSQL

With `DATABASE_URL`, the Seed keeps its memory, its app's data and its
evolutions' scratch databases on that server. Its databases and roles are
prefixed with its name and a per-Seed id, so several Seeds can share one server.

- **Privileges:** the role needs `CREATEDB` and `CREATEROLE` (a superuser also
  works). The Seed creates a database per evolution and separate roles, so its
  app can't read its memory and the chat's queries are read-only. It refuses to
  start with a clear message otherwise.
- **TLS:** `sslmode` in the URL is honored (`require`, `verify-ca`,
  `verify-full` with `sslrootcert`, `prefer`, `disable`).
- **Sandboxes:** the live app has no network, so the kernel bridges the server
  to a Unix socket that sandboxes see as before. It adds TLS on the way out.
  Sandboxes still log in as their own roles.
- The kernel reads `DATABASE_URL` at boot and removes it from its environment,
  so nothing it starts inherits the admin credentials. Only in the container:
  natively, set `database.url` in `seed.yaml`.

## Platform requirements

The Seed sandboxes its experiments and its live app with bubblewrap, which
needs user namespaces. Run it with the seccomp profile in
`cmd/seed/seccomp.json` (Docker's default plus the calls bubblewrap needs) and
without an AppArmor profile that blocks mounts, as `seed run` does. Platforms
that force their default seccomp profile (most managed Kubernetes) need an
opt-in for this.
