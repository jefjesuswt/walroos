-- Trusted guests get one of three fixed roles: owner (everything), deployer (apps and databases
-- too) and developer (their own workspaces). The unused 'member' becomes developer.
-- SQLite can't change a CHECK in place: rebuild the table. The runner turns foreign keys off for
-- this (workspaces and ssh_keys point here) and runs foreign_key_check before committing.

CREATE TABLE users_new (
    login      TEXT PRIMARY KEY,
    role       TEXT NOT NULL CHECK (role IN ('owner', 'deployer', 'developer')),
    created_at DATETIME NOT NULL
);
INSERT INTO users_new (login, role, created_at)
    SELECT login, CASE role WHEN 'member' THEN 'developer' ELSE role END, created_at FROM users;
DROP TABLE users;
ALTER TABLE users_new RENAME TO users;
