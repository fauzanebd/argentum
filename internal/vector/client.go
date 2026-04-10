package vector

import (
	"context"
	"fmt"
	"time"
)

// Client interface for vector database operations
// This interface is implemented by PGVectorStore (in pgvector.go)
type Client interface {
	Upsert(ctx context.Context, vectors []Vector) error
	Query(ctx context.Context, vector []float64, topK int) ([]QueryResult, error)
	Delete(ctx context.Context, ids []string) error
	CreateIndex(ctx context.Context, name string, dimension int) error
}

// Vector represents a vector with metadata
type Vector struct {
	ID       string                 `json:"id"`
	Values   []float64              `json:"values"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// QueryResult represents a similarity search result
type QueryResult struct {
	Vector Vector  `json:"vector"`
	Score  float64 `json:"score"`
}

// ConversationStore provides high-level conversation management
// Works with any Client implementation (pgvector, pinecone, etc.)
type ConversationStore struct {
	client    Client
	indexName string
}

// NewConversationStore creates a conversation store
func NewConversationStore(client Client) *ConversationStore {
	return &ConversationStore{
		client:    client,
		indexName: "conversations",
	}
}

// StoreConversation stores a conversation turn with embedding
func (cs *ConversationStore) StoreConversation(ctx context.Context, sessionID string, turnNum int,
	query, response string, embedding []float64) error {

	vector := Vector{
		ID:     fmt.Sprintf("%s:%d", sessionID, turnNum),
		Values: embedding,
		Metadata: map[string]interface{}{
			"session_id": sessionID,
			"turn_num":   turnNum,
			"query":      query,
			"response":   response,
			"timestamp":  time.Now().Unix(),
		},
	}

	return cs.client.Upsert(ctx, []Vector{vector})
}

// RetrieveRelevant retrieves relevant past conversations using semantic similarity
func (cs *ConversationStore) RetrieveRelevant(ctx context.Context, queryEmbedding []float64,
	sessionID string, topK int) ([]QueryResult, error) {

	results, err := cs.client.Query(ctx, queryEmbedding, topK)
	if err != nil {
		return nil, err
	}

	// Filter by session if provided
	if sessionID != "" {
		var filtered []QueryResult
		for _, r := range results {
			if r.Vector.Metadata["session_id"] == sessionID {
				filtered = append(filtered, r)
			}
		}
		return filtered, nil
	}

	return results, nil
}

// Note: For production and development, we use pgvector (PostgreSQL extension)
// See pgvector.go for the PGVectorClient implementation
// Pinecone and other vector stores can be added here in the future by implementing the Client interface
