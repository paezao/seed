# Skills

Skills are how a Seed improves its *ability to change itself*, not just itself.

A skill is a directory in the repository:

```
skills/postgres-migration/
├── SKILL.md      front matter (name, description) + instructions
├── scripts/      optional helpers (run in the sandbox)
├── templates/    optional file templates
└── tests/        optional checks that the skill still works
```

- **Discovery.** Every agent prompt includes the skill index (name and description). The full
  instructions are loaded on demand with the `read_skill` tool, which keeps context small as
  the library grows.
- **Use.** The planner and builder are told to read the relevant skills before working.
- **Creation.** During reflection, the Seed asks itself whether it discovered a reusable pattern
  or hit a problem a written procedure would have prevented. If so, it writes a new skill or
  improves an existing one, following `skills/skill-authoring`. Skills are committed with the
  generation that produced them, and they roll back with it.
- **Validation.** Reflection cannot finish while any skill is malformed (missing front matter or
  description).

A fresh Seed starts with five general skills. None of them is application-specific:

| skill | |
|---|---|
| `organism-backend` | Go API conventions, error handling, HTTP-level tests against the scratch DB |
| `organism-web` | React/TS structure, API client, styling, identity, Vitest |
| `postgres-migration` | numbered, additive, transactional migrations |
| `skill-authoring` | how to write a good skill |
| `control-plane-screens` | growing its own admin screens into `/_seed` |

After building a few resources, a Seed typically writes something like
`skills/crud-resource/` with the exact file layout and code shapes of its own organism. Later
evolutions use that skill instead of rediscovering the pattern.
