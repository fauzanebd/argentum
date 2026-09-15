-- T-W8: an agent's answer, as it was read aloud — or why it was not.
--
-- **The ticket says `Migration: none`, and it cannot be.** "Synthesised on demand
-- and cached by message id" needs somewhere to look the cache up, and "the same
-- message synthesised twice … bills once" needs that lookup to survive a restart
-- and a second replica. An object named after the message would hold the audio;
-- it could not hold the two things beside it this product needs:
--
--   * **the spoken text**, which is the reduction a model wrote and a check
--     passed. Decision 14 makes a spoken figure the written answer does not state
--     a defect, and a defect nobody can read back afterwards is one nobody can
--     show happened;
--   * **a refusal**, so a second press of a play button on an answer whose
--     spoken form was refused answers from here instead of paying a model to be
--     refused again.
--
-- **message_id is SET NULL, not CASCADE**, for 087's reason: a cascade would
-- delete the row when a conversation is deleted and leave the audio in the
-- bucket with nothing left to find it by. SET NULL keeps the row until the sweep,
-- which deletes a row whose message is gone and its audio with it.
--
-- The audio lives under `voice/<company_id>/answers/`, inside the prefix T-W7's
-- erasure already removes, so an erasure takes it even if a row was never
-- written.

CREATE TABLE IF NOT EXISTS spoken_answers (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id  UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    message_id  UUID REFERENCES messages(id) ON DELETE SET NULL,
    -- Empty on a refusal: nothing was synthesised.
    object_key  TEXT NOT NULL DEFAULT '',
    mime_type   TEXT NOT NULL DEFAULT '',
    size_bytes  BIGINT NOT NULL DEFAULT 0,
    -- What was synthesised; on a refusal, what the model wrote and was refused.
    spoken_text TEXT NOT NULL DEFAULT '',
    -- Why the spoken text was not synthesised. Empty when it was.
    refusal     TEXT NOT NULL DEFAULT '',
    voice       TEXT NOT NULL DEFAULT '',
    model       TEXT NOT NULL DEFAULT '',
    -- Billed on: the spoken text's length in characters.
    chars       INTEGER NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL
);

-- One spoken form per message: the cache's key, and the conflict target a
-- re-synthesis replaces. Partial, because a deleted message leaves NULLs that
-- are not the same message.
CREATE UNIQUE INDEX IF NOT EXISTS idx_spoken_answers_message ON spoken_answers (message_id) WHERE message_id IS NOT NULL;
-- The sweep's two questions, as on voice_clips.
CREATE INDEX IF NOT EXISTS idx_spoken_answers_expires_at ON spoken_answers (expires_at);
CREATE INDEX IF NOT EXISTS idx_spoken_answers_orphaned ON spoken_answers (created_at) WHERE message_id IS NULL;
-- The erasure's question.
CREATE INDEX IF NOT EXISTS idx_spoken_answers_company ON spoken_answers (company_id);
