CREATE TABLE pgbase_meta.request_logs (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 occurred_at timestamptz NOT NULL,
 method varchar(16) NOT NULL,
 route varchar(256) NOT NULL,
 status integer NOT NULL CHECK (status BETWEEN 100 AND 599),
 duration_ms bigint NOT NULL CHECK (duration_ms >= 0)
);
CREATE INDEX request_logs_time_idx ON pgbase_meta.request_logs(occurred_at DESC,id DESC);
CREATE INDEX request_logs_filter_idx ON pgbase_meta.request_logs(method,route,status,id DESC);
ALTER TABLE pgbase_meta.scheduled_jobs ADD COLUMN timezone text NOT NULL DEFAULT 'UTC', ADD COLUMN last_scheduled_at timestamptz;
