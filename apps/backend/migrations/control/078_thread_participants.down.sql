-- Reverses 078.
--
-- Safe in a way 077's down is not: every single-agent thread's membership is
-- reconstructible from conversation_threads.agent_id, which this migration
-- never touched. What is lost is the membership of any thread with more than
-- one participant — and on a database where those exist, dropping this table
-- silently turns every room back into a one-agent conversation. There is no
-- half-measure that would be more honest: the rows have nowhere else to live.
DROP TABLE IF EXISTS thread_participants;
