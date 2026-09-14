-- T-W7: a question somebody said out loud, and — for a short while — the audio
-- it was heard from.
--
-- The transcript is the record; the audio is evidence with a short life
-- (roadmap 11, decision 16). A row exists so that a person can check what was
-- heard, and so that the two things that must delete the audio — the expiry
-- sweep and T-H6's erasure — can find it. Nothing reads a clip back into a
-- turn: the transcript goes to the person who spoke, and what they send is an
-- ordinary message (decision 13).
--
-- **No message_id, although the ticket lists one.** Nothing in T-W7 can write
-- it: a transcript becomes a message only when the person sends it, through
-- POST /api/chat, and linking the two needs that route to carry the clip's id —
-- which is T-W9's composer, not this ticket. A column nobody writes is the shape
-- of T-K1's binding table that nothing read (coverage/skills.md §5h), and adding
-- a nullable column the day something writes it is one forward-compatible
-- ALTER.
--
-- **thread_id and user_id are SET NULL, not CASCADE.** A CASCADE would delete
-- the row when a person deletes the conversation, and leave the audio in the
-- bucket with nothing left to find it by. SET NULL keeps the row, and the sweep
-- deletes any clip whose conversation is gone on its next tick
-- (idx_voice_clips_orphaned) — so deleting a conversation still deletes the
-- recording, one sweep later, instead of never.

CREATE TABLE IF NOT EXISTS voice_clips (
    -- Chosen by the application, because the object key is written before the
    -- row and is named after it.
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id  UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    thread_id   UUID REFERENCES conversation_threads(id) ON DELETE SET NULL,
    user_id     UUID REFERENCES users(id) ON DELETE SET NULL,
    -- Empty when the audio was not kept: a deployment with no object storage,
    -- or an upload that failed after the transcript came back. The transcript
    -- is still the record.
    object_key  TEXT NOT NULL DEFAULT '',
    mime_type   TEXT NOT NULL,
    size_bytes  BIGINT NOT NULL,
    -- As the provider measured it where it reports one, else as the client
    -- declared it. Billed on, so it is stored beside what it was billed as.
    seconds     DOUBLE PRECISION NOT NULL DEFAULT 0,
    transcript  TEXT NOT NULL,
    -- The hint sent, not a guess from the text: "id", "en", or empty when the
    -- provider was left to detect it.
    language    TEXT NOT NULL DEFAULT '',
    model       TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL
);

-- The sweep's two questions, each with its own index: what has expired, and
-- what has lost its conversation. Two indexes rather than one on
-- (expires_at, thread_id), because the second question is about a NULL and the
-- first is a range, and a partial index answers the NULL without scanning the
-- rows that still have a conversation — which is nearly all of them.
CREATE INDEX IF NOT EXISTS idx_voice_clips_expires_at ON voice_clips (expires_at);
CREATE INDEX IF NOT EXISTS idx_voice_clips_orphaned ON voice_clips (created_at) WHERE thread_id IS NULL;
-- The erasure's question: every clip a company has.
CREATE INDEX IF NOT EXISTS idx_voice_clips_company ON voice_clips (company_id);
