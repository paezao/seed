#!/bin/sh
# Boot smoke test: plant a Seed with this CLI, start it, check it answers,
# stop it. No model key needed. Usage: scripts/smoke.sh [path/to/seed]
set -eu
seed="${1:-bin/seed}"
case "$seed" in /*) ;; *) seed="$PWD/$seed" ;; esac
work="$(mktemp -d)"
port="${SMOKE_PORT:-18080}"
cleanup() {
  (cd "$work/smoke" 2>/dev/null && "$seed" stop >/dev/null 2>&1) || true
  rm -rf "$work"
}
trap cleanup EXIT

cd "$work"
"$seed" new smoke --no-run
cd smoke
"$seed" run --detach --addr "127.0.0.1:$port"

# The first start may build the runtime image: give it time.
deadline=$(( $(date +%s) + ${SMOKE_TIMEOUT:-900} ))
until curl -fsS "http://127.0.0.1:$port/_seed/healthz" >/dev/null 2>&1; do
  if [ "$(date +%s)" -gt "$deadline" ]; then echo "smoke: the kernel never answered" >&2; "$seed" status >&2 || true; exit 1; fi
  sleep 3
done
echo "smoke: kernel is up"
until "$seed" status 2>/dev/null | grep -q "Body: running"; do
  if [ "$(date +%s)" -gt "$deadline" ]; then echo "smoke: the starter app never came up" >&2; "$seed" status >&2 || true; exit 1; fi
  sleep 3
done
code="$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$port/")"
[ "$code" = 200 ] || { echo "smoke: / answered $code" >&2; exit 1; }
"$seed" status | grep -q "Generation 1" || { echo "smoke: not at generation 1" >&2; "$seed" status >&2; exit 1; }
"$seed" status
echo "smoke: ok"
