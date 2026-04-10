package context

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/fauzanebd/argentum/internal/cache"
	"github.com/fauzanebd/argentum/internal/llm"
	"github.com/sirupsen/logrus"
)

// Manager handles advanced conversation context with summarization and topic detection
type Manager struct {
	llmProvider llm.Provider
	cache       *cache.Cache
	maxTurns    int
	sessionTTL  time.Duration
	mu          sync.RWMutex
	sessions    map[string]*Session
}

// Session represents an active conversation session
type Session struct {
	ID           string
	Turns        []Turn
	Summary      string
	Topic        string
	LastActivity time.Time
	TurnCount    int
	ResetCount   int
}

// Turn represents a single conversation turn
type Turn struct {
	Query     string
	Response  string
	TokensIn  int
	TokensOut int
	Timestamp time.Time
}

// ResetReason represents why a context was reset
type ResetReason string

const (
	ResetTurnLimit   ResetReason = "Turn limit exceeded"
	ResetTopicChange ResetReason = "Topic change detected"
	ResetTimeout     ResetReason = "Session timeout"
	ResetUserCommand ResetReason = "User requested reset"
)

// NewManager creates a new context manager
func NewManager(llmProvider llm.Provider, cache *cache.Cache, maxTurns int) *Manager {
	return &Manager{
		llmProvider: llmProvider,
		cache:       cache,
		maxTurns:    maxTurns,
		sessionTTL:  30 * time.Minute,
		sessions:    make(map[string]*Session),
	}
}

// GetSession retrieves or creates a conversation session
func (m *Manager) GetSession(sessionID string) (*Session, error) {
	m.mu.RLock()
	if session, exists := m.sessions[sessionID]; exists {
		// Check if session expired
		if time.Since(session.LastActivity) > m.sessionTTL {
			m.mu.RUnlock()
			// Reset expired session
			return m.createNewSession(sessionID), nil
		}
		session.LastActivity = time.Now()
		m.mu.RUnlock()
		return session, nil
	}
	m.mu.RUnlock()

	// Try to load from cache
	if m.cache != nil {
		cached, err := m.cache.GetConversation(sessionID)
		if err != nil {
			logrus.Warnf("Failed to load conversation from cache: %v", err)
		}
		if cached != nil {
			session := &Session{
				ID:           sessionID,
				Turns:        make([]Turn, 0, len(cached.Turns)),
				Summary:      cached.Summary,
				Topic:        cached.Topic,
				LastActivity: cached.LastActivity,
			}
			// Convert cache turns to session turns
			for _, t := range cached.Turns {
				session.Turns = append(session.Turns, Turn{
					Query:     t.Query,
					Response:  t.Response,
					Timestamp: t.Timestamp,
				})
			}
			session.TurnCount = len(session.Turns)

			m.mu.Lock()
			m.sessions[sessionID] = session
			m.mu.Unlock()

			return session, nil
		}
	}

	// Create new session
	return m.createNewSession(sessionID), nil
}

// AddTurn adds a new turn to the session with context management
func (m *Manager) AddTurn(ctx context.Context, sessionID, query, response string, tokensIn, tokensOut int) (*Session, error) {
	session, err := m.GetSession(sessionID)
	if err != nil {
		return nil, err
	}

	// Check if we should reset context
	shouldReset, reason := m.shouldResetContext(session, query)
	if shouldReset {
		logrus.Infof("Resetting context for session %s: %s", sessionID, reason)

		// Generate final summary before reset
		if len(session.Turns) > 0 {
			summary, _ := m.generateSummary(ctx, session)
			if summary != "" {
				// Store summary in cache for long-term reference
				m.cacheSessionSummary(sessionID, summary)
			}
		}

		// Create fresh session
		session = m.createNewSession(sessionID)
		session.ResetCount++
	}

	// Add new turn
	turn := Turn{
		Query:     query,
		Response:  response,
		TokensIn:  tokensIn,
		TokensOut: tokensOut,
		Timestamp: time.Now(),
	}

	session.Turns = append(session.Turns, turn)
	session.TurnCount++
	session.LastActivity = time.Now()

	// Keep only recent turns in memory
	if len(session.Turns) > m.maxTurns {
		// Generate summary of older turns before pruning
		if session.Summary == "" {
			summary, err := m.generateSummary(ctx, session)
			if err == nil && summary != "" {
				session.Summary = summary
			}
		}

		// Prune old turns
		session.Turns = session.Turns[len(session.Turns)-m.maxTurns:]
	}

	// Detect topic
	session.Topic = m.detectTopic(query)

	// Update session in memory
	m.mu.Lock()
	m.sessions[sessionID] = session
	m.mu.Unlock()

	// Cache session
	if m.cache != nil {
		m.cacheSession(session)
	}

	return session, nil
}

// BuildPromptContext builds context string for LLM prompts
func (m *Manager) BuildPromptContext(session *Session) string {
	var parts []string

	// Add summary if available
	if session.Summary != "" {
		parts = append(parts, fmt.Sprintf("Previous conversation summary: %s", session.Summary))
	}

	// Add recent turns
	if len(session.Turns) > 0 {
		parts = append(parts, "\nRecent conversation:")
		for _, turn := range session.Turns {
			parts = append(parts, fmt.Sprintf("User: %s", turn.Query))
			parts = append(parts, fmt.Sprintf("Assistant: %s", truncate(turn.Response, 200)))
		}
	}

	// Add topic context
	if session.Topic != "" {
		parts = append(parts, fmt.Sprintf("\nCurrent topic: %s", session.Topic))
	}

	return strings.Join(parts, "\n")
}

// shouldResetContext determines if context should be reset
func (m *Manager) shouldResetContext(session *Session, newQuery string) (bool, ResetReason) {
	// Check turn limit
	if session.TurnCount >= m.maxTurns*3 { // Reset after 3x max turns
		return true, ResetTurnLimit
	}

	// Check timeout
	if time.Since(session.LastActivity) > m.sessionTTL {
		return true, ResetTimeout
	}

	// Check for user reset command
	lowerQuery := ""
	for _, r := range newQuery {
		lowerQuery += string(r | 32) // lowercase
	}
	if contains(lowerQuery, "start over", "new question", "reset", "clear") {
		return true, ResetUserCommand
	}

	// Check topic drift (simplified - in production use embeddings)
	newTopic := m.detectTopic(newQuery)
	if session.Topic != "" && newTopic != "" && session.Topic != newTopic {
		// Simple heuristic: different topics might indicate drift
		// In production, use embedding similarity
		return false, "" // Be conservative about topic-based resets
	}

	return false, ""
}

// generateSummary creates a summary of the conversation using LLM
func (m *Manager) generateSummary(ctx context.Context, session *Session) (string, error) {
	if len(session.Turns) == 0 {
		return "", nil
	}

	// Build conversation text
	var convoParts []string
	for _, turn := range session.Turns {
		convoParts = append(convoParts, fmt.Sprintf("Q: %s\nA: %s", turn.Query, truncate(turn.Response, 100)))
	}
	convoText := strings.Join(convoParts, "\n\n")

	prompt := fmt.Sprintf(`Summarize this conversation in one sentence, including key metrics discovered:

%s

Summary:`, convoText)

	response, err := m.llmProvider.Generate(ctx, prompt,
		llm.WithTemperature(0.3),
		llm.WithMaxTokens(100),
	)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(response.Content), nil
}

// detectTopic identifies the topic of a query (simplified)
func (m *Manager) detectTopic(query string) string {
	lowerQuery := ""
	for _, r := range query {
		lowerQuery += string(r | 32)
	}

	if contains(lowerQuery, "sales", "revenue", "sold") {
		return "sales"
	}
	if contains(lowerQuery, "product", "item", "category") {
		return "products"
	}
	if contains(lowerQuery, "customer", "buyer", "client") {
		return "customers"
	}
	if contains(lowerQuery, "dashboard", "chart", "graph", "visualization") {
		return "visualization"
	}
	if contains(lowerQuery, "month", "year", "date", "time") {
		return "time_analysis"
	}
	return "general"
}

// cacheSession saves session to Redis
func (m *Manager) cacheSession(session *Session) {
	if m.cache == nil {
		return
	}

	turns := make([]cache.ConversationTurn, len(session.Turns))
	for i, t := range session.Turns {
		turns[i] = cache.ConversationTurn{
			Query:     t.Query,
			Response:  t.Response,
			Timestamp: t.Timestamp,
		}
	}

	entry := &cache.ConversationCacheEntry{
		SessionID:    session.ID,
		Turns:        turns,
		Summary:      session.Summary,
		Topic:        session.Topic,
		LastActivity: session.LastActivity,
		Metadata: map[string]interface{}{
			"turn_count":  session.TurnCount,
			"reset_count": session.ResetCount,
		},
	}

	if err := m.cache.SetConversation(session.ID, entry); err != nil {
		logrus.Warnf("Failed to cache session: %v", err)
	}
}

// cacheSessionSummary saves just the summary
func (m *Manager) cacheSessionSummary(sessionID, summary string) {
	// In production, you might store this in a vector DB for semantic search
	logrus.Debugf("Caching summary for session %s: %s", sessionID, summary)
}

// createNewSession creates a fresh session
func (m *Manager) createNewSession(sessionID string) *Session {
	return &Session{
		ID:           sessionID,
		Turns:        make([]Turn, 0),
		LastActivity: time.Now(),
		Topic:        "",
	}
}

// CleanupSessions removes expired sessions
func (m *Manager) CleanupSessions() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	for id, session := range m.sessions {
		if now.Sub(session.LastActivity) > m.sessionTTL {
			delete(m.sessions, id)
			logrus.Debugf("Cleaned up expired session: %s", id)
		}
	}
}

// GetSessionStats returns statistics for a session
func (m *Manager) GetSessionStats(sessionID string) map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	session, exists := m.sessions[sessionID]
	if !exists {
		return nil
	}

	totalTokensIn := 0
	totalTokensOut := 0
	for _, turn := range session.Turns {
		totalTokensIn += turn.TokensIn
		totalTokensOut += turn.TokensOut
	}

	return map[string]interface{}{
		"session_id":       session.ID,
		"turn_count":       session.TurnCount,
		"reset_count":      session.ResetCount,
		"current_turns":    len(session.Turns),
		"topic":            session.Topic,
		"total_tokens_in":  totalTokensIn,
		"total_tokens_out": totalTokensOut,
		"last_activity":    session.LastActivity,
	}
}

func contains(s string, substrs ...string) bool {
	for _, substr := range substrs {
		if strings.Contains(s, substr) {
			return true
		}
	}
	return false
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
