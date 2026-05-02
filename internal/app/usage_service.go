package app

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/domain"
)

// Pricing is the per-1000-token / per-event cost model used to convert raw
// tokens into the dollar amount we record in `usage_events.cost_micro_usd`.
// Pricing is intentionally conservative — we surface usage to users now,
// charge against their credits later.
type Pricing struct {
	LLMInputCostPer1K     float64 // USD per 1000 input tokens
	LLMOutputCostPer1K    float64 // USD per 1000 output tokens
	SQLQueryCost          float64 // USD per query
	MetabaseCardCost      float64 // USD per card
	MetabaseDashboardCost float64 // USD per dashboard
}

// DefaultPricing approximates GPT-4o + a small per-action operations charge.
var DefaultPricing = Pricing{
	LLMInputCostPer1K:     0.005,
	LLMOutputCostPer1K:    0.015,
	SQLQueryCost:          0.0005,
	MetabaseCardCost:      0.001,
	MetabaseDashboardCost: 0.002,
}

// UsageService persists usage events and produces summaries.
type UsageService struct {
	usage   domain.UsageRepository
	credits domain.CreditsRepository
	pricing Pricing
}

func NewUsageService(usage domain.UsageRepository, credits domain.CreditsRepository, pricing Pricing) *UsageService {
	return &UsageService{usage: usage, credits: credits, pricing: pricing}
}

// RecordLLM records an LLM call. tokensIn / tokensOut may be zero if the
// provider didn't return usage; cost is zero in that case.
func (s *UsageService) RecordLLM(ctx context.Context, companyID, threadID, messageID string, tokensIn, tokensOut int) {
	cost := int64((float64(tokensIn)/1000.0)*s.pricing.LLMInputCostPer1K*1_000_000 +
		(float64(tokensOut)/1000.0)*s.pricing.LLMOutputCostPer1K*1_000_000)
	s.append(ctx, &domain.UsageEvent{
		CompanyID:    companyID,
		ThreadID:     threadID,
		MessageID:    messageID,
		EventType:    domain.UsageEventLLMCall,
		TokensIn:     tokensIn,
		TokensOut:    tokensOut,
		CostMicroUSD: cost,
	})
}

// RecordSQL records a tenant SQL query.
func (s *UsageService) RecordSQL(ctx context.Context, companyID, threadID string) {
	s.append(ctx, &domain.UsageEvent{
		CompanyID:    companyID,
		ThreadID:     threadID,
		EventType:    domain.UsageEventSQLQuery,
		CostMicroUSD: int64(s.pricing.SQLQueryCost * 1_000_000),
	})
}

// RecordMetabaseCard records one chart creation.
func (s *UsageService) RecordMetabaseCard(ctx context.Context, companyID, threadID string) {
	s.append(ctx, &domain.UsageEvent{
		CompanyID:    companyID,
		ThreadID:     threadID,
		EventType:    domain.UsageEventMetabaseCard,
		CostMicroUSD: int64(s.pricing.MetabaseCardCost * 1_000_000),
	})
}

// RecordMetabaseDashboard records one dashboard creation.
func (s *UsageService) RecordMetabaseDashboard(ctx context.Context, companyID, threadID string) {
	s.append(ctx, &domain.UsageEvent{
		CompanyID:    companyID,
		ThreadID:     threadID,
		EventType:    domain.UsageEventMetabaseDashboard,
		CostMicroUSD: int64(s.pricing.MetabaseDashboardCost * 1_000_000),
	})
}

func (s *UsageService) append(ctx context.Context, e *domain.UsageEvent) {
	if e.CompanyID == "" {
		return
	}
	if err := s.usage.Append(ctx, e); err != nil {
		logrus.WithError(err).Warn("usage append failed")
		return
	}
	if e.CostMicroUSD > 0 {
		_ = s.credits.Decrement(ctx, e.CompanyID, e.CostMicroUSD)
	}
}

// SummaryForCompany returns the usage summary for the current month.
func (s *UsageService) SummaryForCompany(ctx context.Context, companyID string) (*domain.UsageSummary, error) {
	now := time.Now()
	from := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 1, 0)
	return s.usage.SummaryByCompany(ctx, companyID, from, to)
}

// CreditsForCompany returns the current soft balance.
func (s *UsageService) CreditsForCompany(ctx context.Context, companyID string) (*domain.CompanyCredits, error) {
	return s.credits.Get(ctx, companyID)
}
