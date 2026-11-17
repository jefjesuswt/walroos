-- M1: SSH keys (for the agents' SSH gateway) and dev workspaces.

CREATE TABLE ssh_keys (
    id          TEXT PRIMARY KEY,
    login       TEXT NOT NULL REFERENCES users (login) ON DELETE CASCADE,
    name        TEXT NOT NULL,                 -- label shown in the UI, e.g. "laptop"
    public_key  TEXT NOT NULL,                 -- authorized_keys line
    fingerprint TEXT NOT NULL UNIQUE,          -- SHA256:..., what agents match on; a key belongs to one user
    created_at  DATETIME NOT NULL,
    UNIQUE (login, name)
);

CREATE TABLE workspaces (
    id                TEXT PRIMARY KEY,
    name              TEXT NOT NULL UNIQUE,              -- DNS label; SSH user and <name>.walroos host
    owner             TEXT NOT NULL REFERENCES users (login),
    node_id           TEXT NOT NULL REFERENCES nodes (id), -- RESTRICT: a node with workspaces can't be removed
    repo_url          TEXT NOT NULL,
    branch            TEXT NOT NULL DEFAULT '',          -- '' = the repo's default branch
    devcontainer_path TEXT NOT NULL DEFAULT '',          -- '' = .devcontainer/devcontainer.json, else the base image
    image             TEXT NOT NULL DEFAULT '',          -- resolved by the agent
    state             TEXT NOT NULL CHECK (state IN ('creating', 'running', 'stopped', 'failed', 'deleting')),
    error             TEXT NOT NULL DEFAULT '',          -- last failure, shown when state = failed
    created_at        DATETIME NOT NULL
);

CREATE INDEX workspaces_node_id ON workspaces (node_id);
