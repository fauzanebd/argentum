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
	Channel    string `json:"channel"` // domain.Channel string ("discord")
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
	JobID        string         `json:"job_id"`
	ThreadID     string         `json:"thread_id"`
	Type         string         `json:"type"` // state | started | delta | thinking | tool_call | tool_result | final | error | render_progress
	Content      string         `json:"content,omitempty"`
	ThinkingStep string         `json:"thinking_step,omitempty"`
	ToolCall     *ToolCallEvent `json:"tool_call,omitempty"`
	Error        string         `json:"error,omitempty"`
	// Live is set on — and only on — a `state` event: the turn already in
	// flight when this socket opened. Every other event describes something
	// that just happened; this one describes what has happened so far.
	Live *LiveTurn `json:"live,omitempty"`
	// Progress is 0..1 on a `render_progress` event and unset on every other
	// type (T-V3). A four-minute video needs a number on the screen, and the
	// dashboard and `/v1` both already read this struct — a second event
	// pipeline for one float is how two vocabularies start.
	Progress  float64                `json:"progress,omitempty"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	Timestamp time.Time              `json:"timestamp"`
}

// LiveTurn is everything a turn still running has produced so far, as the
// first frame of a socket that opened in the middle of it (T-U12).
//
// A WebSocket carries what happens *next*, which is enough only for a reader
// who was watching from the start. Leaving one conversation for another closes
// its socket, and the one that reopens on the way back used to say nothing at
// all until the agent's next token — so a turn thinking through a slow tool
// looked, for as long as that took, like a thread with no answer coming. This
// is the state that gap needed: the same three signals the streaming bubble
// already renders — that work is happening, since when, and what it is doing.
//
// The event stream stays the source of truth. This is a catch-up frame written
// alongside every publish and dropped the moment the turn ends, never a second
// place a turn's result is stored.
type LiveTurn struct {
	JobID     string    `json:"job_id"`
	StartedAt time.Time `json:"started_at"`
	// LastEventAt is the timestamp of the newest event folded in here. It is
	// how the client recognises the one event a reconnect can see twice: the
	// socket subscribes before this snapshot is read, so anything published in
	// between arrives on the wire *and* is already counted here.
	LastEventAt   time.Time       `json:"last_event_at"`
	Content       string          `json:"content,omitempty"`
	ThinkingSteps []string        `json:"thinking_steps,omitempty"`
	ToolCalls     []ToolCallEvent `json:"tool_calls,omitempty"`
	Iteration     int             `json:"iteration,omitempty"`
	MaxIterations int             `json:"max_iterations,omitempty"`
}

// EventState is the catch-up frame described on LiveTurn, sent once per
// connection and only when a turn is actually running.
const EventState = "state"

// EventRenderProgress is emitted while a video renders. It is capped at one a
// second by the render client's own poll interval, and it never carries 1.0:
// completion is `final` for a turn and the terminal `report` event for a job,
// both of which arrive when the file exists rather than when the frames stop.
const EventRenderProgress = "render_progress"
