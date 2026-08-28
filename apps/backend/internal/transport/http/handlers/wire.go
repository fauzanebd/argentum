package handlers

import (
	"encoding/json"
	"time"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
)

// Wire types for the dashboard's `/api` surface that are not domain entities
// (T-02b).
//
// This file exists because `packages/api-types` is generated from Go, and a
// response assembled as a `gin.H` generates nothing — the dashboard was
// hand-writing a TypeScript interface for a shape no Go declaration described,
// which is the drift this ticket is about. Anything the dashboard needs a type
// for and the domain does not already provide belongs here.
//
// Most `/api` responses are still `gin.H` envelopes around domain values
// (`{"threads": […]}`), and those stay as they are: the elements are generated
// from `internal/domain`, and turning forty envelopes into forty structs is a
// wire-shape change per route rather than a type-generation ticket. What is
// here is the case where the *element itself* had no Go type.
//
// `/v1` is not in scope for this file. Its types are generated from
// `apps/backend/openapi/v1.yaml` (T-A4) — a published contract is authored,
// not derived.

// AgentToolInfo is one tool checkbox in Settings → Agents (T-S1).
//
// Name comes from the live registry, so a tool added on the backend appears in
// the dashboard without a frontend change. Label does not: a tool's own
// Description is prompt text written for the model — three sentences of
// instruction on when to call it — and pasting that beside a checkbox would be
// unreadable. An unlabelled tool falls back to its bare name rather than
// disappearing, which is the failure direction a per-agent allowlist can
// afford.
type AgentToolInfo struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	// MCPServerID and MCPServerName are set for a tenant's own MCP tool (T-M2),
	// empty for the static registry. The form groups by the server name, because
	// `mcp__kirim_cepat__quote_shipping` is an identifier and "Kirim Cepat" is
	// what the admin who registered it recognises.
	//
	// These tools were absent from this list entirely until 2026-08-03, which
	// silently un-scoped an agent from every MCP tool it was bound to the moment
	// an admin narrowed its tools in the dashboard.
	MCPServerID   string `json:"mcp_server_id,omitempty"`
	MCPServerName string `json:"mcp_server_name,omitempty"`
	// RequiresApproval marks an MCP tool an admin classified as a write (T-M4).
	// The agent may call it, and the call proposes rather than runs — so the
	// checkbox grants a capability, not a licence, and the form says which.
	RequiresApproval bool `json:"requires_approval,omitempty"`
}

// AgentsResponse is the body of `GET /api/agents`.
//
// The tool vocabulary rides along with the roster rather than sitting on its
// own route: it is only ever read by the same form, and `GET /api/agents/tools`
// beside `GET /api/agents/:id` is a static segment competing with a wildcard in
// one method tree. Templates ride along for both reasons and a third — the chat
// reads the same payload to find a thread's starter questions, so a separate
// route would be a second request on a screen that already has the answer.
type AgentsResponse struct {
	Agents []*domain.Agent `json:"agents"`
	Tools  []AgentToolInfo `json:"tools"`
	// Templates is the create-an-agent gallery (T-B3), already narrowed to the
	// tools this deployment runs. Empty on a deployment that loaded no gallery,
	// which the dashboard renders as the blank form and nothing else — the
	// product as it was before the templates existed.
	Templates []AgentTemplate `json:"templates"`
	// Generation is whether the create form may offer "Generate with AI"
	// (T-B4), and why not when it may not.
	Generation AgentGenerationInfo `json:"generation"`
}

// AgentGenerationInfo is the state of the Generate button before it is pressed
// (T-B4).
//
// Two booleans rather than one, because "this deployment has no generator" and
// "this workspace has no credit left" are different sentences and only the
// second is the tenant's to act on. Both leave the form perfectly able to save
// an agent — generation is a shortcut past the empty textarea, never the way in.
type AgentGenerationInfo struct {
	Available        bool `json:"available"`
	CreditsExhausted bool `json:"credits_exhausted"`
}

// AgentGenerationResult is the body of `POST /api/agents/generate` (T-B4): the
// two fields, improved, for the tenant to review in the form.
//
// Nothing was written when this was produced. The agent row changes when the
// tenant presses Save and not before, which is what makes regenerating and
// undoing free.
type AgentGenerationResult struct {
	Description string `json:"description"`
	Persona     string `json:"persona"`
	// Fallback names what happened when the generated persona was rejected by
	// the output validator: "template" when the picked card's persona came back
	// instead, "input" when the tenant's own text did, and empty when the model
	// wrote what is above. The dashboard says so — a tenant about to save this
	// text should know whether a model wrote it.
	Fallback string `json:"fallback,omitempty"`
}

// AgentTemplate is one gallery card in Settings → Agents (T-B3).
//
// A projection of agenttemplates.Template rather than the loader's own struct,
// for the same reason AgentToolInfo is not a tools.Tool: the config type is a
// YAML shape that the file's authors may extend — an editorial note, an ordering
// hint, a card we ship but do not offer yet — and every field of it would reach
// the browser the day it was added. This is the published half, and it is a
// deliberate list.
type AgentTemplate struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// Persona prefills the instructions field. It is sent to the browser
	// because the tenant edits it before saving — nothing about a template is
	// applied behind the form.
	Persona        string   `json:"persona"`
	SuggestedTools []string `json:"suggested_tools"`
	// SourceHints pre-tick likely databases in the create form, matched against
	// a connection's label and description. The dashboard shows which hint
	// matched, because a silently pre-ticked source scopes an agent away from
	// its own data.
	SourceHints []string `json:"source_hints"`
	// StarterQuestions are offered on a new conversation opened on an agent
	// created from this template.
	StarterQuestions []string `json:"starter_questions"`
}

// AgentBindingsResponse is the body of `GET /api/agent-bindings` (T-S4).
//
// Channels rides along for the same reason AgentsResponse carries the tool
// vocabulary: it is the vocabulary the form beside the table has to offer, and
// it is the backend that decides which channels can be bound at all — a
// frontend array would have to be edited the day a fifth channel is added, and
// nothing would report that it had not been.
type AgentBindingsResponse struct {
	Bindings []*domain.AgentChannelBinding `json:"bindings"`
	Channels []domain.Channel              `json:"channels"`
}

// CompanyProfileResponse is the body of `GET|PUT /api/company/profile` (T-B1).
//
// RenderedBlock rides along because a prompt fragment the tenant cannot read is
// a prompt fragment they cannot debug: the form takes four fields and the model
// reads one composed string, and the only way "this is what your agent sees" is
// true is if the backend that composes it also returns it.
//
// Profile is present even when the company has no row — an empty form with the
// defaults, so the dashboard renders one shape rather than two.
type CompanyProfileResponse struct {
	Profile *domain.CompanyProfile `json:"profile"`
	// Exists distinguishes "never filled in" from "filled in and then
	// emptied". The dashboard uses it to decide between a prompt to describe
	// the business and a saved-but-blank state.
	Exists        bool   `json:"exists"`
	RenderedBlock string `json:"rendered_block"`
	// Truncated is true when the block hit the cap. The UI says so; the turn
	// carries the shortened text either way.
	Truncated bool `json:"truncated"`
	// BlockTokenLimit is the cap in tokens, echoed rather than duplicated in the
	// frontend — the number is a backend policy and the warning beside the
	// textarea should not have its own copy of it.
	BlockTokenLimit int `json:"block_token_limit"`
}

// ProfileSuggestionResponse is the body of `GET /api/company/profile/suggestion`
// (T-B2): what the connected sources say the business is, before anybody has
// agreed to it.
//
// Draft is nil when there is nothing to review, which is the ordinary state of
// a company that has just signed up — the panel is absent rather than empty.
// The two counters beside it are what let the panel explain the absence instead
// of leaving a tenant to guess whether the feature is broken.
type ProfileSuggestionResponse struct {
	Draft *domain.CompanyProfile `json:"draft,omitempty"`
	// Sources is how many connected sources have been described.
	Sources int `json:"sources"`
	// CreditsExhausted says the balance is why there is no draft. Adding a
	// source never fails for credit, so without this the tenant sees silence.
	CreditsExhausted bool `json:"credits_exhausted"`
	// RenderedBlock is the draft as the agent would read it, composed by the
	// same code the turn uses — so what a tenant approves is what they saw.
	RenderedBlock string `json:"rendered_block,omitempty"`
	Truncated     bool   `json:"truncated"`
}

// APIKeysResponse is the body of `GET /api/api-keys` (T-13, extended by T-A5).
//
// Stats rides with the roster for the same reason AgentsResponse carries the
// tool vocabulary: the tab never renders one without the other, and a key's
// traffic is a property of that key rather than a separate resource.
//
// Keyed by key id rather than sorted alongside `Keys`, because a key with no
// traffic in the window has no entry at all — "no calls" and "no such key" are
// different facts, and a parallel array would have to invent a zero row to keep
// the indices lined up.
type APIKeysResponse struct {
	Keys []*domain.APIKey `json:"keys"`
	// Stats is absent on a deployment without the request recorder, and after a
	// failed counters read. A tab that hides the numbers still manages keys.
	Stats map[string]*domain.APIKeyRequestStats `json:"stats,omitempty"`
}

// APIKeyErrorsResponse is the body of `GET /api/api-keys/errors` (T-A5) — the
// last non-2xx `/v1` responses, newest first, optionally for one key.
type APIKeyErrorsResponse struct {
	Errors []*domain.APIRequestError `json:"errors"`
	// Limit is echoed so the tab can say "the last 50" without hardcoding the
	// number the backend actually applied.
	Limit int `json:"limit"`
}

// SendMessageResponse is the body of `POST /api/chat` — the acknowledgement
// that a turn was queued, not the answer. The answer arrives over the
// WebSocket as a stream of app.ChatEvent.
type SendMessageResponse struct {
	// TaskID is the asynq task. It is returned for support and for tests;
	// nothing in the dashboard routes on it.
	TaskID   string `json:"task_id"`
	ThreadID string `json:"thread_id"`
	// IsNewThread tells the dashboard to add a row to the thread list rather
	// than move an existing one to the top.
	IsNewThread bool   `json:"is_new_thread"`
	UserMsgID   string `json:"user_msg_id"`
	// BudgetWarning is present only when the turn ran *and* the tenant is near
	// the end of their credit. Its absence is the ordinary case and is the
	// signal — a client must not read `verdict === "ok"`, because in the
	// ordinary case there is no object to read it from.
	BudgetWarning *app.BudgetState `json:"budget_warning,omitempty"`
}

// MCPServersResponse is the body of `GET /api/mcp-servers` (T-M1).
//
// Transports rides along for the reason AgentsResponse carries the tool
// vocabulary: which transports this release speaks is a backend fact, and a
// frontend `["http", "sse"]` would be the array nobody edits the day a third
// one is added — or, worse, the array somebody adds "stdio" to, which is a
// decision the backend has made and will not revisit.
type MCPServersResponse struct {
	Servers    []*domain.MCPServer   `json:"servers"`
	Transports []domain.MCPTransport `json:"transports"`
	// AllowsInsecureHTTP is whether this deployment accepts a plaintext http
	// URL. The form's hint is written from it, because "must be https" printed
	// beside a deployment that accepts http is a sentence that costs an admin a
	// support ticket — and the reverse is worse.
	AllowsInsecureHTTP bool `json:"allows_insecure_http"`
}

// MCPServerResponse is one server and the tools discovery found on it — the
// shape every write route answers with, so a browser never holds two views of
// the same server.
type MCPServerResponse struct {
	Server *domain.MCPServer `json:"server"`
	Tools  []MCPToolView     `json:"tools"`
}

// MCPToolView is one discovered tool plus the fact a browser cannot compute.
//
// Drifted means an approved tool's description or argument schema has changed
// since the admin approved it. It is the cheapest injection vector this track
// opens — a description is text that enters the agent's context — so it is
// surfaced rather than silently adopted, which is locked decision 6 arriving in
// the UI.
//
// The fields are spelled out rather than embedded: Go would promote them onto
// the wire either way, but `packages/api-types` is generated from these
// declarations and an embedded pointer generates `MCPServerTool?: unknown`,
// which is a browser type that describes nothing.
type MCPToolView struct {
	ID           string          `json:"id"`
	ServerID     string          `json:"server_id"`
	ToolName     string          `json:"tool_name"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"input_schema"`
	ReadOnly     bool            `json:"read_only"`
	Approved     bool            `json:"approved"`
	Drifted      bool            `json:"drifted"`
	DiscoveredAt time.Time       `json:"discovered_at"`
}

// SkillsResponse is the body of `GET /api/skills` (T-K1, T-K6).
//
// Two things ride along with the rows, and both are here for MCPServersResponse'
// reason — a fact the backend owns, printed beside a form that would otherwise
// hard-code it.
//
// `Limits` is the four caps. The form counts characters live because the save
// refuses rather than truncates, and a form that discovers a cap on submit is a
// form that loses a paragraph; a second copy of "60" in TypeScript is the copy
// that disagrees with the CHECK constraint.
//
// `Index` is what this workspace's procedures cost every turn, composed by the
// code that composes the prompt. Without it the bound is invisible until
// somebody reads a production log — which is exactly how a tenant's twenty-first
// procedure stops being offered without anybody noticing.
type SkillsResponse struct {
	Skills []*domain.Skill `json:"skills"`
	// Index is nil when it could not be computed. A failure to price the index
	// is not a failure to list: the list is what the screen is for.
	Index  *app.SkillIndexCost `json:"index"`
	Limits SkillLimits         `json:"limits"`
}

// SkillLimits are T-K1's four caps, served rather than duplicated.
type SkillLimits struct {
	NameChars      int `json:"name_chars"`
	WhenToUseChars int `json:"when_to_use_chars"`
	BodyChars      int `json:"body_chars"`
	PerCompany     int `json:"per_company"`
}

// SkillBindingResponse is the body of `GET /api/agents/:id/skills`.
//
// `Means` is served rather than left to the client, and it is not decoration:
// this endpoint returns an empty array for the state that means *everything*,
// which is the opposite of what the identically-shaped MCP binding endpoint
// means by it. One sentence on the wire is cheaper than two frontends that
// each guessed.
type SkillBindingResponse struct {
	SkillIDs []string `json:"skill_ids"`
	Means    string   `json:"means"`
}

// MetricsResponse is the body of `GET /api/metrics` (T-06).
//
// Grains and Units ride along for the same reason MCPServersResponse carries
// Transports: which values this release understands is a backend fact, and a
// frontend that hard-codes the dropdown is the one that goes stale the day a
// grain is added — or wrong the day the backend's list and the form's disagree.
type MetricsResponse struct {
	Metrics []*domain.MetricDefinition `json:"metrics"`
	Grains  []domain.MetricGrain       `json:"grains"`
	Units   []domain.MetricUnit        `json:"units"`
}

// MetricResponse is one metric — the shape every write route answers with.
type MetricResponse struct {
	Metric *domain.MetricDefinition `json:"metric"`
}

// WatchersResponse is the body of `GET /api/watchers` (T-08).
//
// Grains, Comparators, Channels and CompareOptions ride along for the same
// reason MetricsResponse carries Grains and Units: which values this release
// understands is a backend fact, and a frontend that hard-codes the dropdowns is
// the one that goes stale — or wrong — the day the backend's list changes.
type WatchersResponse struct {
	Watchers    []*domain.Watcher          `json:"watchers"`
	Grains      []domain.WatcherGrain      `json:"grains"`
	Comparators []domain.WatcherComparator `json:"comparators"`
	Channels    []domain.Channel           `json:"channels"`
	// CompareOptions are the compare_to values the pct_change comparators accept.
	// A plain string list rather than a domain enum: compare_to is a bare column
	// on the watcher, shared verbatim with query_metric's Comparison.
	CompareOptions []string `json:"compare_options"`
}

// WatcherResponse is one watcher — the shape every write route answers with.
type WatcherResponse struct {
	Watcher *domain.Watcher `json:"watcher"`
}

// WatcherEventsResponse is the body of `GET /api/watchers/:id/events` (T-08):
// a watcher's recent evaluations, newest first, breached or not.
type WatcherEventsResponse struct {
	Events []*domain.WatcherEvent `json:"events"`
}

// MetricTestResponse is what the "Test" button shows: the rendered SQL and the
// number it returned over the validation window, without saving anything (T-06).
type MetricTestResponse struct {
	Value       float64   `json:"value"`
	From        time.Time `json:"from"`
	To          time.Time `json:"to"`
	RenderedSQL string    `json:"rendered_sql"`
}

// WebhooksResponse is the body of `GET /api/webhooks` (T-15).
//
// It carries the vocabulary and the signing contract beside the rows, so the
// settings form offers what this deployment actually publishes and an admin can
// read how to verify a delivery without opening the API docs. Both are facts
// about the backend, and a copy of either in the frontend is a copy that goes
// stale.
type WebhooksResponse struct {
	Subscriptions []*domain.WebhookSubscription `json:"subscriptions"`
	Events        []string                      `json:"events"`
	// DisableAfter is how many consecutive failed deliveries switch a
	// subscription off by itself.
	DisableAfter     int    `json:"disable_after"`
	SignatureHeader  string `json:"signature_header"`
	SignatureMessage string `json:"signature_message"`
}
