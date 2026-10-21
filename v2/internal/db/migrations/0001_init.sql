-- 公号智写 v2 initial schema. Written from scratch; see /workspace/quality/rebuild_plan.md §4.3.

-- ---------- people ----------
CREATE TABLE users (
    id             BIGSERIAL PRIMARY KEY,
    email          TEXT NOT NULL,
    phone          TEXT,
    password_hash  TEXT NOT NULL,
    display_name   TEXT NOT NULL DEFAULT '',
    -- user: binds Official Accounts. operator / super_admin: admin console only, never bind accounts.
    role           TEXT NOT NULL DEFAULT 'user' CHECK (role IN ('user','operator','super_admin')),
    tier           TEXT NOT NULL DEFAULT 'free',          -- free / standard / premium / buyout / custom
    max_accounts   INT  NOT NULL DEFAULT 1 CHECK (max_accounts >= 0),
    quota_exempt   BOOLEAN NOT NULL DEFAULT FALSE,
    status         TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled')),
    created_by     BIGINT REFERENCES users(id),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX users_email_uq ON users (lower(email));
CREATE UNIQUE INDEX users_phone_uq ON users (phone) WHERE phone IS NOT NULL;

CREATE TABLE sessions (
    token_hash  BYTEA PRIMARY KEY,                 -- sha256 of the cookie token
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL,
    ip          TEXT NOT NULL DEFAULT '',
    user_agent  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX sessions_user_idx ON sessions (user_id);

CREATE TABLE consent_log (
    id          BIGSERIAL PRIMARY KEY,
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    doc_type    TEXT NOT NULL,
    version     TEXT NOT NULL,
    ip          TEXT NOT NULL DEFAULT '',
    device      TEXT NOT NULL DEFAULT '',
    agreed_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at  TIMESTAMPTZ
);

-- user's own third-party keys (only Tavily for now). Encrypted, masked.
CREATE TABLE user_secret (
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind        TEXT NOT NULL CHECK (kind IN ('tavily')),
    cipher      TEXT NOT NULL,
    mask        TEXT NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, kind)
);

-- ---------- Official Accounts and columns ----------
CREATE TABLE wx_account (
    id             BIGSERIAL PRIMARY KEY,
    user_id        BIGINT NOT NULL REFERENCES users(id),
    name           TEXT NOT NULL,
    app_id         TEXT NOT NULL,
    secret_cipher  TEXT NOT NULL,
    secret_mask    TEXT NOT NULL,
    subject_type   TEXT NOT NULL DEFAULT 'personal',   -- personal / enterprise
    verified       BOOLEAN NOT NULL DEFAULT FALSE,
    bind_status    TEXT NOT NULL DEFAULT 'bound' CHECK (bind_status IN ('bound','unbound')),
    check_report   JSONB NOT NULL DEFAULT '{}'::jsonb,
    checked_at     TIMESTAMPTZ,
    unbound_at     TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- one Official Account can be bound by only one user at a time
CREATE UNIQUE INDEX wx_account_app_bound_uq ON wx_account (app_id) WHERE bind_status = 'bound';
CREATE INDEX wx_account_user_idx ON wx_account (user_id);

-- Per-account writing settings. Free text, passed to the model as-is; the program adds no style rules.
CREATE TABLE account_profile (
    account_id       BIGINT PRIMARY KEY REFERENCES wx_account(id) ON DELETE CASCADE,
    positioning      TEXT NOT NULL DEFAULT '',
    persona          TEXT NOT NULL DEFAULT '',
    audience         TEXT NOT NULL DEFAULT '',
    tone             TEXT NOT NULL DEFAULT '',
    taboos           TEXT NOT NULL DEFAULT '',
    signature        TEXT NOT NULL DEFAULT '',   -- appended by the renderer, never written by the model
    opening          TEXT NOT NULL DEFAULT '',
    ending           TEXT NOT NULL DEFAULT '',
    ai_label         BOOLEAN NOT NULL DEFAULT FALSE,
    self_review      BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Article types are admin-managed. A column only picks a type; types are independent of each other.
CREATE TABLE article_type (
    id           BIGSERIAL PRIMARY KEY,
    key          TEXT NOT NULL UNIQUE,
    name         TEXT NOT NULL,
    description  TEXT NOT NULL DEFAULT '',   -- what this type of article is, shown to the model
    needs_news   BOOLEAN NOT NULL DEFAULT FALSE, -- topic should be a recent concrete event
    enabled      BOOLEAN NOT NULL DEFAULT TRUE,
    sort         INT NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE account_column (
    id               BIGSERIAL PRIMARY KEY,
    account_id       BIGINT NOT NULL REFERENCES wx_account(id) ON DELETE CASCADE,
    name             TEXT NOT NULL,
    article_type_id  BIGINT NOT NULL REFERENCES article_type(id),
    note             TEXT NOT NULL DEFAULT '',   -- optional free-text focus for this column
    length_hint      INT CHECK (length_hint IS NULL OR length_hint > 0), -- reference only, never enforced
    weight           INT NOT NULL DEFAULT 1 CHECK (weight > 0),
    enabled          BOOLEAN NOT NULL DEFAULT TRUE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX account_column_account_idx ON account_column (account_id);

-- ---------- model / service configuration (admin config center) ----------
CREATE TABLE service_config (
    id               BIGSERIAL PRIMARY KEY,
    kind             TEXT NOT NULL CHECK (kind IN ('text','search','image','notify','sms','pay')),
    name             TEXT NOT NULL,              -- display name only, never used in logic
    protocol         TEXT NOT NULL DEFAULT 'openai',
    base_url         TEXT NOT NULL DEFAULT '',
    key_cipher       TEXT NOT NULL DEFAULT '',
    key_mask         TEXT NOT NULL DEFAULT '',
    model            TEXT NOT NULL DEFAULT '',
    reasoning_effort TEXT NOT NULL DEFAULT '',   -- passed through as-is; empty = not sent
    params           JSONB NOT NULL DEFAULT '{}'::jsonb,
    first_byte_timeout_s INT NOT NULL DEFAULT 30  CHECK (first_byte_timeout_s > 0),
    idle_timeout_s       INT NOT NULL DEFAULT 180 CHECK (idle_timeout_s > 0),
    total_timeout_s      INT NOT NULL DEFAULT 900 CHECK (total_timeout_s > 0),
    priority         INT NOT NULL DEFAULT 100,
    enabled          BOOLEAN NOT NULL DEFAULT TRUE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- which configs serve which stage, in order (first = primary, rest = backups)
CREATE TABLE stage_route (
    stage       TEXT NOT NULL CHECK (stage IN ('topic','research','write','review')),
    tier        TEXT NOT NULL DEFAULT '*',
    config_ids  BIGINT[] NOT NULL,
    PRIMARY KEY (stage, tier)
);

-- ---------- jobs, articles, calls ----------
CREATE TABLE job (
    id           BIGSERIAL PRIMARY KEY,
    user_id      BIGINT NOT NULL REFERENCES users(id),
    account_id   BIGINT NOT NULL REFERENCES wx_account(id),
    column_id    BIGINT REFERENCES account_column(id),
    idem_key     TEXT NOT NULL UNIQUE,
    status       TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','running','done','failed','cancelled')),
    dry_run      BOOLEAN NOT NULL DEFAULT TRUE,
    run_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    lease_until  TIMESTAMPTZ,
    attempt      INT NOT NULL DEFAULT 0,
    failure      TEXT NOT NULL DEFAULT '',
    trace        JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at  TIMESTAMPTZ
);
CREATE INDEX job_pick_idx ON job (status, run_at);

CREATE TABLE article (
    id              BIGSERIAL PRIMARY KEY,
    job_id          BIGINT REFERENCES job(id),
    user_id         BIGINT NOT NULL REFERENCES users(id),
    account_id      BIGINT NOT NULL REFERENCES wx_account(id),
    column_id       BIGINT REFERENCES account_column(id),
    title           TEXT NOT NULL DEFAULT '',
    alt_titles      TEXT[] NOT NULL DEFAULT '{}',
    summary         TEXT NOT NULL DEFAULT '',
    body_json       JSONB NOT NULL DEFAULT '{}'::jsonb,
    html            TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','blocked','ready','pushed','failed')),
    block_reason    TEXT NOT NULL DEFAULT '',
    draft_media_id  TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX article_account_idx ON article (account_id, created_at DESC);

-- only official bases (national standards, MIIT / government red-header documents) are cited
CREATE TABLE article_source (
    article_id    BIGINT NOT NULL REFERENCES article(id) ON DELETE CASCADE,
    n             INT NOT NULL,
    kind          TEXT NOT NULL CHECK (kind IN ('national_standard','gov_document')),
    title         TEXT NOT NULL,
    issuer        TEXT NOT NULL DEFAULT '',
    doc_number    TEXT NOT NULL DEFAULT '',
    published_on  DATE,
    url           TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (article_id, n)
);

CREATE TABLE model_call (
    id             BIGSERIAL PRIMARY KEY,
    job_id         BIGINT REFERENCES job(id) ON DELETE SET NULL,
    stage          TEXT NOT NULL,
    config_id      BIGINT REFERENCES service_config(id) ON DELETE SET NULL,
    model          TEXT NOT NULL DEFAULT '',     -- actual model reported by the config / upstream
    effort         TEXT NOT NULL DEFAULT '',
    attempt        INT NOT NULL,
    outcome        TEXT NOT NULL,                -- ok / empty / timeout_first_byte / timeout_idle / timeout_total / http_xxx / error
    first_byte_ms  INT,
    total_ms       INT NOT NULL DEFAULT 0,
    finish_reason  TEXT NOT NULL DEFAULT '',
    tokens_in      INT NOT NULL DEFAULT 0,
    tokens_out     INT NOT NULL DEFAULT 0,
    error          TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX model_call_job_idx ON model_call (job_id);

-- ---------- audit ----------
CREATE TABLE audit_log (
    id        BIGSERIAL PRIMARY KEY,
    actor_id  BIGINT REFERENCES users(id),
    action    TEXT NOT NULL,
    target    TEXT NOT NULL DEFAULT '',
    detail    JSONB NOT NULL DEFAULT '{}'::jsonb,  -- never contains secrets
    at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
