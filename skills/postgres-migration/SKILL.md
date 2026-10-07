---
name: postgres-migration
description: How to change my database schema safely with numbered SQL migrations in organism/migrations.
---

# PostgreSQL migrations

The kernel applies `organism/migrations/NNNN_description.sql` in order to my live database,
tracking them in `schema_migrations` with checksums. During an evolution it applies them to
a fresh scratch database (the `migrate` tool, and again before tests).

## Rules

1. **Never edit or delete a migration that exists in my current generation.** It has
   already run against the live database, and the kernel refuses changed checksums. Add a
   new file instead.
2. Number new migrations one past the highest existing number, zero-padded to 4 digits:
   `0003_add_task_priority.sql`.
3. Each file runs in one transaction. Do not use `BEGIN`/`COMMIT` or statements that cannot
   run in a transaction (`CREATE INDEX CONCURRENTLY`).
4. Rolling back my code does **not** roll back migrations, so keep changes additive and
   backwards compatible:
   - New columns get a `DEFAULT` or are nullable: `ALTER TABLE tasks ADD COLUMN priority text NOT NULL DEFAULT 'medium'`.
   - Constrain enumerations with `CHECK (priority IN ('low','medium','high'))`.
   - Don't drop or rename columns that the previous generation's code reads.
5. Use `bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY` (or `bigserial`) for ids,
   `timestamptz NOT NULL DEFAULT now()` for timestamps, and `text` over `varchar(n)`.
6. Add indexes for columns used in filters or sorting.

## Check

After writing a migration, run the `migrate` tool, inspect the result with `db_schema`, then
run the backend tests.
