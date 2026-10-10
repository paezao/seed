#!/bin/sh
# Puts a Seed on this server, with HTTPS: Docker, the Seed and Caddy.
#
#   curl -fsSL https://raw.githubusercontent.com/paezao/seed/main/deploy/vps/setup.sh \
#     | sudo OPENROUTER_API_KEY=sk-or-… SEED_DOMAIN=app.example.com sh
#
# Settings (environment variables):
#   OPENROUTER_API_KEY   the Seed's mind (required)
#   SEED_DOMAIN          its address; point the domain's DNS at this server
#                        first. Empty: <this server's IP>.sslip.io
#   SEED_NAME            the Seed's name (default: seed)
#   SEED_OWNER_USER      your sign-in (default: owner)
#   SEED_OWNER_PASSWORD  your password (default: a new one, printed once)
#   SEED_IMAGE           default ghcr.io/paezao/seed:latest
#   SEED_DIR             where its settings go (default /opt/seed)
# Run it again to change settings: the Seed itself is kept.
set -eu

ref="${SEED_REF:-main}"
raw="https://raw.githubusercontent.com/paezao/seed/$ref/deploy/vps"
dir="${SEED_DIR:-/opt/seed}"

say() { printf '\033[1;32m==>\033[0m %s\n' "$*"; }
die() { printf 'seed setup: %s\n' "$*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || die "run me as root (e.g. with sudo)"
[ "$(uname -s)" = Linux ] || die "this sets up a Linux server"
command -v curl >/dev/null 2>&1 || die "I need curl"

# Keep what an earlier run set, unless given again.
if [ -f "$dir/.env" ]; then
  # shellcheck disable=SC1091
  old() { sed -n "s/^$1=//p" "$dir/.env" | tail -1 | sed 's/\$\$/$/g'; }
  : "${OPENROUTER_API_KEY:=$(old OPENROUTER_API_KEY)}"
  : "${SEED_OWNER_USER:=$(old SEED_OWNER_USER)}"
  : "${SEED_OWNER_PASSWORD:=$(old SEED_OWNER_PASSWORD)}"
  : "${SEED_NAME:=$(old SEED_NAME)}"
  : "${SEED_DOMAIN:=$(old SEED_DOMAIN)}"
  : "${SEED_IMAGE:=$(old SEED_IMAGE)}"
fi
[ -n "${OPENROUTER_API_KEY:-}" ] || die "set OPENROUTER_API_KEY (https://openrouter.ai/keys)"

if ! command -v docker >/dev/null 2>&1; then
  say "installing Docker"
  curl -fsSL https://get.docker.com | sh
fi
docker compose version >/dev/null 2>&1 || die "Docker Compose is missing (install the docker-compose-plugin package)"

# Ubuntu 24.04 limits unprivileged user namespaces through AppArmor; the
# Seed's sandboxes (bubblewrap) need them.
if [ -e /proc/sys/kernel/apparmor_restrict_unprivileged_userns ]; then
  say "allowing user namespaces (for the Seed's sandboxes)"
  echo 'kernel.apparmor_restrict_unprivileged_userns = 0' > /etc/sysctl.d/60-seed.conf
  sysctl -q -w kernel.apparmor_restrict_unprivileged_userns=0
fi

if [ -z "${SEED_DOMAIN:-}" ]; then
  ip="$(ip -4 route get 1.1.1.1 2>/dev/null | sed -n 's/.* src \([0-9.]*\).*/\1/p')"
  case "$ip" in
    ''|10.*|192.168.*|172.1[6-9].*|172.2[0-9].*|172.3[01].*)
      die "set SEED_DOMAIN: I couldn't find this server's public IP (got '${ip:-none}')" ;;
  esac
  SEED_DOMAIN="$ip.sslip.io"
fi
case "$SEED_DOMAIN" in
  *[!a-zA-Z0-9.-]*) die "SEED_DOMAIN should be a host name like app.example.com" ;;
esac

new_password=false
if [ -z "${SEED_OWNER_PASSWORD:-}" ]; then
  SEED_OWNER_PASSWORD="$(head -c 18 /dev/urandom | base64 | tr -d '/+=' | cut -c1-20)"
  new_password=true
fi
[ "${#SEED_OWNER_PASSWORD}" -ge 12 ] || die "SEED_OWNER_PASSWORD needs at least 12 characters"

say "writing $dir"
mkdir -p "$dir"
for f in docker-compose.yml Caddyfile; do curl -fsSL "$raw/$f" -o "$dir/$f"; done
curl -fsSL "https://raw.githubusercontent.com/paezao/seed/$ref/cmd/seed/seccomp.json" -o "$dir/seccomp.json"
# Compose reads $ in .env as a variable: write it as $$.
esc() { printf '%s' "$1" | sed 's/\$/$$/g'; }
umask 077
cat > "$dir/.env" <<ENV
# The Seed's settings. Root-only: it holds your key and password.
SEED_DOMAIN=$SEED_DOMAIN
SEED_NAME=${SEED_NAME:-seed}
SEED_IMAGE=${SEED_IMAGE:-ghcr.io/paezao/seed:latest}
OPENROUTER_API_KEY=$(esc "$OPENROUTER_API_KEY")
SEED_OWNER_USER=${SEED_OWNER_USER:-owner}
SEED_OWNER_PASSWORD=$(esc "$SEED_OWNER_PASSWORD")
ENV
chmod 600 "$dir/.env"

say "starting the Seed (the first start plants it and builds its kernel: a few minutes)"
cd "$dir"
docker compose --env-file .env pull -q
docker compose --env-file .env up -d

# Up when it answers its health check, asked from Caddy's side (the Seed
# refuses most requests from inside its own container).
up=false
for _ in $(seq 1 180); do
  if docker compose --env-file .env exec -T caddy wget -q -O /dev/null http://seed:8080/_seed/healthz 2>/dev/null; then
    up=true; break
  fi
  sleep 5
done
[ "$up" = true ] || die "the Seed hasn't come up yet; see: cd $dir && docker compose logs -f seed"

say "your Seed is up"
echo
echo "  https://$SEED_DOMAIN/_seed/"
echo "  sign in as: ${SEED_OWNER_USER:-owner}"
if [ "$new_password" = true ]; then
  echo "  password:   $SEED_OWNER_PASSWORD   (shown once; it's in $dir/.env)"
fi
echo
echo "HTTPS needs $SEED_DOMAIN to point at this server, and ports 80 and 443 open."
echo "Logs: cd $dir && docker compose logs -f seed"
