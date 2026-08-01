-- T-08: watchers — the wedge. A metric-condition trigger that fires an agent
-- turn into any channel, unprompted.
--
-- This is the ticket that changes how a company works: instead of somebody
-- remembering to ask "how is revenue doing?", a watcher evaluates a defined
-- metric (T-06) on a cron, and when it breaches a threshold it enqueues a real
-- agent turn to explain the move and delivers the explanation to WhatsApp,
-- Discord, Lark, or the dashboard.
--
-- A watcher fires off a *defined* metric, never a number the LLM re-derived —
-- that is the whole reason T-06/T-07 come first. The first false alarm from a
-- flaky number would destroy trust permanently, so a watcher also cannot be
-- enabled until it has been dry-run over trailing data (enforced in the
-- service, recorded here as last_dry_run_at), and every fire is rate-limited by
-- a per-watcher cooldown.
--
-- Numbered 040 from schema_migrations at implementation time. The ticket header
-- read `023` until 2026-07-30; `023` has been `agent_actions` since T-05, and
-- the tree is at 039 (metric_definitions) now — the sixth ticket in a row whose
-- reserved migration number was already spent.
CREATE TABLE IF NOT EXISTS watchers (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id    UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    -- The number this watcher watches. ON DELETE CASCADE is the acceptance
    -- criterion "deleting a metric cascades to its watchers": a watcher on a
    -- metric that no longer exists is a watcher that can only ever error.
    metric_id     UUID NOT NULL REFERENCES metric_definitions(id) ON DELETE CASCADE,
    -- The dedicated thread each fire runs in, minted at creation like a
    -- scheduled task's. Reused across fires so the dashboard renders a watcher's
    -- history as one conversation, and so watcher_events.thread_id has something
    -- stable to point at. Not in the ticket's column list; added for the same
    -- reason scheduled_tasks carries one, and recorded in coverage/watchers.md.
    thread_id     UUID NOT NULL,
    name          TEXT NOT NULL,
    -- The period one evaluation covers: day|week|month. A watcher evaluates the
    -- most recent *complete* period of this grain, so the number is stable
    -- within a period and a comparison is a full period against a full period.
    window_grain  TEXT NOT NULL,
    -- gt|lt|pct_change_gt|pct_change_lt|no_data. The threshold comparators read
    -- the metric's value; the pct_change ones read the delta against compare_to;
    -- no_data fires when the metric returns no row (a stalled pipeline).
    comparator    TEXT NOT NULL,
    threshold     NUMERIC NOT NULL,
    -- previous_period|same_period_last_year, required by the pct_change
    -- comparators and ignored by the rest. NULL for a plain threshold.
    compare_to    TEXT,
    cron_expression TEXT NOT NULL,
    timezone      TEXT NOT NULL DEFAULT 'UTC',
    -- [{channel, ref}] — a WhatsApp phone, a Discord channel id, a Lark chat id,
    -- or the dashboard (whose ref is unused: the dedicated thread is where it
    -- lands). A list, because the same breach is worth saying in more than one
    -- place.
    channels      JSONB NOT NULL DEFAULT '[]'::jsonb,
    -- The minimum gap between two fires of one watcher. The guard against a
    -- breach that stays breached firing on every cron tick. Default 12h.
    cooldown_minutes INT NOT NULL DEFAULT 720,
    -- REQUIRES a passing dry-run in the last 24h to flip true (enforced in the
    -- service). Defaults false: a watcher is born silent.
    enabled       BOOLEAN NOT NULL DEFAULT false,
    last_fired_at   TIMESTAMPTZ,
    last_dry_run_at TIMESTAMPTZ,
    -- Who created it. Nullable and unreferenced, like metric_definitions: a
    -- watcher outlives the admin who wrote it.
    created_by    UUID,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_watchers_company ON watchers(company_id);
CREATE INDEX IF NOT EXISTS idx_watchers_metric ON watchers(metric_id);
-- The config provider polls this on every sync tick; enabled watchers are the
-- only rows it cares about.
CREATE INDEX IF NOT EXISTS idx_watchers_enabled ON watchers(enabled) WHERE enabled;

-- One row per evaluation, breached or not. A non-breaching evaluation writes a
-- row too, because "the watcher ran and everything was fine" is the answer to
-- "is this watcher working?" and silence cannot give it.
CREATE TABLE IF NOT EXISTS watcher_events (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    watcher_id    UUID NOT NULL REFERENCES watchers(id) ON DELETE CASCADE,
    company_id    UUID NOT NULL,
    fired_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- The number the metric returned, its comparison value, and the delta — all
    -- nullable, because a no_data breach has none of them and a threshold breach
    -- has no comparison.
    metric_value     NUMERIC,
    comparison_value NUMERIC,
    delta_pct        NUMERIC,
    breached      BOOLEAN NOT NULL,
    -- 'cooldown' when a real breach was suppressed by the per-watcher gap;
    -- NULL otherwise. A breached row with a suppressed_reason is a fire that
    -- did not go out, which is different from one that did.
    suppressed_reason TEXT,
    -- The dedicated thread the briefing turn ran in and the assistant message it
    -- produced. Both NULL for a non-breaching or suppressed row, which never
    -- runs a turn.
    thread_id     UUID,
    message_id    UUID,
    -- Per-channel delivery outcome: [{channel, ref, status, error}]. Written by
    -- CompleteFire once the turn's answer has been pushed to every channel.
    delivery_status JSONB
);

CREATE INDEX IF NOT EXISTS idx_watcher_events_watcher ON watcher_events(watcher_id, fired_at DESC);
