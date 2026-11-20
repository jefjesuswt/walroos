-- The stack detect picked when the workspace was created (android, go, dotnet...), so the UI can
-- say "Android" instead of the image (a java image plus the SDK). '' = unknown: the UI derives a
-- label from the image (a repo's own devcontainer, the base image, an older workspace).
ALTER TABLE workspaces ADD COLUMN stack TEXT NOT NULL DEFAULT '';
