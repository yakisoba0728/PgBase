-- Scheduled jobs are configuration rows. The binary supplies named handlers;
-- migrations never persist executable code. Times are timestamptz and the
-- runner evaluates schedules in UTC using PostgreSQL's clock.
CREATE TABLE pgbase_meta.scheduled_jobs (
    id uuid PRIMARY KEY,
    name text NOT NULL UNIQUE,
    schedule text NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    next_run_at timestamptz NOT NULL,
    lease_token uuid,
    lease_until timestamptz,
    fence_generation bigint NOT NULL DEFAULT 0 CHECK (fence_generation >= 0),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    last_error text,
    last_started_at timestamptz,
    last_finished_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK ((lease_token IS NULL) = (lease_until IS NULL))
);
CREATE INDEX scheduled_jobs_due_idx ON pgbase_meta.scheduled_jobs(next_run_at, id)
    WHERE enabled;
