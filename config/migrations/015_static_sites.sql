-- A static site: an app from a repo with no Dockerfile, built with Node or Bun and served by
-- walroos's static server. JSON of agent.StaticBuild; '' = the app builds its Dockerfile.

ALTER TABLE apps ADD COLUMN static TEXT NOT NULL DEFAULT '';
