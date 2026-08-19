-- Down for 063. The index first, for 062's reason: a down migration whose steps
-- rely on cascade behaviour breaks the day somebody adds a second index.

DROP INDEX IF EXISTS idx_source_documents_ocr_month;

ALTER TABLE source_documents DROP COLUMN IF EXISTS ocr_cost_micro_usd;
ALTER TABLE source_documents DROP COLUMN IF EXISTS ocr_page_count;
