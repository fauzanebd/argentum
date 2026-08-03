package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
)

// WatcherRepo stores watchers and their events (T-08).
//
// Company-scoped reads everywhere the id arrives from a request, like
// MetricRepo; the two unscoped reads (GetForFire, ListEnabledForScheduler) are
// the worker paths, where the id came from a queue this process filled and
// there is no request tenant to check.
type WatcherRepo struct{ db *sql.DB }

func NewWatcherRepo(db *sql.DB) *WatcherRepo { return &WatcherRepo{db: db} }

const watcherColumns = `id, company_id, metric_id, thread_id, name, window_grain, comparator,
	threshold, COALESCE(compare_to, ''), cron_expression, timezone, channels, cooldown_minutes,
	enabled, last_fired_at, last_dry_run_at, COALESCE(created_by::text, ''), created_at, updated_at`

func scanWatcher(row interface {
	Scan(dest ...interface{}) error
}) (*domain.Watcher, error) {
	w := &domain.Watcher{}
	var grain, comparator string
	var channels []byte
	var lastFired, lastDryRun sql.NullTime
	if err := row.Scan(
		&w.ID, &w.CompanyID, &w.MetricID, &w.ThreadID, &w.Name, &grain, &comparator,
		&w.Threshold, &w.CompareTo, &w.CronExpression, &w.Timezone, &channels, &w.CooldownMinutes,
		&w.Enabled, &lastFired, &lastDryRun, &w.CreatedBy, &w.CreatedAt, &w.UpdatedAt,
	); err != nil {
		return nil, err
	}
	w.WindowGrain = domain.WatcherGrain(grain)
	w.Comparator = domain.WatcherComparator(comparator)
	if len(channels) > 0 {
		if err := json.Unmarshal(channels, &w.Channels); err != nil {
			return nil, fmt.Errorf("unmarshal watcher channels: %w", err)
		}
	}
	if w.Channels == nil {
		w.Channels = []domain.WatcherChannel{}
	}
	if lastFired.Valid {
		v := lastFired.Time
		w.LastFiredAt = &v
	}
	if lastDryRun.Valid {
		v := lastDryRun.Time
		w.LastDryRunAt = &v
	}
	return w, nil
}

func (r *WatcherRepo) Create(ctx context.Context, w *domain.Watcher) error {
	channels, err := json.Marshal(nonNilChannels(w.Channels))
	if err != nil {
		return fmt.Errorf("marshal watcher channels: %w", err)
	}
	const q = `
		INSERT INTO watchers (
			company_id, metric_id, thread_id, name, window_grain, comparator, threshold,
			compare_to, cron_expression, timezone, channels, cooldown_minutes, enabled, created_by
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, NULLIF($8, ''), $9, $10, $11, $12, $13, NULLIF($14, '')::uuid
		)
		RETURNING id, created_at, updated_at`
	err = r.db.QueryRowContext(ctx, q,
		w.CompanyID, w.MetricID, w.ThreadID, w.Name, string(w.WindowGrain), string(w.Comparator),
		w.Threshold, w.CompareTo, w.CronExpression, w.Timezone, channels, w.CooldownMinutes,
		w.Enabled, w.CreatedBy,
	).Scan(&w.ID, &w.CreatedAt, &w.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert watcher: %w", err)
	}
	return nil
}

func (r *WatcherRepo) GetByID(ctx context.Context, companyID, id string) (*domain.Watcher, error) {
	q := `SELECT ` + watcherColumns + ` FROM watchers WHERE company_id = $1 AND id = $2`
	w, err := scanWatcher(r.db.QueryRowContext(ctx, q, companyID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return w, err
}

func (r *WatcherRepo) GetForFire(ctx context.Context, id string) (*domain.Watcher, error) {
	q := `SELECT ` + watcherColumns + ` FROM watchers WHERE id = $1`
	w, err := scanWatcher(r.db.QueryRowContext(ctx, q, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return w, err
}

func (r *WatcherRepo) ListByCompany(ctx context.Context, companyID string) ([]*domain.Watcher, error) {
	q := `SELECT ` + watcherColumns + ` FROM watchers WHERE company_id = $1 ORDER BY lower(name)`
	return r.queryWatchers(ctx, q, companyID)
}

func (r *WatcherRepo) ListEnabledForScheduler(ctx context.Context) ([]*domain.Watcher, error) {
	q := `SELECT ` + watcherColumns + ` FROM watchers WHERE enabled = true ORDER BY id`
	return r.queryWatchers(ctx, q)
}

func (r *WatcherRepo) queryWatchers(ctx context.Context, q string, args ...interface{}) ([]*domain.Watcher, error) {
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list watchers: %w", err)
	}
	defer rows.Close()
	out := []*domain.Watcher{}
	for rows.Next() {
		w, err := scanWatcher(rows)
		if err != nil {
			return nil, fmt.Errorf("scan watcher: %w", err)
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (r *WatcherRepo) CountByCompany(ctx context.Context, companyID string) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx,
		`SELECT count(*) FROM watchers WHERE company_id = $1`, companyID).Scan(&n)
	return n, err
}

func (r *WatcherRepo) Update(ctx context.Context, w *domain.Watcher) error {
	channels, err := json.Marshal(nonNilChannels(w.Channels))
	if err != nil {
		return fmt.Errorf("marshal watcher channels: %w", err)
	}
	// last_dry_run_at is written here as well as by TouchDryRun, because a
	// condition-changing edit clears it (the service sets it nil) so the stale
	// dry-run cannot vouch for the new condition. last_fired_at is not — that
	// belongs to TouchFired on the worker's fire path and an admin edit must not
	// clobber it.
	const q = `
		UPDATE watchers
		   SET metric_id = $3, name = $4, window_grain = $5, comparator = $6, threshold = $7,
		       compare_to = NULLIF($8, ''), cron_expression = $9, timezone = $10, channels = $11,
		       cooldown_minutes = $12, enabled = $13, last_dry_run_at = $14, updated_at = now()
		 WHERE company_id = $1 AND id = $2
		RETURNING updated_at`
	err = r.db.QueryRowContext(ctx, q,
		w.CompanyID, w.ID, w.MetricID, w.Name, string(w.WindowGrain), string(w.Comparator),
		w.Threshold, w.CompareTo, w.CronExpression, w.Timezone, channels, w.CooldownMinutes, w.Enabled,
		w.LastDryRunAt,
	).Scan(&w.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("update watcher: %w", err)
	}
	return nil
}

func (r *WatcherRepo) Delete(ctx context.Context, companyID, id string) error {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM watchers WHERE company_id = $1 AND id = $2`, companyID, id)
	if err != nil {
		return fmt.Errorf("delete watcher: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *WatcherRepo) TouchFired(ctx context.Context, id string, firedAt time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE watchers SET last_fired_at = $1, updated_at = now() WHERE id = $2`, firedAt, id)
	return err
}

func (r *WatcherRepo) TouchDryRun(ctx context.Context, id string, at time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE watchers SET last_dry_run_at = $1, updated_at = now() WHERE id = $2`, at, id)
	return err
}

// --- events ---

func (r *WatcherRepo) AppendEvent(ctx context.Context, e *domain.WatcherEvent) error {
	var delivery []byte
	if e.DeliveryStatus != nil {
		b, err := json.Marshal(e.DeliveryStatus)
		if err != nil {
			return fmt.Errorf("marshal delivery status: %w", err)
		}
		delivery = b
	}
	const q = `
		INSERT INTO watcher_events (
			watcher_id, company_id, metric_value, comparison_value, delta_pct,
			breached, suppressed_reason, thread_id, message_id, delivery_status
		) VALUES (
			$1, $2, $3, $4, $5, $6, NULLIF($7, ''), NULLIF($8, '')::uuid, NULLIF($9, '')::uuid, $10
		)
		RETURNING id, fired_at`
	return r.db.QueryRowContext(ctx, q,
		e.WatcherID, e.CompanyID, nullFloat(e.MetricValue), nullFloat(e.ComparisonValue),
		nullFloat(e.DeltaPct), e.Breached, e.SuppressedReason,
		derefString(e.ThreadID), derefString(e.MessageID), delivery,
	).Scan(&e.ID, &e.FiredAt)
}

func (r *WatcherRepo) SetEventDelivery(ctx context.Context, eventID, messageID string, delivery []domain.WatcherDelivery) error {
	b, err := json.Marshal(delivery)
	if err != nil {
		return fmt.Errorf("marshal delivery status: %w", err)
	}
	_, err = r.db.ExecContext(ctx,
		`UPDATE watcher_events SET message_id = NULLIF($2, '')::uuid, delivery_status = $3 WHERE id = $1`,
		eventID, messageID, b)
	return err
}

func (r *WatcherRepo) GetEvent(ctx context.Context, id string) (*domain.WatcherEvent, error) {
	q := `SELECT ` + watcherEventColumns + ` FROM watcher_events WHERE id = $1`
	e, err := scanWatcherEvent(r.db.QueryRowContext(ctx, q, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return e, err
}

func (r *WatcherRepo) ListEventsByWatcher(ctx context.Context, companyID, watcherID string, limit, offset int, firedOnly bool) ([]*domain.WatcherEvent, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	// The predicate is "breached and nothing stopped it", which is exactly the
	// condition the evaluator writes a delivery for. Suppressed rows are still
	// there, on the unfiltered query — this narrows what fills the window, it
	// does not hide anything.
	firedPredicate := ""
	if firedOnly {
		firedPredicate = ` AND breached AND COALESCE(suppressed_reason, '') = ''`
	}
	q := `SELECT ` + watcherEventColumns + ` FROM watcher_events
		WHERE company_id = $1 AND watcher_id = $2` + firedPredicate + `
		ORDER BY fired_at DESC LIMIT $3 OFFSET $4`
	rows, err := r.db.QueryContext(ctx, q, companyID, watcherID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list watcher events: %w", err)
	}
	defer rows.Close()
	out := []*domain.WatcherEvent{}
	for rows.Next() {
		e, err := scanWatcherEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("scan watcher event: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

const watcherEventColumns = `id, watcher_id, company_id, fired_at, metric_value, comparison_value,
	delta_pct, breached, COALESCE(suppressed_reason, ''), COALESCE(thread_id::text, ''),
	COALESCE(message_id::text, ''), delivery_status`

func scanWatcherEvent(row interface {
	Scan(dest ...interface{}) error
}) (*domain.WatcherEvent, error) {
	e := &domain.WatcherEvent{}
	var metricVal, cmpVal, deltaPct sql.NullFloat64
	var threadID, messageID string
	var delivery []byte
	if err := row.Scan(
		&e.ID, &e.WatcherID, &e.CompanyID, &e.FiredAt, &metricVal, &cmpVal,
		&deltaPct, &e.Breached, &e.SuppressedReason, &threadID, &messageID, &delivery,
	); err != nil {
		return nil, err
	}
	if metricVal.Valid {
		v := metricVal.Float64
		e.MetricValue = &v
	}
	if cmpVal.Valid {
		v := cmpVal.Float64
		e.ComparisonValue = &v
	}
	if deltaPct.Valid {
		v := deltaPct.Float64
		e.DeltaPct = &v
	}
	if threadID != "" {
		e.ThreadID = &threadID
	}
	if messageID != "" {
		e.MessageID = &messageID
	}
	if len(delivery) > 0 {
		if err := json.Unmarshal(delivery, &e.DeliveryStatus); err != nil {
			return nil, fmt.Errorf("unmarshal delivery status: %w", err)
		}
	}
	return e, nil
}

func nonNilChannels(c []domain.WatcherChannel) []domain.WatcherChannel {
	if c == nil {
		return []domain.WatcherChannel{}
	}
	return c
}

func nullFloat(f *float64) interface{} {
	if f == nil {
		return nil
	}
	return *f
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
