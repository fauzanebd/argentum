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

// SummaryByThread returns the same shape as SummaryByCompany but scoped to a
// single thread. The companyID filter is kept as a tenant guard so a leaked
// thread_id from another tenant returns an empty summary, not data.
func (r *UsageRepo) SummaryByThread(ctx context.Context, companyID, threadID string, from, to time.Time) (*domain.UsageSummary, error) {
	const q = `
		SELECT 'event' AS kind, event_type AS k,
			COUNT(*) AS n,
			COALESCE(SUM(tokens_in), 0)  AS tin,
			COALESCE(SUM(tokens_out), 0) AS tout,
			COALESCE(SUM(cost_micro_usd), 0) AS cost
		FROM usage_events
		WHERE company_id = $1 AND thread_id = $4
		  AND created_at >= $2 AND created_at < $3
		GROUP BY event_type
		UNION ALL
		SELECT 'model' AS kind, COALESCE(NULLIF(model, ''), 'unknown') AS k,
			0 AS n,
			COALESCE(SUM(tokens_in), 0)  AS tin,
			COALESCE(SUM(tokens_out), 0) AS tout,
			COALESCE(SUM(cost_micro_usd), 0) AS cost
		FROM usage_events
		WHERE company_id = $1 AND thread_id = $4
		  AND created_at >= $2 AND created_at < $3
		  AND event_type = 'llm_call'
		GROUP BY COALESCE(NULLIF(model, ''), 'unknown')
	`
	rows, err := r.db.QueryContext(ctx, q, companyID, from, to, threadID)
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

// ListThreadUsage returns one row per thread that produced any usage in the
// window, ordered by total cost desc. Both usage_events.company_id and
// conversation_threads.company_id are filtered to keep the JOIN tenant-safe.
func (r *UsageRepo) ListThreadUsage(ctx context.Context, companyID string, from, to time.Time, limit, offset int) ([]*domain.ThreadUsageRow, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	const q = `
		SELECT t.id::text, t.channel, COALESCE(t.title, ''), t.last_message_at,
			COUNT(*) AS event_count,
			COALESCE(SUM(u.tokens_in), 0),
			COALESCE(SUM(u.tokens_out), 0),
			COALESCE(SUM(u.cache_create_tokens_in), 0),
			COALESCE(SUM(u.cache_read_tokens_in), 0),
			COALESCE(SUM(u.cost_micro_usd), 0)
		FROM usage_events u
		JOIN conversation_threads t ON t.id = u.thread_id
		WHERE u.company_id = $1 AND t.company_id = $1
		  AND u.created_at >= $2 AND u.created_at < $3
		GROUP BY t.id, t.channel, t.title, t.last_message_at
		ORDER BY SUM(u.cost_micro_usd) DESC, t.last_message_at DESC
		LIMIT $4 OFFSET $5
	`
	rows, err := r.db.QueryContext(ctx, q, companyID, from, to, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.ThreadUsageRow
	for rows.Next() {
		row := &domain.ThreadUsageRow{}
		var channel string
		var cost int64
		if err := rows.Scan(&row.ThreadID, &channel, &row.Title, &row.LastMessageAt,
			&row.EventCount, &row.TokensIn, &row.TokensOut,
			&row.CacheCreateTokensIn, &row.CacheReadTokensIn, &cost); err != nil {
			return nil, err
		}
		row.Channel = domain.Channel(channel)
		row.CostUSD = float64(cost) / 1_000_000
		out = append(out, row)
	}
	return out, rows.Err()
}

// EventsByThread returns the raw audit trail for one thread (every LLM call,
// SQL query, document generation, etc.), newest first.
func (r *UsageRepo) EventsByThread(ctx context.Context, companyID, threadID string, limit, offset int) ([]*domain.UsageEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	const q = `
		SELECT id, company_id,
			COALESCE(thread_id::text, ''), COALESCE(message_id::text, ''),
			event_type, COALESCE(model, ''),
			tokens_in, tokens_out, cache_create_tokens_in, cache_read_tokens_in,
			cost_micro_usd,
			COALESCE(metadata::text, ''), created_at
		FROM usage_events
		WHERE company_id = $1 AND thread_id = $2
		ORDER BY created_at DESC
		LIMIT $3 OFFSET $4
	`
	rows, err := r.db.QueryContext(ctx, q, companyID, threadID, limit, offset)
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

// UsageByChannel aggregates cost + tokens by entry channel.
func (r *UsageRepo) UsageByChannel(ctx context.Context, companyID string, from, to time.Time) ([]*domain.ChannelUsageRow, error) {
	const q = `
		SELECT t.channel,
			COUNT(DISTINCT t.id) AS thread_count,
			COUNT(*)             AS event_count,
			COALESCE(SUM(u.tokens_in), 0),
			COALESCE(SUM(u.tokens_out), 0),
			COALESCE(SUM(u.cost_micro_usd), 0)
		FROM usage_events u
		JOIN conversation_threads t ON t.id = u.thread_id
		WHERE u.company_id = $1 AND t.company_id = $1
		  AND u.created_at >= $2 AND u.created_at < $3
		GROUP BY t.channel
		ORDER BY SUM(u.cost_micro_usd) DESC
	`
	rows, err := r.db.QueryContext(ctx, q, companyID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.ChannelUsageRow
	for rows.Next() {
		row := &domain.ChannelUsageRow{}
		var channel string
		var cost int64
		if err := rows.Scan(&channel, &row.ThreadCount, &row.EventCount,
			&row.TokensIn, &row.TokensOut, &cost); err != nil {
			return nil, err
		}
		row.Channel = domain.Channel(channel)
		row.CostUSD = float64(cost) / 1_000_000
		out = append(out, row)
	}
	return out, rows.Err()
}

// UsageByUser aggregates cost + tokens by end-user identity. Identity is the
// first non-empty of user_id / phone_number / discord_user_id / lark_open_id /
// api_user_ref, with user_key_kind labelling which column produced it.
//
// The api arm is last in the COALESCE for a reason that is not cosmetic: a
// thread carries exactly one identity column, so order only matters if that
// ever stops being true, and putting the tenant-supplied string last means a
// first-party identity always wins over one a caller chose.
func (r *UsageRepo) UsageByUser(ctx context.Context, companyID string, from, to time.Time) ([]*domain.UserUsageRow, error) {
	const q = `
		SELECT
			t.channel,
			COALESCE(
				NULLIF(t.user_id::text, ''),
				NULLIF(t.phone_number, ''),
				NULLIF(t.discord_user_id, ''),
				NULLIF(t.lark_open_id, ''),
				NULLIF(t.api_user_ref, ''),
				'unknown'
			) AS user_key,
			CASE
				WHEN t.user_id IS NOT NULL THEN 'user_id'
				WHEN NULLIF(t.phone_number, '')     IS NOT NULL THEN 'phone'
				WHEN NULLIF(t.discord_user_id, '')  IS NOT NULL THEN 'discord_user_id'
				WHEN NULLIF(t.lark_open_id, '')     IS NOT NULL THEN 'lark_open_id'
				WHEN NULLIF(t.api_user_ref, '')     IS NOT NULL THEN 'api_user_ref'
				ELSE 'unknown'
			END AS user_key_kind,
			COUNT(DISTINCT t.id) AS thread_count,
			COUNT(*)             AS event_count,
			COALESCE(SUM(u.tokens_in), 0),
			COALESCE(SUM(u.tokens_out), 0),
			COALESCE(SUM(u.cost_micro_usd), 0)
		FROM usage_events u
		JOIN conversation_threads t ON t.id = u.thread_id
		WHERE u.company_id = $1 AND t.company_id = $1
		  AND u.created_at >= $2 AND u.created_at < $3
		GROUP BY t.channel, user_key, user_key_kind
		ORDER BY SUM(u.cost_micro_usd) DESC
	`
	rows, err := r.db.QueryContext(ctx, q, companyID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.UserUsageRow
	for rows.Next() {
		row := &domain.UserUsageRow{}
		var channel string
		var cost int64
		if err := rows.Scan(&channel, &row.UserKey, &row.UserKeyKind,
			&row.ThreadCount, &row.EventCount,
			&row.TokensIn, &row.TokensOut, &cost); err != nil {
			return nil, err
		}
		row.Channel = domain.Channel(channel)
		row.CostUSD = float64(cost) / 1_000_000
		out = append(out, row)
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
