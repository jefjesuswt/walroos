-- Variables shared by a project's services. An app uses one with ${{shared.KEY}} in its own
-- environment; values are sealed like app env values.

CREATE TABLE project_env (
	project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
	key        TEXT NOT NULL,
	value      TEXT NOT NULL,
	secret     INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (project_id, key)
);
