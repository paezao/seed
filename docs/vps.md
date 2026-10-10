# Host a Seed on a server

The simplest way to put a Seed online: one Linux server (a VPS from Hetzner,
DigitalOcean, Linode, AWS…), Docker, and Caddy in front for HTTPS. One
command sets it all up.

## What you need

- A server with **Ubuntu 24.04 or Debian 12** (others with Docker work too),
  **2 GB of memory or more** (4 GB is comfortable), 2 CPUs, 40 GB of disk.
  A Seed idles under 100 MB; building and testing a change takes about 1 GB.
- Ports **80 and 443** open to the internet.
- An **OpenRouter key** ([openrouter.ai/keys](https://openrouter.ai/keys)).
- Optionally a **domain** pointing at the server (an `A` record). Without one,
  the Seed gets `<server IP>.sslip.io`, which works with HTTPS right away.

## Set it up

On the server, as root (or with `sudo`):

```bash
curl -fsSL https://raw.githubusercontent.com/paezao/seed/main/deploy/vps/setup.sh \
  | sudo OPENROUTER_API_KEY=sk-or-… SEED_DOMAIN=app.example.com sh
```

It installs Docker if it's missing, allows the user namespaces the Seed's
sandboxes need (Ubuntu 24.04 restricts them), writes everything to
`/opt/seed`, starts the Seed and Caddy, and waits until the Seed is up. The
first start plants the Seed and builds its kernel: a few minutes. Then it
prints where to sign in, your username, and a new password (shown once).

| setting | |
|---|---|
| `OPENROUTER_API_KEY` | the Seed's mind (required) |
| `SEED_DOMAIN` | its address (default: `<server IP>.sslip.io`) |
| `SEED_NAME` | the new Seed's name (default `seed`) |
| `SEED_OWNER_USER`, `SEED_OWNER_PASSWORD` | your sign-in (default `owner` and a generated password) |
| `SEED_IMAGE` | default `ghcr.io/paezao/seed:latest` |

Run it again with new values to change them (say, a domain once you have
one). The Seed itself is kept.

### When creating the server

Most providers take **cloud-init** "user data" when you create a server.
Paste [`deploy/vps/cloud-init.yaml`](../deploy/vps/cloud-init.yaml) with your
key filled in, and the server comes up with the Seed on it. The output (with
the password) is in `/var/log/seed-setup.log`, readable by root only.

## What's on the server

`/opt/seed` holds the settings: `docker-compose.yml`, the `Caddyfile`, the
Seed's seccomp profile, and `.env` (root-only: your key and password). The
Seed's folder (code, history, knowledge, database, backups) is the Docker
volume `seed_seed`; Caddy's certificates are in `seed_caddy-data`.

The Seed's container runs as it does with `seed run`: all capabilities
dropped, no new privileges, a read-only root, with Seed's seccomp profile and
an unmasked `/proc` so its sandboxes (bubblewrap) can create their
namespaces. Caddy forwards `X-Forwarded-For`, and the Seed trusts exactly one
proxy (`SEED_TRUSTED_PROXIES=1`) and only answers for `SEED_DOMAIN`.

## Everyday things

```bash
cd /opt/seed
docker compose logs -f seed        # what it's doing
docker compose restart seed        # restart it
docker compose pull && docker compose up -d   # a new runtime image
```

Kernel updates don't need any of this: the Seed offers them in its control
plane (Settings → Kernel) and installs them itself
([kernel updates](updates.md)). A new **runtime image** is only needed when a
release says so.

**Backups.** The Seed copies its data before every change and daily
([backups](backups.md)), but those copies live on the same server. Download
one now and then from the Backups page, or snapshot the server's disk with
your provider.

## Other ways

The same image runs anywhere Docker runs with these security options:
Coolify, Dokku or CapRover on your own server (add the options from
`docker-compose.yml`), or Kubernetes you control ([hosting](hosting.md)).
Platforms that force their own seccomp profile on containers (most managed
Kubernetes) block the Seed's sandboxes.
