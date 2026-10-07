-- Operational memory of the Seed kernel.

CREATE TABLE conversations (
    id          text PRIMARY KEY,
    title       text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE messages (
    id              text PRIMARY KEY,
    conversation_id text NOT NULL REFERENCES conversations(id),
    role            text NOT NULL CHECK (role IN ('user', 'seed', 'system')),
    content         text NOT NULL,
    evolution_id    text,
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX messages_conversation_idx ON messages (conversation_id, created_at);

CREATE TABLE evolutions (
    id              text PRIMARY KEY,
    conversation_id text,
    kind            text NOT NULL DEFAULT 'evolve',
    intent          text NOT NULL,
    title           text NOT NULL DEFAULT '',
    status          text NOT NULL,
    plan            jsonb,
    base_generation int NOT NULL DEFAULT 0,
    new_generation  int,
    base_commit     text NOT NULL DEFAULT '',
    branch          text NOT NULL DEFAULT '',
    worktree        text NOT NULL DEFAULT '',
    commit          text NOT NULL DEFAULT '',
    attempts        int NOT NULL DEFAULT 0,
    checks          jsonb NOT NULL DEFAULT '[]',
    reflection      jsonb,
    summary         text NOT NULL DEFAULT '',
    error           text NOT NULL DEFAULT '',
    target_generation int,
    usage           jsonb NOT NULL DEFAULT '{}',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    completed_at    timestamptz
);
CREATE INDEX evolutions_status_idx ON evolutions (status);

CREATE TABLE evolution_events (
    id           bigserial PRIMARY KEY,
    evolution_id text NOT NULL REFERENCES evolutions(id),
    kind         text NOT NULL,
    summary      text NOT NULL,
    data         jsonb,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX evolution_events_evo_idx ON evolution_events (evolution_id, id);

CREATE TABLE generations (
    number        int PRIMARY KEY,
    title         text NOT NULL,
    intent        text NOT NULL DEFAULT '',
    evolution_id  text,
    commit        text NOT NULL,
    parent_commit text NOT NULL DEFAULT '',
    tests_passed  int,
    build_ok      boolean,
    health_ok     boolean,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE approvals (
    id           text PRIMARY KEY,
    evolution_id text,
    action       text NOT NULL,
    level        text NOT NULL,
    detail       text NOT NULL DEFAULT '',
    status       text NOT NULL DEFAULT 'pending',
    created_at   timestamptz NOT NULL DEFAULT now(),
    decided_at   timestamptz
);

CREATE TABLE settings (
    key   text PRIMARY KEY,
    value text NOT NULL
);
