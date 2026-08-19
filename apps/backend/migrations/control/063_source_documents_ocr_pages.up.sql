-- How many pages of this document were read by a model, and what that cost
-- (T-P3/T-P11).
--
-- Ingestion is the first thing in this product a tenant can point at that
-- spends money *outside* a chat turn. Without a meter, a four-hundred-page scan
-- uploaded twice is an unbudgeted bill with no thread to attribute it to — and
-- the ledger's existing rows are keyed by thread, which an upload does not
-- have.
--
-- Two columns rather than one, because they answer different questions. The
-- page count is what the monthly budget is checked against, and it has to be
-- summable across a company's documents without joining anything. The cost is
-- what somebody asking "what did documents cost this month" reads, and it is
-- recorded here *as well as* in the usage ledger: the ledger row proves the
-- spend happened, and this column is what the review surface can show beside
-- the document without a second query.
--
-- Both default to zero, which is the truth for every document that existed
-- before OCR did and for every document on a deployment that leaves it off.

ALTER TABLE source_documents
    ADD COLUMN IF NOT EXISTS ocr_page_count INTEGER NOT NULL DEFAULT 0;

ALTER TABLE source_documents
    ADD COLUMN IF NOT EXISTS ocr_cost_micro_usd BIGINT NOT NULL DEFAULT 0;

-- The budget check is "how many pages has this company had read this month",
-- which is a sum over one company's recent rows. Partial, because the rows that
-- matter are the ones that spent anything.
CREATE INDEX IF NOT EXISTS idx_source_documents_ocr_month
    ON source_documents(company_id, created_at DESC)
    WHERE ocr_page_count > 0;
