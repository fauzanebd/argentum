package slack

import "context"

// Provider is the worker-facing send interface. ChatRunner calls Reply
// after the agent finishes generating a final assistant message; watcher
// delivery calls Send, because a breach has no inbound message to reply to.
type Provider interface {
	Reply(ctx context.Context, companyID, channelID, threadTS, content string) error
	Send(ctx context.Context, companyID, channelID, content string) error
}
