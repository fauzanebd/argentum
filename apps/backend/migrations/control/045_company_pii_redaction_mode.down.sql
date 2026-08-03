-- Reversing T-07b's per-company policy drops the column. The code reads an
-- absent or unrecognised mode as `strict`, so a deployment rolled back to the
-- previous binary redacts everything rather than nothing — the recoverable
-- direction, and the one a tenant who had set `contact_ok` will notice and ask
-- about rather than one that silently widens what the agent may print.
ALTER TABLE companies DROP CONSTRAINT IF EXISTS companies_pii_redaction_mode_check;
ALTER TABLE companies DROP COLUMN IF EXISTS pii_redaction_mode;
