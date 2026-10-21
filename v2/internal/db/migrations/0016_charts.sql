-- P2 data charts: bar chart pictures drawn from numeric comparison tables, stored with the pictures.
ALTER TABLE article_image ADD COLUMN kind TEXT NOT NULL DEFAULT 'picture' CHECK (kind IN ('picture','chart'));
ALTER TABLE article_image ADD COLUMN block INT; -- charts: the table block they follow
