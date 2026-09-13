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

	// Capabilities (T-Z1). Reading your own is a member's: the dashboard has to
	// know whether to render a control enabled, and the route names only what
	// the caller already holds. Reading anybody else's, granting and revoking
	// are an admin's, on the line the role change above draws — each decides
	// what a colleague may do. An admin may grant themselves, on purpose
	// (roadmap 12, decision 4): this is a boundary against accident rather than
	// against a determined admin, and saying so is what keeps it honest.
	"GET /api/users/me/capabilities":                 domain.RoleMember,
	"GET /api/users/:id/capabilities":                domain.RoleAdmin,
	"PUT /api/users/:id/capabilities/:capability":    domain.RoleAdmin,
	"DELETE /api/users/:id/capabilities/:capability": domain.RoleAdmin,

	// Resource access (T-Z2). Admin on every route, the reads included: the view
	// is the list of who can reach an agent or a dashboard, which a member has no
	// use for and an admin needs in order to change it. None of these routes
	// opens the resource to the caller — they answer who may reach an object,
	// never what is in it — so an admin manages a restricted dashboard they
	// cannot themselves open, which is decision 4 working as written.
	//
	// The list is the same read for a whole kind (T-Z7): the matrix Settings →
	// Team draws both of its directions from, and admin for the same reason.
	"GET /api/access/:kind":                       domain.RoleAdmin,
	"GET /api/access/:kind/:id":                   domain.RoleAdmin,
	"PUT /api/access/:kind/:id/mode":              domain.RoleAdmin,
	"PUT /api/access/:kind/:id/grants/:userID":    domain.RoleAdmin,
	"DELETE /api/access/:kind/:id/grants/:userID": domain.RoleAdmin,
	"GET /api/users/:id/grants":                   domain.RoleAdmin,

	// Data sources. Reads are open; everything that writes, tests or spends is
	// not.
	"GET /api/connections":         domain.RoleMember,
	"POST /api/connections":        domain.RoleAdmin,
	"PATCH /api/connections/:id":   domain.RoleAdmin,
	"PUT /api/connections/:id/dsn": domain.RoleAdmin,
	// The table and column allowlist (T-H12). Admin under the line this table
	// already draws — it changes what the agent can reach — and it is the one
	// route here whose *loosening* is the dangerous direction rather than its
	// use.
	"PUT /api/connections/:id/allowlist": domain.RoleAdmin,
	// Admin for the same reason as the allowlist: this is tenant-supplied SQL
	// that the product will run on a schedule against their database, and the
	// thresholds decide whether every answer carries a caveat.
	"PUT /api/connections/:id/freshness":               domain.RoleAdmin,
	"POST /api/connections/:id/freshness/test":         domain.RoleAdmin,
	"POST /api/connections/:id/default":                domain.RoleAdmin,
	"POST /api/connections/:id/regenerate-description": domain.RoleAdmin,
	"POST /api/connections/:id/reindex-embeddings":     domain.RoleAdmin,
	"POST /api/connections/:id/test-rag":               domain.RoleAdmin,
	"DELETE /api/connections/:id":                      domain.RoleAdmin,
	"POST /api/connections/test":                       domain.RoleAdmin,
	"POST /api/connections/:id/test":                   domain.RoleAdmin,
	// Which stored connections no longer decrypt under this deployment's
	// ARGENTUM_DSN_KEY. Admin rather than member, on the line drawn for the
	// writes above: it names credentials that need re-registering, which is a
	// piece of operational state a member can neither act on nor should have to
	// read.
	"GET /api/connections/key-health": domain.RoleAdmin,

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
	// T-Z8: clearing a channel to reach a restricted agent is an admin's
	// decision, and the audit row names the admin who made it.
	"PUT /api/agent-bindings/:id/acknowledgement": domain.RoleAdmin,

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

	// Skills (T-K1). Admin for the writes for the obvious reason and admin for
	// the *reads* for a less obvious one: a body is text that reaches the model
	// as this product's own instruction, so the list of them is the list of
	// things every agent in the workspace is being told to do.
	"GET /api/skills":            domain.RoleAdmin,
	"POST /api/skills":           domain.RoleAdmin,
	"POST /api/skills/preview":   domain.RoleAdmin,
	"POST /api/skills/draft":     domain.RoleAdmin,
	"GET /api/skills/:id":        domain.RoleAdmin,
	"PUT /api/skills/:id":        domain.RoleAdmin,
	"DELETE /api/skills/:id":     domain.RoleAdmin,
	"GET /api/agents/:id/skills": domain.RoleAdmin,
	"PUT /api/agents/:id/skills": domain.RoleAdmin,

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

	// Retention, erasure and export (T-H6). All three are admin, and the
	// export is the one worth arguing for: it is not a mutation, so the line
	// drawn above would put it with the member reads. It is admin because it
	// returns every conversation every member of the company has ever had in
	// one response — a member can already read their own threads, and this is
	// the only route that hands them everybody else's.
	"GET /api/company/data/export":   domain.RoleAdmin,
	"GET /api/company/data/erasures": domain.RoleAdmin,
	"DELETE /api/company/data":       domain.RoleAdmin,

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
	// The picture library (T-G12). Uploading and deleting are company-level
	// acts for branding's reason — a photograph here leaves the building on a
	// public post — while listing and reading are not, because a member
	// composing a post has to see what is available.
	"POST /api/post-images":       domain.RoleAdmin,
	"DELETE /api/post-images/:id": domain.RoleAdmin,
	// Reading is a member's, because a member composing a post has to see
	// what is available and a library nobody can look at is a library nobody
	// uses. The content route is authenticated and company-scoped, so an id
	// alone is not a credential.
	"GET /api/post-images":             domain.RoleMember,
	"GET /api/post-images/:id/content": domain.RoleMember,
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
	// The room (T-N2). Member on all three, matching the thread routes above
	// rather than the admin-only `/api/agent-bindings`: a channel binding is
	// routing configuration for a company's shared rooms, whereas these three
	// act on one conversation the caller already has. Every one of them is
	// tenant-checked inside ThreadParticipantService, so a member cannot reach
	// another company's thread by holding its uuid.
	"GET /api/threads/:id/participants":             domain.RoleMember,
	"POST /api/threads/:id/participants":            domain.RoleMember,
	"DELETE /api/threads/:id/participants/:agentID": domain.RoleMember,
	"GET /api/threads/:id/stream":                   domain.RoleMember,
	"POST /api/chat":                                domain.RoleMember,

	// Answer feedback (T-Q2). Rating is member — deliberately the most open
	// write in this table — because whoever read the answer is the only person
	// who knows whether it was right, and a button behind an admin gate
	// collects verdicts from the people least likely to be reading the day's
	// chat. The blast radius is one row saying what one person thought.
	//
	// The aggregate reads are admin, on the audit log's line rather than the
	// chat's: the tuning list quotes the questions that were answered badly
	// across every thread in the company, which is a wider view than any one
	// conversation gives — the same argument that puts GET /api/audit/actions
	// on the admin side. The per-message read stays member so the UI can show
	// somebody their own rating back.
	"POST /api/messages/:id/feedback": domain.RoleMember,
	"GET /api/messages/:id/feedback":  domain.RoleMember,
	"GET /api/feedback":               domain.RoleAdmin,
	"GET /api/feedback/summary":       domain.RoleAdmin,
	// Metric coverage (T-F4). Admin beside the two above and on their line: it
	// is the workspace's aggregate quality data, and the list underneath it
	// quotes the questions the company's people have been asking.
	"GET /api/quality/metric-coverage": domain.RoleAdmin,
	// Derived figures (T-W3), on the same line: aggregate quality data for the
	// workspace, and nothing on it a member could act on.
	"GET /api/quality/derived-figures": domain.RoleAdmin,

	// Next-step chips (T-U13), split on the same line and for the same reasons.
	// The pick is member because the person clicking is the person reading, and
	// the row it writes says which of three buttons they pressed. The rate is
	// admin because it is a company-wide number about whether a feature is worth
	// its cost, which is a question for whoever pays the bill.
	"POST /api/messages/:id/suggestion-picked": domain.RoleMember,
	"GET /api/suggestions/summary":             domain.RoleAdmin,

	// Native dashboards (T-D10). Reads are member: opening a dashboard is what a
	// member is here to do, and the numbers are the company's own — the same
	// read=member split the metric registry makes. Deleting is admin, which the
	// Metabase rows below do not require, and the difference is deliberate: a
	// native dashboard is a definition this product executes on a schedule
	// somebody's Monday depends on, where a saved_dashboards row is a link to an
	// object that still exists in Metabase after the row is gone.
	"GET /api/dashboards":          domain.RoleMember,
	"GET /api/dashboards/:id":      domain.RoleMember,
	"GET /api/dashboards/:id/data": domain.RoleMember,
	"DELETE /api/dashboards/:id":   domain.RoleAdmin,

	// Dashboard share links (T-D13). Admin on all three, including the read,
	// and matching the report player's shares one line above rather than the
	// dashboards' own RoleMember two lines above. Minting one publishes live
	// access to a customer's warehouse to anyone holding a URL; listing them
	// enumerates every such link that exists. Neither is a thing a member
	// should do without an admin knowing, and revocation sits beside them so
	// the person who can create can also take back.
	"POST /api/dashboards/:id/shares":            domain.RoleAdmin,
	"GET /api/dashboards/:id/shares":             domain.RoleAdmin,
	"DELETE /api/dashboards/:id/shares/:shareID": domain.RoleAdmin,

	// The Metabase-backed dashboards (006), moved off /api/dashboards in T-D10
	// and deleted with the rest of the Metabase surface in T-D15.

	// Usage reporting.
	"GET /api/usage/summary":            domain.RoleMember,
	"GET /api/usage/credits":            domain.RoleMember,
	"GET /api/usage/threads":            domain.RoleMember,
	"GET /api/usage/threads/:id":        domain.RoleMember,
	"GET /api/usage/threads/:id/events": domain.RoleMember,
	"GET /api/usage/by-channel":         domain.RoleMember,
	"GET /api/usage/by-user":            domain.RoleMember,

	// Model catalogue — read-only metadata.
	"GET /api/config/models": domain.RoleMember,

	// The query cookbook (T-Q8). Admin throughout, on the audit log's line
	// below and for its reason: every example carries the SQL the agent ran, so
	// listing them reads the shape of the tenant's warehouse. The two writes are
	// admin for a second reason — harvesting spends embedding calls, and
	// forgetting throws away everything the agent has learned about this
	// tenant's data.
	"GET /api/cookbook":          domain.RoleAdmin,
	"POST /api/cookbook/harvest": domain.RoleAdmin,
	"POST /api/cookbook/sweep":   domain.RoleAdmin,
	"DELETE /api/cookbook":       domain.RoleAdmin,

	// Uploaded documents (T-P1). Reading the list is a member's — seeing what
	// the workspace has ingested is not a privilege — while uploading and
	// deleting are an admin's, because an applied document becomes data every
	// member can query and a deleted one takes its extracted tables with it.
	// The roadmap records this as an owner's decision rather than a settled
	// one (`docs/plan/06-pdf-knowledge-roadmap.md`, open question 2); it is
	// two lines here.
	"POST /api/knowledge/documents":       domain.RoleAdmin,
	"GET /api/knowledge/documents":        domain.RoleMember,
	"GET /api/knowledge/documents/:id":    domain.RoleMember,
	"DELETE /api/knowledge/documents/:id": domain.RoleAdmin,

	// The review surface (T-P7). Reading what was extracted is a member's, for
	// the reason the document list is: it is what the workspace holds. Every
	// write is an admin's, and applying most of all — it is the act that turns
	// this product's reading of a page into data every member can query, which
	// is the decision Decision 3 exists to keep in a person's hands.
	//
	// A member sees the Apply control disabled with a sentence rather than
	// hidden, which is the decision recorded in `docs/coverage/watchers-ui.md`:
	// hiding a control makes a member think the feature is missing, where
	// disabling it tells them who to ask.
	"GET /api/knowledge/documents/:id/tables":       domain.RoleMember,
	"GET /api/knowledge/documents/:id/pages/:page":  domain.RoleMember,
	"GET /api/knowledge/tables/:tableId":            domain.RoleMember,
	"PATCH /api/knowledge/tables/:tableId":          domain.RoleAdmin,
	"POST /api/knowledge/tables/:tableId/apply":     domain.RoleAdmin,
	"POST /api/knowledge/tables/:tableId/unpublish": domain.RoleAdmin,

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
	// Embed keys (T-19). Admin throughout, on the API keys' line and one step
	// past it: this credential decides *which websites may assert who a person
	// is* to Argentum. An admin who edits the origin allowlist is deciding
	// which hosts can mint sessions for their workspace, and the list is where
	// a revoke starts.
	"GET /api/embed-keys":        domain.RoleAdmin,
	"POST /api/embed-keys":       domain.RoleAdmin,
	"PUT /api/embed-keys/:id":    domain.RoleAdmin,
	"DELETE /api/embed-keys/:id": domain.RoleAdmin,
	// The widget's appearance and content (T-23). Admin on read too, matching
	// the keys beside it: the greeting and the prompts are what every visitor
	// of the tenant's site sees first, so editing them is a publishing act.
	"GET /api/embed-config": domain.RoleAdmin,
	"PUT /api/embed-config": domain.RoleAdmin,

	// The `/v1` failure list (T-A5). Admin for the same reason the audit log is:
	// it names every route every integration called and how each call failed,
	// across the whole company. The ticket's acceptance asks for admin-only in
	// as many words, and this table is where that is true.
	"GET /api/api-keys/errors": domain.RoleAdmin,

	// Report share links (T-V4). Admin throughout, and for the API keys'
	// reason rather than the documents' one: a share is a bearer credential
	// that reaches a tenant's figures from outside every session they control.
	// The list is admin too, because it is the inventory a revoke starts from
	// — and because it names who created each link and when it was last read.
	// The list itself is member-readable: a document is a report the staff
	// asked for, and hiding the record of what was generated from the people
	// who generated it buys nothing. Sharing one is admin, below.
	"GET /api/documents": domain.RoleMember,
	// A carousel's slides (T-G6). Member, like the list: the pages are the
	// document the staff asked for, served to the session rather than as a
	// presigned image URL that a persisted message would outlive.
	"GET /api/documents/:id/pages/:page": domain.RoleMember,
	// The caption beside those slides (T-G7). Member for the same reason and
	// with one more: this is what the approval card reads to show a post before
	// somebody authorises it, and whether a member may *decide* an action is
	// already settled per company per kind by `company_actions.allowed_roles`.
	// Making the preview admin-only would leave a member who is allowed to
	// approve a post approving it blind.
	"GET /api/documents/:id/carousel": domain.RoleMember,

	"GET /api/documents/:id/shares":             domain.RoleAdmin,
	"POST /api/documents/:id/shares":            domain.RoleAdmin,
	"DELETE /api/documents/:id/shares/:shareID": domain.RoleAdmin,

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

	// Slack, same split as Lark: a member may see how the channel is wired,
	// only an admin may change the credential or who is allowed to ask.
	"GET /api/slack":              domain.RoleMember,
	"PUT /api/slack":              domain.RoleAdmin,
	"DELETE /api/slack":           domain.RoleAdmin,
	"GET /api/slack/users":        domain.RoleMember,
	"POST /api/slack/users":       domain.RoleAdmin,
	"DELETE /api/slack/users/:id": domain.RoleAdmin,
}

// capabilityPolicy is the second question a route can ask, after apiPolicy's
// (T-Z1): not "is this caller's rank high enough" but "was this person granted
// this". It only ever adds a requirement. RequireCapability runs after
// RequireRole, so a capability can narrow a route the role table opened and can
// never open one the role table refused — and, unlike a role, an admin holds no
// capability nobody granted them (roadmap 12, decision 4).
//
// **It is empty, and that is the ticket's acceptance rather than an unfinished
// table.** Two of the three day-one capabilities name things routes already do:
// `export_data` is GET /api/company/data/export, and `approve_actions` is the
// approve and reject pair. 083 grants nobody anything. Putting any of those
// routes here would 403 every admin who exports and every member who approves,
// on their very next request — the opposite of "every route that exists today
// behaves identically". Moving one behind its capability needs a backfill that
// grants it to whoever can do it today, and that is a decision for the ticket
// that wants the gate; TestExistingRoutesAskForNoCapability pins the three so it
// cannot be made by accident.
//
// `voice` has no route yet. Its entry arrives with roadmap 11's T-W7.
var capabilityPolicy = middleware.CapabilityPolicy{}

// resourcePolicy is the third question a route can ask (T-Z3), and the first
// about the object in its path rather than about the caller: may this person
// reach *this* agent, dashboard, source or document. RequireResource asks
// internal/authz, after RequireCapability, and composes nothing itself.
//
// T-04 chose a table over per-route middleware so that "did we remember to gate
// the new route?" is answered by a test rather than by reading a dozen handlers.
// A resource check written by hand in each handler would bring that question
// back one layer down, so this is a table too, and the classification test holds
// it to more than apiPolicy's property: every route whose path carries a
// restrictable id sits in exactly one of the three tables here — served through
// this one, exempt with a written reason, or pending on the ticket that owns its
// kind — and a route in none of them fails the build.
//
// T-Z3 shipped it empty, as the mechanism. T-Z4 decided the agent routes and
// added nothing here, because talking to an agent is enforced at the enqueuer's
// seams rather than at a route. **Dashboards are the first entries (T-Z5)**:
// opening one, running it, and reading its links are all reads of what is in it.
// **T-Z6 decided sources and documents**, and emptied resourcePending: the
// three source routes that read what is in a source, and the three document
// routes that serve one. The lists ask in their handlers, and so do a document's
// tables, which are served under their own id.
var resourcePolicy = middleware.ResourcePolicy{
	// A restricted dashboard refuses these with a 403 that names the kind, where
	// a restricted agent's picker hides it (T-Z4). The difference is where the
	// person already is: a dashboard is reached by a URL — a bookmark, the link a
	// chat reply carries, its own embed in the transcript — so "not found" would
	// send somebody hunting for a dashboard that exists, and the 403 is what tells
	// them to ask. The list, which has no id, hides it instead (the handler's
	// WithAccess).
	"GET /api/dashboards/:id":      {Kind: domain.ResourceKindDashboard, Param: "id"},
	"GET /api/dashboards/:id/data": {Kind: domain.ResourceKindDashboard, Param: "id"},
	// Admin by the role table, and still a read of what is in the dashboard: a
	// link's pinned filter values are its data's own dimensions — the region it
	// was narrowed to. Restricting revokes every live link, so an admin refused
	// here has nothing live left to find and revoke.
	"GET /api/dashboards/:id/shares": {Kind: domain.ResourceKindDashboard, Param: "id"},

	// Sources (T-Z6). Every connection route is admin, and these three are the
	// ones that read what is in a source: the freshness test runs its SQL and
	// reports the answer, the rebuild has a model describe its schema and returns
	// the description, and the retrieval test returns the schema an agent would be
	// shown. An admin not granted a restricted source is refused them, like
	// anyone (decision 4). The nine that configure without revealing are in
	// resourceExempt; the list a member reaches asks in its handler; and what an
	// agent may query is agent_sources, which no grant touches.
	"POST /api/connections/:id/freshness/test":         {Kind: domain.ResourceKindConnection, Param: "id"},
	"POST /api/connections/:id/regenerate-description": {Kind: domain.ResourceKindConnection, Param: "id"},
	"POST /api/connections/:id/test-rag":               {Kind: domain.ResourceKindConnection, Param: "id"},

	// Uploaded documents (T-Z6), refused by name like a dashboard: a document is
	// reached by a URL — Knowledge's review page — so "not found" would send
	// somebody hunting for a file they were just looking at. The list hides it
	// instead, in its handler. **A document's tables are served under their own
	// id**, which no entry here can name the document behind, so
	// KnowledgeTablesHandler resolves a table to its document and asks there.
	"GET /api/knowledge/documents/:id":             {Kind: domain.ResourceKindDocument, Param: "id"},
	"GET /api/knowledge/documents/:id/tables":      {Kind: domain.ResourceKindDocument, Param: "id"},
	"GET /api/knowledge/documents/:id/pages/:page": {Kind: domain.ResourceKindDocument, Param: "id"},
}

// resourceExempt are routes that carry a restrictable id and deliberately do not
// ask about it. The value is the reason. The classification test refuses one
// shorter than five words, because an exemption is a decision somebody wrote
// down and a Go comment is something no test can read.
//
// What they share: each manages access, takes it away, or configures the object
// — and none reveals what is in it. A route like that put behind the grant it
// manages is how an admin restricts a dashboard nobody holds and then cannot
// undo it — decision 4 permits exactly that lock-out, so the way back out must
// never be gated.
//
// The agent rows are T-Z4's decision, and the line it drew: **a grant on an
// agent gates talking to it and being offered it; configuring the roster is the
// role table's.** Talking is enforced at the enqueuer's seams, not at a route
// (decision 6), and being offered is the roster handler narrowing its answer by
// role — which is why GET /api/agents/:id is here and not in resourcePolicy: its
// answer depends on who is asking, and a (kind, param) entry has no role.
//
// The four /api/access routes name their kind in a parameter, so the detector
// in the test cannot see them and ResourcePolicy could not express them anyway.
// They are listed so that the promise in AccessHandler's comment is held by a
// test that fails if a route is renamed, rather than by that comment alone.
var resourceExempt = map[string]string{
	// Sources (T-Z6): the nine connection routes that configure a source and
	// return nothing of what is in it. T-Z4's roster line, for sources: a grant
	// gates what a person is shown, and configuring is the role table's.
	"PATCH /api/connections/:id":                   "renames or re-describes a source by hand and returns nothing from it",
	"PUT /api/connections/:id/dsn":                 "rotates the credential and returns nothing from the source",
	"PUT /api/connections/:id/allowlist":           "sets which tables agents may read and returns nothing from them",
	"PUT /api/connections/:id/freshness":           "saves the freshness expression without running it or returning anything",
	"POST /api/connections/:id/default":            "chooses the default source and returns nothing from it",
	"POST /api/connections/:id/reindex-embeddings": "rebuilds the table index and answers only with counts",
	"POST /api/connections/:id/test":               "reports whether the source answers, never what is in it",
	"POST /api/connections/:id/rescan":             "queues a profile rescan and answers only that it was queued",
	"DELETE /api/connections/:id":                  "removing a source reveals nothing in it and only takes access away",
	// Deleting a document is admin by the role table, and like deleting a
	// dashboard it reveals nothing of what it removes.
	"DELETE /api/knowledge/documents/:id":         "removing a document reveals nothing in it and only takes access away",
	"GET /api/access/:kind":                       "who may reach each resource of a kind, never what is in one — the matrix a locked-out admin reads",
	"GET /api/access/:kind/:id":                   "who may reach a resource, never what is in it — and how a locked-out admin finds out",
	"PUT /api/access/:kind/:id/mode":              "re-opening a resource nobody holds is the way out of the lock-out decision 4 permits",
	"PUT /api/access/:kind/:id/grants/:userID":    "granting is how an admin who restricted something gives access back, themselves included",
	"DELETE /api/access/:kind/:id/grants/:userID": "revoking a grant can only take access away",
	// Revoking has to keep working for an admin the dashboard is restricted
	// against, or a public link outlives the restriction that was meant to close
	// it. Restricting revokes every live link (T-Z5), so this should find none —
	// and the one that could still exist, minted by the previous release's binary
	// mid-deploy, is exactly the one somebody must be able to take back.
	"DELETE /api/dashboards/:id/shares/:shareID": "revoking a public link can only close a door, grant or no grant",
	// Minting is refused on every restricted dashboard whoever asks, by the share
	// service and again where the row is written (T-Z5). An open dashboard needs
	// no grant. So the grant could only ever admit what "open" already admits,
	// and asking would be a database read that decides nothing.
	"POST /api/dashboards/:id/shares": "a restricted dashboard refuses every mint whoever asks, so a grant would decide nothing here",
	// The agent roster's line, drawn for dashboards: deleting is admin by the role
	// table and reveals nothing inside the dashboard. An admin who restricted one
	// nobody holds must still be able to remove it.
	"DELETE /api/dashboards/:id": "removing a dashboard is admin by the role table and reveals nothing that is in it",
	// Adding an agent to a room names it in the body, not the path, and is
	// T-Z4's check in ThreadParticipantService.Add.
	"DELETE /api/threads/:id/participants/:agentID": "removing an agent from a room can only take reach away",

	// The roster (T-Z4).
	"GET /api/agents/:id":         "the handler answers a member as the roster list does — an agent they may not talk to is not found — and an admin, who manages the roster, sees every agent",
	"PUT /api/agents/:id":         "editing an agent is roster configuration, which the role table gives admins; a grant gates talking to it",
	"DELETE /api/agents/:id":      "removing an agent from the roster is roster configuration, admin by the role table",
	"PUT /api/agents/:id/default": "choosing the default is roster configuration; a person not granted it falls through to an agent they may use",
	"GET /api/agents/:id/skills":  "which procedures an agent follows is roster configuration, admin by the role table",
	"PUT /api/agents/:id/skills":  "binding procedures to an agent is roster configuration, admin by the role table",
}

// resourcePending are routes that carry a restrictable id and whose decision
// belongs to a later ticket of roadmap 12, keyed to that ticket. **None of them
// asks for a grant**, so this map is the precise meaning of "restricted on
// paper": a restricted resource is still reachable through every route below.
//
// The classification test accepts only tickets it lists as open. The day one of
// them lands and is struck from that list, every row still keyed to it fails
// the build — so this is a to-do list that cannot quietly outlive its owner, and
// a new route cannot be parked here under a ticket that has already shipped.
//
// **Empty since T-Z6.** Agents were six rows until T-Z4, dashboards five until
// T-Z5, and sources and documents sixteen until T-Z6: three went to
// resourcePolicy for sources and three for documents, and ten to resourceExempt.
// It stays declared, and the classification test stays able to read it, so a
// future kind has somewhere to park its routes while its own ticket is open.
var resourcePending = map[string]string{}

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

	// The embed session mint (T-19). Deliberately outside the session: its
	// caller is a visitor of a tenant's own website, who has no Argentum
	// account and therefore no role for apiPolicy to check. What authorises the
	// call is a known client key, an allowlisted Origin and the tenant's HMAC —
	// all three inside app.EmbedKeyService.MintSession.
	//
	// Listed here one route at a time, like the auth routes, so "this route
	// authenticates nobody in the usual sense" stays a decision somebody wrote
	// down rather than a path that fell through a wildcard.
	// The CORS preflight for the whole embed surface. Keyless by definition —
	// a preflight carries no credential and is not allowed to.
	"/api/embed/*path":           true,
	"/api/embed/session":         true,
	"/api/embed/session/refresh": true,

	// The widget's own surface (T-20), authenticated by the session those two
	// routes mint. An embed session carries no role — middleware.EmbedAuth sets
	// none, on purpose — so apiPolicy has nothing to check here either. What
	// gates these is the ref on the token: every one of them is scoped to the
	// visitor it was minted for, inside the handler.
	//
	// Named one by one for the reason the mint is, and with one more: this is
	// the list somebody will be tempted to add a sixth route to.
	"/api/embed/config":               true, // greeting, prompts, agent names
	"/api/embed/chat":                 true, // one turn, budget-checked
	"/api/embed/threads/current":      true, // this visitor's live conversation
	"/api/embed/threads/:id/messages": true, // transcript, ref-scoped
	"/api/embed/threads/:id/stream":   true, // the WebSocket the answer arrives on

	// The report player (T-V4). Deliberately keyless: the token in the path is
	// the whole credential, and the visitor has no account to hold a role. It
	// is listed here rather than being outside the walk, so "this route
	// authenticates nobody" stays a decision somebody wrote down — the same
	// reason the auth routes above are named one by one.
	"/share/:token": true,

	// The shared dashboard page (T-D13), keyless for the same reason and with
	// one difference worth writing down: this route runs live SQL against a
	// customer's warehouse for a visitor with no account. Expiry, revocation,
	// the optional password and the per-hour refresh cap are the controls, and
	// they live on the share row rather than in this table.
	"/share/dashboard/:token": true,

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
	"/webhook/slack/events/:app_id": true,
}
