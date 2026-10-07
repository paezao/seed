# The organism's build contract. The kernel runs, from the repository root:
#   make -C organism build   -> bin/server and web/dist
#   make -C organism test    -> backend and frontend tests
# and starts organism/bin/server (cwd = repository root, $PORT, $DATABASE_URL).

.PHONY: build backend web test test-backend test-web

build: backend web

backend:
	go build -o bin/server ./backend

web: web/node_modules/.package-lock.json
	cd web && npm run build

web/node_modules/.package-lock.json: web/package.json web/package-lock.json
	cd web && npm ci --no-audit --no-fund --prefer-offline

test: test-backend test-web

test-backend:
	go test -count=1 -v ./...

test-web: web/node_modules/.package-lock.json
	cd web && npm test
