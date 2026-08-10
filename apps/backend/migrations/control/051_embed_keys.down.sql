-- Reversing this drops every embed key. Every tenant page that embeds Argentum
-- stops being able to mint a session immediately, and the keys cannot be
-- reconstructed: the client keys are recoverable from the tenant's own page
-- source, but the signing secrets exist nowhere else. Re-applying the up
-- migration gives an empty table, and every host page needs a new key pair.

DROP INDEX IF EXISTS idx_embed_keys_company_created;
DROP TABLE IF EXISTS embed_keys;
