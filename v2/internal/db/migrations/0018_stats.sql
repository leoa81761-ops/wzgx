-- P2 analytics for verified accounts: daily datacube article stats (getarticletotaldetail).
ALTER TABLE wx_account
    ADD COLUMN stats_weighting  BOOLEAN NOT NULL DEFAULT FALSE, -- let reads gently weight column rotation (off by default)
    ADD COLUMN stats_fetched_on DATE,                           -- Beijing date of the last daily pull
    ADD COLUMN stats_at         TIMESTAMPTZ,
    ADD COLUMN stats_note       TEXT NOT NULL DEFAULT '',
    ADD COLUMN rotation_note    TEXT NOT NULL DEFAULT '',        -- what the weighting did on the last column pick
    ADD COLUMN rotation_at      TIMESTAMPTZ;
ALTER TABLE article
    ADD COLUMN wx_msgid    TEXT NOT NULL DEFAULT '', -- matched datacube article
    ADD COLUMN wx_ref_date DATE;                     -- its publish date in datacube
-- cumulative totals from the publish date up to stat_date (as datacube reports them)
CREATE TABLE article_stat_day (
    article_id      BIGINT NOT NULL REFERENCES article(id) ON DELETE CASCADE,
    stat_date       DATE NOT NULL,
    read_user       INT NOT NULL DEFAULT 0,
    share_user      INT NOT NULL DEFAULT 0,
    collection_user INT NOT NULL DEFAULT 0,
    like_user       INT NOT NULL DEFAULT 0,
    zaikan_user     INT NOT NULL DEFAULT 0,
    finish_rate     REAL NOT NULL DEFAULT 0,
    PRIMARY KEY (article_id, stat_date)
);
