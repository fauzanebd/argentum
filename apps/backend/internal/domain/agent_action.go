package domain

import (
	"context"
	"encoding/json"
	"time"
)

// ActorKind is the authority a tool call ran under. It answers "who is
// accountable for this", which is not the same question as "who was in the
// room": a scheduled report runs with nobody watching, and a T-13 API key
// belongs to an integration rather than to a person.
type ActorKind string

const (
	ActorKindUser     ActorKind = "user"
	ActorKindSchedule ActorKind = "schedule"
	ActorKindWatcher  ActorKind = "watcher" // T-08
	ActorKindAPIKey   ActorKind = "api_key" // T-13
)

// Valid reports whether k is a kind this system issues.
func (k ActorKind) Valid() bool {
	switch k {
	case ActorKindUser, ActorKindSchedule, ActorKindWatcher, ActorKindAPIKey:
		return true
	}
	return false
}

// ActionStatus is how a tool call ended.
type ActionStatus string

const (
	ActionStatusOK    ActionStatus = "ok"
	ActionStatusError ActionStatus = "error"
	// ActionStatusBlocked means the call never ran: the turn's budget was
	// spent (T-16), or a guardrail replaced the reply the turn produced.
	ActionStatusBlocked ActionStatus = "blocked"
	// ActionStatusTruncated means it ran and the result did not fit — the
	// model saw less than the tool retrieved, which changes how a later
	// answer should be read.
	ActionStatusTruncated ActionStatus = "truncated"
)

// AgentAction is one immutable record of something the agent did.
//
// ArgsRedacted keeps the full SQL text — that is the point of the log — and
// drops anything credential-shaped. ArgsHash is taken over the arguments as
// the tool received them, before redaction, so identical calls collide even
// when the stored form does not show why.
type AgentAction struct {
	ID        string    `json:"id"`
	CompanyID string    `json:"company_id"`
	ThreadID  string    `json:"thread_id,omitempty"`
	MessageID string    `json:"message_id,omitempty"`
	ActorKind ActorKind `json:"actor_kind"`
	ActorRef  string    `json:"actor_ref,omitempty"`
	Channel   Channel   `json:"channel,omitempty"`
	ToolName  string    `json:"tool_name"`
	SourceID  string    `json:"source_id,omitempty"`
	// ArgsRedacted is json.RawMessage rather than []byte so the audit endpoint
	// returns the object itself; encoding/json renders a []byte as base64, and
	// a log whose arguments have to be decoded before they can be read is a
	// log nobody reads.
	ArgsRedacted json.RawMessage `json:"args_redacted"`
	ArgsHash     string          `json:"args_hash"`
	ResultStatus ActionStatus    `json:"result_status"`
	ErrorText    string          `json:"error_text,omitempty"`
	// RowsReturned is nil for tools that do not return rows at all, which is a
	// different fact from a query that matched zero.
	RowsReturned *int      `json:"rows_returned,omitempty"`
	DurationMS   int       `json:"duration_ms"`
	CreatedAt    time.Time `json:"created_at"`
}

// AgentActionFilter narrows an audit read. Zero values mean "no filter" except
// for the window, which the caller always supplies.
type AgentActionFilter struct {
	From     time.Time
	To       time.Time
	ThreadID string
	Tool     string
	Limit    int
	Offset   int
}

// AgentActionRepository is the persistence contract for the audit log.
//
// There is deliberately no Update and no Delete. An audit log that can be
// edited by the code it audits is a log nobody can rely on, so the capability
// does not exist at the repository boundary rather than being merely unused —
// retention, when it is needed, belongs in a scheduled job with its own
// authority, not in the agent's own call path.
type AgentActionRepository interface {
	Create(ctx context.Context, a *AgentAction) error
	ListByCompany(ctx context.Context, companyID string, f AgentActionFilter) ([]*AgentAction, error)
}
