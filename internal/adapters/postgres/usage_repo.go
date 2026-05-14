package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
)

type UsageRepo struct{ db *sql.DB }

func NewUsageRepo(db *sql.DB) *UsageRepo { return &UsageRepo{db: db} }

func (r *UsageRepo) Append(ctx context.Context, e *domain.UsageEvent) error {
	const q = `
		INSERT INTO usage_events
			(company_id, thread_id, message_id, event_type, model,
			 tokens_in, tokens_out, cache_create_tokens_in, cache_read_tokens_in,
			 cost_micro_usd, metadata)
		VALUES ($1, NULLIF($2, '')::uuid, NULLIF($3, '')::uuid, $4, NULLIF($5, ''),
			$6, $7, $8, $9, $10, $11)
		RETURNING id, created_at
	`
	md, _ := json.Marshal(e.Metadata)
	return r.db.QueryRowContext(ctx, q,
		e.CompanyID, e.ThreadID, e.MessageID, string(e.EventType),
		e.Model,
		e.TokensIn, e.TokensOut, e.CacheCreateTokensIn, e.CacheReadTokensIn,
		e.CostMicroUSD, jsonbOrNull(md),
	).Scan(&e.ID, &e.CreatedAt)
}

func (r *UsageRepo) SummaryByCompany(ctx context.Context, companyID string, from, to time.Time) (*domain.UsageSummary, error) {
	// Single round-trip: union event-type aggregates with model aggregates
	// (model only meaningful for llm_call rows). `kind` discriminates row
	// shape so we can scan both shapes with one query.
	const q = `
		SELECT 'event' AS kind, event_type AS k,
			COUNT(*) AS n,
			COALESCE(SUM(tokens_in), 0)  AS tin,
			COALESCE(SUM(tokens_out), 0) AS tout,
			COALESCE(SUM(cost_micro_usd), 0) AS cost
		FROM usage_events
		WHERE company_id = $1 AND created_at >= $2 AND created_at < $3
		GROUP BY event_type
		UNION ALL
		SELECT 'model' AS kind, COALESCE(NULLIF(model, ''), 'unknown') AS k,
			0 AS n,
			COALESCE(SUM(tokens_in), 0)  AS tin,
			COALESCE(SUM(tokens_out), 0) AS tout,
			COALESCE(SUM(cost_micro_usd), 0) AS cost
		FROM usage_events
		WHERE company_id = $1 AND created_at >= $2 AND created_at < $3
		  AND event_type = 'llm_call'
		GROUP BY COALESCE(NULLIF(model, ''), 'unknown')
	`
	rows, err := r.db.QueryContext(ctx, q, companyID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	summary := &domain.UsageSummary{
		From:             from,
		To:               to,
		EventCounts:      map[domain.UsageEventType]int64{},
		CostByEventType:  map[domain.UsageEventType]float64{},
		CostByModel:      map[string]float64{},
		TokensInByModel:  map[string]int64{},
		TokensOutByModel: map[string]int64{},
	}
	for rows.Next() {
		var kind, key string
		var count, in, out, cost int64
		if err := rows.Scan(&kind, &key, &count, &in, &out, &cost); err != nil {
			return nil, err
		}
		switch kind {
		case "event":
			t := domain.UsageEventType(key)
			summary.EventCounts[t] = count
			summary.CostByEventType[t] = float64(cost) / 1_000_000
			summary.TotalTokensIn += in
			summary.TotalTokensOut += out
			summary.TotalCostUSD += float64(cost) / 1_000_000
		case "model":
			summary.CostByModel[key] = float64(cost) / 1_000_000
			summary.TokensInByModel[key] = in
			summary.TokensOutByModel[key] = out
		}
	}
	return summary, rows.Err()
}

func (r *UsageRepo) RecentByCompany(ctx context.Context, companyID string, limit int) ([]*domain.UsageEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	const q = `
		SELECT id, company_id,
			COALESCE(thread_id::text, ''), COALESCE(message_id::text, ''),
			event_type, COALESCE(model, ''),
			tokens_in, tokens_out, cache_create_tokens_in, cache_read_tokens_in,
			cost_micro_usd,
			COALESCE(metadata::text, ''), created_at
		FROM usage_events
		WHERE company_id = $1
		ORDER BY created_at DESC LIMIT $2
	`
	rows, err := r.db.QueryContext(ctx, q, companyID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.UsageEvent
	for rows.Next() {
		e := &domain.UsageEvent{}
		var typ, md string
		if err := rows.Scan(&e.ID, &e.CompanyID, &e.ThreadID, &e.MessageID,
			&typ, &e.Model, &e.TokensIn, &e.TokensOut,
			&e.CacheCreateTokensIn, &e.CacheReadTokensIn,
			&e.CostMicroUSD, &md, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.EventType = domain.UsageEventType(typ)
		if md != "" {
			_ = json.Unmarshal([]byte(md), &e.Metadata)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

type CreditsRepo struct{ db *sql.DB }

func NewCreditsRepo(db *sql.DB) *CreditsRepo { return &CreditsRepo{db: db} }

func (r *CreditsRepo) Get(ctx context.Context, companyID string) (*domain.CompanyCredits, error) {
	const q = `SELECT company_id, balance_micro_usd, monthly_grant_micro_usd, updated_at FROM company_credits WHERE company_id = $1`
	c := &domain.CompanyCredits{}
	err := r.db.QueryRowContext(ctx, q, companyID).
		Scan(&c.CompanyID, &c.BalanceMicroUSD, &c.MonthlyGrantMicroUSD, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return c, err
}

func (r *CreditsRepo) Upsert(ctx context.Context, c *domain.CompanyCredits) error {
	const q = `
		INSERT INTO company_credits (company_id, balance_micro_usd, monthly_grant_micro_usd, updated_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (company_id) DO UPDATE
			SET balance_micro_usd = EXCLUDED.balance_micro_usd,
				monthly_grant_micro_usd = EXCLUDED.monthly_grant_micro_usd,
				updated_at = now()
	`
	_, err := r.db.ExecContext(ctx, q, c.CompanyID, c.BalanceMicroUSD, c.MonthlyGrantMicroUSD)
	return err
}

func (r *CreditsRepo) Decrement(ctx context.Context, companyID string, microUSD int64) error {
	const q = `
		INSERT INTO company_credits (company_id, balance_micro_usd, updated_at)
		VALUES ($1, -$2, now())
		ON CONFLICT (company_id) DO UPDATE
			SET balance_micro_usd = company_credits.balance_micro_usd - $2,
				updated_at = now()
	`
	_, err := r.db.ExecContext(ctx, q, companyID, microUSD)
	return err
}
