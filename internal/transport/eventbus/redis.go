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

// ChannelFor returns the Redis channel name for a given thread ID.
func ChannelFor(threadID string) string {
	return ChannelPrefix + threadID
}

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
