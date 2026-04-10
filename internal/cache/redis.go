package cache

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
)

// Cache provides multi-layer caching capabilities
type Cache struct {
	client *redis.Client
	ctx    context.Context
}

// NewCache creates a new cache instance
func NewCache(redisURL string) (*Cache, error) {
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse redis URL: %w", err)
	}

	client := redis.NewClient(opt)
	ctx := context.Background()

	// Test connection
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to redis: %w", err)
	}

	logrus.Info("Successfully connected to Redis cache")
	return &Cache{
		client: client,
		ctx:    ctx,
	}, nil
}

// SQLResultCacheEntry represents cached SQL query results
type SQLResultCacheEntry struct {
	Result    interface{} `json:"result"`
	SQL       string      `json:"sql"`
	Timestamp time.Time   `json:"timestamp"`
	QueryType string      `json:"query_type"`
}

// ConversationCacheEntry represents cached conversation context
type ConversationCacheEntry struct {
	SessionID    string                 `json:"session_id"`
	Turns        []ConversationTurn     `json:"turns"`
	Summary      string                 `json:"summary"`
	Topic        string                 `json:"topic"`
	LastActivity time.Time              `json:"last_activity"`
	Metadata     map[string]interface{} `json:"metadata"`
}

// ConversationTurn represents a single conversation turn
type ConversationTurn struct {
	Query     string    `json:"query"`
	Response  string    `json:"response"`
	Timestamp time.Time `json:"timestamp"`
}

// DashboardCacheEntry represents cached dashboard info
type DashboardCacheEntry struct {
	DashboardID string    `json:"dashboard_id"`
	PublicURL   string    `json:"public_url"`
	SQL         string    `json:"sql"`
	ChartType   string    `json:"chart_type"`
	CreatedAt   time.Time `json:"created_at"`
}

// QueryType determines cache TTL based on query characteristics
type QueryType string

const (
	QueryTypeToday       QueryType = "today"
	QueryTypeThisMonth   QueryType = "this_month"
	QueryTypeLastMonth   QueryType = "last_month"
	QueryTypeHistorical  QueryType = "historical"
	QueryTypeAggregation QueryType = "aggregation"
)

// CalculateTTL returns appropriate TTL based on query type
func CalculateTTL(queryType QueryType) time.Duration {
	switch queryType {
	case QueryTypeToday:
		return 5 * time.Minute
	case QueryTypeThisMonth:
		return 30 * time.Minute
	case QueryTypeLastMonth:
		return 2 * time.Hour
	case QueryTypeHistorical:
		return 24 * time.Hour
	case QueryTypeAggregation:
		return 1 * time.Hour
	default:
		return 30 * time.Minute
	}
}

// InferQueryType analyzes SQL to determine query type
func InferQueryType(sql string) QueryType {
	sqlLower := ""
	for _, r := range sql {
		sqlLower += string(r | 32) // lowercase
	}

	if contains(sqlLower, "current_date", "today", "now()", "date_trunc('day'", "date('now'") {
		return QueryTypeToday
	}
	if contains(sqlLower, "current_month", "date_trunc('month'", "this month") {
		return QueryTypeThisMonth
	}
	if contains(sqlLower, "last_month", "previous_month") {
		return QueryTypeLastMonth
	}
	if contains(sqlLower, "sum(", "count(", "avg(", "min(", "max(", "group by") {
		return QueryTypeAggregation
	}
	if contains(sqlLower, "2023", "2022", "2021", "2020") {
		return QueryTypeHistorical
	}
	return QueryTypeAggregation
}

func contains(s string, substrs ...string) bool {
	for _, substr := range substrs {
		if len(substr) > len(s) {
			continue
		}
		for i := 0; i <= len(s)-len(substr); i++ {
			if s[i:i+len(substr)] == substr {
				return true
			}
		}
	}
	return false
}

// GetSQLResult retrieves cached SQL results
func (c *Cache) GetSQLResult(sql string) (*SQLResultCacheEntry, error) {
	key := fmt.Sprintf("sql:%x", sha256.Sum256([]byte(sql)))

	data, err := c.client.Get(c.ctx, key).Result()
	if err == redis.Nil {
		return nil, nil // Cache miss
	}
	if err != nil {
		return nil, err
	}

	var entry SQLResultCacheEntry
	if err := json.Unmarshal([]byte(data), &entry); err != nil {
		return nil, err
	}

	// Check if still fresh
	queryType := InferQueryType(sql)
	ttl := CalculateTTL(queryType)

	if time.Since(entry.Timestamp) > ttl {
		// Stale cache entry
		c.client.Del(c.ctx, key)
		return nil, nil
	}

	logrus.Debugf("SQL cache hit for query: %s", sql[:min(50, len(sql))])
	return &entry, nil
}

// SetSQLResult caches SQL results
func (c *Cache) SetSQLResult(sql string, result interface{}) error {
	key := fmt.Sprintf("sql:%x", sha256.Sum256([]byte(sql)))

	queryType := InferQueryType(sql)
	ttl := CalculateTTL(queryType)

	entry := SQLResultCacheEntry{
		Result:    result,
		SQL:       sql,
		Timestamp: time.Now(),
		QueryType: string(queryType),
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}

	return c.client.Set(c.ctx, key, data, ttl).Err()
}

// GetConversation retrieves cached conversation context
func (c *Cache) GetConversation(sessionID string) (*ConversationCacheEntry, error) {
	key := fmt.Sprintf("conv:%s", sessionID)

	data, err := c.client.Get(c.ctx, key).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var entry ConversationCacheEntry
	if err := json.Unmarshal([]byte(data), &entry); err != nil {
		return nil, err
	}

	return &entry, nil
}

// SetConversation caches conversation context
func (c *Cache) SetConversation(sessionID string, entry *ConversationCacheEntry) error {
	key := fmt.Sprintf("conv:%s", sessionID)

	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}

	// Conversations cached for 48 hours
	return c.client.Set(c.ctx, key, data, 48*time.Hour).Err()
}

// UpdateConversationTurn adds a turn to cached conversation
func (c *Cache) UpdateConversationTurn(sessionID string, query, response string) error {
	entry, err := c.GetConversation(sessionID)
	if err != nil {
		return err
	}

	if entry == nil {
		entry = &ConversationCacheEntry{
			SessionID:    sessionID,
			Turns:        []ConversationTurn{},
			Metadata:     make(map[string]interface{}),
			LastActivity: time.Now(),
		}
	}

	// Add new turn
	entry.Turns = append(entry.Turns, ConversationTurn{
		Query:     query,
		Response:  response,
		Timestamp: time.Now(),
	})

	entry.LastActivity = time.Now()

	return c.SetConversation(sessionID, entry)
}

// GetDashboard retrieves cached dashboard info
func (c *Cache) GetDashboard(sql string) (*DashboardCacheEntry, error) {
	key := fmt.Sprintf("dash:%x", sha256.Sum256([]byte(sql)))

	data, err := c.client.Get(c.ctx, key).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var entry DashboardCacheEntry
	if err := json.Unmarshal([]byte(data), &entry); err != nil {
		return nil, err
	}

	return &entry, nil
}

// SetDashboard caches dashboard information
func (c *Cache) SetDashboard(sql string, entry *DashboardCacheEntry) error {
	key := fmt.Sprintf("dash:%x", sha256.Sum256([]byte(sql)))

	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}

	// Dashboards cached for 24 hours
	return c.client.Set(c.ctx, key, data, 24*time.Hour).Err()
}

// InvalidateSQLCache removes cached SQL results
func (c *Cache) InvalidateSQLCache(pattern string) error {
	// In production, you might want to use SCAN for large datasets
	return nil
}

// GetCacheStats returns cache statistics
func (c *Cache) GetCacheStats() (map[string]interface{}, error) {
	info := c.client.Info(c.ctx, "keyspace")
	val, err := info.Result()
	if err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"keyspace": val,
		"time":     time.Now(),
	}, nil
}

// Close closes the Redis connection
func (c *Cache) Close() error {
	return c.client.Close()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
