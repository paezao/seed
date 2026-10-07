# Knowledge and continuity

A normal coding agent reads a repository and reconstructs its purpose. A Seed already knows,
because it was there.

## Two kinds of memory

**Operational memory** in PostgreSQL (`<name>_seed`) is what happened:
conversations and messages, evolutions with plans, status, checks, reflections and token usage,
every phase, tool call and observation (`evolution_events`), generations, and approvals. It powers the
control plane and recovery after restarts.

**Durable knowledge** in the repository (`knowledge/`) is what the Seed *is*. It is human-readable,
versioned with each generation, and included in every agent prompt:

```
knowledge/
├── self.yaml          machine-readable self model
├── logo.svg           its face
├── identity.md        who/what it is
├── product.md         what it does for its owner; features, domain model, rules
├── architecture.md    how the organism is built: packages, routes, tables, conventions
├── roadmap.md         the owner's larger goal and its stages (kernel-maintained; present when staged)
└── decisions/         decision records (context, decision, consequences)
```

## The self model

```yaml
identity:
  slug: tasks                 # stable technical name
  name: Tidy                  # the name it goes by now
  tagline: Small things, done.
  accent: "#2f7fb8"
purpose:
  description: A small single-user todo application.
capabilities:
  - name: tasks
    description: create, complete, delete tasks
  - name: task-priorities
    description: low/medium/high, filter by priority
architecture: {backend: go, frontend: react + typescript (vite), database: postgres}
constraints:
  - single user, no authentication
goals: []
```

It answers *"What am I?"* for the Seed, its owner (Knowledge page, `seed status`) and its
control plane, which renders the identity.

## How it stays true

The reflection phase of every evolution updates knowledge, and reflection can write *only*
`knowledge/` and `skills/`. The prompt asks concrete questions: what changed, whether it satisfied the intent,
what decisions were made, what future evolutions should know, whether there is a reusable pattern, and
what debt was introduced. It asks for rewritten sections rather than appended changelog prose, to keep
memory free of generated filler. Because knowledge is committed with the code, rolling back a
generation also rolls back the Seed's understanding of itself.

The full chain is always inspectable:
`intent → evolution (plan, events, checks, reflection) → diff → commit → generation`.
