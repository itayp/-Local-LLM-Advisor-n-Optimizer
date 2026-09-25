-- One row per watch run (build-plan step 10): the daily scheduler, or a
-- manual check, with its report. Mirrors catalog_refreshes (migration
-- 0003): plain columns for what a list screen queries, report_json for the
-- full entry list underneath.
CREATE TABLE watch_runs (
    id          INTEGER PRIMARY KEY,
    created_at  TEXT NOT NULL,               -- when it started
    finished_at TEXT NOT NULL,
    trigger     TEXT NOT NULL,               -- scheduler | api | cli
    checked     INTEGER NOT NULL DEFAULT 0,
    notified    INTEGER NOT NULL DEFAULT 0,
    suppressed  INTEGER NOT NULL DEFAULT 0,
    flagged     INTEGER NOT NULL DEFAULT 0,
    report_json TEXT NOT NULL DEFAULT '{}'
);
