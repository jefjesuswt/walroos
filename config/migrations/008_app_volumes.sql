-- Storage that outlives releases: a JSON list of agent.Volume ({name | host_path, target, ro}).
-- An app with volumes deploys by stopping the old release first.

ALTER TABLE apps ADD COLUMN volumes TEXT NOT NULL DEFAULT '[]';
