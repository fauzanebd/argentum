package domain

import (
	"context"
	"time"
)

// UsageEventType identifies what kind of work a usage event represents.
type UsageEventType string

const (
	UsageEventLLMCall           UsageEventType = "llm_call"
	UsageEventSQLQuery          UsageEventType = "sql_query"
	UsageEventMetabaseCard      UsageEventType = "metabase_card"
	UsageEventMetabaseDashboard UsageEventType = "metabase_dashboard"
	UsageEventTopicClassify     UsageEventType = "topic_classify"
	UsageEventDocumentGenerated UsageEventType = "document_generated"
	// UsageEventMCPCall is one call to a tenant's own MCP server (T-M2). It is
	// its own type rather than folded into sql_query because the work happens on
	// somebody else's machine: the cost we carry is the round trip and the
	// context the result occupies, not a query against a source we hold.
	UsageEventMCPCall UsageEventType = "mcp_call"
	// UsageEventVideoRender is wall clock spent in the render service (T-V3).
	// Beside `document_generated` rather than instead of it: one video is one
	// document, and it is also three minutes of another pod's CPU. A per-second
	// price is the monetization track's decision; what this event does is make
	// the number exist before anybody has to price it.
	UsageEventVideoRender UsageEventType = "video_render"
)

// UsageEvent is a single billable / observable action taken on behalf of a
// company. Persisted for usage display today; will back per-call billing in
// V2.
type UsageEvent struct {
	ID        string `json:"id"`
	CompanyID string `json:"company_id"`
	ThreadID  string `json:"thread_id,omitempty"`
	MessageID string `json:"message_id,omitempty"`
	// AgentID attributes the spend to a roster agent (T-S2). No foreign key,
	// same reasoning as agent_actions: a deleted agent's costs stay on the
	// month they were incurred in. Empty when the turn ran unscoped.
	AgentID             string                 `json:"agent_id,omitempty"`
	EventType           UsageEventType         `json:"event_type"`
	Model               string                 `json:"model,omitempty"`
	TokensIn            int                    `json:"tokens_in,omitempty"`
	TokensOut           int                    `json:"tokens_out,omitempty"`
	CacheCreateTokensIn int                    `json:"cache_create_tokens_in,omitempty"`
	CacheReadTokensIn   int                    `json:"cache_read_tokens_in,omitempty"`
	CostMicroUSD        int64                  `json:"cost_micro_usd"`
	Metadata            map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt           time.Time              `json:"created_at"`
}

// UsageRepository persists billable events.
type UsageRepository interface {
	Append(ctx context.Context, e *UsageEvent) error
	SummaryByCompany(ctx context.Context, companyID string, from, to time.Time) (*UsageSummary, error)
	RecentByCompany(ctx context.Context, companyID string, limit int) ([]*UsageEvent, error)
	SummaryByThread(ctx context.Context, companyID, threadID string, from, to time.Time) (*UsageSummary, error)
	ListThreadUsage(ctx context.Context, companyID string, from, to time.Time, limit, offset int) ([]*ThreadUsageRow, error)
	EventsByThread(ctx context.Context, companyID, threadID string, limit, offset int) ([]*UsageEvent, error)
	UsageByChannel(ctx context.Context, companyID string, from, to time.Time) ([]*ChannelUsageRow, error)
	UsageByUser(ctx context.Context, companyID string, from, to time.Time) ([]*UserUsageRow, error)
}

// UsageSummary is an aggregate view returned to the dashboard.
type UsageSummary struct {
	From             time.Time                  `json:"from"`
	To               time.Time                  `json:"to"`
	TotalCostUSD     float64                    `json:"total_cost_usd"`
	TotalTokensIn    int64                      `json:"total_tokens_in"`
	TotalTokensOut   int64                      `json:"total_tokens_out"`
	EventCounts      map[UsageEventType]int64   `json:"event_counts"`
	CostByEventType  map[UsageEventType]float64 `json:"cost_by_event_type_usd"`
	CostByModel      map[string]float64         `json:"cost_by_model_usd,omitempty"`
	TokensInByModel  map[string]int64           `json:"tokens_in_by_model,omitempty"`
	TokensOutByModel map[string]int64           `json:"tokens_out_by_model,omitempty"`
}

// ThreadUsageRow is one row in the per-thread usage listing.
type ThreadUsageRow struct {
	ThreadID            string    `json:"thread_id"`
	Channel             Channel   `json:"channel"`
	Title               string    `json:"title,omitempty"`
	LastMessageAt       time.Time `json:"last_message_at"`
	EventCount          int64     `json:"event_count"`
	TokensIn            int64     `json:"tokens_in"`
	TokensOut           int64     `json:"tokens_out"`
	CacheCreateTokensIn int64     `json:"cache_create_tokens_in"`
	CacheReadTokensIn   int64     `json:"cache_read_tokens_in"`
	CostUSD             float64   `json:"cost_usd"`
}

// ChannelUsageRow rolls cost up by entry channel.
type ChannelUsageRow struct {
	Channel     Channel `json:"channel"`
	ThreadCount int64   `json:"thread_count"`
	EventCount  int64   `json:"event_count"`
	TokensIn    int64   `json:"tokens_in"`
	TokensOut   int64   `json:"tokens_out"`
	CostUSD     float64 `json:"cost_usd"`
}

// UserUsageRow rolls cost up by end-user identity. UserKey is the first
// non-empty of user_id / phone_number / discord_user_id / lark_open_id;
// UserKeyKind labels which column produced it.
type UserUsageRow struct {
	Channel     Channel `json:"channel"`
	UserKey     string  `json:"user_key"`
	UserKeyKind string  `json:"user_key_kind"`
	ThreadCount int64   `json:"thread_count"`
	EventCount  int64   `json:"event_count"`
	TokensIn    int64   `json:"tokens_in"`
	TokensOut   int64   `json:"tokens_out"`
	CostUSD     float64 `json:"cost_usd"`
}

// CompanyCredits tracks the soft balance for a company.
type CompanyCredits struct {
	CompanyID            string    `json:"company_id"`
	BalanceMicroUSD      int64     `json:"balance_micro_usd"`
	MonthlyGrantMicroUSD int64     `json:"monthly_grant_micro_usd"`
	UpdatedAt            time.Time `json:"updated_at"`
}

// CreditsRepository persists soft credit balances.
type CreditsRepository interface {
	Get(ctx context.Context, companyID string) (*CompanyCredits, error)
	Upsert(ctx context.Context, c *CompanyCredits) error
	Decrement(ctx context.Context, companyID string, microUSD int64) error
}
