-- pre-M0 table; its tokens were stored in plaintext, nodes must re-join
DROP TABLE IF EXISTS fleet_nodes;

CREATE TABLE users (
    login      TEXT PRIMARY KEY,
    role       TEXT NOT NULL CHECK (role IN ('owner', 'member')),
    created_at DATETIME NOT NULL
);

CREATE TABLE nodes (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE,
    roles      TEXT NOT NULL,                -- comma separated: dev,build,prod
    token_hash TEXT NOT NULL UNIQUE,         -- sha256 of the node token
    address    TEXT NOT NULL DEFAULT '',     -- last tailnet IP seen
    telemetry  TEXT NOT NULL DEFAULT '{}',   -- JSON, last heartbeat
    created_at DATETIME NOT NULL,
    last_seen  DATETIME
);

CREATE TABLE join_tokens (
    token_hash TEXT PRIMARY KEY,
    expires_at DATETIME NOT NULL
);
