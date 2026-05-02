-- Migration: Enable pgvector extension for vector storage
\c demo_analytics;

-- Enable pgvector extension
CREATE EXTENSION IF NOT EXISTS vector;

-- Create table for conversation embeddings
CREATE TABLE IF NOT EXISTS conversation_vectors (
    id SERIAL PRIMARY KEY,
    session_id VARCHAR(255) NOT NULL,
    turn_num INTEGER NOT NULL,
    query TEXT NOT NULL,
    response TEXT NOT NULL,
    embedding vector(1536),  -- OpenAI embedding dimension
    metadata JSONB,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(session_id, turn_num)
);

-- Create index for vector similarity search
CREATE INDEX IF NOT EXISTS idx_conversation_vectors_embedding 
ON conversation_vectors 
USING ivfflat (embedding vector_cosine_ops)
WITH (lists = 100);

-- Create index for session lookups
CREATE INDEX IF NOT EXISTS idx_conversation_vectors_session 
ON conversation_vectors(session_id);

-- Create table for query embeddings (semantic cache)
CREATE TABLE IF NOT EXISTS query_embeddings (
    id SERIAL PRIMARY KEY,
    query_hash VARCHAR(64) UNIQUE NOT NULL,
    query_text TEXT NOT NULL,
    embedding vector(1536),
    sql_query TEXT,
    result_json JSONB,
    insight TEXT,
    dashboard_id VARCHAR(255),
    dashboard_url TEXT,
    hit_count INTEGER DEFAULT 0,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    last_accessed TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    ttl_seconds INTEGER DEFAULT 3600
);

-- Create index for vector similarity search on queries
CREATE INDEX IF NOT EXISTS idx_query_embeddings_embedding 
ON query_embeddings 
USING ivfflat (embedding vector_cosine_ops)
WITH (lists = 100);

-- Create function to update last_accessed timestamp
CREATE OR REPLACE FUNCTION update_last_accessed()
RETURNS TRIGGER AS $$
BEGIN
    NEW.last_accessed = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Create trigger to auto-update last_accessed
DROP TRIGGER IF EXISTS trigger_update_last_accessed ON query_embeddings;
CREATE TRIGGER trigger_update_last_accessed
    BEFORE UPDATE ON query_embeddings
    FOR EACH ROW
    EXECUTE FUNCTION update_last_accessed();

-- Grant permissions to analytics_reader
GRANT SELECT, INSERT, UPDATE ON conversation_vectors TO analytics_reader;
GRANT SELECT, INSERT, UPDATE ON query_embeddings TO analytics_reader;
GRANT USAGE, SELECT ON SEQUENCE conversation_vectors_id_seq TO analytics_reader;
GRANT USAGE, SELECT ON SEQUENCE query_embeddings_id_seq TO analytics_reader;

-- Verify pgvector is installed
SELECT * FROM pg_extension WHERE extname = 'vector';
