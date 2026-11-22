-- Compose: a project can come from a docker-compose.yml (in a repo, or pasted), kept as the JSON
-- of compose.Source; '' for none. Each of its services is an app whose compose column is the
-- service's name: the app's second name on the project's network, and how a sync finds it.

ALTER TABLE projects ADD COLUMN compose TEXT NOT NULL DEFAULT '';
ALTER TABLE apps ADD COLUMN compose TEXT NOT NULL DEFAULT '';
