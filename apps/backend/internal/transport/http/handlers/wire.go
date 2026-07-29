package handlers

import "github.com/fauzanebd/argentum/internal/app"

// Wire types for the dashboard's `/api` surface that are not domain entities
// (T-02b).
//
// This file exists because `packages/api-types` is generated from Go, and a
// response assembled as a `gin.H` generates nothing — the dashboard was
// hand-writing a TypeScript interface for a shape no Go declaration described,
// which is the drift this ticket is about. Anything the dashboard needs a type
// for and the domain does not already provide belongs here.
//
// Most `/api` responses are still `gin.H` envelopes around domain values
// (`{"threads": […]}`), and those stay as they are: the elements are generated
// from `internal/domain`, and turning forty envelopes into forty structs is a
// wire-shape change per route rather than a type-generation ticket. What is
// here is the case where the *element itself* had no Go type.
//
// `/v1` is not in scope for this file. Its types are generated from
// `apps/backend/openapi/v1.yaml` (T-A4) — a published contract is authored,
// not derived.

// SendMessageResponse is the body of `POST /api/chat` — the acknowledgement
// that a turn was queued, not the answer. The answer arrives over the
// WebSocket as a stream of app.ChatEvent.
type SendMessageResponse struct {
	// TaskID is the asynq task. It is returned for support and for tests;
	// nothing in the dashboard routes on it.
	TaskID   string `json:"task_id"`
	ThreadID string `json:"thread_id"`
	// IsNewThread tells the dashboard to add a row to the thread list rather
	// than move an existing one to the top.
	IsNewThread bool   `json:"is_new_thread"`
	UserMsgID   string `json:"user_msg_id"`
	// BudgetWarning is present only when the turn ran *and* the tenant is near
	// the end of their credit. Its absence is the ordinary case and is the
	// signal — a client must not read `verdict === "ok"`, because in the
	// ordinary case there is no object to read it from.
	BudgetWarning *app.BudgetState `json:"budget_warning,omitempty"`
}
