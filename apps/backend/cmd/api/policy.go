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

	// The agent roster (T-S1). Reads are member because T-S3 puts this list in
	// the chat picker, and an agent nobody but an admin can see is a settings
	// page rather than a product. Every write is admin on the same line drawn
	// for connections just above: an agent's tool and source allowlist is
	// "what the agent can reach", and editing a persona rewrites what every
	// member of the company gets answered by.
	"GET /api/agents":             domain.RoleMember,
	"GET /api/agents/:id":         domain.RoleMember,
	"POST /api/agents":            domain.RoleAdmin,
	"PUT /api/agents/:id":         domain.RoleAdmin,
	"DELETE /api/agents/:id":      domain.RoleAdmin,
	"PUT /api/agents/:id/default": domain.RoleAdmin,
	// "Generate with AI" (T-B4) sits on the same admin row as the agent writes
	// it feeds, for both of that row's reasons: it spends the company's credit,
	// and what it returns becomes prompt text every member gets answered by.
	// A read-shaped verb would have been the mistake here — nothing is stored,
	// but a member who could call it could bill the workspace in a loop.
	"POST /api/agents/generate": domain.RoleAdmin,

	// Channel bindings (T-S4). Admin on read too, unlike the roster above: a
	// binding is routing configuration rather than a choice a member makes, and
	// the rows are the identifiers of the company's own Discord channels and
	// Lark chats.
	"GET /api/agent-bindings":        domain.RoleAdmin,
	"POST /api/agent-bindings":       domain.RoleAdmin,
	"DELETE /api/agent-bindings/:id": domain.RoleAdmin,

	// The tenant's MCP servers (T-M1). **Admin on read too**, which is stricter
	// than the roster above and matches the connections rows instead: a server
	// is a bearer credential for a system we do not own plus an address we will
	// open a connection to, which is a DSN-class object. Approving one of its
	// tools decides what an agent may do to that system, so the review route is
	// on the same line by construction.
	// Outbound webhook subscriptions (T-15). Admin on every route including the
	// reads, like MCP servers: the list is a map of where a workspace's events
	// go, and each row is an egress destination we POST to unattended.
	"GET /api/webhooks":        domain.RoleAdmin,
	"POST /api/webhooks":       domain.RoleAdmin,
	"PUT /api/webhooks/:id":    domain.RoleAdmin,
	"DELETE /api/webhooks/:id": domain.RoleAdmin,

	"GET /api/mcp-servers":                   domain.RoleAdmin,
	"POST /api/mcp-servers":                  domain.RoleAdmin,
	"GET /api/mcp-servers/:id":               domain.RoleAdmin,
	"PUT /api/mcp-servers/:id":               domain.RoleAdmin,
	"DELETE /api/mcp-servers/:id":            domain.RoleAdmin,
	"POST /api/mcp-servers/:id/refresh":      domain.RoleAdmin,
	"PUT /api/mcp-servers/:id/tools/:toolId": domain.RoleAdmin,

	// Metric registry (T-06). Reads are open to members — a member asking a
	// question gets the same authoritative number — and writes plus Test are
	// admin, because defining one is a privileged act and Test runs tenant SQL.
	"GET /api/metrics":        domain.RoleMember,
	"GET /api/metrics/:id":    domain.RoleMember,
	"POST /api/metrics":       domain.RoleAdmin,
	"PUT /api/metrics/:id":    domain.RoleAdmin,
	"DELETE /api/metrics/:id": domain.RoleAdmin,
	"POST /api/metrics/test":  domain.RoleAdmin,

	// Watchers (T-08). Same split and same reasoning as metrics: reads are
	// member because a watcher is company configuration and its event history is
	// how anyone confirms it is working, while writes, the dry-run, and enabling
	// are admin — a watcher runs tenant SQL unattended and delivers to the
	// company's channels, and the dry-run runs that SQL directly like the metric
	// Test button does.
	"GET /api/watchers":              domain.RoleMember,
	"GET /api/watchers/:id":          domain.RoleMember,
	"GET /api/watchers/:id/events":   domain.RoleMember,
	"POST /api/watchers":             domain.RoleAdmin,
	"PUT /api/watchers/:id":          domain.RoleAdmin,
	"DELETE /api/watchers/:id":       domain.RoleAdmin,
	"POST /api/watchers/:id/dry-run": domain.RoleAdmin,

	// The action framework's human side (T-11). Reads are member because the
	// approval card renders in the chat stream every member of a thread sees, so
	// the pending list has to be member-visible. Approve and reject are member in
	// this coarse table and refined per kind in the handler: a company_actions
	// row's allowed_roles names who may decide that kind, and a caller outside it
	// gets a 403 the card shows as read-only. Member here is the floor, not the
	// grant — the per-kind check can only narrow it.
	"GET /api/actions/pending":      domain.RoleMember,
	"GET /api/actions/:id":          domain.RoleMember,
	"POST /api/actions/:id/approve": domain.RoleMember,
	"POST /api/actions/:id/reject":  domain.RoleMember,
	// Enabling a kind and setting whether it needs approval is admin: it decides
	// what the agent may set in motion for the whole company, the same line the
	// connections and watchers rows draw. Turning approval off is reachable only
	// here, which is why the off switch is admin-gated rather than a member field.
	"GET /api/actions/config":       domain.RoleAdmin,
	"PUT /api/actions/config/:kind": domain.RoleAdmin,

	// Registered HTTP endpoints (T-12b): the targets an http_action may call.
	// Admin throughout, including the list, for the reason the MCP rows give — a
	// row is an egress destination plus a credential, a DSN-class object, so who
	// can see and change them is the connections line, not the actions line.
	"GET /api/http-endpoints":        domain.RoleAdmin,
	"POST /api/http-endpoints":       domain.RoleAdmin,
	"DELETE /api/http-endpoints/:id": domain.RoleAdmin,

	// WhatsApp allowlist: adding a number grants a phone the company's agent.
	"GET /api/phones":           domain.RoleMember,
	"POST /api/phones":          domain.RoleAdmin,
	"DELETE /api/phones/:phone": domain.RoleAdmin,

	// Company settings.
	"GET /api/settings": domain.RoleMember,
	"PUT /api/settings": domain.RoleAdmin,

	// The business profile (T-B1). Read is member because it is a description
	// of the company every member already works for, and because the agents
	// page next to it is member-readable for the same reason. Write is admin:
	// this text joins the system prompt of every agent on every channel, so a
	// member who could edit it could rewrite what all four agents believe the
	// business is.
	"GET /api/company/profile": domain.RoleMember,
	"PUT /api/company/profile": domain.RoleAdmin,

	// The inferred draft (T-B2). Read is member on the same reasoning as the
	// profile above — it is a description of the company, and one nobody has
	// agreed to yet. Apply is admin because it writes that profile, and a route
	// that reaches the system prompt of every agent is admin however the text
	// got there: "a machine wrote it" is not a smaller permission than "an admin
	// typed it".
	"GET /api/company/profile/suggestion":        domain.RoleMember,
	"POST /api/company/profile/suggestion/apply": domain.RoleAdmin,
	"POST /api/connections/:id/rescan":           domain.RoleAdmin,

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
	// The `/v1` failure list (T-A5). Admin for the same reason the audit log is:
	// it names every route every integration called and how each call failed,
	// across the whole company. The ticket's acceptance asks for admin-only in
	// as many words, and this table is where that is true.
	"GET /api/api-keys/errors": domain.RoleAdmin,

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
	"/v1/me":     true, // none — identity, and the one route a key with no scopes must reach
	"/v1/agents": true, // none — the roster, so `agent_id` is fillable from the API (T-S5)
	"/v1/usage":  true, // read:usage (T-A5)

	// T-A4's published contract. The only `/v1` route with no credential at
	// all: an integrator reads the spec before they have a key.
	"/v1/openapi.json": true, // none — public and keyless

	// T-A2's report surface.
	"/v1/reports/render":        true, // write:reports
	"/v1/reports":               true, // write:reports
	"/v1/reports/:id":           true, // read:documents
	"/v1/reports/:id/events":    true, // read:documents
	"/v1/documents":             true, // read:documents
	"/v1/documents/:id":         true, // read:documents
	"/v1/documents/:id/content": true, // read:documents

	// T-A3's chat surface. The split is write:chat to spend, read:threads to
	// read — and DELETE is a write, because destroying a conversation is not
	// something a read-only key should be able to do.
	"/v1/chat":                 true, // write:chat
	"/v1/threads":              true, // read:threads
	"/v1/threads/:id":          true, // read:threads (GET), write:chat (DELETE)
	"/v1/threads/:id/messages": true, // read:threads
	"/v1/threads/:id/events":   true, // read:threads

	// Inbound webhooks authenticate by provider signature, not by JWT.
	"/webhook/whatsapp":             true,
	"/webhook/discord/interactions": true,
	"/webhook/lark/events/:app_id":  true,

	// The Metabase reverse proxy forwards to Metabase's own auth.
	"/metabase/*path": true,
}
