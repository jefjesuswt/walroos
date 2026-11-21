-- Database services on a prod node (a Quadlet each), and when their last backup ran.

CREATE TABLE databases (
    id                 TEXT PRIMARY KEY,
    name               TEXT NOT NULL UNIQUE,                -- DNS label; the container is walroos-db-<name>
    node_id            TEXT NOT NULL REFERENCES nodes (id), -- RESTRICT: a node with databases can't be removed
    engine             TEXT NOT NULL,                       -- postgres, mysql, mariadb, redis
    version            TEXT NOT NULL,
    db_user            TEXT NOT NULL,
    db_name            TEXT NOT NULL,
    password           TEXT NOT NULL,                       -- sealed like app_env
    backup_every_hours INTEGER NOT NULL DEFAULT 24,         -- 0 = no scheduled backups
    backup_keep        INTEGER NOT NULL DEFAULT 7,          -- dumps kept on the node
    last_backup_at     DATETIME,                            -- last attempt, ok or not
    last_backup_error  TEXT NOT NULL DEFAULT '',
    created_at         DATETIME NOT NULL
);
