-- The report job the agentic door hands back (T-A2).
--
-- `POST /v1/reports` answers 202 and finishes minutes later, so it has to
-- return something the caller can name afterwards. A thread id will not do —
-- a thread outlives the turn and accumulates more of them, so "is my report
-- ready?" would have no answer. This row is the job: one request, one
-- lifecycle, one document at the end of it.
--
-- The same row backs the render door's timeout fallback. A spec that takes
-- longer than API_V1_SYNC_RENDER_TIMEOUT stops being an HTTP response and
-- becomes a job, and giving that job a different shape would mean an
-- integrator writing two collection paths for one endpoint.

CREATE TABLE IF NOT EXISTS api_reports (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id   UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    -- Which credential asked. SET NULL, like documents.api_key_id: deleting a
    -- key must not delete the tenant's record of what it did.
    api_key_id   UUID REFERENCES api_keys(id) ON DELETE SET NULL,
    -- 'agentic' — a prompt, a real turn, tokens billed.
    -- 'render'   — a spec that overran the synchronous window.
    kind         TEXT NOT NULL,
    -- queued | running | completed | failed
    status       TEXT NOT NULL DEFAULT 'queued',
    format       TEXT NOT NULL,
    -- The prompt, for the agentic door only. Kept because "what did I ask
    -- for?" is the first question about a report that came back wrong, and the
    -- alternative is reading it out of the thread's first message.
    prompt       TEXT NOT NULL DEFAULT '',
    -- Null for a render job: it has no conversation. CASCADE would be wrong
    -- here — deleting a thread should not silently erase the record that a
    -- billed job ran — so this is a plain reference with SET NULL.
    thread_id    UUID REFERENCES conversation_threads(id) ON DELETE SET NULL,
    document_id  UUID REFERENCES documents(id) ON DELETE SET NULL,
    callback_url TEXT NOT NULL DEFAULT '',
    -- The message a failed job hands the caller. Never a raw Go error: what
    -- lands here is written for an integrator reading it in their own logs.
    error        TEXT NOT NULL DEFAULT '',
    -- The X-Request-Id of the call that created it, so a support conversation
    -- that starts with a request id reaches the job as well as the audit rows.
    request_id   TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ
);

-- Every read of this table is tenant-scoped by id, which the primary key
-- already serves. What it does not serve is the sweep a stuck-job alert would
-- want: "anything still running for this tenant". Partial, because a finished
-- job is not what that query is looking for.
CREATE INDEX IF NOT EXISTS idx_api_reports_company_open
    ON api_reports(company_id, created_at DESC)
    WHERE status IN ('queued', 'running');
