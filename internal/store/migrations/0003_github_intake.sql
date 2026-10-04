-- +goose Up
CREATE TABLE intake_seen (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id   INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    source       TEXT NOT NULL,          -- 'intent_file' | 'github_issue'
    ref          TEXT NOT NULL,          -- relative file path | 'owner/repo#123'
    content_hash TEXT NOT NULL DEFAULT '',
    job_id       INTEGER NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (project_id, source, ref)
);

CREATE INDEX idx_intake_seen_project ON intake_seen(project_id);
CREATE INDEX idx_intake_seen_job ON intake_seen(job_id);

CREATE TABLE intake_errors (
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    source     TEXT NOT NULL,              -- 'intent_file' | 'github_issue' | 'provider'
    ref        TEXT NOT NULL,              -- file path, issue ref, or project key
    message    TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (project_id, source, ref)
);

CREATE INDEX idx_intake_errors_project ON intake_errors(project_id);

CREATE TABLE github_feedback (
    job_id        INTEGER PRIMARY KEY REFERENCES jobs(id) ON DELETE CASCADE,
    applied_state TEXT NOT NULL,           -- e.g. 'in-progress', 'needs-clarification', 'needs-approval', 'delivered'
    updated_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

UPDATE _meta SET value = '3' WHERE key = 'schema_version';

-- +goose Down
UPDATE _meta SET value = '2' WHERE key = 'schema_version';
DROP TABLE IF EXISTS github_feedback;
DROP TABLE IF EXISTS intake_errors;
DROP TABLE IF EXISTS intake_seen;
