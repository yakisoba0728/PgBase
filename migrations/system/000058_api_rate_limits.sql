CREATE TABLE pgbase_meta.api_rate_limits (
    bucket text PRIMARY KEY CHECK (length(bucket) <= 512),
    window_started_at timestamptz NOT NULL,
    requests integer NOT NULL CHECK (requests > 0),
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX api_rate_limits_updated_at_idx ON pgbase_meta.api_rate_limits(updated_at);
