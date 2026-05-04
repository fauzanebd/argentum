// Package queue defines the asynq task contract for background work.
//
// Today the only task is `chat:run`, which processes one user message
// through the agent. The payload is JSON-marshalled into the asynq task body.
package queue

import "github.com/fauzanebd/argentum/internal/domain"

// Task type constants. These are the values asynq uses to dispatch tasks
// to handlers; keep them stable across deploys.
const (
	TypeChatRun = "chat:run"
)

// ChatRunPayload carries everything the worker needs to process one chat
// turn. UserMsgID lets the worker re-derive the original message row in
// case retries fire after the user message has been persisted but before
// the assistant reply was written.
type ChatRunPayload struct {
	CompanyID       string         `json:"company_id"`
	ThreadID        string         `json:"thread_id"`
	UserID          string         `json:"user_id,omitempty"`
	PhoneNumber     string         `json:"phone_number,omitempty"`
	Channel         domain.Channel `json:"channel"`
	Message         string         `json:"message"`
	UserMsgID       string         `json:"user_msg_id"`
	CompanyName     string         `json:"company_name,omitempty"`
	DefaultCurrency string         `json:"default_currency,omitempty"` // ISO 4217
}
