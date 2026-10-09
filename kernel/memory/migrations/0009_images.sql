-- Images my owner attached (e.g. a screenshot of what they pointed at), and
-- which messages and evolutions carry them.
CREATE TABLE images (
    id         text PRIMARY KEY,
    media_type text NOT NULL,
    data       bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE messages ADD COLUMN images text[] NOT NULL DEFAULT '{}';
ALTER TABLE evolutions ADD COLUMN images text[] NOT NULL DEFAULT '{}';
