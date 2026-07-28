-- Dropping the column discards every tenant's branding, including uploaded
-- logos' keys — the objects themselves stay in object storage under
-- branding/{company_id}/, orphaned. That is deliberate: a down migration that
-- deletes customer-supplied files cannot be undone by re-running the up.

ALTER TABLE companies DROP COLUMN IF EXISTS report_branding;
