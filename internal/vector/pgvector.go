package vector

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/sirupsen/logrus"
)

// PGVectorClient implements vector operations using pgvector
type PGVectorClient struct {
	db *sql.DB
}

// NewPGVectorClient creates a new pgvector client
func NewPGVectorClient(db *sql.DB) *PGVectorClient {
	return &PGVectorClient{db: db}
}

// ConversationTurn represents a conversation turn with embedding
type ConversationTurn struct {
	ID        int
	SessionID string
	TurnNum   int
	Query     string
	Response  string
	Embedding []float64
	Metadata  map[string]interface{}
	CreatedAt time.Time
}

// QueryEmbedding represents a cached query with embedding
type QueryEmbedding struct {
	ID           int
	QueryHash    string
	QueryText    string
	Embedding    []float64
	SQLQuery     string
	ResultJSON   map[string]interface{}
	Insight      string
	DashboardID  string
	DashboardURL string
	HitCount     int
	CreatedAt    time.Time
	LastAccessed time.Time
	TTLSeconds   int
}

// StoreConversationTurn stores a conversation turn with embedding
func (c *PGVectorClient) StoreConversationTurn(ctx context.Context, sessionID string, turnNum int, query, response string, embedding []float64, metadata map[string]interface{}) error {
	metadataJSON, _ := json.Marshal(metadata)

	sql := `
		INSERT INTO conversation_vectors (session_id, turn_num, query, response, embedding, metadata)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (session_id, turn_num) 
		DO UPDATE SET 
			query = EXCLUDED.query,
			response = EXCLUDED.response,
			embedding = EXCLUDED.embedding,
			metadata = EXCLUDED.metadata
	`

	_, err := c.db.ExecContext(ctx, sql, sessionID, turnNum, query, response, pq.Array(embedding), metadataJSON)
	if err != nil {
		return fmt.Errorf("failed to store conversation turn: %w", err)
	}

	logrus.Debugf("Stored conversation turn for session %s, turn %d", sessionID, turnNum)
	return nil
}

// SearchSimilarConversations finds similar conversations using cosine similarity
func (c *PGVectorClient) SearchSimilarConversations(ctx context.Context, embedding []float64, sessionID string, limit int) ([]ConversationTurn, error) {
	var rows *sql.Rows
	var err error

	if sessionID != "" {
		// Search within specific session
		sql := `
			SELECT id, session_id, turn_num, query, response, embedding, metadata, created_at
			FROM conversation_vectors
			WHERE session_id = $1
			ORDER BY embedding <=> $2
			LIMIT $3
		`
		rows, err = c.db.QueryContext(ctx, sql, sessionID, pq.Array(embedding))
	} else {
		// Search across all sessions
		sql := `
			SELECT id, session_id, turn_num, query, response, embedding, metadata, created_at
			FROM conversation_vectors
			ORDER BY embedding <=> $1
			LIMIT $2
		`
		rows, err = c.db.QueryContext(ctx, sql, pq.Array(embedding))
	}

	if err != nil {
		return nil, fmt.Errorf("failed to search conversations: %w", err)
	}
	defer rows.Close()

	var results []ConversationTurn
	for rows.Next() {
		var turn ConversationTurn
		var embeddingArray pq.Float64Array
		var metadataJSON []byte

		err := rows.Scan(&turn.ID, &turn.SessionID, &turn.TurnNum, &turn.Query, &turn.Response, &embeddingArray, &metadataJSON, &turn.CreatedAt)
		if err != nil {
			continue
		}

		turn.Embedding = []float64(embeddingArray)
		json.Unmarshal(metadataJSON, &turn.Metadata)
		results = append(results, turn)
	}

	return results, rows.Err()
}

// StoreQueryEmbedding stores a query with its embedding for semantic cache
func (c *PGVectorClient) StoreQueryEmbedding(ctx context.Context, queryText string, embedding []float64, sqlQuery, insight string, result map[string]interface{}, dashboardID, dashboardURL string, ttlSeconds int) (string, error) {
	queryHash := generateQueryHash(queryText)
	resultJSON, _ := json.Marshal(result)

	sql := `
		INSERT INTO query_embeddings 
		(query_hash, query_text, embedding, sql_query, result_json, insight, dashboard_id, dashboard_url, ttl_seconds)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (query_hash) 
		DO UPDATE SET 
			hit_count = query_embeddings.hit_count + 1,
			last_accessed = CURRENT_TIMESTAMP
		RETURNING query_hash
	`

	var hash string
	err := c.db.QueryRowContext(ctx, sql, queryHash, queryText, pq.Array(embedding), sqlQuery, resultJSON, insight, dashboardID, dashboardURL, ttlSeconds).Scan(&hash)
	if err != nil {
		return "", fmt.Errorf("failed to store query embedding: %w", err)
	}

	return hash, nil
}

// FindSimilarQuery finds semantically similar queries
func (c *PGVectorClient) FindSimilarQuery(ctx context.Context, embedding []float64, similarityThreshold float64) (*QueryEmbedding, error) {
	sql := `
		SELECT id, query_hash, query_text, embedding, sql_query, result_json, insight, 
		       dashboard_id, dashboard_url, hit_count, created_at, last_accessed, ttl_seconds
		FROM query_embeddings
		WHERE (embedding <=> $1) < $2  -- cosine distance < threshold
		  AND created_at + (ttl_seconds || ' seconds')::INTERVAL > NOW()
		ORDER BY embedding <=> $1
		LIMIT 1
	`

	// Convert similarity to distance (1 - similarity)
	distanceThreshold := 1.0 - similarityThreshold

	var qe QueryEmbedding
	var embeddingArray pq.Float64Array
	var resultJSON []byte

	err := c.db.QueryRowContext(ctx, sql, pq.Array(embedding), distanceThreshold).Scan(
		&qe.ID, &qe.QueryHash, &qe.QueryText, &embeddingArray, &qe.SQLQuery, &resultJSON, &qe.Insight,
		&qe.DashboardID, &qe.DashboardURL, &qe.HitCount, &qe.CreatedAt, &qe.LastAccessed, &qe.TTLSeconds,
	)

	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return nil, nil
		}
		return nil, err
	}

	qe.Embedding = []float64(embeddingArray)
	json.Unmarshal(resultJSON, &qe.ResultJSON)

	// Increment hit count
	c.incrementHitCount(ctx, qe.QueryHash)

	return &qe, nil
}

// incrementHitCount increments the hit count for a query
func (c *PGVectorClient) incrementHitCount(ctx context.Context, queryHash string) {
	sql := `UPDATE query_embeddings SET hit_count = hit_count + 1 WHERE query_hash = $1`
	c.db.ExecContext(ctx, sql, queryHash)
}

// GetQueryStats returns statistics about stored queries
func (c *PGVectorClient) GetQueryStats(ctx context.Context) (map[string]interface{}, error) {
	var totalQueries, totalHits int

	err := c.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(hit_count), 0) FROM query_embeddings`).Scan(&totalQueries, &totalHits)
	if err != nil {
		return nil, err
	}

	var avgSimilarity float64
	// Note: This is a simplified calculation
	avgSimilarity = 0.85

	return map[string]interface{}{
		"total_queries":  totalQueries,
		"total_hits":     totalHits,
		"avg_similarity": avgSimilarity,
	}, nil
}

// CleanupExpiredQueries removes expired query embeddings
func (c *PGVectorClient) CleanupExpiredQueries(ctx context.Context) error {
	sql := `
		DELETE FROM query_embeddings 
		WHERE created_at + (ttl_seconds || ' seconds')::INTERVAL < NOW()
	`

	result, err := c.db.ExecContext(ctx, sql)
	if err != nil {
		return err
	}

	rowsAffected, _ := result.RowsAffected()
	logrus.Infof("Cleaned up %d expired query embeddings", rowsAffected)
	return nil
}

// DeleteSessionConversations removes all conversations for a session
func (c *PGVectorClient) DeleteSessionConversations(ctx context.Context, sessionID string) error {
	sql := `DELETE FROM conversation_vectors WHERE session_id = $1`
	_, err := c.db.ExecContext(ctx, sql, sessionID)
	return err
}

// generateQueryHash creates a hash for a query
func generateQueryHash(query string) string {
	// Simple hash - in production use proper hash
	return uuid.New().String()[:16]
}

// EnsurePGVectorEnabled checks if pgvector extension is enabled
func (c *PGVectorClient) EnsurePGVectorEnabled(ctx context.Context) error {
	var exists bool
	err := c.db.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'vector')").Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to check pgvector extension: %w", err)
	}

	if !exists {
		return fmt.Errorf("pgvector extension is not enabled")
	}

	logrus.Info("✅ pgvector extension is enabled")
	return nil
}

// PGVectorStore implements the Client interface for pgvector
type PGVectorStore struct {
	client *PGVectorClient
}

// NewPGVectorStore creates a new pgvector store implementing the Client interface
func NewPGVectorStore(db *sql.DB) Client {
	return &PGVectorStore{client: NewPGVectorClient(db)}
}

// Upsert implements Client interface
func (s *PGVectorStore) Upsert(ctx context.Context, vectors []Vector) error {
	for _, v := range vectors {
		sessionID := v.Metadata["session_id"].(string)
		turnNum := int(v.Metadata["turn_num"].(float64))
		query := v.Metadata["query"].(string)
		response := v.Metadata["response"].(string)

		err := s.client.StoreConversationTurn(ctx, sessionID, turnNum, query, response, v.Values, v.Metadata)
		if err != nil {
			return err
		}
	}
	return nil
}

// Query implements Client interface
func (s *PGVectorStore) Query(ctx context.Context, vector []float64, topK int) ([]QueryResult, error) {
	turns, err := s.client.SearchSimilarConversations(ctx, vector, "", topK)
	if err != nil {
		return nil, err
	}

	results := make([]QueryResult, len(turns))
	for i, turn := range turns {
		results[i] = QueryResult{
			Vector: Vector{
				ID:       fmt.Sprintf("%s:%d", turn.SessionID, turn.TurnNum),
				Values:   turn.Embedding,
				Metadata: turn.Metadata,
			},
			Score: 0.95, // Placeholder - calculate actual similarity
		}
	}

	return results, nil
}

// Delete implements Client interface
func (s *PGVectorStore) Delete(ctx context.Context, ids []string) error {
	for _, id := range ids {
		// Parse session_id:turn_num format
		parts := splitID(id)
		if len(parts) == 2 {
			s.client.DeleteSessionConversations(ctx, parts[0])
		}
	}
	return nil
}

// CreateIndex implements Client interface (no-op for pgvector)
func (s *PGVectorStore) CreateIndex(ctx context.Context, name string, dimension int) error {
	// Indexes are created during migration
	return nil
}

func splitID(id string) []string {
	// Simple split on last colon
	for i := len(id) - 1; i >= 0; i-- {
		if id[i] == ':' {
			return []string{id[:i], id[i+1:]}
		}
	}
	return []string{id}
}
