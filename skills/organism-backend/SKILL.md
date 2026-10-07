---
name: organism-backend
description: How to add HTTP API endpoints and domain logic to my Go backend (organism/backend), including tests against the scratch database.
---

# Organism backend

My backend is one Go binary (`organism/backend`, package `main`, module `organism`)
built with `go build -o bin/server ./backend`. It uses the standard library
(`net/http` with method patterns, `database/sql`) and the `pgx` driver
(`github.com/jackc/pgx/v5/stdlib`, driver name `pgx`).

## Layout

Keep `main.go` for startup only. For each resource, add one file plus its test:

```
organism/backend/
  main.go            startup: $PORT, $DATABASE_URL, graceful shutdown
  routes.go          route table: register handlers here
  respond.go         writeJSON / writeError helpers
  <resource>.go      types, SQL queries and handlers for one resource
  <resource>_test.go tests for that resource
```

Grow into sub-packages (`organism/backend/<name>/`) only when a file gets big.

## Handlers

- Register handlers in `routes()` with method patterns, for example
  `mux.HandleFunc("GET /api/things", h.list)` and `mux.HandleFunc("PATCH /api/things/{id}", h.update)`.
  Read path values with `r.PathValue("id")`.
- Keep `/api/` routes JSON-only. Use `writeJSON` and `writeError`. Return `[]` rather than
  `null` for empty lists (initialize slices with `make([]T, 0)` or `[]T{}`).
- Validate input and return 400 with a clear message. Return 404 for missing rows
  (`errors.Is(err, sql.ErrNoRows)`), 201 for creates and 204 for deletes.
- Decode bodies with `json.NewDecoder(r.Body)` and `DisallowUnknownFields()` when strictness helps.
- Use `$1, $2` placeholders. Never build SQL from strings.
- Timestamps: use `timestamptz` in SQL and `time.Time` in Go, which encodes as RFC 3339.
- For PATCH, decode into a struct with pointer fields so you can tell an absent field from a zero value.

## Tests

- Tests run with `DATABASE_URL` pointing at a scratch database where all migrations
  are already applied (the kernel runs them before `make test`). Use `testDB(t)` from
  `routes_test.go`.
- Test through HTTP: build `routes(db, t.TempDir())` and call `ServeHTTP` with
  `httptest.NewRecorder()`. This covers routing, validation and SQL together.
- Tests share the database. Create the data each test needs and do not assume tables
  are empty: filter by IDs you created, or clean up with `t.Cleanup`.
- Run `make -C organism test-backend`. It uses `-v`, so the kernel can count passing tests.
