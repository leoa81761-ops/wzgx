-- draft push bookkeeping; pushes happen only on an explicit user action
ALTER TABLE article ADD COLUMN push_error TEXT NOT NULL DEFAULT '';
ALTER TABLE article ADD COLUMN pushed_at TIMESTAMPTZ;
