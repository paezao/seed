---
name: organism-web
description: How to build my React + TypeScript frontend (organism/web) — structure, API calls, styling, identity and Vitest tests.
---

# Organism web

The frontend in `organism/web` is React 19 + TypeScript + Vite. `npm run build` type-checks
with `tsc -b` and writes `dist/`, which the Go server serves. Tests use Vitest with jsdom
and Testing Library (`npm test`).

## Structure

```
organism/web/
  index.html          <title> and favicon (my identity)
  public/logo.svg     my logo (keep identical to knowledge/logo.svg)
  src/main.tsx        entry; imports styles.css
  src/App.tsx         root component / layout
  src/api.ts          typed fetch helpers for /api
  src/components/     UI components
  src/styles.css      global styles (CSS variables, light + dark)
```

## Conventions

- Call the API with same-origin relative URLs (`fetch('/api/things')`). Put all calls in
  `src/api.ts` with explicit TypeScript types, and throw an `Error` carrying the server's
  `error` message when a response is not OK.
- Keep state simple: `useState` + `useEffect`, and refetch after mutations. Don't add a
  state library unless asked.
- Write plain CSS in `styles.css` with CSS variables and `prefers-color-scheme` support.
  Make it clean and calm; my identity's accent color is `--accent`.
- Use accessible markup: real `<button>`s, `<label>`s for inputs, form submit on Enter.
- The header shows my logo (`/logo.svg`) and my name.
- If client-side routes are needed, the Go server already falls back to `index.html`.
- `tsconfig` has `noUnusedLocals`, so remove unused imports or the build fails.

## Tests

- Put a test next to the component (`Thing.test.tsx`). Mock `fetch` with
  `vi.spyOn(globalThis, 'fetch')` returning `new Response(JSON.stringify(data))`.
- Use `findBy*` queries for async rendering and `@testing-library/react`'s `fireEvent`
  for interaction. `user-event` is not installed, so add it if you need it.

## Adding dependencies

Run `cd organism/web && npm install <pkg>` with `run` so that `package-lock.json` is
updated. The build uses `npm ci`.
