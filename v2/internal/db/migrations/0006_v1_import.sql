-- which v1 rows the one-off v1 → v2 import (cmd/migrate-v1) has already brought over, so it can be rerun
CREATE TABLE v1_import (
    kind      TEXT NOT NULL,          -- user | account | column | plan | config | tavily
    old_id    BIGINT NOT NULL,
    new_id    BIGINT NOT NULL,
    at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (kind, old_id)
);
