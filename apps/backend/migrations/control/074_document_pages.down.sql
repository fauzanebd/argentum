-- The column carries nothing a rollback needs to keep: a carousel row without
-- its page count still downloads as a zip, and only the page route and the
-- "N slides" label read it.
ALTER TABLE documents DROP COLUMN IF EXISTS page_count;
