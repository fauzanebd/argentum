-- Migration 005: Drop unused pgvector tables
-- These tables were scaffolded but never wired into the running application.
-- Conversation memory is now handled by agent-sdk-go's pkg/memory (buffer/window/Redis).
-- Semantic caching can be added later as a middleware layer.
-- The pgvector extension itself is kept for potential future use.

\c analytics_db;

DROP TABLE IF EXISTS conversation_vectors CASCADE;
DROP TABLE IF EXISTS query_embeddings CASCADE;

-- Keep the pgvector extension available for future use
-- DO NOT drop: CREATE EXTENSION IF NOT EXISTS vector;
