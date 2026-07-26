package app

import "time"

// EventBus is the minimal pub/sub abstraction the chat pipeline uses to
// stream agent results to subscribers. The worker publishes events to the
// bus; the API process subscribes (today via Redis pub/sub in
// internal/transport/eventbus) and forwards frames to WebSocket clients.
//
// PublishOutbound fans the final assistant message out to a side channel
// keyed by tenant + delivery channel (e.g. discord). cmd/discord subscribes
// and writes the message via its own gateway session. Worker uses this for
// channels that don't share an outbound provider with the worker process.
type EventBus interface {
	Publish(threadID string, evt ChatEvent) error
	PublishOutbound(evt OutboundEvent) error
}

// OutboundEvent is a final assistant message destined for a channel the
// worker process can't deliver directly (e.g. Discord, where each tenant
// has its own gateway session held by cmd/discord).
type OutboundEvent struct {
	Channel    string `json:"channel"`     // domain.Channel string ("discord")
	CompanyID  string `json:"company_id"`
	ChannelRef string `json:"channel_ref"` // discord_channel_id
	UserRef    string `json:"user_ref,omitempty"`
	Content    string `json:"content"`
}

// ToolCallEvent carries live tool-execution metadata for the dashboard.
type ToolCallEvent struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments,omitempty"`
	Result    map[string]interface{} `json:"result,omitempty"`
}

// ChatEvent is one streaming update for a chat run. The dashboard renders
// these directly. WhatsApp delivery is handled by the worker — no fan-out
// indirection required anymore.
type ChatEvent struct {
	JobID        string                 `json:"job_id"`
	ThreadID     string                 `json:"thread_id"`
	Type         string                 `json:"type"` // started | delta | thinking | tool_call | tool_result | final | error
	Content      string                 `json:"content,omitempty"`
	ThinkingStep string                 `json:"thinking_step,omitempty"`
	ToolCall     *ToolCallEvent         `json:"tool_call,omitempty"`
	Error        string                 `json:"error,omitempty"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
	Timestamp    time.Time              `json:"timestamp"`
}
