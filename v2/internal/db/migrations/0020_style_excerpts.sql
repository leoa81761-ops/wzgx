-- 范文片段: with the user's explicit consent, up to two excerpts of their sample articles are kept
-- and shown to the writer as a style reference (只学语气和写法，不抄内容). Empty = traits only.
ALTER TABLE style_profile ADD COLUMN excerpts JSONB NOT NULL DEFAULT '[]'::jsonb;

-- 智能体写作: plan first, then write, then compare the draft with the account's own settings and
-- excerpts and revise once (two more model calls; off by default).
ALTER TABLE account_profile ADD COLUMN agent_mode BOOLEAN NOT NULL DEFAULT FALSE;
