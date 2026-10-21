-- P1-4: auto-publish for verified accounts (off unless the user turns it on)
ALTER TABLE wx_account
    ADD COLUMN auto_publish    BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN review_minutes  INT NOT NULL DEFAULT 120 CHECK (review_minutes BETWEEN 10 AND 10080),
    ADD COLUMN publish_cap     INT NOT NULL DEFAULT 1 CHECK (publish_cap BETWEEN 1 AND 5);
ALTER TABLE users ADD COLUMN publish_paused BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE article
    ADD COLUMN publish_status       TEXT NOT NULL DEFAULT ''
        CHECK (publish_status IN ('','scheduled','cancelled','submitting','publishing','published','failed')),
    ADD COLUMN publish_at           TIMESTAMPTZ,   -- end of the review window
    ADD COLUMN publish_submitted_at TIMESTAMPTZ,   -- freepublish/submit was attempted (at most once)
    ADD COLUMN publish_id           TEXT NOT NULL DEFAULT '',
    ADD COLUMN publish_url          TEXT NOT NULL DEFAULT '',
    ADD COLUMN publish_note         TEXT NOT NULL DEFAULT '',
    ADD COLUMN published_at         TIMESTAMPTZ,
    ADD COLUMN publish_checked_at   TIMESTAMPTZ;
CREATE INDEX article_publish_due_idx ON article (publish_status, publish_at) WHERE publish_status IN ('scheduled','publishing','submitting');
