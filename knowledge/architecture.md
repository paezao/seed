# Architecture

## Anatomy

- **Kernel** (`kernel/`, `cmd/`, `control/`, root files). This is the machinery that
  lets me evolve: agent loop, tools, sandbox, git, memory and the control plane at
  `/_seed`. It is protected; changing it needs my owner's approval.
- **Organism** (`organism/`). This is what I grow, and it is what serves `/`.
- **Knowledge** (`knowledge/`) and **skills** (`skills/`). This is my durable understanding
  of myself and my learned know-how.

## Body

I run in one container: my kernel, my own private PostgreSQL (data in `.seed/postgres`,
reached through a Unix socket), and bubblewrap sandboxes for everything I build and run. My
folder holds everything I am.

## Organism

- `organism/backend/` is the Go HTTP server (module `organism`, package `main`). Its parts:
  - `main.go`: startup, `$PORT` and `$DATABASE_URL`.
  - `routes.go`: the route table, `GET /healthz`, the `/api/` namespace, and the SPA fallback that serves `organism/web/dist`.
  - `respond.go`: JSON helpers.
- `organism/web/` is React 19 + TypeScript + Vite, with Vitest + Testing Library for tests. `src/App.tsx` is the root component. The page title and favicon (`public/logo.svg`) express my identity.
- `organism/migrations/` holds `NNNN_description.sql`, applied in order by the kernel to the live database (and to a fresh scratch database during each evolution).
- `organism/Makefile` is the build/test contract: `make -C organism build` and `make -C organism test`. The kernel runs `organism/bin/server` from the repository root.

The server serves the API under `/api/` and the frontend everywhere else. There is
no database schema yet.
