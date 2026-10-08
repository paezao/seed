# Seed kernel development.
#
#   make up      start local infrastructure (PostgreSQL, sandbox network/image)
#   make build   build the control plane, the template and bin/seed
#   make test    run kernel tests (needs `make up`; Docker tests need the image)
#   make lint    vet + format check + typecheck
#   make e2e     the canonical Seed -> todo -> priorities run (real model; costs money)

GO      ?= go
# Kernel version stamped into the template (what `seed upgrade` installs).
VERSION ?= $(shell date -u +%Y.%m.%d)-$(shell git rev-parse --short HEAD 2>/dev/null || echo dev)$(shell git diff --quiet HEAD 2>/dev/null || echo +)
BIN     := bin/seed
UID     := $(shell id -u)
GID     := $(shell id -g)
# Same hardened settings as a running Seed (see cmd/seed/container.go).
SECURITY := --cap-drop ALL --security-opt no-new-privileges --security-opt seccomp=cmd/seed/seccomp.json \
            --security-opt apparmor=unconfined --security-opt systempaths=unconfined

.PHONY: up build control template install test lint e2e clean

up: build
	$(BIN) image
	@echo "ready. plant a Seed: $(BIN) new myapp"

build: control template
	$(GO) build -o $(BIN) ./cmd/seed

control:
	cd control/web && npm ci --no-audit --no-fund && npm run build

# Snapshot the pristine Seed (tracked + untracked, non-ignored files) into
# the binary so `seed new` can plant it.
template:
	rm -rf .stage && mkdir .stage
	git ls-files -co --exclude-standard -z \
	  | xargs -0 sh -c 'for f; do [ -e "$$f" ] && printf "%s\0" "$$f"; done' sh \
	  | grep -zv '^kernel/template/assets/template.tar.gz$$' \
	  | tar --null -cf - -T - | tar -C .stage -xf -
	echo "$(VERSION)" > .stage/kernel/VERSION
	tar -C .stage -czf kernel/template/assets/template.tar.gz .
	rm -rf .stage

# The deploy image: the runtime Dockerfile plus planting (see deploy/).
deploy-dockerfile:
	{ echo "# GENERATED from Dockerfile and deploy/*.Dockerfile by \`make deploy-dockerfile\`: do not edit."; \
	  sed '/^FROM golang:.* AS go$$/r deploy/plant.Dockerfile' Dockerfile; cat deploy/tail.Dockerfile; } > Dockerfile.deploy

install: build
	install -m 0755 $(BIN) $(HOME)/.local/bin/seed

test: build
	mkdir -p $(HOME)/.cache/seed-dev
	docker run --rm $(SECURITY) --user $(UID):$(GID) -v $(CURDIR):/src -w /src \
	  -v $(HOME)/.cache/seed-dev:/cache -e GOCACHE=/cache/go-build -e GOMODCACHE=/cache/go-mod \
	  --entrypoint /usr/bin/tini $$($(BIN) image) -- go test ./kernel/... ./cmd/...

lint:
	$(GO) vet ./...
	@test -z "$$(gofmt -l kernel cmd control)" || (gofmt -l kernel cmd control; exit 1)
	cd control/web && npx tsc --noEmit
	cd organism && $(GO) vet ./...

e2e: build
	SEED_E2E=1 $(GO) test -v -count=1 -timeout 120m ./e2e/...

clean:
	rm -rf bin kernel/template/assets/template.tar.gz
