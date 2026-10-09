-- Trying an evolution before it goes live: whether it waits for its owner,
-- and how its preview is going.
ALTER TABLE evolutions ADD COLUMN preview jsonb;
