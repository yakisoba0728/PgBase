-- One row per claimed execution of a scheduled job. The claim inserts the row
-- as running in the same statement that takes the lease; completion records
-- the outcome only for that row. A running row whose lease expired or was
-- replaced is reported as abandoned on read. Retention is bounded by the
-- runner (per-job row count and age), so the table never grows unbounded.
CREATE TABLE pgbase_meta.scheduled_job_runs (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    job_id uuid NOT NULL REFERENCES pgbase_meta.scheduled_jobs(id) ON DELETE CASCADE,
    fence_generation bigint NOT NULL CHECK (fence_generation >= 0),
    attempt integer NOT NULL CHECK (attempt >= 1),
    status text NOT NULL CHECK (status IN ('running', 'succeeded', 'failed', 'superseded')),
    scheduled_at timestamptz,
    started_at timestamptz NOT NULL,
    finished_at timestamptz,
    error text CHECK (error IS NULL OR char_length(error) <= 1024),
    CHECK ((status = 'running') = (finished_at IS NULL)),
    CHECK (status NOT IN ('running', 'succeeded') OR error IS NULL)
);
CREATE INDEX scheduled_job_runs_job_idx ON pgbase_meta.scheduled_job_runs(job_id, id DESC);
CREATE INDEX scheduled_job_runs_started_idx ON pgbase_meta.scheduled_job_runs(started_at);
