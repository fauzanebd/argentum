-- Per-tenant widget appearance and content (T-23).
--
-- Next free on landing: 052 is thread_embed, so this is 053.
--
-- One JSONB column on `companies`, exactly like `report_branding` (022) and for
-- the same reason: this is a settings blob that one tenant reads for their own
-- rendering, never queried across companies and never joined to. A table would
-- buy indexing nobody needs and cost a join on a route a browser hits on every
-- widget open.
--
-- `'{}'` rather than a seeded default, so "the tenant has not chosen" and "the
-- tenant chose our defaults" stay distinguishable. The defaults live in Go,
-- where changing them is a deploy rather than a backfill.

ALTER TABLE companies
    ADD COLUMN IF NOT EXISTS widget_config JSONB NOT NULL DEFAULT '{}'::jsonb;
