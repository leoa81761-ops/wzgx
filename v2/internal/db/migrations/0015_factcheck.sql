-- P2 fact check: optional, per account, default off. Results are shown to the user only.
ALTER TABLE account_profile ADD COLUMN fact_check BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE article ADD COLUMN fact_check JSONB; -- NULL = not checked
