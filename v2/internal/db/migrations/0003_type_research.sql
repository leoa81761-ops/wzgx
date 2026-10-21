-- per article type: whether the pipeline searches for material before writing (是否检索资料)
ALTER TABLE article_type ADD COLUMN research BOOLEAN NOT NULL DEFAULT TRUE;
-- news-type columns: 'news' when the topic is a real recent event, 'practical' when the pre-scan
-- found none and the topic fell back to a non-news one; NULL for other types
ALTER TABLE article ADD COLUMN topic_kind TEXT CHECK (topic_kind IN ('news','practical'));
