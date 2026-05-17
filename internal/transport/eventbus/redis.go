// Package eventbus contains the Redis-backed implementation of
// app.EventBus. The worker publishes chat events here; API replicas
// subscribe to forward them down WebSocket connections.
package eventbus

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/redis/go-redis/v9"

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
func (b *RedisBus) Publish(threadID string, evt app.ChatEvent) error {
	if threadID == "" {
		return fmt.Errorf("eventbus: empty threadID")
	}
	body, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("eventbus: marshal event: %w", err)
	}
	if err := b.rdb.Publish(context.Background(), ChannelFor(threadID), body).Err(); err != nil {
		return fmt.Errorf("eventbus: publish: %w", err)
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
