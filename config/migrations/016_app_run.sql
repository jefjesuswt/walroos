-- How an app runs and proves it's up: its health check (http, tcp or running; '' is http), a
-- command replacing the image's (JSON array; '' for none) and the repo folder it builds from.

ALTER TABLE apps ADD COLUMN health TEXT NOT NULL DEFAULT '';
ALTER TABLE apps ADD COLUMN command TEXT NOT NULL DEFAULT '';
ALTER TABLE apps ADD COLUMN context TEXT NOT NULL DEFAULT '';
