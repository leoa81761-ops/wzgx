-- P1-2: per-account layout template (render only; never sent to the model)
ALTER TABLE wx_account ADD COLUMN layout TEXT NOT NULL DEFAULT 'classic'
    CHECK (layout IN ('classic','fresh','warm','plain'));
ALTER TABLE article ADD COLUMN layout TEXT NOT NULL DEFAULT 'classic';
