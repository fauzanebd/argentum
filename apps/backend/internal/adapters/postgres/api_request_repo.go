package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
)

// APIRequestRepo persists `/v1` request observability (T-A5).
//
// Both writes are batch-only and both are called from internal/apiobs's flush,
// never from a handler: one Postgres round trip per flush interval instead of
// one per API call. See 032_api_observability.up.sql for the shape and the
// trade.
type APIRequestRepo struct{ db *sql.DB }

func NewAPIRequestRepo(db *sql.DB) *APIRequestRepo { return &APIRequestRepo{db: db} }

// UpsertStats folds a batch of buckets into the rollup.
//
// `SET requests = api_request_stats.requests + EXCLUDED.requests` and not
// `= EXCLUDED.requests`: two API replicas flush the same (key, hour, route)
// bucket independently, and an assignment would keep whichever landed last and
// silently drop the other replica's traffic. The max is a GREATEST for the same
// reason.
//
// One statement with a widening VALUES list rather than a prepared statement in
// a loop — a flush is tens of rows, and the round trip is the cost worth
// avoiding.
func (r *APIRequestRepo) UpsertStats(ctx context.Context, rows []domain.APIRequestStatRow) error {
	if len(rows) == 0 {
		return nil
	}
	values, args := tuples(len(rows), 9, func(i int) []any {
		row := rows[i]
		return []any{row.CompanyID, row.APIKeyID, row.BucketHour, row.Route,
			row.Method, row.StatusClass, row.Requests, row.LatencyMSSum, row.LatencyMSMax}
	})
	q := `
		INSERT INTO api_request_stats
			(company_id, api_key_id, bucket_hour, route, method, status_class,
			 requests, latency_ms_sum, latency_ms_max)
		VALUES ` + values + `
		ON CONFLICT (company_id, api_key_id, bucket_hour, route, method, status_class)
		DO UPDATE SET
			requests       = api_request_stats.requests + EXCLUDED.requests,
			latency_ms_sum = api_request_stats.latency_ms_sum + EXCLUDED.latency_ms_sum,
			latency_ms_max = GREATEST(api_request_stats.latency_ms_max, EXCLUDED.latency_ms_max)
	`
	if _, err := r.db.ExecContext(ctx, q, args...); err != nil {
		return fmt.Errorf("upsert api request stats: %w", err)
	}
	return nil
}

// tuples renders `($1,$2,…),($n,…)` for a multi-row VALUES list and collects the
// arguments in the same order. The row builder returns exactly cols values;
// building the placeholders and the arguments in one pass is what keeps the two
// from drifting, which is the only way this kind of statement goes wrong.
func tuples(rows, cols int, valuesOf func(i int) []any) (string, []any) {
	var (
		b    strings.Builder
		args = make([]any, 0, rows*cols)
	)
	for i := range rows {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('(')
		for j := range cols {
			if j > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, "$%d", i*cols+j+1)
		}
		b.WriteByte(')')
		args = append(args, valuesOf(i)...)
	}
	return b.String(), args
}

// InsertErrors appends the batch's failures. No ON CONFLICT: each row is a
// distinct event, and two identical failures a second apart are two facts.
func (r *APIRequestRepo) InsertErrors(ctx context.Context, rows []domain.APIRequestError) error {
	if len(rows) == 0 {
		return nil
	}
	values, args := tuples(len(rows), 9, func(i int) []any {
		row := rows[i]
		return []any{row.CompanyID, row.APIKeyID, row.RequestID, row.Method,
			row.Route, row.Status, row.ErrorCode, row.ErrorType, row.LatencyMS}
	})
	q := `
		INSERT INTO api_request_errors
			(company_id, api_key_id, request_id, method, route, status,
			 error_code, error_type, latency_ms)
		VALUES ` + values
	if _, err := r.db.ExecContext(ctx, q, args...); err != nil {
		return fmt.Errorf("insert api request errors: %w", err)
	}
	return nil
}

// StatsByKey summarises the window per key.
//
// The window is `bucket_hour >= since` truncated to the hour by the caller: an
// hour bucket either is or is not in the window, and comparing a bucket start
// against a mid-hour timestamp would drop the current hour's traffic — the only
// traffic anyone debugging is looking at.
func (r *APIRequestRepo) StatsByKey(
	ctx context.Context, companyID string, since time.Time,
) (map[string]*domain.APIKeyRequestStats, error) {
	const q = `
		SELECT api_key_id,
		       SUM(requests)                                        AS requests,
		       SUM(CASE WHEN status_class = 2 THEN 0 ELSE requests END) AS failed,
		       SUM(latency_ms_sum)                                  AS latency_sum,
		       MAX(latency_ms_max)                                  AS latency_max
		FROM api_request_stats
		WHERE company_id = $1 AND bucket_hour >= $2
		GROUP BY api_key_id
	`
	rows, err := r.db.QueryContext(ctx, q, companyID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]*domain.APIKeyRequestStats{}
	for rows.Next() {
		var (
			keyID                  string
			requests, failed       int64
			latencySum, latencyMax sql.NullInt64
		)
		if err := rows.Scan(&keyID, &requests, &failed, &latencySum, &latencyMax); err != nil {
			return nil, err
		}
		out[keyID] = &domain.APIKeyRequestStats{
			APIKeyID:     keyID,
			Requests:     requests,
			Failed:       failed,
			ErrorRatePct: errorRatePct(failed, requests),
			AvgLatencyMS: avgLatencyMS(latencySum.Int64, requests),
			MaxLatencyMS: int(latencyMax.Int64),
		}
	}
	return out, rows.Err()
}

// errorRatePct rounds to one decimal. Zero requests is a 0% rate rather than a
// division by zero, and the caller distinguishes the two by Requests == 0.
func errorRatePct(failed, requests int64) float64 {
	if requests <= 0 {
		return 0
	}
	return float64(int64(float64(failed)/float64(requests)*1000+0.5)) / 10
}

func avgLatencyMS(sum, requests int64) int {
	if requests <= 0 {
		return 0
	}
	return int(sum / requests)
}

// RecentErrors reads the failure list. keyID empty means every key.
func (r *APIRequestRepo) RecentErrors(
	ctx context.Context, companyID, keyID string, limit int,
) ([]*domain.APIRequestError, error) {
	if limit <= 0 || limit > 200 {
		// The tab asks for 50. The ceiling is here rather than only at the
		// handler because this repository is reachable from anywhere in the
		// process, and an unbounded read of an append-only table is the kind of
		// thing that is only ever discovered in production.
		limit = 50
	}
	q := `
		SELECT id, api_key_id, request_id, method, route, status,
		       error_code, error_type, latency_ms, created_at
		FROM api_request_errors
		WHERE company_id = $1`
	args := []any{companyID}
	if keyID != "" {
		q += ` AND api_key_id = $2`
		args = append(args, keyID)
	}
	q += fmt.Sprintf(` ORDER BY created_at DESC, id DESC LIMIT %d`, limit)

	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*domain.APIRequestError
	for rows.Next() {
		e := &domain.APIRequestError{}
		if err := rows.Scan(&e.ID, &e.APIKeyID, &e.RequestID, &e.Method, &e.Route,
			&e.Status, &e.ErrorCode, &e.ErrorType, &e.LatencyMS, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Prune drops both tables' rows older than before, and returns how many.
//
// Two statements rather than a transaction: they are independent deletions of
// expired observability, and a partial prune is a prune that runs again in an
// hour.
func (r *APIRequestRepo) Prune(ctx context.Context, before time.Time) (int64, error) {
	var total int64
	for _, q := range []string{
		`DELETE FROM api_request_stats WHERE bucket_hour < $1`,
		`DELETE FROM api_request_errors WHERE created_at < $1`,
	} {
		res, err := r.db.ExecContext(ctx, q, before)
		if err != nil {
			return total, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}
