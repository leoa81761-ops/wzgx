-- P1-6: operations — prices and spend caps, cost per call, manual plan activation.

-- optional prices (yuan per million tokens) and daily spend cap (yuan) per model config; NULL = not set / off
ALTER TABLE service_config ADD COLUMN price_in_per_m  NUMERIC(12,4) CHECK (price_in_per_m IS NULL OR price_in_per_m >= 0);
ALTER TABLE service_config ADD COLUMN price_out_per_m NUMERIC(12,4) CHECK (price_out_per_m IS NULL OR price_out_per_m >= 0);
ALTER TABLE service_config ADD COLUMN daily_cap       NUMERIC(12,2) CHECK (daily_cap IS NULL OR daily_cap > 0);

-- estimated cost of each attempt at the price in force when it ran
ALTER TABLE model_call ADD COLUMN cost NUMERIC(14,6) NOT NULL DEFAULT 0;
CREATE INDEX model_call_day_idx ON model_call (created_at, config_id);

-- manual plan activation (no payment yet): what was granted, by whom
ALTER TABLE users ADD COLUMN tier_expires_at TIMESTAMPTZ;
CREATE TABLE plan_order (
    id            BIGSERIAL PRIMARY KEY,
    user_id       BIGINT NOT NULL REFERENCES users(id),
    tier          TEXT NOT NULL,
    max_accounts  INT NOT NULL CHECK (max_accounts >= 0),
    daily_quota   INT NOT NULL CHECK (daily_quota >= 0),
    months        INT CHECK (months IS NULL OR months > 0),   -- NULL = no end date
    expires_at    TIMESTAMPTZ,
    amount        NUMERIC(12,2),                               -- recorded only; nothing is charged
    note          TEXT NOT NULL DEFAULT '',
    created_by    BIGINT NOT NULL REFERENCES users(id),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX plan_order_user_idx ON plan_order (user_id, id DESC);
