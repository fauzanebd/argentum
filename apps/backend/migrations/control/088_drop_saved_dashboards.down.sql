-- The schema round trips; the data does not.
--
-- This recreates the table exactly as `006` made it, so the migration sequence can
-- be replayed in both directions against a real database. The rows are gone and
-- nothing exists to repopulate them from — they pointed at a Metabase that T-D15
-- decommissioned — and no code in any release since `073` reads the table, so a
-- rollback past this point gets an empty table nobody asks for.
CREATE TABLE IF NOT EXISTS saved_dashboards (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id            UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    thread_id             UUID NOT NULL REFERENCES conversation_threads(id) ON DELETE CASCADE,
    metabase_dashboard_id INT NOT NULL,
    name                  TEXT NOT NULL,
    public_url            TEXT NOT NULL,
    created_at            TIMESTAMPTZ DEFAULT now(),
    updated_at            TIMESTAMPTZ DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_saved_dashboards_company ON saved_dashboards(company_id);
CREATE INDEX IF NOT EXISTS idx_saved_dashboards_thread ON saved_dashboards(thread_id);
