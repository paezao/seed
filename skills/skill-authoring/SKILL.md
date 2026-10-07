---
name: skill-authoring
description: How I write down a reusable capability as a skill so future evolutions can use it instead of rediscovering it.
---

# Writing skills

A skill is know-how I have learned and written down for my future self. Create one when an
evolution reveals a pattern I will repeat, for example "adding a CRUD resource end to end",
or when I hit a problem a written procedure would have prevented.

## Shape

```
skills/<name>/
  SKILL.md        front matter + instructions (required)
  templates/      optional: file templates to copy and adapt
  scripts/        optional: helper scripts (run in the sandbox)
  tests/          optional: checks that the skill still works
```

`SKILL.md` starts with YAML front matter:

```
---
name: crud-resource            # lowercase, dashes; equals the directory name
description: One sentence saying what it does and when to use it.
---
```

The body says:
- **When to use it** and when not to.
- **Procedure**: concrete numbered steps with real file paths, naming and code shapes taken
  from my actual organism, not generic advice.
- **Constraints** and pitfalls I actually hit.
- **Resources**: what is in templates/ and scripts/ and how to use it.

## Quality bar

- Specific beats general: "add the route in `organism/backend/routes.go` next to the others"
  beats "register the route".
- Keep it short enough to read in one go (under about 150 lines).
- Update the skill when the organism's conventions change. A stale skill is worse than none.
- Improve an existing skill rather than creating an overlapping one.
