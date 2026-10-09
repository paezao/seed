-- What my model calls cost: one row per call, labelled by what it was for.
CREATE TABLE spend (
    id                 bigserial PRIMARY KEY,
    at                 timestamptz NOT NULL DEFAULT now(),
    kind               text NOT NULL,
    ref                text NOT NULL DEFAULT '',
    model              text NOT NULL DEFAULT '',
    input_tokens       integer NOT NULL DEFAULT 0,
    output_tokens      integer NOT NULL DEFAULT 0,
    cache_read_tokens  integer NOT NULL DEFAULT 0,
    cache_write_tokens integer NOT NULL DEFAULT 0,
    cost_usd           double precision NOT NULL DEFAULT 0,
    priced             boolean NOT NULL DEFAULT false
);
CREATE INDEX spend_at ON spend (at);
