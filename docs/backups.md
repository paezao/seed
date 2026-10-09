# Backups

A Seed keeps copies of its app's data, so a bad change can be undone with
the data and not only with the code.

## When copies are taken

- **Before every generation goes live**: an evolution being applied, or a
  rollback. If the copy can't be taken, the generation doesn't go live and
  nothing changes.
- **Once a day** (on by default; Backups page). The first check runs 10
  minutes after start, then hourly; a copy is taken when the last daily one
  is over 23 hours old and the app is running.
- **When the owner asks**: *Back up now*.
- **Before a restore** replaces the data, so a restore can be undone.

Kept: the last 10 copies taken before a generation, 7 daily, 10 made by the
owner, and 5 taken before a restore. Older ones are deleted.

## Where they are

`pg_dump` archives (custom format) of the live database, in
`.seed/backups/` in the Seed's folder (or its volume, when hosted), mode
0700. No sandbox can see `.seed/`. They are taken and restored as the app's
own database role, so they hold exactly what the app owns: tables, data,
sequences and its migration history. Each can be downloaded from the
Backups page and restored anywhere with `pg_restore`.

With an external `DATABASE_URL`, backups work the same way (pg_dump talks
to that server). A managed database's own backups are still worth having:
these protect against a bad change, not against losing the volume.

## Restoring

From the Backups page (**Restore…**), or with a rollback (**with its
data**). A restore:

1. copies the data of now (a *before a restore* copy);
2. stops the app (visitors see an "I am restoring" page that refreshes);
3. drops and recreates the live database, and restores the copy into it,
   in one transaction;
4. puts back the read-only role's permissions;
5. deploys the current generation again, which applies any migrations newer
   than the copy, and starts the app.

If step 3 or 4 fails, the data of now is put back. The chat reports how it
went.

A rollback **with its data** restores the last copy taken while the target
generation was live: the data as it was when that generation was replaced.
Changes to the data since then are no longer live, but they are in the
*before a restore* copy.

## API

| | | |
|---|---|---|
| GET | `/backups` | `{backups, daily, unavailable, keep}` |
| POST | `/backups` | take a copy now |
| POST | `/backups/settings` `{daily}` | daily copies on or off |
| POST | `/backups/:id/restore` | restore (in the background; `restore` events report it) |
| GET | `/backups/:id/download` | the archive |
| POST | `/generations/:n/rollback` `{with_data}` | roll back, with the data of then |
