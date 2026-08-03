package domain

import (
	"context"
	"slices"
	"time"
)

// Webhook event names (T-15). The vocabulary is closed and lives here, because
// a subscription to an event nobody publishes is a tenant waiting for a
// delivery that will never come — and the failure is silent at every layer
// below this one.
//
// Report callbacks are deliberately absent. `POST /v1/reports` takes a
// `callback_url` per request (T-A2): the caller is waiting on that one report
// and named the URL in the same breath, which is a different thing from a
// standing subscription to what a workspace does.
const (
	// WebhookWatcherBreached fires when a watcher's condition is met and the
	// briefing turn has been enqueued (T-08).
	WebhookWatcherBreached = "watcher.breached"
	// WebhookActionExecuted fires when an approved action ran — including one
	// that ran and failed, because "we tried and it did not work" is the event
	// an integration most needs (T-10).
	WebhookActionExecuted = "action.executed"
	// WebhookScheduledTaskCompleted fires when a scheduled task's turn ends.
	WebhookScheduledTaskCompleted = "scheduled_task.completed"
)

// WebhookEvents is every event a subscription may name, in a stable order for
// the settings form.
func WebhookEvents() []string {
	return []string{WebhookWatcherBreached, WebhookActionExecuted, WebhookScheduledTaskCompleted}
}

// ValidWebhookEvent reports whether e is one this system publishes.
func ValidWebhookEvent(e string) bool { return slices.Contains(WebhookEvents(), e) }

// WebhookAutoDisableAfter is how many consecutive failed deliveries disable a
// subscription (T-15).
//
// Twenty rather than three: a tenant's server being down for an hour is
// ordinary, and disabling on the first blip would make this feature something
// people stop trusting. Twenty consecutive terminal failures — each of which is
// already five delivery attempts with backoff — is a server that is not coming
// back without someone looking at it.
const WebhookAutoDisableAfter = 20

// WebhookSubscription is one tenant's standing request to be told when
// something happens (T-15).
//
// The signing secret is not here: it is the company's, on `companies.
// webhook_secret`, minted on first use and shared by every callback we send
// them — a receiver verifying two subscriptions with one secret is the shape
// every webhook integration expects, and rotating per subscription would mean a
// tenant holding a table of secrets.
type WebhookSubscription struct {
	ID        string `json:"id"`
	CompanyID string `json:"company_id"`
	URL       string `json:"url"`
	// Events is what this subscription wants. Empty is not "everything" — it is
	// a subscription that matches nothing, and the service refuses to store one.
	// The opposite rule to an agent's tool allowlist, and deliberately: there,
	// an empty list widens what an agent may do inside Argentum; here it would
	// widen what leaves it.
	Events  []string `json:"events"`
	Enabled bool     `json:"enabled"`
	// ConsecutiveFailures counts terminal delivery failures since the last
	// success. Reset to zero by any delivery that lands.
	ConsecutiveFailures int `json:"consecutive_failures"`
	// DisabledReason is set when this system disabled the subscription rather
	// than the tenant. Empty for one an admin switched off, so the settings
	// screen can tell "you turned this off" from "we did, and here is why".
	DisabledReason string     `json:"disabled_reason,omitempty"`
	LastSuccessAt  *time.Time `json:"last_success_at,omitempty"`
	LastFailureAt  *time.Time `json:"last_failure_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// WantsEvent reports whether this subscription should receive e.
func (s *WebhookSubscription) WantsEvent(e string) bool {
	return s != nil && s.Enabled && slices.Contains(s.Events, e)
}

// WebhookSubscriptionRepository persists subscriptions and their health.
//
// The health methods are here rather than in the service for the same reason
// ActionRepository.Approve is: the counter is read-modify-written by the worker
// on every delivery outcome, and two deliveries failing at once must not each
// read 19 and write 20.
type WebhookSubscriptionRepository interface {
	Create(ctx context.Context, s *WebhookSubscription) error
	GetByID(ctx context.Context, companyID, id string) (*WebhookSubscription, error)
	ListByCompany(ctx context.Context, companyID string) ([]*WebhookSubscription, error)
	Update(ctx context.Context, s *WebhookSubscription) error
	Delete(ctx context.Context, companyID, id string) error

	// ListForEvent returns the enabled subscriptions of one company that name
	// this event. It is the fan-out's only read, and it is on the hot path of
	// every watcher breach.
	ListForEvent(ctx context.Context, companyID, event string) ([]*WebhookSubscription, error)

	// RecordSuccess zeroes the failure counter and stamps the success.
	RecordSuccess(ctx context.Context, id string, at time.Time) error
	// RecordFailure increments the counter atomically and disables the
	// subscription when it reaches disableAt, reporting whether this call is
	// the one that disabled it — so exactly one caller logs it and exactly one
	// notification could ever be sent.
	RecordFailure(ctx context.Context, id string, at time.Time, disableAt int) (disabled bool, err error)
}
