-- A picture a tenant supplied for the agent to put in a post (T-G12).
--
-- The third kind of file this product accepts from a tenant, and the three do
-- not belong together. `source_documents` (059) is what a tenant supplied to
-- be *read* — parsed, chunked, retrieved from. `documents` (031) is what the
-- agent *generated*. This is what a tenant supplied to be *drawn*: a product
-- photograph that appears, unchanged and unread, on a promotion card. It has
-- no pipeline, no status and nothing to extract; what it has is a name
-- somebody can ask for it by.
--
-- **The name is the interface, and that is why it is unique per company.**
-- The model asks for "jeruk cara cara" and the door resolves it against this
-- table, because the alternative — every image a tenant owns in every turn's
-- prompt — is unbounded cost for a lookup a query does better. Two rows with
-- the same name would make that resolution a coin toss, so the constraint is
-- what keeps "use the jeruk photo" a question with one answer. Lower-cased in
-- the index because nobody typing a product name is thinking about case.
--
-- **No content hash and no dedupe**, deliberately, where `source_documents`
-- has both: the same photograph uploaded twice under two names is two useful
-- library entries ("jeruk cara cara" and "promo akhir pekan"), and the OCR
-- bill that justifies dedupe over there has no equivalent here.
--
-- Dimensions are stored because the layout needs the aspect ratio to reserve
-- a box before the bytes are read, and reading them back out of the object
-- store to lay out one card would be a download per render.
CREATE TABLE IF NOT EXISTS post_images (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id  UUID        NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    -- What the tenant calls it, and what the model asks for it by.
    name        TEXT        NOT NULL,
    -- How the picture is described to somebody who cannot see it. It reaches
    -- the slide's alt text, so an empty one costs a publisher a caption.
    alt         TEXT        NOT NULL DEFAULT '',
    -- The object key, not a URL, for 059's reason: a URL embeds an endpoint.
    storage_key TEXT        NOT NULL,
    width       INTEGER     NOT NULL,
    height      INTEGER     NOT NULL,
    byte_size   BIGINT      NOT NULL,
    uploaded_by UUID        REFERENCES users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The picker's list: one tenant's images, newest first.
CREATE INDEX IF NOT EXISTS idx_post_images_company_recent
    ON post_images(company_id, created_at DESC);

-- One name, one image, per company. See the note above: this constraint is
-- what makes resolving a name a lookup rather than a guess.
CREATE UNIQUE INDEX IF NOT EXISTS uq_post_images_company_name
    ON post_images(company_id, lower(name));
