# Seed runtime image.
#
# One container is one Seed's body: its kernel, its own PostgreSQL, the Go
# and Node toolchains it builds itself with, and bubblewrap to sandbox its
# experiments and its live organism. The Seed's folder is mounted at /seed and
# holds everything it is (code, history, data). Built by the kernel/CLI from
# this file without a build context (tagged by content hash).
FROM golang:1.27-trixie AS go

FROM node:24-trixie-slim
COPY --from=go /usr/local/go /usr/local/go
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      postgresql-17 bubblewrap git make ca-certificates curl jq tini procps \
 && rm -rf /var/lib/apt/lists/*
ENV PATH=/usr/lib/postgresql/17/bin:/usr/local/go/bin:$PATH \
    GOTOOLCHAIN=local \
    CGO_ENABLED=0 \
    HOME=/tmp/home \
    SEED_IN_CONTAINER=1 \
    GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=safe.directory GIT_CONFIG_VALUE_0=*
# Boot: build this Seed's own kernel from its own source, run it, and rebuild
# and restart when an evolution changed the kernel (exit code 75).
RUN printf '%s\n' \
  '#!/bin/sh' \
  'set -u' \
  'mkdir -p /tmp/home /seed/.seed/bin /seed/.seed/cache' \
  'export GOCACHE=/seed/.seed/cache/go-build GOMODCACHE=/seed/.seed/cache/go-mod' \
  'cd /seed' \
  'child=""' \
  'trap '"'"'[ -n "$child" ] && kill -TERM "$child" 2>/dev/null; wait "$child"; exit 0'"'"' TERM INT' \
  'while :; do' \
  '  echo "building my kernel…"' \
  '  GOWORK=off GOFLAGS="-mod=readonly -buildvcs=false -modcacherw" go build -o .seed/bin/seed ./cmd/seed || exit 1' \
  '  .seed/bin/seed serve --dir /seed --addr 0.0.0.0:8080 & child=$!' \
  '  wait "$child"; code=$?; child=""' \
  '  [ "$code" -eq 75 ] || exit "$code"' \
  '  echo "my kernel changed; restarting into it…"' \
  'done' > /usr/local/bin/seed-boot \
 && chmod 0755 /usr/local/bin/seed-boot
WORKDIR /seed
EXPOSE 8080
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/seed-boot"]
