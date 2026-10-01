-- +goose Up
CREATE TABLE projects (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    name               TEXT NOT NULL UNIQUE,
    repo_path          TEXT NOT NULL UNIQUE,
    base_ref           TEXT NOT NULL DEFAULT 'origin/main',
    enabled_work_types TEXT NOT NULL DEFAULT '["bug_fix","feature","refactor","docs"]',
    is_archived        INTEGER NOT NULL DEFAULT 0,
    created_at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE jobs (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id    INTEGER NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
    work_type     TEXT NOT NULL,
    title         TEXT NOT NULL,
    intent        TEXT NOT NULL DEFAULT '',
    source        TEXT NOT NULL DEFAULT 'dashboard',
    source_ref    TEXT NOT NULL DEFAULT '',
    stage         TEXT NOT NULL DEFAULT '01_Intent',
    status        TEXT NOT NULL DEFAULT 'queued',
    branch_name   TEXT NOT NULL DEFAULT '',
    worktree_path TEXT NOT NULL DEFAULT '',
    base_sha      TEXT NOT NULL DEFAULT '',
    head_sha      TEXT NOT NULL DEFAULT '',
    pr_url        TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX idx_jobs_project_id ON jobs(project_id);
CREATE INDEX idx_jobs_status ON jobs(status);
CREATE INDEX idx_jobs_stage ON jobs(stage);

CREATE TABLE step_runs (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id           INTEGER NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    stage            TEXT NOT NULL,
    kind             TEXT NOT NULL,
    attempt          INTEGER NOT NULL DEFAULT 1,
    executor         TEXT NOT NULL,
    status           TEXT NOT NULL DEFAULT 'running',
    failure_category TEXT NOT NULL DEFAULT '',
    exit_code        INTEGER,
    log_path         TEXT NOT NULL DEFAULT '',
    started_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    ended_at         TEXT
);

CREATE INDEX idx_step_runs_job_id ON step_runs(job_id);

CREATE TABLE events (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id     INTEGER NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    type       TEXT NOT NULL,
    payload    TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX idx_events_job_id ON events(job_id);
CREATE INDEX idx_events_id ON events(id);

CREATE TABLE approvals (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id     INTEGER NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    gate       TEXT NOT NULL,
    decision   TEXT NOT NULL,
    note       TEXT NOT NULL DEFAULT '',
    head_sha   TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX idx_approvals_job_id ON approvals(job_id);

CREATE TABLE sessions (
    id         TEXT PRIMARY KEY,
    token_hash TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE process_records (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    step_run_id INTEGER NOT NULL REFERENCES step_runs(id) ON DELETE CASCADE,
    pid         INTEGER NOT NULL,
    pgid        INTEGER NOT NULL,
    start_time  INTEGER NOT NULL,
    active      INTEGER NOT NULL DEFAULT 1,
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX idx_process_records_active ON process_records(active);

UPDATE _meta SET value = '2' WHERE key = 'schema_version';

-- +goose Down
UPDATE _meta SET value = '1' WHERE key = 'schema_version';
DROP TABLE IF EXISTS process_records;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS approvals;
DROP TABLE IF EXISTS events;
DROP TABLE IF EXISTS step_runs;
DROP TABLE IF EXISTS jobs;
DROP TABLE IF EXISTS projects;
