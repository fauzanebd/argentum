package lark

import "context"

// Provider is the worker-facing send interface. ChatRunner calls Reply
// after the agent finishes generating a final assistant message.
type Provider interface {
	Reply(ctx context.Context, companyID, messageID, content string) error
}
