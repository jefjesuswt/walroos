-- How an app deploys by itself: manual (the button, the API, MCP), on_push (a signed webhook
-- from the forge for its branch) or on_ci (the forge's CI calls /hooks/deploy with a deploy
-- token once the tests pass). The webhook secret is sealed like env values; the deploy token
-- is only stored as its sha256.

ALTER TABLE apps ADD COLUMN trigger TEXT NOT NULL DEFAULT 'manual';
ALTER TABLE apps ADD COLUMN webhook_secret TEXT NOT NULL DEFAULT '';
ALTER TABLE apps ADD COLUMN deploy_token_hash TEXT NOT NULL DEFAULT '';
