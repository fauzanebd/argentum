-- Reversing 002 drops every conversation and every message in it. Messages
-- first: they reference the thread.
DROP TABLE IF EXISTS messages;
DROP TABLE IF EXISTS conversation_threads;
