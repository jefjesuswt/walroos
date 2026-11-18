-- M3.1: apps deployed from a git repo's Dockerfile to a prod node, and their deployments.

CREATE TABLE apps (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,                -- DNS label; containers are <name>-<deployment id>
    node_id     TEXT NOT NULL REFERENCES nodes (id), -- RESTRICT: a node with apps can't be removed
    repo_url    TEXT NOT NULL,
    branch      TEXT NOT NULL DEFAULT '',            -- '' = the repo's default branch
    dockerfile  TEXT NOT NULL DEFAULT '',            -- '' = Dockerfile
    port        INTEGER NOT NULL,                    -- what the container listens on
    health_path TEXT NOT NULL DEFAULT '',            -- '' = /
    created_at  DATETIME NOT NULL
);

CREATE TABLE app_env (
    app_id TEXT NOT NULL REFERENCES apps (id) ON DELETE CASCADE,
    key    TEXT NOT NULL,
    value  TEXT NOT NULL,                            -- base64(nonce || ciphertext), like workspace_env
    secret INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (app_id, key)
);

CREATE TABLE deployments (
    id          TEXT PRIMARY KEY,                    -- 16 hex chars; also the container suffix
    app_id      TEXT NOT NULL REFERENCES apps (id) ON DELETE CASCADE,
    sha         TEXT NOT NULL DEFAULT '',            -- requested ('' = branch head), then the deployed one
    image       TEXT NOT NULL DEFAULT '',            -- set on rollback (run it, no build), else the built one
    state       TEXT NOT NULL CHECK (state IN ('queued', 'running', 'succeeded', 'failed')),
    error       TEXT NOT NULL DEFAULT '',
    log         TEXT NOT NULL DEFAULT '',
    created_at  DATETIME NOT NULL,
    finished_at DATETIME
);

CREATE INDEX deployments_app_id ON deployments (app_id, created_at);
