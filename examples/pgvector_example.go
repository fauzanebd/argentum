package main

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/fauzanebd/argentum/internal/vector"
	_ "github.com/lib/pq"
)

func main() {
	// Connect to database
	db, err := sql.Open("postgres", "host=localhost port=5432 user=analytics password=analytics123 dbname=analytics_db sslmode=disable")
	if err != nil {
		panic(err)
	}
	defer db.Close()

	// Create pgvector client
	client := vector.NewPGVectorClient(db)

	// Check if pgvector is enabled
	ctx := context.Background()
	if err := client.EnsurePGVectorEnabled(ctx); err != nil {
		panic(err)
	}

	// Example: Store a conversation turn
	sessionID := "session-123"
	embedding := make([]float64, 1536) // OpenAI embedding dimension
	for i := range embedding {
		embedding[i] = float64(i%10) * 0.1 // Dummy embedding
	}

	err = client.StoreConversationTurn(
		ctx,
		sessionID,
		1, // turn number
		"What were our sales last month?",
		"Last month sales were $450,000",
		embedding,
		map[string]interface{}{
			"timestamp": "2024-01-01T00:00:00Z",
		},
	)
	if err != nil {
		panic(err)
	}

	fmt.Println("✅ Stored conversation turn")

	// Example: Store query embedding
	queryHash, err := client.StoreQueryEmbedding(
		ctx,
		"sales last month",
		embedding,
		"SELECT SUM(amount) FROM sales WHERE date >= '2024-01-01'",
		"Last month sales totaled $450,000",
		map[string]interface{}{"total": 450000},
		"dash-123",
		"https://metabase/dashboard/123",
		3600, // TTL: 1 hour
	)
	if err != nil {
		panic(err)
	}

	fmt.Printf("✅ Stored query embedding: %s\n", queryHash)

	// Example: Find similar query
	similarQuery, err := client.FindSimilarQuery(ctx, embedding, 0.85)
	if err != nil {
		panic(err)
	}

	if similarQuery != nil {
		fmt.Printf("✅ Found similar query: %s (hits: %d)\n", similarQuery.QueryText, similarQuery.HitCount)
	} else {
		fmt.Println("No similar queries found")
	}

	// Example: Search similar conversations
	turns, err := client.SearchSimilarConversations(ctx, embedding, sessionID, 5)
	if err != nil {
		panic(err)
	}

	fmt.Printf("✅ Found %d similar conversations\n", len(turns))
	for _, turn := range turns {
		fmt.Printf("  - Q: %s\n", turn.Query)
	}

	// Get stats
	stats, err := client.GetQueryStats(ctx)
	if err != nil {
		panic(err)
	}

	fmt.Printf("\n📊 Stats: %+v\n", stats)
}
