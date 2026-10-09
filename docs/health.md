# Health: a Seed that looks after itself

A Seed watches its live app and fixes what goes wrong, with its owner's go-ahead (or on its own,
if the owner allows it).

## What it notices

- **Errors.** The kernel's proxy sees every response. Three or more server errors (5xx, or the
  app not answering) on the same route within 10 minutes become an incident, named by route:
  `GET /api/recipes/:id → 500`.
- **Crashes.** A watchdog checks the app's process every few seconds. If it died, the kernel
  restarts it at once and records a crash incident ("My app crashed"). Crashes with the same
  first error line count as one incident; that line appears only in the evidence.
  After 3 crashes in 10 minutes it stops restarting and says so. A new generation gets fresh
  restarts.
- **Failing jobs.** A [scheduled job](routines.md) that fails twice in a row.

## What it does

1. **Investigates.** For each new incident, the Seed investigates on its own, read-only: its code,
   its app's logs and data. It concludes with the likely cause and the fix it would make. At most 8
   investigations a day, one at a time.
2. **Tells you.** In chat ("Health · GET /api/recipes/:id → 500 … I can fix it: …") and on the
   **Health** page.
3. **Fixes, when you say so.** **Fix it** starts an evolution with the diagnosis and the evidence.
   It must first write a test that reproduces the problem, then fix it. With **Fix problems on my
   own** on (off by default), the Seed starts the fix itself once it knows the cause. Either way the
   fix is an ordinary evolution: tested before it goes live, and a generation you can roll back.
4. **Watches.** After the fix goes live, the incident stays under watch for 30 minutes. If it
   doesn't come back, it is resolved. If it does, it reopens ("It came back after the fix").

**Ignore** stops tracking a problem, including future occurrences of it.

## Evidence is untrusted

Request paths and log lines can be written by anyone using the app. Paths are normalized: ids
become `:id`, odd segments become `:x`, and there is no query string. Log excerpts are bounded
(the last 40 lines, 300 characters each, printable only). Every agent that sees an incident gets its name and evidence only inside a fence marked as
data never to follow. The fix evolution gets the earlier diagnosis that way too, since it was
written after reading that data: as leads to verify against the code. It also gets a narrow
scope: fix only the cause, test first, and don't add or change endpoints, sign-in, permissions,
outbound access, secrets or kernel files. Crash incidents are never named after log text. Chat sees health reports
as records written from the app's logs, not as its own words. Investigations are read-only and
can't change data.
