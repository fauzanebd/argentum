package agent

import (
	"fmt"
	"strings"
	"time"
)

// ConversationTurn represents a single exchange
type ConversationTurn struct {
	Query     string
	Response  string
	Timestamp time.Time
}

// ConversationContext holds the conversation state
type ConversationContext struct {
	SessionID   string
	Summary     string
	RecentTurns []ConversationTurn
	LastUpdated time.Time
}

// AddTurn adds a new turn to the conversation
func (c *ConversationContext) AddTurn(query, response string) {
	turn := ConversationTurn{
		Query:     query,
		Response:  response,
		Timestamp: time.Now(),
	}

	c.RecentTurns = append(c.RecentTurns, turn)
	c.LastUpdated = time.Now()

	// Generate summary (simplified for MVP)
	c.updateSummary()
}

// updateSummary creates a simple summary of the conversation
func (c *ConversationContext) updateSummary() {
	if len(c.RecentTurns) == 0 {
		return
	}

	// For MVP, create a simple concatenated summary
	// In production, this would use LLM to generate a proper summary
	var parts []string
	for _, turn := range c.RecentTurns {
		parts = append(parts, fmt.Sprintf("Q: %s", turn.Query))
	}

	c.Summary = strings.Join(parts, "; ")
}

// ContextManager manages conversation contexts
type ContextManager struct {
	contexts   map[string]*ConversationContext
	maxTurns   int
	sessionTTL time.Duration
}

// NewContextManager creates a new context manager
func NewContextManager(maxTurns int) *ContextManager {
	return &ContextManager{
		contexts:   make(map[string]*ConversationContext),
		maxTurns:   maxTurns,
		sessionTTL: 30 * time.Minute,
	}
}

// GetContext retrieves or creates a conversation context
func (cm *ContextManager) GetContext(sessionID string) *ConversationContext {
	if ctx, exists := cm.contexts[sessionID]; exists {
		// Check if session has expired
		if time.Since(ctx.LastUpdated) > cm.sessionTTL {
			// Reset expired session
			cm.contexts[sessionID] = cm.createNewContext(sessionID)
		}
		return cm.contexts[sessionID]
	}

	// Create new context
	ctx := cm.createNewContext(sessionID)
	cm.contexts[sessionID] = ctx
	return ctx
}

// UpdateContext updates a conversation context
func (cm *ContextManager) UpdateContext(sessionID string, ctx *ConversationContext) {
	// Keep only recent turns
	if len(ctx.RecentTurns) > cm.maxTurns {
		ctx.RecentTurns = ctx.RecentTurns[len(ctx.RecentTurns)-cm.maxTurns:]
	}

	cm.contexts[sessionID] = ctx
}

// createNewContext creates a new empty context
func (cm *ContextManager) createNewContext(sessionID string) *ConversationContext {
	return &ConversationContext{
		SessionID:   sessionID,
		RecentTurns: make([]ConversationTurn, 0),
		LastUpdated: time.Now(),
	}
}

// CleanupOldContexts removes expired contexts (should be called periodically)
func (cm *ContextManager) CleanupOldContexts() {
	now := time.Now()
	for id, ctx := range cm.contexts {
		if now.Sub(ctx.LastUpdated) > cm.sessionTTL {
			delete(cm.contexts, id)
		}
	}
}
