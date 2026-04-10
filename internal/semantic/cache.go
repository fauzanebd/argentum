package semantic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
)

// Cache provides semantic caching based on query meaning
type Cache struct {
	redisClient         *redis.Client
	ctx                 context.Context
	embeddingFunc       EmbeddingFunc
	similarityThreshold float64
	defaultTTL          time.Duration
	timeSensitiveTTL    time.Duration
}

// EmbeddingFunc generates embeddings for text
type EmbeddingFunc func(text string) ([]float64, error)

// CachedResult represents a cached query with embedding
type CachedResult struct {
	OriginalQuery       string                 `json:"original_query"`
	Embedding           []float64              `json:"embedding"`
	NormalizedEmbedding []float64              `json:"normalized_embedding"`
	SQL                 string                 `json:"sql"`
	Result              map[string]interface{} `json:"result"`
	Insight             string                 `json:"insight"`
	DashboardID         string                 `json:"dashboard_id,omitempty"`
	DashboardURL        string                 `json:"dashboard_url,omitempty"`
	Timestamp           time.Time              `json:"timestamp"`
	TTL                 int                    `json:"ttl"`
	QueryType           string                 `json:"query_type"`
	HitCount            int                    `json:"hit_count"`
	IsTimeSensitive     bool                   `json:"is_time_sensitive"`
}

// Time-sensitive SQL patterns
var timeSensitivePatterns = []string{
	// Date/Time columns
	"date", "timestamp", "datetime", "time",
	// PostgreSQL date functions
	"now()", "current_date", "current_timestamp", "localtimestamp",
	"date_trunc", "date_part", "extract(",
	// Interval operations
	"interval", "age(",
	// Relative time keywords in SQL
	"today", "yesterday", "tomorrow",
	"this_month", "last_month", "next_month",
	"this_year", "last_year", "next_year",
}

// NewCache creates a new semantic cache
func NewCache(redisClient *redis.Client, embeddingFunc EmbeddingFunc) *Cache {
	return &Cache{
		redisClient:         redisClient,
		ctx:                 context.Background(),
		embeddingFunc:       embeddingFunc,
		similarityThreshold: 0.85, // 85% similarity threshold
		defaultTTL:          24 * time.Hour,
		timeSensitiveTTL:    5 * time.Minute,
	}
}

// IsTimeSensitiveSQL checks if SQL query contains time-sensitive patterns
func IsTimeSensitiveSQL(sql string) bool {
	sqlLower := strings.ToLower(sql)

	for _, pattern := range timeSensitivePatterns {
		if strings.Contains(sqlLower, pattern) {
			return true
		}
	}

	return false
}

// CalculateTTL determines appropriate TTL based on SQL content
func CalculateTTL(sql string) time.Duration {
	if IsTimeSensitiveSQL(sql) {
		// Time-sensitive queries get short TTL
		return 5 * time.Minute
	}

	// Non-time-sensitive queries can be cached longer
	return 24 * time.Hour
}

// Get retrieves cached result based on semantic similarity
func (c *Cache) Get(query string) (*CachedResult, error) {
	// Generate embedding for query
	embedding, err := c.embeddingFunc(query)
	if err != nil {
		return nil, fmt.Errorf("failed to generate embedding: %w", err)
	}

	normalized := normalizeVector(embedding)

	// Search for similar queries in cache
	bucketKey := c.getBucketKey(normalized)

	cachedData, err := c.redisClient.Get(c.ctx, bucketKey).Result()
	if err == redis.Nil {
		// No bucket found, try broader search
		return c.fallbackSearch(normalized)
	}
	if err != nil {
		return nil, err
	}

	var cached CachedResult
	if err := json.Unmarshal([]byte(cachedData), &cached); err != nil {
		return nil, err
	}

	// Calculate similarity
	similarity := cosineSimilarity(normalized, cached.NormalizedEmbedding)

	if similarity >= c.similarityThreshold {
		// Check if still fresh (respect time-sensitive TTL)
		maxAge := time.Duration(cached.TTL) * time.Second
		if cached.IsTimeSensitive {
			// Use stricter freshness check for time-sensitive queries
			maxAge = 5 * time.Minute
		}

		if time.Since(cached.Timestamp) > maxAge {
			logrus.Debugf("Cache entry expired (time-sensitive: %v, age: %v)",
				cached.IsTimeSensitive, time.Since(cached.Timestamp))
			c.redisClient.Del(c.ctx, bucketKey)
			return nil, nil
		}

		// Update hit count
		cached.HitCount++
		c.updateHitCount(bucketKey, &cached)

		logrus.Infof("Semantic cache hit (similarity: %.2f, time-sensitive: %v): '%s' -> '%s'",
			similarity, cached.IsTimeSensitive, query, cached.OriginalQuery)

		return &cached, nil
	}

	return nil, nil
}

// Set caches a query result with embedding
// Automatically detects time-sensitive queries and sets appropriate TTL
func (c *Cache) Set(query string, result *CachedResult, ttl time.Duration) error {
	// If TTL is 0, calculate based on SQL content
	if ttl == 0 {
		ttl = CalculateTTL(result.SQL)
	}

	// Determine if this is a time-sensitive query
	isTimeSensitive := IsTimeSensitiveSQL(result.SQL)

	// Generate embedding
	embedding, err := c.embeddingFunc(query)
	if err != nil {
		return fmt.Errorf("failed to generate embedding: %w", err)
	}

	result.OriginalQuery = query
	result.Embedding = embedding
	result.NormalizedEmbedding = normalizeVector(embedding)
	result.Timestamp = time.Now()
	result.TTL = int(ttl.Seconds())
	result.HitCount = 0
	result.IsTimeSensitive = isTimeSensitive

	bucketKey := c.getBucketKey(result.NormalizedEmbedding)

	data, err := json.Marshal(result)
	if err != nil {
		return err
	}

	// Store in Redis with TTL
	if err := c.redisClient.Set(c.ctx, bucketKey, data, ttl).Err(); err != nil {
		return err
	}

	// Also store in search index for broader queries
	searchKey := fmt.Sprintf("semantic:index:%s", hashEmbedding(result.NormalizedEmbedding))
	c.redisClient.Set(c.ctx, searchKey, bucketKey, ttl)

	if isTimeSensitive {
		logrus.Debugf("Cached time-sensitive query (TTL: %v): %s", ttl, query)
	} else {
		logrus.Debugf("Cached query semantically (TTL: %v): %s", ttl, query)
	}

	return nil
}

// SetWithSQL caches with explicit SQL for time detection
func (c *Cache) SetWithSQL(query, sql string, result *CachedResult) error {
	result.SQL = sql
	ttl := CalculateTTL(sql)
	return c.Set(query, result, ttl)
}

// fallbackSearch searches across all cached queries
func (c *Cache) fallbackSearch(queryEmbedding []float64) (*CachedResult, error) {
	// Get all semantic cache keys
	pattern := "semantic:index:*"
	iter := c.redisClient.Scan(c.ctx, 0, pattern, 100).Iterator()

	var bestMatch *CachedResult
	bestSimilarity := 0.0

	for iter.Next(c.ctx) {
		bucketKey, err := c.redisClient.Get(c.ctx, iter.Val()).Result()
		if err != nil {
			continue
		}

		cachedData, err := c.redisClient.Get(c.ctx, bucketKey).Result()
		if err != nil {
			continue
		}

		var cached CachedResult
		if err := json.Unmarshal([]byte(cachedData), &cached); err != nil {
			continue
		}

		similarity := cosineSimilarity(queryEmbedding, cached.NormalizedEmbedding)
		if similarity > bestSimilarity && similarity >= c.similarityThreshold {
			// Check if still fresh
			maxAge := time.Duration(cached.TTL) * time.Second
			if cached.IsTimeSensitive {
				maxAge = 5 * time.Minute
			}

			if time.Since(cached.Timestamp) <= maxAge {
				bestSimilarity = similarity
				cachedCopy := cached
				bestMatch = &cachedCopy
			}
		}
	}

	if err := iter.Err(); err != nil {
		return nil, err
	}

	if bestMatch != nil {
		logrus.Infof("Semantic cache hit via search (similarity: %.2f, time-sensitive: %v): '%s'",
			bestSimilarity, bestMatch.IsTimeSensitive, bestMatch.OriginalQuery)
		return bestMatch, nil
	}

	return nil, nil
}

// getBucketKey creates a hash bucket key for LSH
func (c *Cache) getBucketKey(embedding []float64) string {
	bucketDims := 16
	if len(embedding) < bucketDims {
		bucketDims = len(embedding)
	}

	bucket := "semantic:bucket:"
	for i := 0; i < bucketDims; i++ {
		if embedding[i] > 0 {
			bucket += "1"
		} else {
			bucket += "0"
		}
	}

	return bucket
}

// updateHitCount updates the hit count in cache
func (c *Cache) updateHitCount(key string, result *CachedResult) {
	data, _ := json.Marshal(result)
	remainingTTL, _ := c.redisClient.TTL(c.ctx, key).Result()
	c.redisClient.Set(c.ctx, key, data, remainingTTL)
}

// cosineSimilarity calculates cosine similarity between two vectors
func cosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) {
		return 0
	}

	dotProduct := 0.0
	normA := 0.0
	normB := 0.0

	for i := 0; i < len(a); i++ {
		dotProduct += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dotProduct / (math.Sqrt(normA) * math.Sqrt(normB))
}

// normalizeVector normalizes a vector to unit length
func normalizeVector(v []float64) []float64 {
	norm := 0.0
	for _, val := range v {
		norm += val * val
	}
	norm = math.Sqrt(norm)

	if norm == 0 {
		return v
	}

	normalized := make([]float64, len(v))
	for i := range v {
		normalized[i] = v[i] / norm
	}

	return normalized
}

// hashEmbedding creates a hash of the embedding for indexing
func hashEmbedding(embedding []float64) string {
	data, _ := json.Marshal(embedding)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:8])
}

// GetCacheStats returns semantic cache statistics
func (c *Cache) GetCacheStats() (map[string]interface{}, error) {
	bucketCount := 0
	iter := c.redisClient.Scan(c.ctx, 0, "semantic:bucket:*", 1000).Iterator()
	for iter.Next(c.ctx) {
		bucketCount++
	}

	indexCount := 0
	iter = c.redisClient.Scan(c.ctx, 0, "semantic:index:*", 1000).Iterator()
	for iter.Next(c.ctx) {
		indexCount++
	}

	return map[string]interface{}{
		"buckets":            bucketCount,
		"index_entries":      indexCount,
		"threshold":          c.similarityThreshold,
		"default_ttl":        c.defaultTTL.String(),
		"time_sensitive_ttl": c.timeSensitiveTTL.String(),
	}, nil
}

// InvalidateCache clears all semantic cache entries
func (c *Cache) InvalidateCache() error {
	patterns := []string{"semantic:bucket:*", "semantic:index:*"}

	for _, pattern := range patterns {
		iter := c.redisClient.Scan(c.ctx, 0, pattern, 1000).Iterator()
		for iter.Next(c.ctx) {
			c.redisClient.Del(c.ctx, iter.Val())
		}
	}

	logrus.Info("Semantic cache invalidated")
	return nil
}
