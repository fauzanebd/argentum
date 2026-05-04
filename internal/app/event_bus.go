package app

import "time"

// EventBus is the minimal pub/sub abstraction the chat pipeline uses to
// stream agent results to subscribers. The worker publishes events to the
// bus; the API process subscribes (today via Redis pub/sub in
// internal/transport/eventbus) and forwards frames to WebSocket clients.
type EventBus interface {
	Publish(threadID string, evt ChatEvent) error
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
