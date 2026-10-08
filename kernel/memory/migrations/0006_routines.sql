-- Things I do on a schedule: agent routines (a prompt I run) and jobs (a
-- request to my organism), from my owner or declared by my organism.
CREATE TABLE routines (
    id          text PRIMARY KEY,
    name        text NOT NULL,
    kind        text NOT NULL CHECK (kind IN ('agent', 'job')),
    source      text NOT NULL CHECK (source IN ('owner', 'organism')),
    schedule    text NOT NULL,
    timezone    text NOT NULL DEFAULT 'UTC',
    prompt      text NOT NULL DEFAULT '',
    method      text NOT NULL DEFAULT '',
    path        text NOT NULL DEFAULT '',
    description text NOT NULL DEFAULT '',
    enabled     boolean NOT NULL DEFAULT true,
    next_run_at timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX routines_organism_name ON routines (name) WHERE source = 'organism';

CREATE TABLE routine_runs (
    id          text PRIMARY KEY,
    routine_id  text NOT NULL REFERENCES routines (id) ON DELETE CASCADE,
    trigger     text NOT NULL,
    status      text NOT NULL,
    output      text NOT NULL DEFAULT '',
    started_at  timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz
);
CREATE INDEX routine_runs_recent ON routine_runs (routine_id, started_at DESC);
