-- Things going wrong in my live organism (errors, crashes, failing jobs),
-- what I think caused them, and what I did about it.
CREATE TABLE incidents (
    id            text PRIMARY KEY,
    signature     text NOT NULL,
    kind          text NOT NULL CHECK (kind IN ('http', 'crash', 'job')),
    title         text NOT NULL,
    count         integer NOT NULL DEFAULT 1,
    first_seen    timestamptz NOT NULL DEFAULT now(),
    last_seen     timestamptz NOT NULL DEFAULT now(),
    evidence      text NOT NULL DEFAULT '',
    status        text NOT NULL,
    diagnosis     text NOT NULL DEFAULT '',
    fix           text NOT NULL DEFAULT '',
    note          text NOT NULL DEFAULT '',
    evolution_id  text NOT NULL DEFAULT '',
    fixed_at      timestamptz,
    resolved_at   timestamptz
);
-- One live incident per signature (resolved and ignored ones are history).
CREATE UNIQUE INDEX incidents_live_signature ON incidents (signature) WHERE status NOT IN ('resolved', 'ignored');
CREATE INDEX incidents_recent ON incidents (last_seen DESC);
