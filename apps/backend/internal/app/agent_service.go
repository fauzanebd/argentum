package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/agenttemplates"
	"github.com/fauzanebd/argentum/internal/domain"
	mcptools "github.com/fauzanebd/argentum/internal/tools/mcp"
)

// The agent roster (T-S1).
//
// A customer with four jobs has one agent: Marketing, Ops, HR and Finance ask
// incompatible questions of incompatible data through a single prompt. This
// service owns the roster that lets them have four, and the rules that keep it
// usable — a company always has exactly one default, and never has zero agents.
//
// Nothing here runs a turn. Composing an agent into a run is T-S2; this is the
// noun.

const (
	// agentNameMax bounds what shows in a picker. It is a label, not prose.
	agentNameMax = 60
	// agentDescriptionMax bounds the line under the name in the roster list.
	agentDescriptionMax = 240
	// agentPersonaMax bounds the addendum appended to the system prompt on
	// every turn this agent takes. Unbounded persona text is unbounded token
	// spend on the tenant's own credits, paid per turn and per channel, and
	// the person writing it sees no meter. 8000 characters is roughly 2k
	// tokens — long enough for a real briefing, short enough that nobody
	// pastes their handbook into it.
	agentPersonaMax = 8000
)

// AgentService is the CRUD surface behind Settings → Agents.
type AgentService struct {
	repo  domain.AgentRepository
	conns domain.ConnectionRepository
	// tools is this deployment's live registry, by name (tools.Registry). It
	// is what a submitted allowlist is checked against, so an agent cannot be
	// scoped to a tool that does not exist here.
	tools []string
	// templates is the create-an-agent gallery (T-B3), or nil on a wiring that
	// loaded no file. Nil means the blank path only — which is exactly what
	// existed before this ticket, so a deployment without the file degrades to
	// the previous product rather than to a broken one.
	templates *agenttemplates.Set
}

// NewAgentService wires the roster. toolNames comes from tools.Names over the
// same registry the worker runs, so a deployment without object storage
// refuses `generate_document` here for the same reason it never offers it.
func NewAgentService(
	repo domain.AgentRepository, conns domain.ConnectionRepository, toolNames []string,
) *AgentService {
	return &AgentService{repo: repo, conns: conns, tools: toolNames}
}

// WithTemplates installs the gallery an agent can be created from (T-B3).
// Optional wiring rather than a constructor argument: the templates are a file
// this repo ships, and every caller that only manages the roster — the policy
// test, the seeding path — has no business loading one.
func (s *AgentService) WithTemplates(set *agenttemplates.Set) *AgentService {
	s.templates = set
	return s
}

// ToolNames returns the registry this deployment actually has. The dashboard
// renders one checkbox per entry.
func (s *AgentService) ToolNames() []string { return s.tools }

// Templates returns the gallery with each card's suggested tools narrowed to
// what this deployment runs.
//
// The narrowing happens here rather than in the dashboard because the reason
// for it is a backend fact: generate_document exists only where object storage
// does, and a card that pre-ticks it would produce a form that fails on first
// save — validated against the live registry by the same service — with an
// error naming a tool the admin never chose.
func (s *AgentService) Templates() []agenttemplates.Template {
	return s.templates.ForRegistry(s.tools)
}

// AgentInput is one submitted agent. Empty AllowedTools and empty SourceIDs
// both mean unrestricted — see domain.Agent.AllowsTool for why that is the
// rule and not the other way round.
type AgentInput struct {
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	PersonaPrompt string   `json:"persona_prompt"`
	AllowedTools  []string `json:"allowed_tools"`
	SourceIDs     []string `json:"source_ids"`
	// TemplateKey names the gallery card this agent was created from (T-B3),
	// or "" for the blank path. Read on create and ignored on update: it
	// records where the agent came from, which an edit cannot change.
	TemplateKey string `json:"template_key"`
	// Enabled is a pointer so an update that omits it leaves the flag alone.
	// A plain bool would silently disable every agent edited by a client that
	// did not know the field existed.
	Enabled *bool `json:"enabled"`
}

// List returns the company's roster, default first.
func (s *AgentService) List(ctx context.Context, companyID string) ([]*domain.Agent, error) {
	agents, err := s.repo.ListByCompany(ctx, companyID)
	if err != nil {
		return nil, err
	}
	if agents == nil {
		agents = []*domain.Agent{}
	}
	return agents, nil
}

// Get returns one agent. Another company's id is domain.ErrNotFound, which the
// handler answers as 404: a 403 would confirm the row exists.
func (s *AgentService) Get(ctx context.Context, companyID, id string) (*domain.Agent, error) {
	return s.repo.GetByID(ctx, companyID, id)
}

// Create adds an agent. The first one a company has becomes its default —
// there is no state in which a company has agents but no default, because
// T-S2 resolves an unspecified thread to exactly that row.
func (s *AgentService) Create(ctx context.Context, companyID string, in AgentInput) (*domain.Agent, error) {
	a, err := s.validated(ctx, companyID, in)
	if err != nil {
		return nil, err
	}
	// Checked rather than trusted, even though nothing reads it at turn time: a
	// provenance column that accepts arbitrary strings answers no analytics
	// question, and the dashboard's own starter questions are looked up by it.
	key := strings.TrimSpace(in.TemplateKey)
	if key != "" && !s.templates.Has(key) {
		return nil, fmt.Errorf("%w: no agent template called %q", domain.ErrInvalidInput, key)
	}
	a.TemplateKey = key

	existing, err := s.repo.ListByCompany(ctx, companyID)
	if err != nil {
		return nil, err
	}
	a.IsDefault = len(existing) == 0
	a.Enabled = in.Enabled == nil || *in.Enabled

	if err := s.repo.Create(ctx, a); err != nil {
		if errors.Is(err, domain.ErrAlreadyExists) {
			return nil, fmt.Errorf("%w: an agent called %q already exists", domain.ErrAlreadyExists, a.Name)
		}
		return nil, err
	}
	logrus.WithFields(logrus.Fields{
		"company_id": companyID, "agent_id": a.ID, "name": a.Name,
		"tools": len(a.AllowedTools), "sources": len(a.SourceIDs),
		// Logged because it is the one question this column exists to answer:
		// which starting points do tenants actually pick, and how many start
		// from blank ("").
		"template": a.TemplateKey,
	}).Info("agent created")
	return a, nil
}

// Update rewrites an agent in place. It cannot move the default — that is
// SetDefault, which has to demote the current holder in the same transaction.
func (s *AgentService) Update(ctx context.Context, companyID, id string, in AgentInput) (*domain.Agent, error) {
	current, err := s.repo.GetByID(ctx, companyID, id)
	if err != nil {
		return nil, err
	}
	a, err := s.validated(ctx, companyID, in)
	if err != nil {
		return nil, err
	}
	a.ID = current.ID
	a.IsDefault = current.IsDefault
	// Provenance survives every edit, including one that replaces all of the
	// template's text. "Created from Finance" stays true; "still says what
	// Finance said" was never claimed.
	a.TemplateKey = current.TemplateKey
	a.Enabled = current.Enabled
	if in.Enabled != nil {
		a.Enabled = *in.Enabled
	}
	// Disabling the default would leave every unspecified turn pointing at an
	// agent the tenant has switched off, which is a broken product rather than
	// a configuration. Move the default first.
	if current.IsDefault && !a.Enabled {
		return nil, fmt.Errorf("%w: make another agent the default before disabling this one", domain.ErrConflict)
	}

	if err := s.repo.Update(ctx, a); err != nil {
		if errors.Is(err, domain.ErrAlreadyExists) {
			return nil, fmt.Errorf("%w: an agent called %q already exists", domain.ErrAlreadyExists, a.Name)
		}
		return nil, err
	}
	a.CreatedAt = current.CreatedAt
	return a, nil
}

// Delete removes an agent, subject to the two rules that keep a roster usable:
// the last agent cannot go, and the default cannot go while there is another
// agent to hold the flag. Both are refusals rather than silent promotions —
// "which one is the default now?" should never be answered by a delete.
func (s *AgentService) Delete(ctx context.Context, companyID, id string) error {
	agents, err := s.repo.ListByCompany(ctx, companyID)
	if err != nil {
		return err
	}
	idx := slices.IndexFunc(agents, func(a *domain.Agent) bool { return a.ID == id })
	if idx < 0 {
		return domain.ErrNotFound
	}
	switch {
	case len(agents) == 1:
		return fmt.Errorf("%w: a company needs at least one agent", domain.ErrConflict)
	case agents[idx].IsDefault:
		return fmt.Errorf("%w: make another agent the default before deleting this one", domain.ErrConflict)
	}
	if err := s.repo.Delete(ctx, companyID, id); err != nil {
		return err
	}
	logrus.WithFields(logrus.Fields{"company_id": companyID, "agent_id": id}).Info("agent deleted")
	return nil
}

// SetDefault moves the flag to one agent. A disabled agent is refused: the
// default is what an unspecified turn resolves to, so making it unrunnable is
// the same outcome as having no default at all.
func (s *AgentService) SetDefault(ctx context.Context, companyID, id string) error {
	a, err := s.repo.GetByID(ctx, companyID, id)
	if err != nil {
		return err
	}
	if !a.Enabled {
		return fmt.Errorf("%w: enable %q before making it the default", domain.ErrConflict, a.Name)
	}
	if err := s.repo.SetDefault(ctx, companyID, id); err != nil {
		return err
	}
	logrus.WithFields(logrus.Fields{"company_id": companyID, "agent_id": id}).Info("default agent changed")
	return nil
}

// EnsureDefault gives a brand-new company the same starting roster the
// migration's backfill gave every existing one: a single unrestricted default.
//
// It is called from signup rather than lazily on first read, because a read
// path that writes is a read path that races itself. It is idempotent — a
// company that already has an agent is left alone — and its failure is logged
// rather than returned, since a tenant with an empty roster can create one
// from the dashboard, while a signup that fails over it leaves a half-built
// company nobody can log into.
func (s *AgentService) EnsureDefault(ctx context.Context, companyID string) {
	existing, err := s.repo.ListByCompany(ctx, companyID)
	if err != nil {
		logrus.WithError(err).WithField("company_id", companyID).Warn("agent roster seed skipped")
		return
	}
	if len(existing) > 0 {
		return
	}
	a := &domain.Agent{
		CompanyID:    companyID,
		Name:         defaultAgentName,
		Description:  defaultAgentDescription,
		AllowedTools: []string{},
		SourceIDs:    []string{},
		IsDefault:    true,
		Enabled:      true,
	}
	if err := s.repo.Create(ctx, a); err != nil {
		logrus.WithError(err).WithField("company_id", companyID).Warn("agent roster seed failed")
		return
	}
	logrus.WithFields(logrus.Fields{"company_id": companyID, "agent_id": a.ID}).Info("default agent seeded")
}

// The seeded agent's name and description match 030_agents' backfill exactly.
// A tenant who signed up before the migration and one who signed up after
// should not be able to tell which they are.
const (
	defaultAgentName        = "Analyst"
	defaultAgentDescription = "General analytics assistant"
)

// validated turns submitted input into an agent, or into the reason it is not
// one. Both allowlists are checked against reality — the live tool registry
// and the company's own connections — because an allowlist naming something
// that does not exist is indistinguishable, later, from one that was never
// meant to include it.
func (s *AgentService) validated(ctx context.Context, companyID string, in AgentInput) (*domain.Agent, error) {
	name := strings.TrimSpace(in.Name)
	description := strings.TrimSpace(in.Description)
	persona := strings.TrimSpace(in.PersonaPrompt)
	switch {
	case companyID == "":
		return nil, fmt.Errorf("%w: a company is required", domain.ErrInvalidInput)
	case name == "":
		return nil, fmt.Errorf("%w: an agent needs a name", domain.ErrInvalidInput)
	case len([]rune(name)) > agentNameMax:
		return nil, fmt.Errorf("%w: name must be %d characters or fewer", domain.ErrInvalidInput, agentNameMax)
	case len([]rune(description)) > agentDescriptionMax:
		return nil, fmt.Errorf("%w: description must be %d characters or fewer", domain.ErrInvalidInput, agentDescriptionMax)
	case len([]rune(persona)) > agentPersonaMax:
		return nil, fmt.Errorf("%w: instructions must be %d characters or fewer", domain.ErrInvalidInput, agentPersonaMax)
	}

	tools, err := s.normalizeTools(in.AllowedTools)
	if err != nil {
		return nil, err
	}
	sources, err := s.normalizeSources(ctx, companyID, in.SourceIDs)
	if err != nil {
		return nil, err
	}
	return &domain.Agent{
		CompanyID:     companyID,
		Name:          name,
		Description:   description,
		PersonaPrompt: persona,
		AllowedTools:  tools,
		SourceIDs:     sources,
	}, nil
}

// normalizeTools deduplicates and orders the allowlist by the registry, so two
// agents ticked in different orders store the same array and a diff of the two
// rows shows a real difference.
func (s *AgentService) normalizeTools(in []string) ([]string, error) {
	want := map[string]bool{}
	mcp := map[string]bool{}
	for _, t := range in {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, mcptools.NamePrefix) {
			// A per-company MCP tool (T-M2). Its name cannot be checked against
			// the static registry — the set is per company and discovered at
			// runtime — so what is validated here is the reserved namespace, and
			// the turn-time provider is what actually gates it: a name bound to no
			// approved, read-only, in-scope tool never appears in the turn's list,
			// so scoping to a stale one silently reaches nothing rather than
			// erroring. Validation against the company's live approved set arrives
			// with T-M3's binding UI, which has the company in hand.
			mcp[t] = true
			continue
		}
		if !slices.Contains(s.tools, t) {
			// Named, not counted: "unknown tool" without the name sends an
			// admin back to the checkboxes to find which one.
			return nil, fmt.Errorf("%w: no tool called %q on this deployment", domain.ErrInvalidInput, t)
		}
		want[t] = true
	}
	out := make([]string, 0, len(want)+len(mcp))
	for _, t := range s.tools {
		if want[t] {
			out = append(out, t)
		}
	}
	// MCP names after the static ones and sorted, so two agents scoped to the
	// same set store the same array and a diff of the two rows shows a real
	// difference — the same reason the static half is ordered by the registry.
	mcpNames := make([]string, 0, len(mcp))
	for t := range mcp {
		mcpNames = append(mcpNames, t)
	}
	sort.Strings(mcpNames)
	return append(out, mcpNames...), nil
}

// normalizeSources checks every id against the company's own connections. The
// repository re-checks this on write; doing it here is what turns "your
// warehouse is not on the list" into a sentence rather than a silently shorter
// allowlist.
func (s *AgentService) normalizeSources(ctx context.Context, companyID string, in []string) ([]string, error) {
	if len(in) == 0 {
		return []string{}, nil
	}
	conns, err := s.conns.ListByCompany(ctx, companyID)
	if err != nil {
		return nil, err
	}
	owned := make(map[string]bool, len(conns))
	for _, c := range conns {
		owned[c.ID] = true
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, id := range in {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		if !owned[id] {
			// Same answer for a source that does not exist and one belonging
			// to another company: this route must not confirm the second.
			return nil, fmt.Errorf("%w: no such data source", domain.ErrInvalidInput)
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}
