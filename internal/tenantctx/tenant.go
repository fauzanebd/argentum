// Package tenantctx defines context keys for propagating per-request tenant
// identity into the agent runtime. Used by HTTP middleware, the queue
// consumer, and tool implementations.
package tenantctx

import "context"

type companyKey struct{}
type userKey struct{}
type threadKey struct{}

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
