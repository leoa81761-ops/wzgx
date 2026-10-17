-- scheduled writing: on the chosen weekdays at a time of day (Beijing time) the worker queues one
-- dry-run job for the account (a fixed column, or the account's usual column pick). Never pushes.
CREATE TABLE write_plan (
    id           BIGSERIAL PRIMARY KEY,
    user_id      BIGINT NOT NULL REFERENCES users(id),
    account_id   BIGINT NOT NULL REFERENCES wx_account(id),
    column_id    BIGINT REFERENCES account_column(id) ON DELETE SET NULL,
    weekdays     SMALLINT[] NOT NULL,                 -- 1 = Monday … 7 = Sunday
    at_minute    INT NOT NULL CHECK (at_minute BETWEEN 0 AND 1439),
    enabled      BOOLEAN NOT NULL DEFAULT TRUE,
    last_run_on  DATE,
    last_result  TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX write_plan_due ON write_plan (enabled, at_minute);
