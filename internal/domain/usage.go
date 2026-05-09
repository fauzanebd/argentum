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
)

// UsageEvent is a single billable / observable action taken on behalf of a
// company. Persisted for usage display today; will back per-call billing in
// V2.
type UsageEvent struct {
	ID           string         `json:"id"`
	CompanyID    string         `json:"company_id"`
	ThreadID     string         `json:"thread_id,omitempty"`
	MessageID    string         `json:"message_id,omitempty"`
	EventType    UsageEventType `json:"event_type"`
	TokensIn     int            `json:"tokens_in,omitempty"`
	TokensOut    int            `json:"tokens_out,omitempty"`
	CostMicroUSD int64          `json:"cost_micro_usd"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
}

// UsageRepository persists billable events.
type UsageRepository interface {
	Append(ctx context.Context, e *UsageEvent) error
	SummaryByCompany(ctx context.Context, companyID string, from, to time.Time) (*UsageSummary, error)
	RecentByCompany(ctx context.Context, companyID string, limit int) ([]*UsageEvent, error)
}

// UsageSummary is an aggregate view returned to the dashboard.
type UsageSummary struct {
	From            time.Time                  `json:"from"`
	To              time.Time                  `json:"to"`
	TotalCostUSD    float64                    `json:"total_cost_usd"`
	TotalTokensIn   int64                      `json:"total_tokens_in"`
	TotalTokensOut  int64                      `json:"total_tokens_out"`
	EventCounts     map[UsageEventType]int64   `json:"event_counts"`
	CostByEventType map[UsageEventType]float64 `json:"cost_by_event_type_usd"`
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
