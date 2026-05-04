package tools

import (
	"sync"
	"time"

	"github.com/fauzanebd/argentum/internal/metabase"
)

var (
	threadCardsMu sync.RWMutex
	threadCards   = make(map[string][]metabase.DashCardEntry)
	threadCardsAt = make(map[string]time.Time)
)

const threadCardsTTL = 10 * time.Minute

// RecordThreadCard remembers a card created in the current thread so
// create_dashboard can auto-resolve cards when the LLM omits them.
func RecordThreadCard(threadID string, entry metabase.DashCardEntry) {
	threadCardsMu.Lock()
	defer threadCardsMu.Unlock()
	threadCards[threadID] = append(threadCards[threadID], entry)
	threadCardsAt[threadID] = time.Now()
}

// GetThreadCards returns cards recorded for this thread.
func GetThreadCards(threadID string) []metabase.DashCardEntry {
	threadCardsMu.RLock()
	defer threadCardsMu.RUnlock()
	if at, ok := threadCardsAt[threadID]; !ok || time.Since(at) > threadCardsTTL {
		return nil
	}
	out := make([]metabase.DashCardEntry, len(threadCards[threadID]))
	copy(out, threadCards[threadID])
	return out
}

// ClearThreadCards drops recorded cards for a thread (call after dashboard creation).
func ClearThreadCards(threadID string) {
	threadCardsMu.Lock()
	defer threadCardsMu.Unlock()
	delete(threadCards, threadID)
	delete(threadCardsAt, threadID)
}
