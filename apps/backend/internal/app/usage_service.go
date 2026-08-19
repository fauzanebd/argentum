package app

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/agentscope"
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
	MCPCallCost           float64 // USD per call to a tenant's own MCP server
	// VideoRenderCostPerSec is USD per second of wall clock in the render
	// service (T-V3). Per second rather than per video, because the two
	// numbers a video costs are the same for a 30-second summary and a
	// nine-minute one only if you do not look: a render pod is one job at a
	// time, so the resource being spent is time on it.
	VideoRenderCostPerSec float64
}

// DefaultPricing approximates GPT-4o + a small per-action operations charge.
var DefaultPricing = Pricing{
	LLMInputCostPer1K:     0.005,
	LLMOutputCostPer1K:    0.015,
	SQLQueryCost:          0.0005,
	MetabaseCardCost:      0.001,
	MetabaseDashboardCost: 0.002,
	DocumentCost:          0.001,
	// Priced like a SQL query: one round trip on the tenant's behalf. It is
	// deliberately not zero — a metered call at zero cost is invisible in every
	// summary that sorts by spend, which is where an operator looks first when a
	// server starts being called in a loop.
	MCPCallCost: 0.0005,
	// A minute of a render pod, priced at roughly what a minute of the machine
	// it runs on costs. The kpi_summary fixture renders in about three minutes,
	// so an ordinary video lands near $0.03 — two orders of magnitude above a
	// PDF, which is the fact this number exists to make visible.
	VideoRenderCostPerSec: 0.00017,
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

// RecordDocumentOCR records what reading one page of an uploaded document cost
// (T-P3/T-P11), and returns that cost so the document row can carry it too.
//
// **It is an llm_call event with no thread.** Every other model call in this
// product happens inside a chat turn and is attributed to one; ingestion is the
// first thing a tenant can point at that spends outside a turn, so the
// attribution is the document id in the metadata instead. Without that, "what
// did documents cost this month" is a question the ledger holds the answer to
// and cannot be asked.
func (s *UsageService) RecordDocumentOCR(
	ctx context.Context, companyID, documentID, model string, tokensIn, tokensOut int,
) int64 {
	inRate := s.pricing.LLMInputCostPer1K
	outRate := s.pricing.LLMOutputCostPer1K
	if mp, ok := lookupModelPricing(model); ok {
		inRate, outRate = mp.InputCostPer1K, mp.OutputCostPer1K
	}
	cost := int64((float64(tokensIn)/1000.0)*inRate*1_000_000 +
		(float64(tokensOut)/1000.0)*outRate*1_000_000)
	s.append(ctx, &domain.UsageEvent{
		CompanyID:    companyID,
		EventType:    domain.UsageEventLLMCall,
		Model:        model,
		TokensIn:     tokensIn,
		TokensOut:    tokensOut,
		CostMicroUSD: cost,
		Metadata: map[string]interface{}{
			"document_id": documentID,
			"feature":     UsageFeatureDocumentOCR,
		},
	})
	return cost
}

// UsageFeatureDocumentOCR labels the ingestion spend, so the ledger can be
// split by feature without a migration — the pattern T-B2 established.
const UsageFeatureDocumentOCR = "document_ocr"

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

// RecordRenderSeconds records wall clock spent rendering a video (T-V3).
//
// It is a second event beside the document's, not a replacement for it: the
// document row is what a tenant downloaded and this is what producing it took.
// A render that reported no time — an older service, a clock that went
// backwards — records nothing rather than a zero-cost row, because a free
// video in the usage table is a wrong answer where an absent one is a gap.
func (s *UsageService) RecordRenderSeconds(ctx context.Context, companyID, threadID, format string, seconds float64) {
	if seconds <= 0 {
		return
	}
	s.append(ctx, &domain.UsageEvent{
		CompanyID:    companyID,
		ThreadID:     threadID,
		EventType:    domain.UsageEventVideoRender,
		CostMicroUSD: int64(seconds * s.pricing.VideoRenderCostPerSec * 1_000_000),
		Metadata:     map[string]interface{}{"format": format, "render_seconds": seconds},
	})
}

// RecordMCPCall records one call to a tenant's own MCP server (T-M2). The
// server id and the tool's own name go in the metadata rather than into new
// columns: this is the fourth per-event kind to want two identifiers, and the
// audit row (`agent_actions`, written by the same call) is where the arguments
// and the outcome already live. The agent id is filled in by append, from the
// turn's scope.
func (s *UsageService) RecordMCPCall(ctx context.Context, companyID, threadID, serverID, toolName string) {
	s.append(ctx, &domain.UsageEvent{
		CompanyID:    companyID,
		ThreadID:     threadID,
		EventType:    domain.UsageEventMCPCall,
		CostMicroUSD: int64(s.pricing.MCPCallCost * 1_000_000),
		Metadata:     map[string]interface{}{"mcp_server_id": serverID, "tool": toolName},
	})
}

// usageFeatureKey carries which product feature is spending, for the passes
// that are not a chat turn (T-B2).
//
// A context value rather than a parameter on six Record* methods, for the same
// reason the agent id is one: the spender is MeteredLLM, four packages away
// from anything that knows why it was called, and the alternative is threading
// a string through every LLM wrapper so that one caller can label itself.
type usageFeatureKey struct{}

// WithUsageFeature labels every usage event recorded under ctx. Callers pass a
// stable slug — UsageFeatureBusinessInference is the first — so the numbers can
// be split by feature later without a migration.
func WithUsageFeature(ctx context.Context, feature string) context.Context {
	if feature == "" {
		return ctx
	}
	return context.WithValue(ctx, usageFeatureKey{}, feature)
}

func usageFeature(ctx context.Context) string {
	s, _ := ctx.Value(usageFeatureKey{}).(string)
	return s
}

func (s *UsageService) append(ctx context.Context, e *domain.UsageEvent) {
	if e.CompanyID == "" {
		return
	}
	// Which feature spent this (T-B2). Set here rather than at the call site so
	// a pass that labels its context has every event it causes labelled — the
	// LLM call, and anything else the same code path records.
	if f := usageFeature(ctx); f != "" {
		if e.Metadata == nil {
			e.Metadata = map[string]interface{}{}
		}
		if _, ok := e.Metadata["feature"]; !ok {
			e.Metadata["feature"] = f
		}
	}
	// Which agent spent this (T-S2). One assignment here rather than a
	// parameter on six Record* methods, for the same reason the audit
	// decorator reads it off the context: every caller is inside a turn that
	// already carries the scope, and the ones that are not — the connection
	// describer, a reindex — correctly record nothing.
	if e.AgentID == "" {
		e.AgentID = agentscope.AgentID(ctx)
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
