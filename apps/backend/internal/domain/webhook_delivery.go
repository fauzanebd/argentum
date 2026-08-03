package domain

import (
	"context"
	"time"
)

// WebhookDeliveryStatus is where one outbound callback got to.
type WebhookDeliveryStatus string

const (
	WebhookPending   WebhookDeliveryStatus = "pending"
	WebhookDelivered WebhookDeliveryStatus = "delivered"
	// WebhookFailed is set once the retry budget is spent, not on the first
	// refusal. A receiver that was redeploying and answered 502 twice has not
	// failed; a row that said so would make the log useless for the question
	// it exists to answer.
	WebhookFailed WebhookDeliveryStatus = "failed"
)

// WebhookDelivery is one attempt to hand a tenant's server an event, and the
// record of how it went (T-A2).
//
// The record is half the feature. "We never got the callback" is otherwise an
// unanswerable support ticket: without a row there is nothing to say whether
// we sent it, what we sent, or what their server answered.
type WebhookDelivery struct {
	ID        string `json:"id"`
	CompanyID string `json:"company_id"`
	Event     string `json:"event"`
	URL       string `json:"url"`
	// Payload is exactly the bytes that were signed. Not a struct to
	// re-marshal: JSON with different key order verifies against nothing, so a
	// tenant debugging a signature mismatch needs the bytes themselves.
	Payload  []byte                `json:"-"`
	Status   WebhookDeliveryStatus `json:"status"`
	Attempts int                   `json:"attempts"`
	// LastStatus is the receiver's HTTP status, or 0 when the request never
	// got one — DNS, TLS, a timeout. That distinction is the first thing to
	// look at and it is lost if both are recorded as a failure.
	LastStatus  int        `json:"last_status"`
	LastError   string     `json:"last_error,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	DeliveredAt *time.Time `json:"delivered_at,omitempty"`
	// SubscriptionID is the standing subscription this delivery came from
	// (T-15), and empty for a `report.completed` callback, which belongs to the
	// one request that named its URL. It is what lets the worker count a
	// terminal failure against the subscription that caused it rather than
	// guessing by URL — two subscriptions may legitimately share one.
	SubscriptionID string `json:"subscription_id,omitempty"`
}

// WebhookDeliveryRepository persists the delivery log.
type WebhookDeliveryRepository interface {
	Create(ctx context.Context, d *WebhookDelivery) error
	Get(ctx context.Context, id string) (*WebhookDelivery, error)
	// RecordAttempt appends one attempt's outcome. It increments rather than
	// setting attempts, because the sender is asynq-driven and a retry does
	// not know which attempt it is.
	RecordAttempt(ctx context.Context, id string, status WebhookDeliveryStatus, httpStatus int, errMsg string, at time.Time) error
	ListByCompany(ctx context.Context, companyID string, limit int) ([]*WebhookDelivery, error)
}
