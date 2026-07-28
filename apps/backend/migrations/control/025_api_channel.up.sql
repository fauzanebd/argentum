-- The `api` channel (T-A1): a turn that arrives over the public API.
--
-- Filed as T-A1's `031_api_channel`, landing as 025. golang-migrate only
-- applies versions above the schema's current one, so a reserved number that
-- has since been spent strands everything below it. This is the fourth
-- consecutive ticket whose reserved number was already taken; `01-tickets.md`
-- now says explicitly that those numbers are not binding.
--
-- Both consumers need this before either can be built: T-A2's agentic report
-- door runs a real turn through ChatEnqueuer, and a turn needs a channel.
--
-- api_user_ref is the tenant's own identifier for the person the call is made
-- on behalf of — their user id, their email, whatever their system uses. It is
-- deliberately opaque to us: an API key belongs to a company, not to a person,
-- so the only identity available is the one the caller supplies.

ALTER TABLE conversation_threads
    ADD COLUMN IF NOT EXISTS api_user_ref TEXT;

-- A lookup index, not a uniqueness constraint, and the ticket asked for the
-- opposite in as many words: "unique index on (company_id, api_user_ref, id)".
-- Including the primary key makes uniqueness vacuous — every row is already
-- unique by id — so that index constrains nothing and only reads as if it
-- does. And a genuinely unique (company_id, api_user_ref) would be wrong in a
-- different way: the resolver forks a new thread when a caller returns after
-- an idle gap on a new topic, exactly as it does for WhatsApp and Discord, so
-- one api_user_ref legitimately owns many threads over time.
--
-- Partial, because every row written by the other four channels has a NULL
-- here and there is no reason to carry them in this index.
CREATE INDEX IF NOT EXISTS idx_threads_api_user
    ON conversation_threads(company_id, api_user_ref, last_message_at DESC)
    WHERE api_user_ref IS NOT NULL;
