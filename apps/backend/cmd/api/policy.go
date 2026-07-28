package main

import (
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/transport/http/middleware"
)

// apiPolicy is the access decision for every authenticated route in the API.
// It is the whole of T-04's step 1: `AdminOnly()` existed and gated nothing,
// so nine credential- and config-mutating routes were reachable by any member.
//
// The line the table draws is *who can change what the agent can reach or
// spend*, not "who can write":
//
//   - Anything holding or exercising a credential is admin. That is wider than
//     the ticket's list, which named PUT /connections/:id/dsn and DELETE
//     /connections/:id but not POST /connections — and a member who can add a
//     source can point one at any host they like, which is the same power the
//     ticket was closing off. `POST /connections/test` is admin for the same
//     reason: it opens an outbound connection to an attacker-chosen host:port
//     with no row written, so leaving it open would leave the interesting half
//     of the capability behind.
//   - Anything that spends the company's LLM budget on demand is admin:
//     regenerate-description and reindex-embeddings each fan out one API call
//     per table. test-rag is admin as the third tool on that same page rather
//     than for its cost, which is a single embedding call.
//   - Chat, threads, dashboards, usage reads and scheduled-task authoring stay
//     open to members. Those are the product; gating them would make "member"
//     a role with nothing to do. Deleting a scheduled task is admin, per the
//     ticket, because a task belongs to whoever created it and deletion is the
//     one operation that reaches across users.
//
// Every entry is keyed by the pattern gin registers, so a path that changes
// shape breaks the classification test rather than silently falling through to
// the deny branch in production.
var apiPolicy = middleware.RolePolicy{
	// Own profile and team management.
	"GET /api/users/me":      domain.RoleMember,
	"GET /api/users":         domain.RoleAdmin,
	"POST /api/users/invite": domain.RoleAdmin,
	"PATCH /api/users/:id":   domain.RoleAdmin,
	"DELETE /api/users/:id":  domain.RoleAdmin,

	// Data sources. Reads are open; everything that writes, tests or spends is
	// not.
	"GET /api/connections":                             domain.RoleMember,
	"POST /api/connections":                            domain.RoleAdmin,
	"PATCH /api/connections/:id":                       domain.RoleAdmin,
	"PUT /api/connections/:id/dsn":                     domain.RoleAdmin,
	"POST /api/connections/:id/default":                domain.RoleAdmin,
	"POST /api/connections/:id/regenerate-description": domain.RoleAdmin,
	"POST /api/connections/:id/reindex-embeddings":     domain.RoleAdmin,
	"POST /api/connections/:id/test-rag":               domain.RoleAdmin,
	"DELETE /api/connections/:id":                      domain.RoleAdmin,
	"POST /api/connections/test":                       domain.RoleAdmin,
	"POST /api/connections/:id/test":                   domain.RoleAdmin,

	// WhatsApp allowlist: adding a number grants a phone the company's agent.
	"GET /api/phones":           domain.RoleMember,
	"POST /api/phones":          domain.RoleAdmin,
	"DELETE /api/phones/:phone": domain.RoleAdmin,

	// Company settings.
	"GET /api/settings": domain.RoleMember,
	"PUT /api/settings": domain.RoleAdmin,

	// Report branding (T-R5). Read is admin rather than member, unlike the
	// other settings reads: this is the one panel where the *read* is the
	// interesting operation. GET returns nothing a member needs — no report
	// route consults branding on the member's behalf — while the preview it
	// feeds renders a full document, and every write beside it is admin. A
	// read a member cannot act on is a button that answers 403.
	"GET /api/reports/branding":       domain.RoleAdmin,
	"PUT /api/reports/branding":       domain.RoleAdmin,
	"POST /api/reports/branding/logo": domain.RoleAdmin,
	// The preview renders a fixed sample with caller-supplied branding: no
	// tenant data, but a full PDF render per call, which is a cost a member
	// should not be able to spend in a loop.
	"POST /api/reports/preview": domain.RoleAdmin,

	// Chat.
	"GET /api/threads":              domain.RoleMember,
	"POST /api/threads":             domain.RoleMember,
	"GET /api/threads/:id":          domain.RoleMember,
	"DELETE /api/threads/:id":       domain.RoleMember,
	"GET /api/threads/:id/messages": domain.RoleMember,
	"GET /api/threads/:id/stream":   domain.RoleMember,
	"POST /api/chat":                domain.RoleMember,

	// Saved dashboards and usage reporting.
	"GET /api/dashboards":               domain.RoleMember,
	"DELETE /api/dashboards/:id":        domain.RoleMember,
	"GET /api/usage/summary":            domain.RoleMember,
	"GET /api/usage/credits":            domain.RoleMember,
	"GET /api/usage/threads":            domain.RoleMember,
	"GET /api/usage/threads/:id":        domain.RoleMember,
	"GET /api/usage/threads/:id/events": domain.RoleMember,
	"GET /api/usage/by-channel":         domain.RoleMember,
	"GET /api/usage/by-user":            domain.RoleMember,

	// Model catalogue — read-only metadata.
	"GET /api/config/models": domain.RoleMember,

	// Agent action audit log (T-05). Admin rather than member for the same
	// reason the DSN routes are: every row carries the full SQL the agent ran,
	// so the log reads the shape of the tenant's warehouse to anyone who can
	// list it — a wider view than any single chat thread gives.
	"GET /api/audit/actions": domain.RoleAdmin,

	// API keys (T-13). Admin throughout, including the list: a key is a
	// credential that reaches the company's data from outside the dashboard,
	// so minting one is at least as privileged as rotating a DSN. The list is
	// admin too — it is the inventory of who can reach the tenant from
	// outside, and it is where a revoke starts.
	"GET /api/api-keys":        domain.RoleAdmin,
	"POST /api/api-keys":       domain.RoleAdmin,
	"DELETE /api/api-keys/:id": domain.RoleAdmin,
	// The scope vocabulary is static metadata, but it is only ever read by the
	// create form, which is admin-only. A member-readable route nobody can act
	// on is a wider surface for nothing.
	"GET /api/api-keys/scopes": domain.RoleAdmin,

	// Scheduled tasks.
	"GET /api/scheduled-tasks":                 domain.RoleMember,
	"POST /api/scheduled-tasks":                domain.RoleMember,
	"GET /api/scheduled-tasks/:id":             domain.RoleMember,
	"PATCH /api/scheduled-tasks/:id":           domain.RoleMember,
	"DELETE /api/scheduled-tasks/:id":          domain.RoleAdmin,
	"GET /api/scheduled-tasks/:id/runs":        domain.RoleMember,
	"GET /api/scheduled-tasks/:id/runs/:runID": domain.RoleMember,

	// Discord: the credential pair and the allowlist of who may talk to the
	// agent through it.
	"GET /api/discord":              domain.RoleMember,
	"PUT /api/discord":              domain.RoleAdmin,
	"DELETE /api/discord":           domain.RoleAdmin,
	"GET /api/discord/users":        domain.RoleMember,
	"POST /api/discord/users":       domain.RoleAdmin,
	"DELETE /api/discord/users/:id": domain.RoleAdmin,

	// Lark: same shape as Discord.
	"GET /api/lark":              domain.RoleMember,
	"PUT /api/lark":              domain.RoleAdmin,
	"DELETE /api/lark":           domain.RoleAdmin,
	"GET /api/lark/users":        domain.RoleMember,
	"POST /api/lark/users":       domain.RoleAdmin,
	"DELETE /api/lark/users/:id": domain.RoleAdmin,
}

// unpolicedPaths are the routes that legitimately sit outside the policy: they
// run before or without authentication, or they authenticate as something
// other than a dashboard session, so there is no role to check. The
// classification test uses this list to decide which routes it may skip, which
// keeps "not gated" an explicit decision rather than an omission.
var unpolicedPaths = map[string]bool{
	"/health":  true,
	"/ready":   true,
	"/metrics": true,

	// Signup, login, refresh, logout and the two invite routes: reachable
	// before a session exists, by definition.
	"/api/auth/signup":        true,
	"/api/auth/login":         true,
	"/api/auth/refresh":       true,
	"/api/auth/logout":        true,
	"/api/auth/invite":        true,
	"/api/auth/accept-invite": true,

	// Static metadata, no tenant data.
	"/api/meta/supported-databases": true,

	// The public API (T-13). Authenticated by an API key, which carries
	// scopes rather than a role — apiPolicy answers a question that does not
	// apply, and RequireRole never runs on this group. What gates a /v1 route
	// is middleware.RequireScope beside it; TestV1RoutesAreKeyAuthenticated
	// is what keeps this exemption from becoming a hole.
	//
	// The scope each one names is in the comment, and
	// TestEveryV1RouteNamesAScope proves the comment against the router: a
	// route added here without a RequireScope reaches every key its tenant has
	// ever minted, and that is the one failure this list cannot catch on its
	// own.
	"/v1/me": true, // none — identity, and the one route a key with no scopes must reach

	// T-A2's report surface.
	"/v1/reports/render":        true, // write:reports
	"/v1/reports":               true, // write:reports
	"/v1/reports/:id":           true, // read:documents
	"/v1/reports/:id/events":    true, // read:documents
	"/v1/documents":             true, // read:documents
	"/v1/documents/:id":         true, // read:documents
	"/v1/documents/:id/content": true, // read:documents

	// Inbound webhooks authenticate by provider signature, not by JWT.
	"/webhook/whatsapp":             true,
	"/webhook/discord/interactions": true,
	"/webhook/lark/events/:app_id":  true,

	// The Metabase reverse proxy forwards to Metabase's own auth.
	"/metabase/*path": true,
}
