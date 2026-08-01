-- T-06: the metric registry — a number defined once, so the same question
-- returns the same answer in two threads.
--
-- The accuracy foundation the whole "it tells you first" half of the product
-- rests on. Today every question re-derives its SQL, so "what was revenue last
-- month" can come back two different numbers on two different turns; a watcher
-- firing off a number the LLM re-derived would destroy trust on the first false
-- alarm. A metric is a named, validated, parameterised SELECT the agent runs
-- through query_metric (T-07) instead of composing run_sql from scratch.
--
-- v1 is deliberately narrow: one number, one source, one window. No dimensions,
-- no joins, no DSL — those turn the registry into a semantic layer, which is a
-- multi-week design problem and a Sprint-2 item at the earliest.
--
-- Numbered 039 from schema_migrations at implementation time. The ticket's
-- header said 022 until 2026-07-30, then noted 022 became report_branding under
-- T-R5; the tree is at 038 (agent_mcp_servers) now.
CREATE TABLE IF NOT EXISTS metric_definitions (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id    UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    -- Which of the tenant's databases the metric is measured against. A metric
    -- belongs to one source: the same key can mean different SQL on two
    -- warehouses, and a number with no source is a number nobody can reproduce.
    source_id     UUID NOT NULL REFERENCES db_connections(id) ON DELETE CASCADE,
    -- The stable handle the agent names in query_metric. Unique per company so
    -- "revenue" resolves to exactly one definition.
    key           TEXT NOT NULL,
    label         TEXT NOT NULL,
    -- What the agent reads to decide whether this metric answers the question.
    -- It is the field query_metric's catalog surfaces, so it earns its keep by
    -- being specific: "net revenue, orders in Paid state, excluding refunds".
    description   TEXT NOT NULL,
    -- The parameterised SELECT. It must contain {{from}} and {{to}}, which are
    -- bound as query parameters at run time and NEVER string-interpolated — the
    -- one rule that keeps a window value from becoming a SQL injection. Stored
    -- with the {{...}} tokens intact; the driver's placeholder syntax is applied
    -- per dialect when it runs.
    sql_template  TEXT NOT NULL,
    -- Which column of the single result row carries the number. Named rather
    -- than positional so a template can SELECT more than the value and still be
    -- unambiguous about which cell is the metric.
    value_column  TEXT NOT NULL,
    -- The natural period one value covers: day|week|month|quarter|year. It is
    -- what a comparison ("previous period") is measured in and what a watcher
    -- evaluates on; it is not enforced against the SQL, which the window params
    -- already bound.
    grain         TEXT NOT NULL,
    -- How to read the number: currency|count|percent|ratio. It decides
    -- formatting and, with higher_is_better, how a delta reads.
    unit          TEXT NOT NULL,
    -- Set when unit is currency. Null otherwise.
    currency      TEXT,
    -- Whether a rise is good news. Revenue up is good; churn up is not. A
    -- watcher and a delta both need to know which direction to alarm on.
    higher_is_better BOOLEAN NOT NULL DEFAULT true,
    enabled       BOOLEAN NOT NULL DEFAULT true,
    -- Who defined it. Nullable and unreferenced: a metric outlives the admin who
    -- wrote it, and "which user created this" is not worth a foreign key that
    -- would block deleting the user.
    created_by    UUID,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (company_id, key)
);

CREATE INDEX IF NOT EXISTS idx_metric_definitions_company ON metric_definitions(company_id);
