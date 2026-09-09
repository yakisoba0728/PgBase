ALTER TABLE pgbase_meta.auth_mail_templates
ADD COLUMN html_body text NOT NULL DEFAULT ''
CHECK (octet_length(html_body) <= 32768);

ALTER TABLE pgbase_meta.auth_oauth_providers
ADD COLUMN avatar_field text NOT NULL DEFAULT 'picture';

CREATE TABLE pgbase_meta.user_oauth_avatars (
    user_id uuid PRIMARY KEY REFERENCES pgbase_meta.users(id) ON DELETE CASCADE,
    content bytea NOT NULL CHECK (octet_length(content) BETWEEN 1 AND 2097152),
    media_type text NOT NULL CHECK (media_type IN ('image/jpeg','image/png','image/gif','image/webp')),
    width integer NOT NULL CHECK (width BETWEEN 1 AND 4096),
    height integer NOT NULL CHECK (height BETWEEN 1 AND 4096),
    sha256 bytea NOT NULL CHECK (octet_length(sha256) = 32),
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP
);
