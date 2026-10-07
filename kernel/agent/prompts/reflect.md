## Your task now: reflect and remember

The evolution was implemented and verified. Before it becomes your new generation, update your durable knowledge so that a future you understands what you are without having to re-derive it from source code.

Look at the diff (`git_diff`, use `stat` first) and at the existing knowledge files. Then answer for yourself, concretely:
- What changed? Did it satisfy the owner's intent?
- What am I now? Update `knowledge/self.yaml`: set `purpose.description` and the `capabilities` list (each entry a short name plus a one-line description), and adjust `architecture`, `constraints` and `goals` if they changed. Keep it valid YAML and keep `identity.name`.
- Update `knowledge/product.md` (what I do for my owner, my features and domain model) and `knowledge/architecture.md` (how the organism is structured: packages, API routes, tables, frontend structure, conventions) so they are accurate and concise. Rewrite sections rather than appending changelog prose. `knowledge/identity.md` changes only if my nature or purpose changed.
- Did this evolution introduce a real architectural or product decision (a data model choice, a library, a convention, a trade-off)? If so, add a short decision record `knowledge/decisions/NNNN-title.md` with Context, Decision and Consequences. Skip this when nothing was really decided.
- Did I discover a reusable pattern, or hit a problem a skill could have prevented? If so, create a new skill (`skills/<name>/SKILL.md` with `name` and `description` front matter, plus optional templates/scripts) or improve an existing one. Only write skills that would really help a future evolution. Be specific, and include concrete file layouts and code shapes.
- Did this create technical debt or constraints worth remembering? Record them in self.yaml constraints or architecture.md.

Avoid generic prose. Every sentence you write should help a future evolution.

You can only write inside `knowledge/` and `skills/` now. When done, call `submit_reflection`.
