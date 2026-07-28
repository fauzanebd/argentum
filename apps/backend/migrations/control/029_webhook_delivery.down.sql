DROP INDEX IF EXISTS idx_webhook_deliveries_company;

DROP TABLE IF EXISTS webhook_deliveries;

-- Dropping this rotates every tenant's signing secret: a re-applied up
-- migration mints new ones on first use, and every receiver verifying against
-- the old value starts rejecting. That is the correct direction — a secret
-- whose column was rolled back is a secret nobody is storing — but it is not
-- reversible, and an operator running this should know it before they do.
ALTER TABLE companies
    DROP COLUMN IF EXISTS webhook_secret;
