-- P2 authorized reference images: pictures the user supplies by URL (fetched with SSRF protection)
-- after confirming they may use them. An account keeps a small library; an article can use one
-- for its cover or an image slot. The confirmation is recorded with the picture.
CREATE TABLE ref_image (
    id            BIGSERIAL PRIMARY KEY,
    key           TEXT NOT NULL UNIQUE,                 -- preview URL /api/media/{key}
    user_id       BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    account_id    BIGINT NOT NULL REFERENCES wx_account(id) ON DELETE CASCADE,
    source_url    TEXT NOT NULL,
    caption       TEXT NOT NULL DEFAULT '',
    mime          TEXT NOT NULL,
    data          BYTEA NOT NULL,
    width         INT NOT NULL,
    height        INT NOT NULL,
    statement     TEXT NOT NULL,                        -- the authorization statement the user ticked
    authorized_at TIMESTAMPTZ NOT NULL,
    used          INT NOT NULL DEFAULT 0,
    last_used_at  TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ref_image_account ON ref_image (account_id, id);

ALTER TABLE article_image ADD COLUMN ref_id BIGINT REFERENCES ref_image(id) ON DELETE SET NULL; -- from the user's library
