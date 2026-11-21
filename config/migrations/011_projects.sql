-- Projects group services (apps and databases) like Dokploy's: each gets its own podman network
-- on the node, where its services reach each other by name. What exists goes to 'default',
-- which keeps the shared walroos network. The runner has foreign keys off here, which ADD COLUMN
-- needs for a REFERENCES column with a default.

CREATE TABLE projects (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,             -- DNS label; the network is walroos-p-<name>
    description TEXT NOT NULL DEFAULT '',
    created_at  DATETIME NOT NULL
);

INSERT INTO projects (id, name, description, created_at)
    VALUES ('default', 'default', 'Services created without a project.', CURRENT_TIMESTAMP);

ALTER TABLE apps ADD COLUMN project_id TEXT NOT NULL DEFAULT 'default' REFERENCES projects (id);
ALTER TABLE databases ADD COLUMN project_id TEXT NOT NULL DEFAULT 'default' REFERENCES projects (id);
