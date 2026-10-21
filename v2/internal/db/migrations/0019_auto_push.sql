-- 自动推送草稿箱: plan-written articles that pass the checks are pushed to the WeChat draft box by
-- the worker (off by default). auto_push_at marks the one attempt per article.
ALTER TABLE wx_account ADD COLUMN auto_push BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE article ADD COLUMN auto_push_at TIMESTAMPTZ;
