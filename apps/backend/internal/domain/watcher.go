package domain

import (
	"context"
	"time"
)

// WatcherGrain is the period one watcher evaluation covers (T-08). A subset of
// MetricGrain: a watcher runs on a cron, and day/week/month are the cadences a
// human reads a business on. Quarter and year are metric grains for a report,
// not alert windows.
type WatcherGrain string

const (
	WatcherGrainDay   WatcherGrain = "day"
	WatcherGrainWeek  WatcherGrain = "week"
	WatcherGrainMonth WatcherGrain = "month"
)

// Valid reports whether g is a grain a watcher can evaluate on.
func (g WatcherGrain) Valid() bool {
	switch g {
	case WatcherGrainDay, WatcherGrainWeek, WatcherGrainMonth:
		return true
	}
	return false
}

// WatcherComparator is the condition a watcher breaches on (T-08).
//
// The threshold comparators read the metric's value directly; the pct_change
// comparators read the delta against a comparison window and so require
// CompareTo; no_data breaches when the metric returns no usable row at all,
// which is how a watcher catches a stalled pipeline rather than a bad number.
type WatcherComparator string

const (
	WatcherComparatorGT          WatcherComparator = "gt"
	WatcherComparatorLT          WatcherComparator = "lt"
	WatcherComparatorPctChangeGT WatcherComparator = "pct_change_gt"
	WatcherComparatorPctChangeLT WatcherComparator = "pct_change_lt"
	WatcherComparatorNoData      WatcherComparator = "no_data"
)

// Valid reports whether c is a comparator this release evaluates.
func (c WatcherComparator) Valid() bool {
	switch c {
	case WatcherComparatorGT, WatcherComparatorLT,
		WatcherComparatorPctChangeGT, WatcherComparatorPctChangeLT,
		WatcherComparatorNoData:
		return true
	}
	return false
}

// NeedsComparison reports whether the comparator reads a comparison window, and
// therefore whether CompareTo must be set.
func (c WatcherComparator) NeedsComparison() bool {
	return c == WatcherComparatorPctChangeGT || c == WatcherComparatorPctChangeLT
}

// WatcherChannel is one delivery destination for a watcher's fire.
//
// Ref means whatever the channel keys delivery on: a WhatsApp phone number, a
// Discord channel id, a Lark chat id. The dashboard channel has no ref — the
// watcher's dedicated thread is where it lands — so Ref is empty there.
type WatcherChannel struct {
	Channel Channel `json:"channel"`
	Ref     string  `json:"ref,omitempty"`
}

// WatcherDelivery is one channel's outcome for one fire, stored in
// watcher_events.delivery_status.
type WatcherDelivery struct {
	Channel Channel `json:"channel"`
	Ref     string  `json:"ref,omitempty"`
	// Status is "delivered", "failed", or "skipped" (a channel with no provider
	// wired in this deployment).
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// Watcher is a metric-condition trigger (T-08): evaluate MetricID on a cron, and
// when Comparator/Threshold breaches, fire an agent turn into Channels.
type Watcher struct {
	ID        string `json:"id"`
	CompanyID string `json:"company_id"`
	MetricID  string `json:"metric_id"`
	// ThreadID is the dedicated thread each fire runs in, reused across fires so
	// the dashboard shows one conversation per watcher.
	ThreadID        string            `json:"thread_id"`
	Name            string            `json:"name"`
	WindowGrain     WatcherGrain      `json:"window_grain"`
	Comparator      WatcherComparator `json:"comparator"`
	Threshold       float64           `json:"threshold"`
	CompareTo       string            `json:"compare_to,omitempty"`
	CronExpression  string            `json:"cron_expression"`
	Timezone        string            `json:"timezone"`
	Channels        []WatcherChannel  `json:"channels"`
	CooldownMinutes int               `json:"cooldown_minutes"`
	Enabled         bool              `json:"enabled"`
	LastFiredAt     *time.Time        `json:"last_fired_at,omitempty"`
	LastDryRunAt    *time.Time        `json:"last_dry_run_at,omitempty"`
	CreatedBy       string            `json:"created_by,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

// WatcherEvent is one evaluation of a watcher, breached or not (T-08).
type WatcherEvent struct {
	ID        string    `json:"id"`
	WatcherID string    `json:"watcher_id"`
	CompanyID string    `json:"company_id"`
	FiredAt   time.Time `json:"fired_at"`
	// MetricValue/ComparisonValue/DeltaPct are nil when the evaluation had no
	// number to record: a no_data breach, or a threshold breach with no
	// comparison window.
	MetricValue     *float64 `json:"metric_value,omitempty"`
	ComparisonValue *float64 `json:"comparison_value,omitempty"`
	DeltaPct        *float64 `json:"delta_pct,omitempty"`
	Breached        bool     `json:"breached"`
	// SuppressedReason is "cooldown" when a real breach did not fire; empty
	// otherwise.
	SuppressedReason string            `json:"suppressed_reason,omitempty"`
	ThreadID         *string           `json:"thread_id,omitempty"`
	MessageID        *string           `json:"message_id,omitempty"`
	DeliveryStatus   []WatcherDelivery `json:"delivery_status,omitempty"`
}

// WatcherRepository is the persistence contract for watchers and their events.
//
// Every method that names a watcher takes the company id beside it, like
// MetricRepository and for the same reason: the id is a bare uuid on an
// admin-only surface, and a repository that answers for any company is one
// forgotten check from a cross-tenant read.
type WatcherRepository interface {
	Create(ctx context.Context, w *Watcher) error
	GetByID(ctx context.Context, companyID, id string) (*Watcher, error)
	ListByCompany(ctx context.Context, companyID string) ([]*Watcher, error)
	CountByCompany(ctx context.Context, companyID string) (int, error)
	Update(ctx context.Context, w *Watcher) error
	Delete(ctx context.Context, companyID, id string) error

	// TouchFired records that a fire went out, for the cooldown gate. Separate
	// from Update so a fire does not have to round-trip the whole row.
	TouchFired(ctx context.Context, id string, firedAt time.Time) error
	// TouchDryRun records a dry-run, which is what a later enable checks the age
	// of.
	TouchDryRun(ctx context.Context, id string, at time.Time) error

	// GetForFire loads a watcher by id alone — no company scope — for the worker
	// path, where the id comes from a queue payload this process wrote itself and
	// there is no request tenant to check against.
	GetForFire(ctx context.Context, id string) (*Watcher, error)
	// ListEnabledForScheduler returns every enabled watcher across all companies,
	// for the periodic task manager's config provider.
	ListEnabledForScheduler(ctx context.Context) ([]*Watcher, error)

	// Events
	AppendEvent(ctx context.Context, e *WatcherEvent) error
	// SetEventDelivery records the assistant message and per-channel delivery
	// outcome once a fire's turn has completed.
	SetEventDelivery(ctx context.Context, eventID, messageID string, delivery []WatcherDelivery) error
	GetEvent(ctx context.Context, id string) (*WatcherEvent, error)
	// ListEventsByWatcher returns a watcher's evaluations, newest first.
	//
	// firedOnly narrows to the evaluations that actually delivered — breached and
	// not suppressed. It is a query parameter rather than a client-side filter
	// because the window is the last N rows: a per-minute watcher inside a
	// 12-hour cooldown fills 50 rows with identical `suppressed` lines in under an
	// hour, so the delivery that *started* the cooldown is not merely off screen,
	// it is not in the payload at all.
	ListEventsByWatcher(ctx context.Context, companyID, watcherID string, limit, offset int, firedOnly bool) ([]*WatcherEvent, error)
}
