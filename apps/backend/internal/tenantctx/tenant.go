// Package tenantctx defines context keys for propagating per-request tenant
// identity into the agent runtime. Used by HTTP middleware, the queue
// consumer, and tool implementations.
package tenantctx

import "context"

type companyKey struct{}
type userKey struct{}
type threadKey struct{}
type actorKey struct{}
type channelKey struct{}
type messageKey struct{}

// actor is the authority a turn runs under. Kinds and refs are plain strings
// rather than domain types because this package is a leaf: domain imports
// nothing from here, and it stays that way so tools, middleware and the queue
// consumer can all reach it.
type actor struct{ kind, ref string }

// WithCompanyID returns ctx annotated with the company ID. Tools resolve the
// per-tenant DB connection from this value.
func WithCompanyID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, companyKey{}, id)
}

// CompanyID extracts the company ID from ctx, or "" if unset.
func CompanyID(ctx context.Context) string {
	v, _ := ctx.Value(companyKey{}).(string)
	return v
}

// WithUserID returns ctx annotated with the dashboard user ID. Empty for
// WhatsApp-originated requests.
func WithUserID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, userKey{}, id)
}

// UserID extracts the user ID from ctx, or "" if unset.
func UserID(ctx context.Context) string {
	v, _ := ctx.Value(userKey{}).(string)
	return v
}

// WithThreadID returns ctx annotated with the conversation thread ID.
func WithThreadID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, threadKey{}, id)
}

// ThreadID extracts the conversation thread ID from ctx, or "" if unset.
func ThreadID(ctx context.Context) string {
	v, _ := ctx.Value(threadKey{}).(string)
	return v
}

// WithActor returns ctx annotated with who the turn runs on behalf of: a
// dashboard user, a cron schedule, a watcher, or an API key. The audit log
// (T-05) attributes every tool call from this, so a caller that sets no actor
// produces rows nobody can trace back — which is why the queue consumer sets
// it at the top of a turn beside the company and thread.
func WithActor(ctx context.Context, kind, ref string) context.Context {
	return context.WithValue(ctx, actorKey{}, actor{kind: kind, ref: ref})
}

// Actor extracts the turn's actor, or two empty strings if unset.
func Actor(ctx context.Context) (kind, ref string) {
	a, _ := ctx.Value(actorKey{}).(actor)
	return a.kind, a.ref
}

// WithChannel returns ctx annotated with the channel the turn arrived on.
func WithChannel(ctx context.Context, channel string) context.Context {
	return context.WithValue(ctx, channelKey{}, channel)
}

// Channel extracts the turn's channel, or "" if unset.
func Channel(ctx context.Context) string {
	v, _ := ctx.Value(channelKey{}).(string)
	return v
}

// WithMessageID returns ctx annotated with the user message that started the
// turn. It is what ties a row in the audit log to the question that caused it.
func WithMessageID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, messageKey{}, id)
}

// MessageID extracts the triggering user message ID, or "" if unset.
func MessageID(ctx context.Context) string {
	v, _ := ctx.Value(messageKey{}).(string)
	return v
}
