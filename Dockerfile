# Seed sandbox image.
#
# Every piece of generated code -- evolution builds, tests, and the live
# organism -- runs in a container from this image, never directly on the host.
# The kernel builds it from this file (tagged by content hash). It is built
# without a context, so it cannot COPY from the repository.
FROM golang:1.26-bookworm AS go

FROM node:24-bookworm
COPY --from=go /usr/local/go /usr/local/go
ENV PATH=/usr/local/go/bin:$PATH \
    GOTOOLCHAIN=local \
    GOFLAGS=-buildvcs=false \
    CGO_ENABLED=0
RUN apt-get update \
 && apt-get install -y --no-install-recommends postgresql-client make ca-certificates curl jq \
 && rm -rf /var/lib/apt/lists/*
WORKDIR /workspace
