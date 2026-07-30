-- What an integrator's own key has been doing (T-A5).
--
-- The failure this exists for: a script holding a key gets a 403 at 11pm and
-- the only record of it is our server log, which the person debugging cannot
-- read. Everything below is scoped to one company so the tenant can answer the
-- question themselves.
--
-- Filed as T-A5 and claiming 032, which `../../../docs/coverage/agent-roster.md`
-- had pencilled in for T-S4. Reserved numbers are not binding — golang-migrate
-- only applies versions above the schema's current one, so a number held open
-- for a ticket that has not started would strand everything filed below it.
-- T-S4 takes the next free number when it lands, which is 033.
--
-- **Two tables, not one row per request.** A single `api_requests` log would
-- answer both questions and cost the control plane one row per machine call
-- forever — a nightly job polling a report every 10s is 8,640 rows a day for
-- one key, and 99% of them say "200". So the counters are a rollup and only
-- the failures keep their detail:
--
--   api_request_stats   — how much traffic, how much of it failed, how slow
--   api_request_errors  — what exactly failed, with the request id to quote
--
-- Both are written in batches by internal/apiobs, off the request path. The
-- cost of that choice is stated where it is paid: up to one flush interval of
-- observability is lost if the API is killed, and observability is the right
-- thing to lose in that trade.

-- The rollup. One row per (key, hour, route, method, status class), upserted.
--
-- The hour is the bucket because it is the coarsest grain that still answers
-- "did it start failing after the 14:00 deploy?" — a day would not, and a
-- minute would multiply the row count by sixty for a question nobody asks of
-- a counter. Rows per key per day are bounded by routes × methods × 3 classes
-- × 24, not by traffic, which is the whole point.
CREATE TABLE IF NOT EXISTS api_request_stats (
    company_id     UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    api_key_id     UUID NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
    bucket_hour    TIMESTAMPTZ NOT NULL,
    -- The gin route pattern (`/v1/reports/:id`), never the concrete path. A
    -- raw path would make the row count depend on how many report ids a
    -- tenant has asked about, which is unbounded traffic-shaped cardinality
    -- in a table whose whole design is to not have any.
    route          TEXT NOT NULL,
    method         TEXT NOT NULL,
    -- The first digit: 2, 4 or 5. The exact status of a failure lives in
    -- api_request_errors, where there is one row to carry it; here it would
    -- triple the row count to store a number the error rate does not use.
    status_class   SMALLINT NOT NULL,
    requests       BIGINT NOT NULL DEFAULT 0,
    -- Sum and max rather than a histogram: this table feeds a mean and a
    -- worst case in the dashboard. Percentiles need the distribution and are
    -- served from the in-process histogram on /metrics, which is where a
    -- percentile belongs — it is a scrape-time question, not a stored one.
    latency_ms_sum BIGINT NOT NULL DEFAULT 0,
    latency_ms_max INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (company_id, api_key_id, bucket_hour, route, method, status_class)
);

-- The read is always "this company's keys over the last N hours", and it is
-- served by the primary key's leading columns for a single key. This index is
-- for the tab, which reads every key at once.
CREATE INDEX IF NOT EXISTS idx_api_request_stats_company_window
    ON api_request_stats(company_id, bucket_hour DESC);

-- The failures, one row each.
--
-- No foreign key to anything but the company and the key, and no join to a
-- request: this is a leaf record whose only job is to be quotable. The
-- request_id is the string the caller was handed in `X-Request-Id`, which is
-- what makes "the request id shown matches what the caller received" a
-- property of the schema rather than of a correlation step.
CREATE TABLE IF NOT EXISTS api_request_errors (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    api_key_id UUID NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
    request_id TEXT NOT NULL,
    method     TEXT NOT NULL,
    route      TEXT NOT NULL,
    status     SMALLINT NOT NULL,
    -- The `code` and `type` out of the /v1 error envelope — the two fields a
    -- client switches on. Defaulted to empty rather than NOT NULL-with-a-value
    -- because a 5xx that escaped a handler without going through apierr has
    -- neither, and recording that honestly is better than inventing a code.
    error_code TEXT NOT NULL DEFAULT '',
    error_type TEXT NOT NULL DEFAULT '',
    latency_ms INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Both reads the tab makes: one key's failures, and the company's failures
-- across every key. Newest first in both cases, because "what just broke" is
-- the only question this table is asked.
CREATE INDEX IF NOT EXISTS idx_api_request_errors_key_recent
    ON api_request_errors(company_id, api_key_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_api_request_errors_company_recent
    ON api_request_errors(company_id, created_at DESC);
