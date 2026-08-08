package slack

import "context"

// Provider is the worker-facing send interface. ChatRunner calls Reply
// after the agent finishes generating a final assistant message.
type Provider interface {
	Reply(ctx context.Context, companyID, channelID, threadTS, content string) error
}
