package app

import (
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
)

// The bodies of the three events T-15 publishes.
//
// They are structs rather than maps for the reason the signature contract
// demands: the payload is marshalled exactly once, in webhookout.Sender, and
// those bytes are what get signed and sent. A map would marshal with a
// different key order on a different day, which verifies against nothing — the
// single most common way a webhook implementation is wrong. A struct also makes
// the shape reviewable, which matters more here than anywhere else in this
// codebase: it is the only JSON we ask somebody else to write code against.
//
// Every one carries `event` and `occurred_at` so a receiver can route and
// de-duplicate from the body alone, without trusting the headers to survive a
// proxy.

// WebhookEnvelope is the two fields every event body starts with. Embedded
// rather than wrapped so the event's own fields stay at the top level, which is
// what a receiver's `payload.watcher_id` expects.
type WebhookEnvelope struct {
	Event      string    `json:"event"`
	OccurredAt time.Time `json:"occurred_at"`
	CompanyID  string    `json:"company_id"`
}

func envelope(event, companyID string, at time.Time) WebhookEnvelope {
	return WebhookEnvelope{Event: event, OccurredAt: at.UTC(), CompanyID: companyID}
}

// WatcherBreachedPayload is `watcher.breached`. It carries the observed value
// and the threshold rather than a rendered sentence: a receiver deciding
// whether to page someone needs the number, and the sentence the agent writes
// goes to the tenant's chat channels, not here.
type WatcherBreachedPayload struct {
	WebhookEnvelope
	WatcherID   string `json:"watcher_id"`
	WatcherName string `json:"watcher_name"`
	MetricID    string `json:"metric_id"`
	EventID     string `json:"event_id"`
	// Value and ComparisonValue are pointers because the domain's are: a
	// `no_data` breach has no number, and a zero would be a different claim from
	// "there was nothing to measure".
	Value           *float64  `json:"value,omitempty"`
	ComparisonValue *float64  `json:"comparison_value,omitempty"`
	DeltaPct        *float64  `json:"delta_pct,omitempty"`
	Comparator      string    `json:"comparator"`
	Threshold       float64   `json:"threshold"`
	WindowGrain     string    `json:"window_grain"`
	FiredAt         time.Time `json:"fired_at"`
}

func newWatcherBreachedPayload(w *domain.Watcher, ev *domain.WatcherEvent, at time.Time) WatcherBreachedPayload {
	p := WatcherBreachedPayload{
		WebhookEnvelope: envelope(domain.WebhookWatcherBreached, w.CompanyID, at),
		WatcherID:       w.ID,
		WatcherName:     w.Name,
		MetricID:        w.MetricID,
		Comparator:      string(w.Comparator),
		Threshold:       w.Threshold,
		WindowGrain:     string(w.WindowGrain),
		FiredAt:         at.UTC(),
	}
	if ev != nil {
		p.EventID = ev.ID
		p.Value = ev.MetricValue
		p.ComparisonValue = ev.ComparisonValue
		p.DeltaPct = ev.DeltaPct
		p.FiredAt = ev.FiredAt.UTC()
	}
	return p
}

// ActionExecutedPayload is `action.executed`, and it is published for a failed
// execution too — "we tried and it did not work" is the case an integration
// most needs to hear about, and a receiver that only wants successes can read
// `status`.
//
// The parameters are the redacted ones, which is the same object the approval
// card showed and the executor ran off. Not the result: an action's result can
// be a whole HTTP response body, and none of it is ours to forward.
type ActionExecutedPayload struct {
	WebhookEnvelope
	InvocationID string `json:"invocation_id"`
	ActionKind   string `json:"action_kind"`
	Status       string `json:"status"`
	Description  string `json:"description,omitempty"`
	ThreadID     string `json:"thread_id,omitempty"`
	DecidedBy    string `json:"decided_by,omitempty"`
	ErrorText    string `json:"error_text,omitempty"`
}

func newActionExecutedPayload(inv *domain.ActionInvocation, description string, at time.Time) ActionExecutedPayload {
	return ActionExecutedPayload{
		WebhookEnvelope: envelope(domain.WebhookActionExecuted, inv.CompanyID, at),
		InvocationID:    inv.ID,
		ActionKind:      inv.Kind,
		Status:          string(inv.Status),
		Description:     description,
		ThreadID:        inv.ThreadID,
		DecidedBy:       inv.DecidedBy,
		ErrorText:       inv.ErrorText,
	}
}

// ScheduledTaskCompletedPayload is `scheduled_task.completed`. Like the action
// event it fires on failure as well, because a nightly report that stopped
// arriving is exactly what a tenant wants told rather than left to notice.
type ScheduledTaskCompletedPayload struct {
	WebhookEnvelope
	TaskID    string `json:"task_id"`
	TaskName  string `json:"task_name,omitempty"`
	RunID     string `json:"run_id"`
	ThreadID  string `json:"thread_id,omitempty"`
	MessageID string `json:"message_id,omitempty"`
	Status    string `json:"status"`
	ErrorText string `json:"error_text,omitempty"`
}
