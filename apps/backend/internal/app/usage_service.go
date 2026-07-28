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
	DocumentCost          float64 // USD per generated document
}

// DefaultPricing approximates GPT-4o + a small per-action operations charge.
var DefaultPricing = Pricing{
	LLMInputCostPer1K:     0.005,
	LLMOutputCostPer1K:    0.015,
	SQLQueryCost:          0.0005,
	MetabaseCardCost:      0.001,
	MetabaseDashboardCost: 0.002,
	DocumentCost:          0.001,
}

// UsageService persists usage events and produces summaries. Credit
// enforcement (T-03) is opt-in through WithCredits — the zero value records
// and decrements exactly as it did before, and refuses nothing.
type UsageService struct {
	usage   domain.UsageRepository
	credits domain.CreditsRepository
	pricing Pricing

	credit      CreditPolicy
	llmCreds    domain.CompanyLLMCredentialRepository
	budgetCache BudgetCache
}

func NewUsageService(usage domain.UsageRepository, credits domain.CreditsRepository, pricing Pricing) *UsageService {
	return &UsageService{usage: usage, credits: credits, pricing: pricing}
}

// Anthropic prompt-caching multipliers vs. the base input rate.
// Cache writes cost 1.25x normal input; cache reads cost 0.10x.
// https://docs.anthropic.com/en/docs/build-with-claude/prompt-caching#pricing
const (
	cacheCreateMultiplier = 1.25
	cacheReadMultiplier   = 0.10
)

// RecordLLM records an LLM call. tokensIn / tokensOut may be zero if the
// provider didn't return usage; cost is zero in that case. cacheCreate and
// cacheRead are Anthropic-only — zero for other providers. Per-model rates
// come from modelPricing; unknown models fall back to s.pricing (the flat
// DefaultPricing rate) so unfamiliar model strings never produce zero-cost
// rows.
func (s *UsageService) RecordLLM(ctx context.Context, companyID, threadID, messageID, model string, tokensIn, tokensOut, cacheCreate, cacheRead int) {
	inRate := s.pricing.LLMInputCostPer1K
	outRate := s.pricing.LLMOutputCostPer1K
	if mp, ok := lookupModelPricing(model); ok {
		inRate, outRate = mp.InputCostPer1K, mp.OutputCostPer1K
	}
	cost := int64((float64(tokensIn)/1000.0)*inRate*1_000_000 +
		(float64(tokensOut)/1000.0)*outRate*1_000_000 +
		(float64(cacheCreate)/1000.0)*inRate*cacheCreateMultiplier*1_000_000 +
		(float64(cacheRead)/1000.0)*inRate*cacheReadMultiplier*1_000_000)
	s.append(ctx, &domain.UsageEvent{
		CompanyID:           companyID,
		ThreadID:            threadID,
		MessageID:           messageID,
		EventType:           domain.UsageEventLLMCall,
		Model:               model,
		TokensIn:            tokensIn,
		TokensOut:           tokensOut,
		CacheCreateTokensIn: cacheCreate,
		CacheReadTokensIn:   cacheRead,
		CostMicroUSD:        cost,
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

// RecordDocument records one document generation. format is informational
// metadata so we can later split pricing per format if we want.
func (s *UsageService) RecordDocument(ctx context.Context, companyID, threadID, format string) {
	s.append(ctx, &domain.UsageEvent{
		CompanyID:    companyID,
		ThreadID:     threadID,
		EventType:    domain.UsageEventDocumentGenerated,
		CostMicroUSD: int64(s.pricing.DocumentCost * 1_000_000),
		Metadata:     map[string]interface{}{"format": format},
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

// parseUsageWindow resolves the from/to query params for audit endpoints.
// Empty strings default to the last 30 days; "to" defaults to now.
func parseUsageWindow(fromStr, toStr string) (time.Time, time.Time, error) {
	to := time.Now().UTC()
	from := to.AddDate(0, 0, -30)
	if fromStr != "" {
		t, err := time.Parse(time.RFC3339, fromStr)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		from = t
	}
	if toStr != "" {
		t, err := time.Parse(time.RFC3339, toStr)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		to = t
	}
	return from, to, nil
}

// SummaryForThread returns event + model breakdown for one thread in the
// given window. companyID scopes the query so cross-tenant IDs return empty.
func (s *UsageService) SummaryForThread(ctx context.Context, companyID, threadID, fromStr, toStr string) (*domain.UsageSummary, error) {
	from, to, err := parseUsageWindow(fromStr, toStr)
	if err != nil {
		return nil, err
	}
	return s.usage.SummaryByThread(ctx, companyID, threadID, from, to)
}

// ListThreadsUsage returns one row per thread with totals over the window.
func (s *UsageService) ListThreadsUsage(ctx context.Context, companyID, fromStr, toStr string, limit, offset int) ([]*domain.ThreadUsageRow, error) {
	from, to, err := parseUsageWindow(fromStr, toStr)
	if err != nil {
		return nil, err
	}
	return s.usage.ListThreadUsage(ctx, companyID, from, to, limit, offset)
}

// EventsForThread returns the raw per-message audit trail for one thread.
func (s *UsageService) EventsForThread(ctx context.Context, companyID, threadID string, limit, offset int) ([]*domain.UsageEvent, error) {
	return s.usage.EventsByThread(ctx, companyID, threadID, limit, offset)
}

// CostByChannel rolls cost up by entry channel for the window.
func (s *UsageService) CostByChannel(ctx context.Context, companyID, fromStr, toStr string) ([]*domain.ChannelUsageRow, error) {
	from, to, err := parseUsageWindow(fromStr, toStr)
	if err != nil {
		return nil, err
	}
	return s.usage.UsageByChannel(ctx, companyID, from, to)
}

// CostByUser rolls cost up by end-user identity for the window.
func (s *UsageService) CostByUser(ctx context.Context, companyID, fromStr, toStr string) ([]*domain.UserUsageRow, error) {
	from, to, err := parseUsageWindow(fromStr, toStr)
	if err != nil {
		return nil, err
	}
	return s.usage.UsageByUser(ctx, companyID, from, to)
}
