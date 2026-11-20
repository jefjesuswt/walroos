-- Apps that run a published image (registry/repo:tag) instead of building a repo.
-- An app has one or the other: repo_url = '' when image is set.

ALTER TABLE apps ADD COLUMN image TEXT NOT NULL DEFAULT '';
