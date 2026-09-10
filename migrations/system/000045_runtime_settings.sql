CREATE TABLE pgbase_meta.runtime_settings (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    document jsonb NOT NULL,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP
);
