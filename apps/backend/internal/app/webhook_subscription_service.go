package app

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/webhookout"
)

// WebhookDispatcher is the half of webhookout.Sender the fan-out uses. An
// interface so the service is testable without Redis or a delivery table, and
// narrowed to the one method so nothing here can be made to deliver something
// nobody subscribed to.
type WebhookDispatcher interface {
	SendFrom(ctx context.Context, companyID, subscriptionID, event, url string, payload any) (string, error)
	AllowPrivate() bool
}

// WebhookSubscriptionService is T-15: the subscription model and the fan-out.
//
// It is deliberately not a delivery mechanism. `T-A2` built one — HMAC over
// `<timestamp>.<body>`, asynq retry with backoff, an SSRF refusal on our own
// network, a row per attempt — for report callbacks, and the ticket's own
// instruction is to subscribe events to it rather than write a second signer or
// a second retry loop. So everything below decides *who* gets told and *what
// the body says*; `webhookout` decides how it travels.
type WebhookSubscriptionService struct {
	repo   domain.WebhookSubscriptionRepository
	sender WebhookDispatcher
}

func NewWebhookSubscriptionService(repo domain.WebhookSubscriptionRepository, sender WebhookDispatcher) *WebhookSubscriptionService {
	return &WebhookSubscriptionService{repo: repo, sender: sender}
}

// --- admin surface ---

func (s *WebhookSubscriptionService) List(ctx context.Context, companyID string) ([]*domain.WebhookSubscription, error) {
	return s.repo.ListByCompany(ctx, companyID)
}

// Create validates and stores a subscription.
//
// The URL is checked against the same rule the worker will apply at delivery
// time, so a tenant learns "that address is not one we will post to" while they
// are looking at the form — rather than as a delivery that silently fails
// forever afterwards.
func (s *WebhookSubscriptionService) Create(ctx context.Context, companyID, url string, events []string) (*domain.WebhookSubscription, error) {
	url = strings.TrimSpace(url)
	if err := webhookout.CheckTarget(url, s.sender.AllowPrivate()); err != nil {
		return nil, fmt.Errorf("%w: %s", domain.ErrInvalidInput, err)
	}
	clean, err := cleanEvents(events)
	if err != nil {
		return nil, err
	}
	sub := &domain.WebhookSubscription{CompanyID: companyID, URL: url, Events: clean, Enabled: true}
	if err := s.repo.Create(ctx, sub); err != nil {
		return nil, err
	}
	logrus.WithFields(logrus.Fields{
		"company_id": companyID, "subscription_id": sub.ID, "events": clean,
	}).Info("webhook subscription created")
	return sub, nil
}

// Update replaces the URL, the events and the enabled flag. Re-enabling clears
// the failure count — a subscription switched back on after the receiver was
// fixed must not be disabled again by the first blip on top of nineteen old
// failures.
func (s *WebhookSubscriptionService) Update(ctx context.Context, companyID, id, url string, events []string, enabled bool) (*domain.WebhookSubscription, error) {
	sub, err := s.repo.GetByID(ctx, companyID, id)
	if err != nil {
		return nil, err
	}
	url = strings.TrimSpace(url)
	if err := webhookout.CheckTarget(url, s.sender.AllowPrivate()); err != nil {
		return nil, fmt.Errorf("%w: %s", domain.ErrInvalidInput, err)
	}
	clean, err := cleanEvents(events)
	if err != nil {
		return nil, err
	}
	sub.URL, sub.Events, sub.Enabled = url, clean, enabled
	if err := s.repo.Update(ctx, sub); err != nil {
		return nil, err
	}
	return sub, nil
}

func (s *WebhookSubscriptionService) Delete(ctx context.Context, companyID, id string) error {
	return s.repo.Delete(ctx, companyID, id)
}

// cleanEvents de-duplicates, orders and validates. An empty list is refused:
// a subscription that matches nothing is a tenant waiting for a delivery that
// cannot arrive, and the silence is indistinguishable from a broken fan-out.
func cleanEvents(events []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, e := range events {
		e = strings.TrimSpace(e)
		if e == "" || seen[e] {
			continue
		}
		if !domain.ValidWebhookEvent(e) {
			return nil, fmt.Errorf("%w: %q is not an event this system publishes; the events are %v",
				domain.ErrInvalidInput, e, domain.WebhookEvents())
		}
		seen[e] = true
		out = append(out, e)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: choose at least one event — a subscription to nothing is never delivered",
			domain.ErrInvalidInput)
	}
	// Stable order, so two subscriptions with the same events read the same in
	// the settings list and in a diff.
	slices.Sort(out)
	return out, nil
}

// --- the fan-out ---

// Publish sends one event to every subscription of this company that wants it.
//
// It never returns an error and never blocks the thing that produced the event.
// A watcher that breached, an action that ran, a scheduled task that finished:
// each of those has already happened, and a tenant's unreachable server must
// not turn a completed piece of work into a failed one. Failures are the
// delivery log's business, and twenty consecutive ones are the subscription's.
//
// It is safe on a nil service, which is what a deployment with no webhook
// support — and every test that does not care — passes.
func (s *WebhookSubscriptionService) Publish(ctx context.Context, companyID, event string, payload any) {
	if s == nil || s.repo == nil || s.sender == nil || companyID == "" {
		return
	}
	subs, err := s.repo.ListForEvent(ctx, companyID, event)
	if err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"company_id": companyID, "event": event,
		}).Warn("webhook fan-out could not read subscriptions; this event is not delivered")
		return
	}
	for _, sub := range subs {
		if _, err := s.sender.SendFrom(ctx, companyID, sub.ID, event, sub.URL, payload); err != nil {
			// Registering the delivery failed — a refused target, a payload over
			// the cap, a queue that would not take it. The event itself stands.
			logrus.WithError(err).WithFields(logrus.Fields{
				"company_id": companyID, "subscription_id": sub.ID, "event": event,
			}).Warn("webhook delivery could not be queued")
		}
	}
}
