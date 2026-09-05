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
type requestKey struct{}

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

// WithRequestID returns ctx annotated with the HTTP request that started this
// work (T-A1). A support conversation opens with the request id the caller
// read off `X-Request-Id`, so that id has to reach the rows the request
// produced — including the audit rows a worker writes minutes later, which is
// why it travels in the queue payload rather than only in the API process.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestKey{}, id)
}

// RequestID extracts the originating request id, or "" if unset. Empty is the
// ordinary case for anything that did not start with an HTTP call: a cron
// tick, a watcher, a channel webhook.
func RequestID(ctx context.Context) string {
	v, _ := ctx.Value(requestKey{}).(string)
	return v
}

type replyTargetKey struct{}

// ReplyTarget is where a turn's reply goes: the channel it arrived on and the
// refs that channel needs to send something back. Plain strings for the
// reason `actor` is — this package is a leaf.
//
// It exists so that work a turn hands off can still answer the person who
// asked (T-G6, finding 6). A render that finishes minutes after its turn has
// no `ChatRunPayload` to read the phone number or channel id from, and the
// thread row cannot supply every one of them — a Discord thread is keyed by
// user, not channel. So the queue consumer puts the target on the turn's
// context, and a tool that enqueues a job copies it into the job.
type ReplyTarget struct {
	Channel          string `json:"channel"`
	PhoneNumber      string `json:"phone_number,omitempty"`
	DiscordChannelID string `json:"discord_channel_id,omitempty"`
	DiscordUserID    string `json:"discord_user_id,omitempty"`
	LarkChatID       string `json:"lark_chat_id,omitempty"`
	LarkMessageID    string `json:"lark_message_id,omitempty"`
	SlackChannelID   string `json:"slack_channel_id,omitempty"`
	SlackThreadTS    string `json:"slack_thread_ts,omitempty"`
}

// WithReplyTarget returns ctx annotated with where this turn's reply goes.
func WithReplyTarget(ctx context.Context, t ReplyTarget) context.Context {
	return context.WithValue(ctx, replyTargetKey{}, t)
}

// ReplyTargetFrom extracts the turn's reply target. ok is false when none was
// set, which is every caller that is not the queue consumer — an HTTP handler
// rendering inline, a test — and means "there is nowhere but the thread".
func ReplyTargetFrom(ctx context.Context) (t ReplyTarget, ok bool) {
	t, ok = ctx.Value(replyTargetKey{}).(ReplyTarget)
	return t, ok
}
