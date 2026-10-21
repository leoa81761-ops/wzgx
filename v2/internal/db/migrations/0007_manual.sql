-- P1-1: the user's manual controls.

-- pause / resume a whole account; 'auto' = paused by the untouched-drafts rule
ALTER TABLE wx_account ADD COLUMN paused BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE wx_account ADD COLUMN paused_reason TEXT NOT NULL DEFAULT '' CHECK (paused_reason IN ('', 'user', 'auto'));
ALTER TABLE wx_account ADD COLUMN paused_at TIMESTAMPTZ;
ALTER TABLE wx_account ADD COLUMN resumed_at TIMESTAMPTZ;
-- topic confirmation: the job stops after the topic stage until the user approves, changes or rejects it
ALTER TABLE wx_account ADD COLUMN confirm_topic BOOLEAN NOT NULL DEFAULT FALSE;

-- a job can wait for the user's topic decision
ALTER TABLE job DROP CONSTRAINT job_status_check;
ALTER TABLE job ADD CONSTRAINT job_status_check CHECK (status IN ('queued','running','awaiting_topic','done','failed','cancelled'));
ALTER TABLE job ADD COLUMN topic TEXT NOT NULL DEFAULT '';          -- fixed topic: given by the user or approved; skips the topic stage
ALTER TABLE job ADD COLUMN topic_kind TEXT NOT NULL DEFAULT '';     -- carried over from an approved proposal (news | practical)
ALTER TABLE job ADD COLUMN proposal JSONB;                          -- the topic waiting for a decision (topic, angle, search_query, kind, events)
ALTER TABLE job ADD COLUMN rejected TEXT[] NOT NULL DEFAULT '{}';   -- topics the user rejected for this job
ALTER TABLE job ADD COLUMN source TEXT NOT NULL DEFAULT 'manual' CHECK (source IN ('manual', 'plan'));

-- what we put in the draft box, and what read-back found later
ALTER TABLE article ADD COLUMN pushed_content TEXT NOT NULL DEFAULT '';
ALTER TABLE article ADD COLUMN readback_status TEXT NOT NULL DEFAULT '' CHECK (readback_status IN ('', 'unedited', 'edited', 'gone', 'error'));
ALTER TABLE article ADD COLUMN change_ratio REAL;                    -- rough share of text changed, 0..1
ALTER TABLE article ADD COLUMN readback_at TIMESTAMPTZ;
ALTER TABLE article ADD COLUMN readback_note TEXT NOT NULL DEFAULT '';
CREATE INDEX article_readback_idx ON article (status, pushed_at) WHERE status = 'pushed';

-- thumbs up / down plus an optional note, one per article
CREATE TABLE article_feedback (
    article_id  BIGINT PRIMARY KEY REFERENCES article(id) ON DELETE CASCADE,
    user_id     BIGINT NOT NULL REFERENCES users(id),
    rating      SMALLINT NOT NULL CHECK (rating IN (-1, 1)),
    note        TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- small platform settings edited in the admin console
CREATE TABLE platform_setting (
    key         TEXT PRIMARY KEY,
    value       JSONB NOT NULL,
    updated_by  BIGINT REFERENCES users(id),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
