-- Conversation threads keyed by (company_id, discord_user_id) for the
-- discord channel — mirrors the (company_id, phone_number) pattern.

ALTER TABLE conversation_threads
    ADD COLUMN IF NOT EXISTS discord_user_id TEXT;

CREATE INDEX IF NOT EXISTS idx_threads_discord
    ON conversation_threads(company_id, discord_user_id)
    WHERE channel = 'discord';
