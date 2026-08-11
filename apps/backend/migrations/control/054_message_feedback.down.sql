-- Reversing this discards every rating anyone has given. Unlike most down
-- migrations in this directory, the loss is not recoverable from anywhere
-- else: a thumbs-down is the only record that an answer was wrong, and it
-- exists nowhere but here — not in the transcript, which holds what was said
-- rather than whether it was right, and not in agent_actions, which records
-- that a query ran rather than that it answered.
--
-- Anything T-Q8 learned from these rows stays learned, which is worse than it
-- sounds: the cookbook would keep its examples and lose the ability to explain
-- why they were chosen.

DROP INDEX IF EXISTS idx_message_feedback_negative;
DROP INDEX IF EXISTS idx_message_feedback_company_recent;
DROP TABLE IF EXISTS message_feedback;
