ALTER TABLE pgbase_meta.auth_oauth_providers
  ADD COLUMN issuer text NOT NULL DEFAULT '',
  ADD COLUMN jwks_url text NOT NULL DEFAULT '';
