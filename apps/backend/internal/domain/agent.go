package domain

import (
	"context"
	"slices"
	"time"
)

// Agent is one entry in a company's roster (T-S1).
//
// The customer has four jobs and one agent: Marketing, Ops, HR and Finance ask
// incompatible questions of incompatible data through a single prompt. An
// Agent is persona + tools + sources — a named, tenant-editable configuration
// of the one pipeline this product runs.
//
// It is **not** an access boundary. Company membership remains the
// authorization boundary, so any member can open any of their company's
// agents; the Finance agent physically cannot query the HR source, but nothing
// stops an employee from opening it and asking what it can reach. Per-agent
// user grants are a follow-on, and this struct is shaped so adding them later
// changes no field here.
//
// T-S2 is what reads these rows at turn time: the persona is appended to the
// system prompt, AllowedTools filters the registry the turn is built with, and
// SourceIDs becomes the scope tools.ResolveSource enforces.
type Agent struct {
	ID          string `json:"id"`
	CompanyID   string `json:"company_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// PersonaPrompt is appended to the shared system prompt, never a
	// replacement for it (bootstrap.SystemPrompt). The shared prompt carries
	// the SQL-dialect rules, the anti-fabrication language and the formatting
	// contract; a customer-authored prompt that could replace it would be a
	// self-service route back to fabricated answers.
	PersonaPrompt string `json:"persona_prompt"`
	// AllowedTools is empty for "every registered tool". See AllowsTool.
	AllowedTools []string `json:"allowed_tools"`
	// SourceIDs is empty for "every source the company owns". See
	// AllowsSource. Stored in agent_sources rather than inline: these are real
	// foreign keys, and a deleted connection has to leave every agent's
	// allowlist with it.
	SourceIDs []string `json:"source_ids"`
	// MCPServerIDs is the tenant MCP servers this agent may call (T-M2). Unlike
	// SourceIDs, **empty means NONE** — see AllowsMCPServer. Stored in
	// agent_mcp_servers, and a deleted server leaves every agent's binding with
	// it, exactly as a deleted connection leaves SourceIDs.
	MCPServerIDs []string `json:"mcp_server_ids"`
	// SkillIDs is the tenant procedures this agent is offered (T-K1). Empty is
	// SourceIDs' rule — **every enabled company skill** — and not
	// MCPServerIDs'; see AllowsSkill for why two adjacent bindings differ.
	// Stored in agent_skills, and a deleted skill leaves every agent's binding
	// with it.
	SkillIDs []string `json:"skill_ids"`
	// TemplateKey records which gallery card this agent was created from
	// (T-B3), or "" for the blank path and for every agent that predates it.
	//
	// **Analytics only — never read at turn time** (locked decision 4). Nothing
	// in the composed prompt consults it, and editing a template in
	// config/agent_templates.yaml changes no agent that already exists: the
	// text was copied into this row at create time and belongs to the tenant
	// from that moment. The dashboard reads it for one thing only, which is
	// which starter questions to offer on an empty thread.
	TemplateKey string    `json:"template_key"`
	IsDefault   bool      `json:"is_default"`
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// AllowsTool reports whether this agent may call the named tool.
//
// **An empty allowlist means unrestricted**, here and in AllowsSource, and
// this method is the one place that rule is written down. The alternative —
// empty means nothing — reads safer and behaves worse: every tool added after
// an agent was created would be invisible to it, and a new database connection
// would reach no agent until somebody remembered to tick it. An agent that
// must be restricted carries an explicit list.
func (a *Agent) AllowsTool(name string) bool {
	return len(a.AllowedTools) == 0 || slices.Contains(a.AllowedTools, name)
}

// AllowsSource reports whether this agent may reach the given connection.
// Empty means every source the company owns; see AllowsTool.
//
// At turn time the same rule is applied through agentscope.Scope, which is how
// it reaches tools.ResolveSource without every tool learning what an agent is.
// Scoping is enforced at the tool, not in the persona: a prompt saying "only
// use the finance database" is a wish.
func (a *Agent) AllowsSource(connectionID string) bool {
	return len(a.SourceIDs) == 0 || slices.Contains(a.SourceIDs, connectionID)
}

// AllowsMCPServer reports whether this agent may call the tools of the given
// MCP server (T-M2).
//
// **Empty means NONE**, which is the opposite of AllowsTool and AllowsSource
// and is the whole point of locked decision 5: a warehouse the tenant connected
// is already theirs, but a tool that acts on a third-party system we hold a
// token for reaches an agent only when an admin bound it. An agent with no
// binding gets no MCP tools, which is also why the eval harness and every
// pre-T-M2 turn are unaffected — they carry no binding and so reach nothing.
func (a *Agent) AllowsMCPServer(serverID string) bool {
	return slices.Contains(a.MCPServerIDs, serverID)
}

// AgentRepository is the persistence contract for the roster.
//
// Every method takes companyID, including the ones that already have a primary
// key. A repository that can be asked for an agent by id alone is a repository
// whose callers have to remember the tenant check, and "read another company's
// agent by guessing a uuid" is the one failure this table cannot afford — the
// persona is the tenant's own words about their own business.
type AgentRepository interface {
	Create(ctx context.Context, a *Agent) error
	GetByID(ctx context.Context, companyID, id string) (*Agent, error)
	// GetDefault returns the company's default agent — what a turn that names
	// no agent runs as (T-S2). ErrNotFound when the company has no roster at
	// all, which the caller treats as "run unscoped" rather than as a failure:
	// a tenant whose seed did not run must still be able to ask a question.
	GetDefault(ctx context.Context, companyID string) (*Agent, error)
	ListByCompany(ctx context.Context, companyID string) ([]*Agent, error)
	Update(ctx context.Context, a *Agent) error
	Delete(ctx context.Context, companyID, id string) error
	SetDefault(ctx context.Context, companyID, agentID string) error
}
