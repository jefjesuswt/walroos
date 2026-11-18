-- M1 task 10: environment variables per workspace, values encrypted at rest (AES-GCM, key in
-- the hub's data dir). secret=1 only changes how the UI shows the value.

CREATE TABLE workspace_env (
    workspace_id TEXT NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    key          TEXT NOT NULL,
    value        TEXT NOT NULL,              -- base64(nonce || ciphertext)
    secret       INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (workspace_id, key)
);
