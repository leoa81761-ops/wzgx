-- P2 personal style: traits extracted from the user's sample articles. Only the traits are kept;
-- the sample text is never stored (lesson 6).
CREATE TABLE style_profile (
    id          BIGSERIAL PRIMARY KEY,
    account_id  BIGINT NOT NULL REFERENCES wx_account(id) ON DELETE CASCADE,
    column_id   BIGINT REFERENCES account_column(id) ON DELETE CASCADE, -- NULL = whole account
    traits      JSONB NOT NULL,
    samples     INT NOT NULL,                                      -- how many samples it came from
    enabled     BOOLEAN NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX style_profile_scope ON style_profile (account_id, COALESCE(column_id, 0));
