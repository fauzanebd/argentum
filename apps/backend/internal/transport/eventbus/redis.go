// Package eventbus contains the Redis-backed implementation of
// app.EventBus. The worker publishes chat events here; API replicas
// subscribe to forward them down WebSocket connections.
package eventbus

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/app"
)

// ChannelPrefix is the Redis pub/sub channel namespace for thread events.
// Channels are formatted as `argentum:thread:{thread_id}`.
const ChannelPrefix = "argentum:thread:"

// OutboundChannelPrefix is the namespace for cross-process outbound
// delivery (e.g. discord, where cmd/discord owns the gateway sessions and
// the worker publishes finished replies here). Channels are formatted as
// `argentum:outbound:{channel}:{company_id}`.
const OutboundChannelPrefix = "argentum:outbound:"

// ChannelFor returns the Redis channel name for a given thread ID.
func ChannelFor(threadID string) string {
	return ChannelPrefix + threadID
}

// ReportChannelPrefix is the namespace for a report job that has no thread —
// `POST /v1/reports/render`, which takes a spec and never talks to an agent
// (T-V3). Channels are `argentum:report:{report_id}`.
//
// It exists because a video renders for minutes and that door's SSE endpoint
// had nothing to subscribe to: a render job publishes on no thread channel,
// so `GET /v1/reports/:id/events` answered once and closed. That was right
// when every render was sub-second and is a four-minute silent spinner now.
const ReportChannelPrefix = "argentum:report:"

// ReportChannelFor returns the Redis channel name for a threadless report job.
func ReportChannelFor(reportID string) string {
	return ReportChannelPrefix + reportID
}

// OutboundChannelFor returns the Redis channel name for outbound delivery
// to a specific (channel, company) pair.
func OutboundChannelFor(channel, companyID string) string {
	return OutboundChannelPrefix + channel + ":" + companyID
}

// OutboundPattern returns the PSUBSCRIBE pattern for a channel kind.
func OutboundPattern(channel string) string {
	return OutboundChannelPrefix + channel + ":*"
}

// DiscordReloadChannel is the pub/sub channel API publishes to when a
// tenant's Discord credentials change; cmd/discord subscribes and reopens
// the session for the named company_id (message body is the company_id).
const DiscordReloadChannel = "argentum:discord:reload"

// RedisBus implements app.EventBus. Publishing serializes the event as
// JSON and pushes it onto a per-thread channel so any subscriber (API
// process serving a WebSocket) can pick it up.
type RedisBus struct {
	rdb *redis.Client
}

// NewRedisBus wires a RedisBus around an existing redis.Client. The bus
// holds no internal state.
func NewRedisBus(rdb *redis.Client) *RedisBus {
	return &RedisBus{rdb: rdb}
}

// Publish serializes evt and PUBLISHes it to argentum:thread:{threadID}.
// Network errors are surfaced; subscriber count is ignored (no live WS
// is a normal state, e.g. WhatsApp-only threads).
//
// The same call folds the event into the thread's live-turn snapshot (T-U12),
// in one pipeline with the PUBLISH: a client that opens its socket mid-turn is
// greeted with that snapshot, so the two must not be able to disagree about
// what has already happened. Doing it here rather than at the ~20 publish sites
// in the runner is what makes that guarantee hold for events added later.
func (b *RedisBus) Publish(threadID string, evt app.ChatEvent) error {
	if threadID == "" {
		return fmt.Errorf("eventbus: empty threadID")
	}
	body, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("eventbus: marshal event: %w", err)
	}
	ctx := context.Background()
	pipe := b.rdb.Pipeline()
	// Snapshot first, PUBLISH second, and the order is load-bearing. Between
	// the two commands a reconnecting client can read a snapshot that already
	// counts an event its socket is about to receive — a duplicate, which the
	// snapshot's `last_event_at` lets the client drop. Publishing first would
	// invert that into an event delivered to nobody and missing from the
	// snapshot the next connection reads, which nothing can recover.
	recordLive(ctx, pipe, threadID, evt)
	pub := pipe.Publish(ctx, ChannelFor(threadID), body)
	if _, err := pipe.Exec(ctx); err != nil {
		// Exec reports the first command that failed. Only the PUBLISH is
		// worth failing the call over — a snapshot that did not get written
		// costs a reconnecting reader a few seconds of missing spinner, and
		// the turn itself is unaffected.
		if pubErr := pub.Err(); pubErr != nil {
			return fmt.Errorf("eventbus: publish: %w", pubErr)
		}
		logrus.WithError(err).WithField("thread_id", threadID).
			Warn("live-turn snapshot write failed")
	}
	return nil
}

// PublishReport publishes progress for a report job that has no thread.
//
// A separate method rather than Publish with a different key, because the two
// have different failure meanings: nobody listening to a thread is ordinary
// (a WhatsApp turn has no browser attached), and nobody listening to a report
// channel is the normal case too — a caller who polls instead of streaming.
// Both are non-errors, and neither should be able to be published to the
// other's namespace by passing the wrong id.
func (b *RedisBus) PublishReport(reportID string, evt app.ChatEvent) error {
	if reportID == "" {
		return fmt.Errorf("eventbus: empty reportID")
	}
	body, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("eventbus: marshal event: %w", err)
	}
	if err := b.rdb.Publish(context.Background(), ReportChannelFor(reportID), body).Err(); err != nil {
		return fmt.Errorf("eventbus: publish report: %w", err)
	}
	return nil
}

// NotifyDiscordReload publishes a reload signal so cmd/discord can re-open
// the gateway session for the named company.
func (b *RedisBus) NotifyDiscordReload(ctx context.Context, companyID string) error {
	if companyID == "" {
		return fmt.Errorf("eventbus: empty companyID")
	}
	if err := b.rdb.Publish(ctx, DiscordReloadChannel, companyID).Err(); err != nil {
		return fmt.Errorf("eventbus: publish discord reload: %w", err)
	}
	return nil
}

// PublishOutbound delivers a final assistant message to the per-tenant
// outbound channel. cmd/discord PSUBSCRIBEs the pattern and writes the
// message through that tenant's gateway session.
func (b *RedisBus) PublishOutbound(evt app.OutboundEvent) error {
	if evt.Channel == "" || evt.CompanyID == "" {
		return fmt.Errorf("eventbus: outbound requires channel and company_id")
	}
	body, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("eventbus: marshal outbound: %w", err)
	}
	chName := OutboundChannelFor(evt.Channel, evt.CompanyID)
	if err := b.rdb.Publish(context.Background(), chName, body).Err(); err != nil {
		return fmt.Errorf("eventbus: publish outbound: %w", err)
	}
	return nil
}
