-- Copies of my live data: before each generation goes live, daily, when my
-- owner asks, and before a restore replaces it. The files live in
-- .seed/backups; this is what they are.
CREATE TABLE backups (
    id         text PRIMARY KEY,
    kind       text NOT NULL,          -- before_generation, daily, manual, before_restore
    generation integer NOT NULL,       -- the generation that was live when it was taken
    label      text NOT NULL DEFAULT '',
    file       text NOT NULL,
    size       bigint NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX backups_created ON backups (created_at DESC);
-- A rollback can bring back the data of then, too.
ALTER TABLE evolutions ADD COLUMN restore_backup text NOT NULL DEFAULT '';
