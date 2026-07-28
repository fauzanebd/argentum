-- The originating HTTP request on an audit row (T-A1).
--
-- A support conversation about the public API opens with a request id, because
-- that is the one string the caller has. It has to resolve to the rows the
-- request produced — and those rows are written by the worker, minutes later,
-- in a different process from the one that answered. So the id travels with
-- the turn (queue payload → context → audit decorator) and lands here.
--
-- Empty for everything that did not start with an HTTP call: a cron tick, a
-- watcher, a channel webhook. NOT NULL DEFAULT '' rather than nullable so
-- there is one way to say "no request", which is what makes the index below
-- usable.

ALTER TABLE agent_actions
    ADD COLUMN IF NOT EXISTS request_id TEXT NOT NULL DEFAULT '';

-- "Show me everything this request did" is the only query this column has, and
-- it is always tenant-scoped. Partial, because the overwhelming majority of
-- rows have no request id and indexing them would be storage bought for a
-- lookup nobody performs.
CREATE INDEX IF NOT EXISTS idx_agent_actions_request
    ON agent_actions(company_id, request_id)
    WHERE request_id <> '';
