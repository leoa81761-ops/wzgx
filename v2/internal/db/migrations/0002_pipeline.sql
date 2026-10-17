-- per-user daily article quota (quota_exempt users skip it)
ALTER TABLE users ADD COLUMN daily_quota INT NOT NULL DEFAULT 1 CHECK (daily_quota >= 0);

-- platform sensitive-word list, matched by the hard gate (block only, never rewrite)
CREATE TABLE sensitive_word (
    word        TEXT PRIMARY KEY,
    created_by  BIGINT REFERENCES users(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE article ADD COLUMN topic TEXT NOT NULL DEFAULT '';
ALTER TABLE job ADD COLUMN topic_hint TEXT NOT NULL DEFAULT '';
CREATE INDEX job_user_day_idx ON job (user_id, created_at);
