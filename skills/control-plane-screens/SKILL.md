---
name: control-plane-screens
description: How I add admin screens to my own control plane (/_seed), e.g. stats, data management or signups, when my owner wants to operate me rather than use me.
---

# Control plane screens

My control plane at `/_seed` is my owner's admin panel. The kernel provides chat,
evolutions, generations, skills, knowledge, logs and settings. Everything specific to
what I have become, such as stats, moderation, data management or signups, I add myself as
**admin screens** declared by my organism.

Use this skill when my owner asks for something to *operate* me ("I want somewhere to see
signups", "an admin view of all tasks", "stats"), as opposed to a feature for users at `/`.

## How it works

1. Declare the screen in `organism/control/extensions.json` (create the file if missing):

   ```json
   [
     { "id": "stats", "title": "Stats", "path": "/admin/stats" }
   ]
   ```

   - `id`: lowercase slug, unique.
   - `title`: shown in the control plane's navigation under "Admin".
   - `path`: an organism URL starting with `/` (never `/_seed`). The kernel ignores invalid entries.

2. Serve the screen from the organism at that path. The control plane shows it in a frame
   inside its own layout, and the list is refreshed when a new generation is applied. The frame
   is loaded from my organism's own origin (`organism.localhost:<port>`), not the control
   plane's, so always use relative URLs (`fetch('/api/admin/...')`). The screen cannot (and must
   not try to) reach the control plane.
   - The simplest approach is a separate Vite entry or an SPA route. In the SPA, render an
     admin component when `location.pathname` starts with `/admin/`. The Go server already
     falls back to `index.html` for unknown non-API paths.
   - Keep admin screens visually quiet: no app header, a full-width layout, my accent
     color, and support for light and dark.
   - Admin APIs go under `/api/admin/...`, with handlers and tests like any other API.

3. Verify it like any other change:
   - `make -C organism build test`
   - `start_app`, then `http_request GET /admin/<id>` returns 200 and
     `http_request GET /api/admin/...` returns the expected JSON
   - add these to the `finish` checks

## Constraints

- Admin screens are part of my organism: they are versioned, tested and rolled back with my generations.
- Do not put secrets or kernel data in them. They only see my organism's database.
- v0.1 has no authentication on `/` or `/_seed`. Admin screens are as public as the rest of
  me, so note this in `knowledge/self.yaml` constraints when adding sensitive views.
