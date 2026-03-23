CREATE TABLE jobs (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    queue        VARCHAR(255) NOT NULL,
    type         VARCHAR(255) NOT NULL,
    payload      JSONB NOT NULL DEFAULT '{}',
    status       VARCHAR(20) NOT NULL DEFAULT 'pending',
    max_retries  INT NOT NULL DEFAULT 3,
    retry_count  INT NOT NULL DEFAULT 0,
    run_at       TIMESTAMPTZ,
    started_at   TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    error        TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_jobs_status ON jobs(status);
CREATE INDEX idx_jobs_queue ON jobs(queue);
CREATE INDEX idx_jobs_status_queue ON jobs(status, queue);
CREATE INDEX idx_jobs_created_at ON jobs(created_at);
CREATE INDEX idx_jobs_run_at ON jobs(run_at) WHERE run_at IS NOT NULL;
