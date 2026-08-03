package app

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/agenttemplates"
	"github.com/fauzanebd/argentum/internal/domain"
)

// fakeAgents is an in-memory stand-in for postgres.AgentRepo (T-S1).
//
// It reproduces the three guarantees the schema enforces rather than the
// service: the unique index on (company_id, lower(name)), the partial unique
// index that allows one default per company, and company scoping on every
// read. A fake without them would let this suite pass on a service that has
// none — the "finance"/"Finance" case in particular is a database index, not a
// Go comparison, and testing it against a map keyed on the raw name would
// prove nothing.
type fakeAgents struct {
	byID map[string]*domain.Agent
	seq  int
	// failCreate makes the next Create fail, for the seeding path that has to
	// survive a repository error without failing a signup.
	failCreate bool
}

func newFakeAgents() *fakeAgents { return &fakeAgents{byID: map[string]*domain.Agent{}} }

func (f *fakeAgents) nameTaken(companyID, name, exceptID string) bool {
	for _, a := range f.byID {
		if a.CompanyID == companyID && a.ID != exceptID && strings.EqualFold(a.Name, name) {
			return true
		}
	}
	return false
}

func (f *fakeAgents) Create(_ context.Context, a *domain.Agent) error {
	if f.failCreate {
		return errors.New("connection refused")
	}
	if f.nameTaken(a.CompanyID, a.Name, "") {
		return domain.ErrAlreadyExists
	}
	if a.IsDefault {
		for _, other := range f.byID {
			if other.CompanyID == a.CompanyID && other.IsDefault {
				return domain.ErrAlreadyExists
			}
		}
	}
	f.seq++
	a.ID = "a" + string(rune('0'+f.seq))
	a.CreatedAt = time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	a.UpdatedAt = a.CreatedAt
	cp := *a
	f.byID[a.ID] = &cp
	return nil
}

func (f *fakeAgents) GetByID(_ context.Context, companyID, id string) (*domain.Agent, error) {
	a, ok := f.byID[id]
	if !ok || a.CompanyID != companyID {
		return nil, domain.ErrNotFound
	}
	cp := *a
	return &cp, nil
}

// GetDefault is the turn-time read T-S2 added. The fake answers it the way the
// partial unique index does: at most one row can be the default, so the first
// match is the only match.
func (f *fakeAgents) GetDefault(_ context.Context, companyID string) (*domain.Agent, error) {
	for _, a := range f.byID {
		if a.CompanyID == companyID && a.IsDefault {
			cp := *a
			return &cp, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (f *fakeAgents) ListByCompany(_ context.Context, companyID string) ([]*domain.Agent, error) {
	var out []*domain.Agent
	for _, a := range f.byID {
		if a.CompanyID == companyID {
			cp := *a
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (f *fakeAgents) Update(_ context.Context, a *domain.Agent) error {
	cur, ok := f.byID[a.ID]
	if !ok || cur.CompanyID != a.CompanyID {
		return domain.ErrNotFound
	}
	if f.nameTaken(a.CompanyID, a.Name, a.ID) {
		return domain.ErrAlreadyExists
	}
	cp := *a
	cp.IsDefault = cur.IsDefault
	cp.CreatedAt = cur.CreatedAt
	f.byID[a.ID] = &cp
	return nil
}

func (f *fakeAgents) Delete(_ context.Context, companyID, id string) error {
	a, ok := f.byID[id]
	if !ok || a.CompanyID != companyID {
		return domain.ErrNotFound
	}
	delete(f.byID, id)
	return nil
}

func (f *fakeAgents) SetDefault(_ context.Context, companyID, agentID string) error {
	target, ok := f.byID[agentID]
	if !ok || target.CompanyID != companyID {
		return domain.ErrNotFound
	}
	for _, a := range f.byID {
		if a.CompanyID == companyID {
			a.IsDefault = false
		}
	}
	target.IsDefault = true
	return nil
}

// fakeConns answers only the one question the roster asks of connections:
// which sources does this company own. Every other method exists to satisfy
// domain.ConnectionRepository and is unreachable from AgentService.
type fakeConns struct {
	byCompany map[string][]*domain.DBConnection
}

func (f *fakeConns) ListByCompany(_ context.Context, companyID string) ([]*domain.DBConnection, error) {
	return f.byCompany[companyID], nil
}
func (f *fakeConns) Create(context.Context, *domain.DBConnection) error { return nil }
func (f *fakeConns) GetByID(context.Context, string) (*domain.DBConnection, error) {
	return nil, domain.ErrNotFound
}
func (f *fakeConns) GetDefaultForCompany(context.Context, string) (*domain.DBConnection, error) {
	return nil, domain.ErrNotFound
}
func (f *fakeConns) Update(context.Context, *domain.DBConnection) error { return nil }
func (f *fakeConns) Delete(context.Context, string) error               { return nil }
func (f *fakeConns) SetDefault(context.Context, string, string) error   { return nil }

const (
	companyA = "co-a"
	companyB = "co-b"
)

// registry is a stand-in for what tools.Names returns on a deployment with
// object storage. The service only ever checks membership.
var registry = []string{"list_sources", "get_schema", "run_sql", "generate_document"}

func newAgentFixture() (*AgentService, *fakeAgents, *fakeConns) {
	repo := newFakeAgents()
	conns := &fakeConns{byCompany: map[string][]*domain.DBConnection{
		companyA: {{ID: "src-a1", CompanyID: companyA}, {ID: "src-a2", CompanyID: companyA}},
		companyB: {{ID: "src-b1", CompanyID: companyB}},
	}}
	return NewAgentService(repo, conns, registry), repo, conns
}

func mustCreate(t *testing.T, svc *AgentService, companyID string, in AgentInput) *domain.Agent {
	t.Helper()
	a, err := svc.Create(context.Background(), companyID, in)
	if err != nil {
		t.Fatalf("Create(%q): %v", in.Name, err)
	}
	return a
}

// The first agent a company has is its default. Nothing else can make one:
// there is no state in which a company holds agents and no default, because
// T-S2 resolves an unspecified thread to exactly that row.
func TestCreateFirstAgentBecomesDefault(t *testing.T) {
	svc, _, _ := newAgentFixture()

	first := mustCreate(t, svc, companyA, AgentInput{Name: "Analyst"})
	if !first.IsDefault {
		t.Error("first agent is not the default")
	}
	second := mustCreate(t, svc, companyA, AgentInput{Name: "Finance"})
	if second.IsDefault {
		t.Error("second agent claimed the default")
	}
}

// The acceptance case: an agent scoped to one source and a subset of tools
// comes back out of the roster as it went in.
func TestCreateRoundTripsScope(t *testing.T) {
	svc, _, _ := newAgentFixture()

	created := mustCreate(t, svc, companyA, AgentInput{
		Name:          "  Finance  ",
		Description:   "Revenue and margin questions",
		PersonaPrompt: "Answer in the finance team's vocabulary.",
		// Out of registry order and with a duplicate, both of which the
		// service normalises away.
		AllowedTools: []string{"run_sql", "get_schema", "run_sql"},
		SourceIDs:    []string{"src-a2"},
	})
	if created.Name != "Finance" {
		t.Errorf("name = %q, want the trimmed %q", created.Name, "Finance")
	}
	if got, want := strings.Join(created.AllowedTools, ","), "get_schema,run_sql"; got != want {
		t.Errorf("allowed_tools = %q, want %q in registry order with no duplicate", got, want)
	}

	got, err := svc.Get(context.Background(), companyA, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.SourceIDs) != 1 || got.SourceIDs[0] != "src-a2" {
		t.Errorf("source_ids = %v, want [src-a2]", got.SourceIDs)
	}
	if got.AllowsSource("src-a1") {
		t.Error("a scoped agent allows a source it was not given")
	}
	if !got.AllowsSource("src-a2") {
		t.Error("a scoped agent refuses the source it was given")
	}
}

// Empty means unrestricted, for both allowlists. This is the rule the whole
// backfill depends on: an agent created with nothing ticked behaves exactly
// like the single agent every tenant had before the roster existed.
func TestEmptyAllowlistsAreUnrestricted(t *testing.T) {
	svc, _, _ := newAgentFixture()

	a := mustCreate(t, svc, companyA, AgentInput{Name: "Analyst"})
	if !a.AllowsTool("run_sql") || !a.AllowsTool("a_tool_added_next_year") {
		t.Error("an empty tool allowlist refused a tool")
	}
	if !a.AllowsSource("src-a1") || !a.AllowsSource("src-b1") {
		t.Error("an empty source allowlist refused a source")
	}
}

// lower(name) uniqueness, not raw: "Finance" and "finance" in one picker is a
// support ticket.
func TestCreateRejectsCaseInsensitiveDuplicateName(t *testing.T) {
	svc, _, _ := newAgentFixture()
	mustCreate(t, svc, companyA, AgentInput{Name: "Finance"})

	_, err := svc.Create(context.Background(), companyA, AgentInput{Name: "finance"})
	if !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("Create duplicate: err = %v, want ErrAlreadyExists", err)
	}
	// Another company's roster is its own namespace.
	if _, err := svc.Create(context.Background(), companyB, AgentInput{Name: "finance"}); err != nil {
		t.Fatalf("Create in a second company: %v", err)
	}
}

// An allowlist naming something that does not exist is indistinguishable,
// later, from one that was never meant to include it — so it is refused at the
// door, by name.
func TestCreateRejectsUnknownTool(t *testing.T) {
	svc, _, _ := newAgentFixture()

	_, err := svc.Create(context.Background(), companyA, AgentInput{
		Name: "Ops", AllowedTools: []string{"run_sql", "delete_everything"},
	})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if !strings.Contains(err.Error(), "delete_everything") {
		t.Errorf("err = %q, want it to name the offending tool", err)
	}
}

// A source belonging to another company and a source that does not exist get
// the same answer. This route must not confirm the first.
func TestCreateRejectsForeignSource(t *testing.T) {
	svc, _, _ := newAgentFixture()

	for _, id := range []string{"src-b1", "src-nonexistent"} {
		_, err := svc.Create(context.Background(), companyA, AgentInput{
			Name: "Ops-" + id, SourceIDs: []string{id},
		})
		if !errors.Is(err, domain.ErrInvalidInput) {
			t.Errorf("source %q: err = %v, want ErrInvalidInput", id, err)
		}
		if err != nil && strings.Contains(err.Error(), companyB) {
			t.Errorf("source %q: err = %q leaks the owning company", id, err)
		}
	}
}

// An edit does not have to answer for a tool the row already holds.
//
// Migration 043 writes generate_document into every scoped agent, and object
// storage is per-deployment: on one without a bucket the tool is not in the
// registry, so the stored name is one this service cannot validate. Refusing
// the save would show an admin "no tool called generate_document on this
// deployment" over a checkbox they never ticked, on an edit to the agent's
// name. The name is carried through instead and reaches nothing until the
// deployment has storage — filterTools matches by name and does not find it.
func TestUpdateKeepsAStoredToolThisDeploymentCannotRegister(t *testing.T) {
	svc, repo, conns := newAgentFixture()
	ctx := context.Background()
	mustCreate(t, svc, companyA, AgentInput{Name: "Analyst"})
	stored := mustCreate(t, svc, companyA, AgentInput{
		Name: "Sales", AllowedTools: []string{"run_sql", "generate_document"},
	})

	// The same rows, seen by a process with no object storage.
	withoutStorage := slices.DeleteFunc(slices.Clone(registry),
		func(n string) bool { return n == "generate_document" })
	storageless := NewAgentService(repo, conns, withoutStorage)

	got, err := storageless.Update(ctx, companyA, stored.ID, AgentInput{
		Name: "Sales", Description: "edited", AllowedTools: stored.AllowedTools,
	})
	if err != nil {
		t.Fatalf("editing an agent holding a tool this deployment does not run: %v", err)
	}
	if !slices.Contains(got.AllowedTools, "generate_document") {
		t.Errorf("allowed_tools = %v, want generate_document kept; the agent would lose it "+
			"permanently the first time somebody renamed it", got.AllowedTools)
	}

	// The check the tolerance must not swallow: a name the row does not hold is
	// still refused, by name.
	_, err = storageless.Update(ctx, companyA, stored.ID, AgentInput{
		Name: "Sales", AllowedTools: []string{"run_sql", "delete_everything"},
	})
	if !errors.Is(err, domain.ErrInvalidInput) || !strings.Contains(err.Error(), "delete_everything") {
		t.Errorf("err = %v, want ErrInvalidInput naming delete_everything", err)
	}

	// And a create cannot invent one: there is no row yet, so nothing to keep.
	if _, err := storageless.Create(ctx, companyA, AgentInput{
		Name: "New", AllowedTools: []string{"generate_document"},
	}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("creating with an unregistered tool: err = %v, want ErrInvalidInput", err)
	}
}

// A company cannot read or edit another company's agent by id. Not found, not
// forbidden: the id is a bare uuid in a URL, and a 403 would confirm the row.
func TestCrossCompanyAccessIsNotFound(t *testing.T) {
	svc, _, _ := newAgentFixture()
	a := mustCreate(t, svc, companyA, AgentInput{Name: "Finance"})
	ctx := context.Background()

	if _, err := svc.Get(ctx, companyB, a.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Get across companies: err = %v, want ErrNotFound", err)
	}
	if _, err := svc.Update(ctx, companyB, a.ID, AgentInput{Name: "Stolen"}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Update across companies: err = %v, want ErrNotFound", err)
	}
	if err := svc.Delete(ctx, companyB, a.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Delete across companies: err = %v, want ErrNotFound", err)
	}
	if err := svc.SetDefault(ctx, companyB, a.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("SetDefault across companies: err = %v, want ErrNotFound", err)
	}
}

// The two rules that keep a roster usable. Both are refusals rather than
// silent promotions: "which one is the default now?" should never be answered
// by a delete.
func TestDeleteRefusesLastAgentAndDefault(t *testing.T) {
	svc, _, _ := newAgentFixture()
	ctx := context.Background()
	first := mustCreate(t, svc, companyA, AgentInput{Name: "Analyst"})

	if err := svc.Delete(ctx, companyA, first.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("deleting the last agent: err = %v, want ErrConflict", err)
	}

	second := mustCreate(t, svc, companyA, AgentInput{Name: "Finance"})
	if err := svc.Delete(ctx, companyA, first.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("deleting the default: err = %v, want ErrConflict", err)
	}
	// Move the flag, and the same delete goes through.
	if err := svc.SetDefault(ctx, companyA, second.ID); err != nil {
		t.Fatalf("SetDefault: %v", err)
	}
	if err := svc.Delete(ctx, companyA, first.ID); err != nil {
		t.Fatalf("Delete after moving the default: %v", err)
	}
	left, err := svc.List(ctx, companyA)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(left) != 1 || !left[0].IsDefault {
		t.Errorf("roster = %d agents, want 1 holding the default", len(left))
	}
}

// The default is what an unspecified turn resolves to, so it must stay
// runnable: neither disabling it in place nor promoting a disabled agent is
// allowed to leave a company whose default cannot run.
func TestDefaultStaysRunnable(t *testing.T) {
	svc, _, _ := newAgentFixture()
	ctx := context.Background()
	def := mustCreate(t, svc, companyA, AgentInput{Name: "Analyst"})
	off := false
	other := mustCreate(t, svc, companyA, AgentInput{Name: "Finance", Enabled: &off})

	if _, err := svc.Update(ctx, companyA, def.ID, AgentInput{Name: "Analyst", Enabled: &off}); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("disabling the default: err = %v, want ErrConflict", err)
	}
	if err := svc.SetDefault(ctx, companyA, other.ID); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("promoting a disabled agent: err = %v, want ErrConflict", err)
	}
}

// Update leaves enabled alone when the field is absent. A plain bool here
// would disable every agent edited by a client that did not know the field
// existed.
func TestUpdateOmittingEnabledLeavesItAlone(t *testing.T) {
	svc, _, _ := newAgentFixture()
	ctx := context.Background()
	mustCreate(t, svc, companyA, AgentInput{Name: "Analyst"})
	off := false
	target := mustCreate(t, svc, companyA, AgentInput{Name: "Finance", Enabled: &off})

	got, err := svc.Update(ctx, companyA, target.ID, AgentInput{Name: "Finance", Description: "edited"})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.Enabled {
		t.Error("an update with no enabled field re-enabled a disabled agent")
	}
}

// The persona is bounded because it is appended to the system prompt on every
// turn this agent takes, on the tenant's own credits, with no meter in front
// of whoever pasted it.
func TestValidationBounds(t *testing.T) {
	svc, _, _ := newAgentFixture()
	ctx := context.Background()

	for _, tc := range []struct {
		name string
		in   AgentInput
	}{
		{"empty name", AgentInput{Name: "   "}},
		{"long name", AgentInput{Name: strings.Repeat("x", agentNameMax+1)}},
		{"long description", AgentInput{Name: "Ops", Description: strings.Repeat("x", agentDescriptionMax+1)}},
		{"long persona", AgentInput{Name: "Ops", PersonaPrompt: strings.Repeat("x", agentPersonaMax+1)}},
	} {
		if _, err := svc.Create(ctx, companyA, tc.in); !errors.Is(err, domain.ErrInvalidInput) {
			t.Errorf("%s: err = %v, want ErrInvalidInput", tc.name, err)
		}
	}
}

// A company created after migration 030 gets the same starting roster the
// backfill gave every company that predates it — and a signup does not fail
// because the seed did.
func TestEnsureDefaultSeedsOnceAndSurvivesFailure(t *testing.T) {
	svc, repo, _ := newAgentFixture()
	ctx := context.Background()

	svc.EnsureDefault(ctx, companyA)
	svc.EnsureDefault(ctx, companyA)
	seeded, err := svc.List(ctx, companyA)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(seeded) != 1 {
		t.Fatalf("roster = %d agents after two seeds, want 1", len(seeded))
	}
	if !seeded[0].IsDefault || seeded[0].Name != defaultAgentName {
		t.Errorf("seeded agent = %+v, want the default %q", seeded[0], defaultAgentName)
	}
	if len(seeded[0].AllowedTools) != 0 || len(seeded[0].SourceIDs) != 0 {
		t.Error("the seeded agent is scoped; it must be unrestricted or it changes behaviour")
	}

	// A repository failure is logged and swallowed: the alternative is a
	// signup that fails after the company row is already written.
	repo.failCreate = true
	svc.EnsureDefault(ctx, companyB)
	if left, _ := svc.List(ctx, companyB); len(left) != 0 {
		t.Errorf("company B roster = %d agents, want 0 after a failed seed", len(left))
	}
}

// --- templates (T-B3) --------------------------------------------------------

// A two-card gallery, one of which suggests a tool this fixture's registry
// does not run. That second card is the generate_document case in miniature:
// the release knows the tool, this deployment does not, and the difference has
// to reach the form rather than the first save.
func templateFixture(t *testing.T) *agenttemplates.Set {
	t.Helper()
	set, err := agenttemplates.New(agenttemplates.Config{Templates: []agenttemplates.Template{
		{
			Key: "finance", Name: "Finance", Description: "Money questions.",
			Persona:          "You serve the finance function of the business described above.",
			SuggestedTools:   []string{"get_schema", "run_sql", "generate_document"},
			SourceHints:      []string{"invoice", "ledger"},
			StarterQuestions: []string{"What was revenue last month?"},
		},
		{
			Key: "ops", Name: "Operations", Description: "Throughput questions.",
			Persona:        "You serve the operations function of the business described above.",
			SuggestedTools: []string{"get_schema", "run_sql", "create_visualization"},
		},
	}}, []string{"get_schema", "run_sql", "generate_document", "create_visualization"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return set
}

func newTemplatedFixture(t *testing.T) (*AgentService, *fakeAgents) {
	t.Helper()
	svc, repo, _ := newAgentFixture()
	return svc.WithTemplates(templateFixture(t)), repo
}

// What saves is an ordinary roster row. The key rides along for analytics and
// for the starter questions the chat offers; nothing else reads it.
func TestCreateFromATemplateRecordsItsKey(t *testing.T) {
	svc, _ := newTemplatedFixture(t)

	a := mustCreate(t, svc, companyA, AgentInput{
		Name:          "Finance",
		PersonaPrompt: "You serve the finance function of the business described above.",
		AllowedTools:  []string{"get_schema", "run_sql"},
		TemplateKey:   "finance",
	})
	if a.TemplateKey != "finance" {
		t.Errorf("template_key = %q, want finance", a.TemplateKey)
	}

	// And the blank path stores nothing, which is also what every agent
	// created before this column existed carries.
	blank := mustCreate(t, svc, companyA, AgentInput{Name: "Analyst"})
	if blank.TemplateKey != "" {
		t.Errorf("blank agent template_key = %q, want empty", blank.TemplateKey)
	}
}

// A provenance column that accepts arbitrary strings answers no analytics
// question. The name is in the error because the caller sent it.
func TestCreateRejectsUnknownTemplateKey(t *testing.T) {
	svc, _ := newTemplatedFixture(t)

	_, err := svc.Create(context.Background(), companyA, AgentInput{
		Name: "Finance", TemplateKey: "marketing",
	})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if !strings.Contains(err.Error(), "marketing") {
		t.Errorf("err = %q, want it to name the key", err)
	}
}

// A wiring with no gallery is the product as it was before this ticket: the
// blank path works, and a key nobody can have been offered is refused.
func TestWithNoGalleryOnlyTheBlankPathWorks(t *testing.T) {
	svc, _, _ := newAgentFixture()
	ctx := context.Background()

	if got := svc.Templates(); len(got) != 0 {
		t.Errorf("Templates() = %v, want empty with no gallery loaded", got)
	}
	if _, err := svc.Create(ctx, companyA, AgentInput{Name: "Analyst"}); err != nil {
		t.Fatalf("the blank path failed with no gallery: %v", err)
	}
	if _, err := svc.Create(ctx, companyA, AgentInput{Name: "Finance", TemplateKey: "finance"}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("err = %v, want ErrInvalidInput", err)
	}
}

// The two halves of "the tenant owns the text from the moment they save":
// edits persist, and the provenance does not change with them. An update that
// carried a template_key from the client could relabel an agent's origin, and
// one that dropped it would erase the only record of where it came from.
func TestEditingATemplatedAgentKeepsItsProvenance(t *testing.T) {
	svc, _ := newTemplatedFixture(t)
	ctx := context.Background()

	a := mustCreate(t, svc, companyA, AgentInput{
		Name: "Finance", PersonaPrompt: "the template's words", TemplateKey: "finance",
	})

	edited, err := svc.Update(ctx, companyA, a.ID, AgentInput{
		Name: "Finance", PersonaPrompt: "the tenant's own words",
		// A client asserting a different origin is ignored rather than refused:
		// the field is create-only, and an update that never meant to touch it
		// must not fail because it echoed back what it read.
		TemplateKey: "ops",
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if edited.PersonaPrompt != "the tenant's own words" {
		t.Errorf("persona = %q, want the tenant's edit", edited.PersonaPrompt)
	}
	if edited.TemplateKey != "finance" {
		t.Errorf("template_key = %q after an edit, want finance", edited.TemplateKey)
	}
}

// The narrowing that keeps a card from pre-ticking a tool this deployment
// cannot save. The fixture registry has no create_visualization, so the Ops
// card must lose it — and the Finance card must keep generate_document, which
// this deployment does run.
func TestTemplatesAreNarrowedToThisDeploymentsRegistry(t *testing.T) {
	svc, _ := newTemplatedFixture(t)

	got := svc.Templates()
	if len(got) != 2 {
		t.Fatalf("Templates() = %d cards, want 2", len(got))
	}
	byKey := map[string][]string{}
	for _, tpl := range got {
		byKey[tpl.Key] = tpl.SuggestedTools
	}
	if slices.Contains(byKey["ops"], "create_visualization") {
		t.Errorf("ops still suggests create_visualization, which this deployment does not run: %v", byKey["ops"])
	}
	if !slices.Contains(byKey["finance"], "generate_document") {
		t.Errorf("finance lost generate_document, which this deployment does run: %v", byKey["finance"])
	}

	// The narrowed list has to be savable as-is — which is the whole point,
	// since it is what the form submits.
	a := mustCreate(t, svc, companyA, AgentInput{
		Name: "Operations", TemplateKey: "ops", AllowedTools: byKey["ops"],
	})
	if len(a.AllowedTools) != len(byKey["ops"]) {
		t.Errorf("saved tools = %v, want %v", a.AllowedTools, byKey["ops"])
	}
}
