-- What my owner allowed my live organism to reach and use: hosts it may
-- connect to (HTTPS) and secrets (by name) it may read from its environment.
CREATE TABLE egress_grants (
    kind       text NOT NULL CHECK (kind IN ('host', 'secret')),
    value      text NOT NULL,
    reason     text NOT NULL DEFAULT '',
    granted_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (kind, value)
);
