-- P1-5: public pages, pricing tiers, export / deletion, complaints, invites

-- tiers shown on the pricing page and used when activating orders (admin-editable)
CREATE TABLE plan_tier (
    key           TEXT PRIMARY KEY CHECK (key IN ('free','standard','premium','buyout','custom')),
    name          TEXT NOT NULL,
    price_text    TEXT NOT NULL DEFAULT '',     -- shown as is, e.g. "¥99 / 月"; empty = 联系管理员
    max_accounts  INT  NOT NULL DEFAULT 1 CHECK (max_accounts >= 0),
    daily_quota   INT  NOT NULL DEFAULT 1 CHECK (daily_quota >= 0),
    description   TEXT NOT NULL DEFAULT '',
    visible       BOOLEAN NOT NULL DEFAULT TRUE,
    sort          INT  NOT NULL DEFAULT 0,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO plan_tier (key, name, max_accounts, daily_quota, description, visible, sort) VALUES
  ('free',     '免费版', 1, 1,  '绑定 1 个公众号，每天写 1 篇，适合先试试', TRUE, 1),
  ('standard', '标准版', 2, 3,  '绑定 2 个公众号，每天共写 3 篇', TRUE, 2),
  ('premium',  '高级版', 5, 10, '绑定 5 个公众号，每天共写 10 篇', TRUE, 3),
  ('buyout',   '买断版', 3, 5,  '一次付费长期使用', FALSE, 4),
  ('custom',   '定制版', 10, 20, '按需定制，联系管理员', TRUE, 5);

ALTER TABLE users
    ADD COLUMN delete_requested_at TIMESTAMPTZ,      -- account deletion pending; logging in cancels it
    ADD COLUMN deleted_at          TIMESTAMPTZ,      -- data removed, row kept anonymised for orders / audit
    ADD COLUMN invite_code         TEXT,
    ADD COLUMN invited_by          BIGINT REFERENCES users(id),
    ADD COLUMN reg_ip              TEXT NOT NULL DEFAULT '',
    ADD COLUMN reg_device          TEXT NOT NULL DEFAULT '',
    ADD COLUMN bonus_articles      INT  NOT NULL DEFAULT 0 CHECK (bonus_articles >= 0);
CREATE UNIQUE INDEX users_invite_code_uq ON users (invite_code) WHERE invite_code IS NOT NULL;

ALTER TABLE job ADD COLUMN used_bonus BOOLEAN NOT NULL DEFAULT FALSE;

-- one row per invited registration
CREATE TABLE invite (
    invitee_id   BIGINT PRIMARY KEY REFERENCES users(id),
    inviter_id   BIGINT NOT NULL REFERENCES users(id),
    status       TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','valid','rejected')),
    reason       TEXT NOT NULL DEFAULT '',
    bonus        INT  NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    decided_at   TIMESTAMPTZ
);
CREATE INDEX invite_inviter_idx ON invite (inviter_id);

CREATE TABLE complaint (
    id          BIGSERIAL PRIMARY KEY,
    kind        TEXT NOT NULL,
    content     TEXT NOT NULL,
    link        TEXT NOT NULL DEFAULT '',
    contact     TEXT NOT NULL DEFAULT '',
    ip          TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'new' CHECK (status IN ('new','handled')),
    note        TEXT NOT NULL DEFAULT '',
    handled_by  BIGINT REFERENCES users(id),
    handled_at  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX complaint_ip_idx ON complaint (ip, created_at);
