package webhookout

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/domain"
)

// SecretResolver hands back the tenant's signing secret, minting one on first
// use. Narrow, and declared at the consumer, so this package does not depend
// on the whole company repository to read one column.
type SecretResolver interface {
	EnsureWebhookSecret(ctx context.Context, companyID string) (string, error)
}

// Dispatcher hands a queued delivery to the worker. Declared here rather than
// taking *queue.Enqueuer so a test can drive the sender without Redis, and so
// the queue package stays a dependency of the wiring rather than of this one.
type Dispatcher interface {
	EnqueueWebhookDelivery(ctx context.Context, deliveryID string) error
}

// maxPayloadBytes bounds one callback body. Nothing this package sends is
// large — a `report.completed` body is a few hundred bytes — so a body over
// this is a bug upstream, and discovering it here beats discovering it as a
// row that will not fit.
const maxPayloadBytes = 64 * 1024

// Sender registers an outbound callback and queues it for delivery.
//
// Registering and delivering are separate on purpose. The caller of
// `POST /v1/reports` is holding an HTTP response open, and a tenant's slow
// server must not become our latency; the worker owns the retry budget and the
// wall-clock, and the row exists before either runs so a crash between them
// leaves something to find.
type Sender struct {
	deliveries   domain.WebhookDeliveryRepository
	secrets      SecretResolver
	dispatch     Dispatcher
	allowPrivate bool
}

func NewSender(deliveries domain.WebhookDeliveryRepository, secrets SecretResolver, dispatch Dispatcher, allowPrivate bool) *Sender {
	return &Sender{deliveries: deliveries, secrets: secrets, dispatch: dispatch, allowPrivate: allowPrivate}
}

// AllowPrivate reports whether this deployment permits callbacks to addresses
// only it can reach. Handlers read it so the URL they validate at registration
// is validated by the same rule the worker will apply.
func (s *Sender) AllowPrivate() bool { return s.allowPrivate }

// Send marshals the event, records it, and queues delivery. The returned id is
// the delivery row's, which is also what rides in `Argentum-Delivery`.
//
// The payload is marshalled once, here, and the bytes are what get stored,
// signed and sent. Re-marshalling at any later point would produce JSON with a
// different key order that verifies against nothing — the single most common
// way a webhook signature implementation is wrong.
func (s *Sender) Send(ctx context.Context, companyID, event, url string, payload any) (string, error) {
	if s == nil || s.deliveries == nil {
		return "", fmt.Errorf("webhook delivery is not configured on this deployment")
	}
	if err := CheckTarget(url, s.allowPrivate); err != nil {
		return "", err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal webhook payload: %w", err)
	}
	if len(body) > maxPayloadBytes {
		return "", fmt.Errorf("webhook payload is %d bytes; the limit is %d", len(body), maxPayloadBytes)
	}

	rec := &domain.WebhookDelivery{
		CompanyID: companyID,
		Event:     event,
		URL:       url,
		Payload:   body,
		Status:    domain.WebhookPending,
	}
	if err := s.deliveries.Create(ctx, rec); err != nil {
		return "", fmt.Errorf("record webhook delivery: %w", err)
	}
	if s.dispatch == nil {
		return rec.ID, fmt.Errorf("no queue to deliver on")
	}
	if err := s.dispatch.EnqueueWebhookDelivery(ctx, rec.ID); err != nil {
		return rec.ID, fmt.Errorf("queue webhook delivery: %w", err)
	}
	return rec.ID, nil
}

// Deliverer performs one attempt. It lives in the worker, where a retry budget
// and a ten-second wall clock belong.
type Deliverer struct {
	deliveries   domain.WebhookDeliveryRepository
	secrets      SecretResolver
	client       *http.Client
	allowPrivate bool
	maxAttempts  int
}

// NewDeliverer wires the worker side. maxAttempts is the point at which the
// row is marked failed rather than pending; asynq owns the backoff between
// attempts, and this only decides when to stop calling it pending.
func NewDeliverer(deliveries domain.WebhookDeliveryRepository, secrets SecretResolver, allowPrivate bool, maxAttempts int) *Deliverer {
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	return &Deliverer{
		deliveries: deliveries,
		secrets:    secrets,
		// A tenant's server holding the connection open must not hold a worker
		// slot with it. Ten seconds is generous for "acknowledge receipt",
		// which is all a receiver should be doing before it answers.
		client:       &http.Client{Timeout: 10 * time.Second},
		allowPrivate: allowPrivate,
		maxAttempts:  maxAttempts,
	}
}

// Deliver makes one attempt at delivering the recorded callback.
//
// A returned error asks asynq to retry; nil ends the task. The distinction is
// the whole contract: a 500 from the receiver returns an error so it is tried
// again, and a 400 returns nil because a receiver that rejected the body will
// reject it identically in ten minutes.
func (d *Deliverer) Deliver(ctx context.Context, deliveryID string) error {
	rec, err := d.deliveries.Get(ctx, deliveryID)
	if err != nil {
		// A row that is not there cannot be delivered and will not appear. Ending
		// the task is the only outcome that is not an infinite retry.
		logrus.WithError(err).WithField("delivery_id", deliveryID).
			Warn("webhook delivery record missing; dropping")
		return nil
	}
	if rec.Status == domain.WebhookDelivered {
		return nil
	}
	// Resolved here, and not at registration: DNS can change between the two,
	// and a name that answers with an internal address is only discoverable by
	// asking immediately before the request.
	if err := CheckResolvedTarget(rec.URL, d.allowPrivate); err != nil {
		d.record(ctx, rec.ID, domain.WebhookFailed, 0, err.Error())
		return nil
	}

	secret, err := d.secrets.EnsureWebhookSecret(ctx, rec.CompanyID)
	if err != nil {
		return fmt.Errorf("resolve webhook secret: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rec.URL, bytes.NewReader(rec.Payload))
	if err != nil {
		d.record(ctx, rec.ID, domain.WebhookFailed, 0, err.Error())
		return nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Argentum-Webhooks/1")
	req.Header.Set(EventHeader, rec.Event)
	req.Header.Set(DeliveryHeader, rec.ID)
	req.Header.Set(SignatureHeader, Sign(secret, time.Now(), rec.Payload))

	resp, err := d.client.Do(req)
	if err != nil {
		// No HTTP status at all — DNS, TLS, a timeout. Recorded as status 0,
		// which is the distinction the log exists to preserve: "your server
		// said no" and "we could not reach your server" are different
		// conversations.
		return d.retryOrFail(ctx, rec, 0, err.Error())
	}
	defer func() { _ = resp.Body.Close() }()
	// Read and discard a bounded prefix so the connection can be reused. The
	// body is not stored: a receiver's error page can be megabytes, and none of
	// it is ours to keep.
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		d.record(ctx, rec.ID, domain.WebhookDelivered, resp.StatusCode, "")
		return nil
	}
	if resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
		// A receiver that rejected this body will reject it identically in ten
		// minutes. 429 is the exception — it is a request to come back later,
		// which is exactly what a retry is.
		d.record(ctx, rec.ID, domain.WebhookFailed, resp.StatusCode, string(snippet))
		return nil
	}
	return d.retryOrFail(ctx, rec, resp.StatusCode, string(snippet))
}

// retryOrFail records the attempt and decides whether to ask for another.
func (d *Deliverer) retryOrFail(ctx context.Context, rec *domain.WebhookDelivery, status int, msg string) error {
	// rec.Attempts is the count *before* this attempt, which the record call
	// below increments. So the last permitted attempt is the one where the
	// prior count has already reached maxAttempts-1.
	if rec.Attempts+1 >= d.maxAttempts {
		d.record(ctx, rec.ID, domain.WebhookFailed, status, msg)
		logrus.WithFields(logrus.Fields{
			"company_id":  rec.CompanyID,
			"delivery_id": rec.ID,
			"url":         rec.URL,
			"last_status": status,
		}).Warn("webhook delivery gave up after the retry budget")
		return nil
	}
	d.record(ctx, rec.ID, domain.WebhookPending, status, msg)
	return fmt.Errorf("webhook delivery to %s answered %d", rec.URL, status)
}

func (d *Deliverer) record(ctx context.Context, id string, status domain.WebhookDeliveryStatus, httpStatus int, msg string) {
	if len(msg) > 1024 {
		msg = msg[:1024]
	}
	if err := d.deliveries.RecordAttempt(ctx, id, status, httpStatus, msg, time.Now()); err != nil {
		logrus.WithError(err).WithField("delivery_id", id).
			Warn("webhook attempt not recorded; the delivery log is now incomplete")
	}
}
