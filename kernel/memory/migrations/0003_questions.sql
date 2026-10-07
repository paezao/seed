-- Human in the loop: questions an evolution is waiting on, and the
-- question/answer rounds it has had with its owner.
ALTER TABLE evolutions ADD COLUMN questions jsonb;
ALTER TABLE evolutions ADD COLUMN clarifications jsonb NOT NULL DEFAULT '[]';
