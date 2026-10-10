-- Browsers my owner turned notifications on in (Web Push subscriptions).
CREATE TABLE push_subscriptions (
    id           text PRIMARY KEY,
    endpoint     text NOT NULL UNIQUE,
    p256dh       text NOT NULL,
    auth         text NOT NULL,
    user_agent   text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_sent_at timestamptz,
    last_error   text NOT NULL DEFAULT ''
);
