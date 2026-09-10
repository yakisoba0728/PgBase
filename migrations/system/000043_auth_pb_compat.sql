-- Additive PocketBase-compatible auth settings.  Existing session and Argon2
-- behaviour stays the default; these columns only opt a collection into the
-- richer per-collection contract.
ALTER TABLE pgbase_meta.auth_collection_policies
  ADD COLUMN maximum_password_bytes integer NOT NULL DEFAULT 1024 CHECK (maximum_password_bytes BETWEEN 12 AND 4096),
  ADD COLUMN password_pattern text NOT NULL DEFAULT '',
  ADD COLUMN identity_fields jsonb NOT NULL DEFAULT '["email"]'::jsonb CHECK (jsonb_typeof(identity_fields) = 'array'),
  ADD COLUMN mfa_duration_seconds integer NOT NULL DEFAULT 1800 CHECK (mfa_duration_seconds BETWEEN 60 AND 86400),
  ADD COLUMN mfa_rule text NOT NULL DEFAULT '';

ALTER TABLE pgbase_meta.auth_mail_templates DROP CONSTRAINT auth_mail_templates_purpose_check;
ALTER TABLE pgbase_meta.auth_mail_templates ADD CONSTRAINT auth_mail_templates_purpose_check
  CHECK (purpose IN ('verify','password_reset','email_change','otp','auth_alert'));
