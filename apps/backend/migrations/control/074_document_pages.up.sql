-- T-G6 · A carousel is a document with pages.
--
-- `documents.page_count` is how many slides a `carousel` row has. Zero for
-- every other format, and DEFAULT 0 backfills every existing row correctly
-- because until this migration no document had pages.
--
-- It is a column where `has_plan` (T-V4) deliberately is not, and the
-- difference is drift: whether a plan sits beside a document is something only
-- the bucket knows, so a boolean here would be a second answer. A page count is
-- fixed at write time — the pages are uploaded before the row is inserted and
-- never added to — and the dashboard's list needs it to say "7 slides" on a
-- row without N object-store reads.
--
-- Additive and forward-compatible: the binary that ships without this column
-- never reads it, and the one that reads it tolerates the default.
ALTER TABLE documents
    ADD COLUMN IF NOT EXISTS page_count INTEGER NOT NULL DEFAULT 0;
