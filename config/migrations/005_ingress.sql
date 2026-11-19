-- M3.2: apps reach the internet through a Cloudflare Tunnel per prod node.

CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL                              -- secrets sealed like app_env
);

CREATE TABLE app_domains (
    domain TEXT PRIMARY KEY,                         -- a hostname routes to one app
    app_id TEXT NOT NULL REFERENCES apps (id) ON DELETE CASCADE
);

CREATE TABLE tunnels (
    node_id    TEXT PRIMARY KEY REFERENCES nodes (id) ON DELETE CASCADE,
    account_id TEXT NOT NULL,
    tunnel_id  TEXT NOT NULL                         -- remote-managed: its ingress lives in Cloudflare
);
