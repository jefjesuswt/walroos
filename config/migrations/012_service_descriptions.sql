-- A short description per service, like a project's: the project view shows it on the card.

ALTER TABLE apps ADD COLUMN description TEXT NOT NULL DEFAULT '';
ALTER TABLE databases ADD COLUMN description TEXT NOT NULL DEFAULT '';
