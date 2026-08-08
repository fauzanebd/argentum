-- Slack thread keying. Per the add-channel playbook: a platform with native
-- threads keys on the platform's thread id and skips fork classification,
-- because the user already drew the boundary.
--
-- Slack has both shapes, so both columns are keys:
--   * a message inside a thread carries thread_ts  -> (channel_id, thread_ts)
--   * a top-level mention or DM carries none       -> (channel_id, user_id)
--
-- The two agree rather than compete. A top-level trigger stores the ts our
-- reply will thread under as slack_thread_ts, so the follow-ups that arrive
-- inside that new thread resolve to this same row instead of opening a
-- second conversation for what the user sees as one.
--
-- slack_channel_id is part of both keys because Slack's `ts` is only unique
-- within a channel.

ALTER TABLE conversation_threads
    ADD COLUMN IF NOT EXISTS slack_team_id    TEXT,
    ADD COLUMN IF NOT EXISTS slack_channel_id TEXT,
    ADD COLUMN IF NOT EXISTS slack_thread_ts  TEXT,
    ADD COLUMN IF NOT EXISTS slack_user_id    TEXT;

-- One Slack thread is one conversation. The unique index is what makes that
-- an invariant of the schema rather than of the resolver: a concurrent pair
-- of events in the same thread cannot open two rows.
CREATE UNIQUE INDEX IF NOT EXISTS idx_threads_slack_thread
    ON conversation_threads(company_id, slack_channel_id, slack_thread_ts)
    WHERE channel = 'slack' AND slack_thread_ts IS NOT NULL AND NOT is_archived;

-- The fallback lookup: the newest conversation this person has in this
-- channel, used when an inbound message carries no thread_ts. Not unique —
-- archiving a thread and starting another is ordinary.
CREATE INDEX IF NOT EXISTS idx_threads_slack_user
    ON conversation_threads(company_id, slack_channel_id, slack_user_id, last_message_at DESC)
    WHERE channel = 'slack';
