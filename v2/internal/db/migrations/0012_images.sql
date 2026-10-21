-- P1-3 images: an 'image' stage route (image configs only), AI covers and inline images stored in
-- the DB and served for preview, and a per-tier switch (free tier off by default).
ALTER TABLE stage_route DROP CONSTRAINT IF EXISTS stage_route_stage_check;
ALTER TABLE stage_route ADD CONSTRAINT stage_route_stage_check CHECK (stage IN ('topic','research','write','review','image'));

CREATE TABLE article_image (
    id          BIGSERIAL PRIMARY KEY,
    key         TEXT NOT NULL UNIQUE,                 -- random, used in the preview URL /api/media/{key}
    job_id      BIGINT REFERENCES job(id) ON DELETE SET NULL,
    article_id  BIGINT REFERENCES article(id) ON DELETE CASCADE,
    slot        INT  NOT NULL CHECK (slot BETWEEN 0 AND 3), -- 0 = cover, 1..3 = inline image slots in order
    mime        TEXT NOT NULL,
    data        BYTEA NOT NULL,
    width       INT  NOT NULL DEFAULT 0,
    height      INT  NOT NULL DEFAULT 0,
    prompt      TEXT NOT NULL DEFAULT '',
    config_id   BIGINT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX article_image_article ON article_image (article_id, slot);
CREATE INDEX article_image_orphan ON article_image (created_at) WHERE article_id IS NULL;

ALTER TABLE article ADD COLUMN ai_images BOOLEAN NOT NULL DEFAULT FALSE; -- rendered with AI images (empty slots dropped)

ALTER TABLE plan_tier ADD COLUMN ai_images BOOLEAN NOT NULL DEFAULT TRUE;
UPDATE plan_tier SET ai_images = FALSE WHERE key = 'free';

-- image configs already imported from v1 (old config #8 grok-imagine) serve the new image route
INSERT INTO stage_route (stage, tier, config_ids)
SELECT 'image', '*', array_agg(id ORDER BY priority, id) FROM service_config WHERE kind = 'image' AND enabled
HAVING count(*) > 0
ON CONFLICT (stage, tier) DO NOTHING;
