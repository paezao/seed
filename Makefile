# Seed kernel development.
#
#   make up      start local infrastructure (PostgreSQL, sandbox network/image)
#   make build   build the control plane, the template and bin/seed
#   make test    run kernel tests (needs `make up`; Docker tests need the image)
#   make lint    vet + format check + typecheck
#   make e2e     the canonical Seed -> todo -> priorities run (real model; costs money)

GO      ?= go
BIN     := bin/seed
SANDBOX := $(shell docker images --format '{{.Repository}}:{{.Tag}}' seed-sandbox 2>/dev/null | head -1)

.PHONY: up down build control template install test lint e2e clean

up: build
	$(BIN) infra up
	@echo "infrastructure ready. create a Seed: $(BIN) new myapp"

down:
	-$(BIN) infra down

build: control template
	$(GO) build -o $(BIN) ./cmd/seed

control:
	cd control/web && npm ci --no-audit --no-fund && npm run build

# Snapshot the pristine Seed (tracked + untracked, non-ignored files) into
# the binary so `seed new` can plant it.
template:
	git ls-files -co --exclude-standard -z | grep -zv '^kernel/template/assets/template.tar.gz$$' \
	  | tar --null -czf kernel/template/assets/template.tar.gz -T -

install: build
	install -m 0755 $(BIN) $(HOME)/.local/bin/seed

test:
	SEED_TEST_SANDBOX_IMAGE=$(SANDBOX) $(GO) test ./kernel/... ./cmd/...

lint:
	$(GO) vet ./...
	@test -z "$$(gofmt -l kernel cmd control)" || (gofmt -l kernel cmd control; exit 1)
	cd control/web && npx tsc --noEmit
	cd organism && $(GO) vet ./...

e2e: build
	SEED_E2E=1 $(GO) test -v -count=1 -timeout 120m ./e2e/...

clean:
	rm -rf bin kernel/template/assets/template.tar.gz
