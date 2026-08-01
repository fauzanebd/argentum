package lark

import "context"

// Provider is the worker-facing send interface. ChatRunner calls Reply
// after the agent finishes generating a final assistant message; a watcher
// (T-08) calls Send, because a proactive alert has no inbound message to reply
// to — it posts fresh into a chat.
type Provider interface {
	Reply(ctx context.Context, companyID, messageID, content string) error
	Send(ctx context.Context, companyID, chatID, content string) error
}
