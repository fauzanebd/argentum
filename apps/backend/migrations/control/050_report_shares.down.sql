-- Reversing this drops every live share link. Nobody's data is lost — the
-- documents and their plans are untouched — but every URL a tenant has already
-- sent to somebody stops working, and the tokens cannot be reconstructed
-- because only their hashes were ever stored. Re-applying the up migration
-- gives an empty table, not the old links.

DROP INDEX IF EXISTS idx_report_shares_document;
DROP INDEX IF EXISTS idx_report_shares_token;
DROP TABLE IF EXISTS report_shares;
