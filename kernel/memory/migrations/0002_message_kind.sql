-- Distinguish the chat agent's own replies from reports the kernel writes
-- (evolution results). The chat agent sees reports as kernel records, so it
-- never learns to imitate them.
ALTER TABLE messages ADD COLUMN kind text NOT NULL DEFAULT 'chat';
