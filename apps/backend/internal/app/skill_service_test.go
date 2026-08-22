package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/domain"
)

// A fake store rather than a live Postgres, for the reason retention_service's
// fake carries: what these tests are about is the decision layer — which caps
// refuse, which ids answer 404, what an empty binding means. That the CASCADE
// really takes bindings with a deleted skill is a property of the database and
// belongs in the live gate (§1p), where it can be proven against one.

type fakeSkillRepo struct {
	byID    map[string]*domain.Skill
	binding map[string][]string
	created []*domain.Skill
	count   int

	createErr error
	// bindingWrites records every SetAgentBinding call so a test can assert
	// the repository was not reached at all when validation refused.
	bindingWrites int
}

func newFakeSkillRepo(skills ...*domain.Skill) *fakeSkillRepo {
	f := &fakeSkillRepo{byID: map[string]*domain.Skill{}, binding: map[string][]string{}}
	for _, s := range skills {
		f.byID[s.ID] = s
	}
	f.count = len(skills)
	return f
}

func (f *fakeSkillRepo) Create(_ context.Context, s *domain.Skill) error {
	if f.createErr != nil {
		return f.createErr
	}
	s.ID = "skill-new"
	f.byID[s.ID] = s
	f.created = append(f.created, s)
	f.count++
	return nil
}

func (f *fakeSkillRepo) GetByID(_ context.Context, companyID, id string) (*domain.Skill, error) {
	s, ok := f.byID[id]
	if !ok || s.CompanyID != companyID {
		return nil, domain.ErrNotFound
	}
	c := *s
	return &c, nil
}

func (f *fakeSkillRepo) GetByName(_ context.Context, companyID, name string) (*domain.Skill, error) {
	for _, s := range f.byID {
		if s.CompanyID == companyID && strings.EqualFold(s.Name, name) {
			c := *s
			return &c, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (f *fakeSkillRepo) ListByCompany(_ context.Context, companyID string) ([]*domain.Skill, error) {
	return f.list(companyID, false), nil
}

func (f *fakeSkillRepo) ListEnabledForIndex(_ context.Context, companyID string) ([]*domain.Skill, error) {
	return f.list(companyID, true), nil
}

func (f *fakeSkillRepo) list(companyID string, enabledOnly bool) []*domain.Skill {
	out := []*domain.Skill{}
	for _, s := range f.byID {
		if s.CompanyID != companyID || (enabledOnly && !s.Enabled) {
			continue
		}
		out = append(out, s)
	}
	return out
}

func (f *fakeSkillRepo) Update(_ context.Context, s *domain.Skill) error {
	cur, ok := f.byID[s.ID]
	if !ok || cur.CompanyID != s.CompanyID {
		return domain.ErrNotFound
	}
	c := *s
	f.byID[s.ID] = &c
	return nil
}

func (f *fakeSkillRepo) Delete(_ context.Context, companyID, id string) error {
	s, ok := f.byID[id]
	if !ok || s.CompanyID != companyID {
		return domain.ErrNotFound
	}
	delete(f.byID, id)
	f.count--
	return nil
}

func (f *fakeSkillRepo) CountByCompany(context.Context, string) (int, error) { return f.count, nil }

func (f *fakeSkillRepo) SetAgentBinding(_ context.Context, _, agentID string, skillIDs []string) error {
	f.bindingWrites++
	f.binding[agentID] = skillIDs
	return nil
}

func (f *fakeSkillRepo) AgentBinding(_ context.Context, _, agentID string) ([]string, error) {
	return f.binding[agentID], nil
}

type fakeSkillAgents struct{ agents map[string]*domain.Agent }

func (f *fakeSkillAgents) Create(context.Context, *domain.Agent) error { return nil }
func (f *fakeSkillAgents) GetByID(_ context.Context, companyID, id string) (*domain.Agent, error) {
	a, ok := f.agents[id]
	if !ok || a.CompanyID != companyID {
		return nil, domain.ErrNotFound
	}
	return a, nil
}
func (f *fakeSkillAgents) GetDefault(context.Context, string) (*domain.Agent, error) {
	return nil, domain.ErrNotFound
}
func (f *fakeSkillAgents) ListByCompany(context.Context, string) ([]*domain.Agent, error) {
	return nil, nil
}
func (f *fakeSkillAgents) Update(context.Context, *domain.Agent) error         { return nil }
func (f *fakeSkillAgents) Delete(context.Context, string, string) error        { return nil }
func (f *fakeSkillAgents) SetDefault(context.Context, string, string) error    { return nil }
func (f *fakeSkillAgents) CountByCompany(context.Context, string) (int, error) { return 0, nil }

func skillFixture(companyID, id, name string) *domain.Skill {
	return &domain.Skill{
		ID:        id,
		CompanyID: companyID,
		Name:      name,
		WhenToUse: "The user asks for a weekly sales summary.",
		Body:      "1. Query fact_sales.\n2. Exclude cancelled orders.",
		Enabled:   true,
		Source:    domain.SkillSourceTenant,
	}
}

func TestCreateSkillRefusesPastThePerCompanyLimit(t *testing.T) {
	repo := newFakeSkillRepo()
	repo.count = domain.MaxSkillsPerCompany
	svc := NewSkillService(repo, &fakeSkillAgents{})

	_, err := svc.Create(context.Background(), "co-1", "user-1", skillFixture("co-1", "", "One more"))
	if !errors.Is(err, domain.ErrSkillLimit) {
		t.Fatalf("error = %v, want ErrSkillLimit", err)
	}
	if len(repo.created) != 0 {
		t.Error("the row was written despite the limit")
	}
	// One under the limit still saves — a cap that refuses at N-1 is a
	// different cap from the one the message names.
	repo.count = domain.MaxSkillsPerCompany - 1
	if _, err := svc.Create(context.Background(), "co-1", "user-1", skillFixture("co-1", "", "One more")); err != nil {
		t.Errorf("the last allowed skill was refused: %v", err)
	}
}

// A cross-tenant id must answer 404 and change nothing. Not 403: a directory
// that tells you which uuids exist is a directory.
func TestSkillReadAndUpdateAreScopedToTheCompany(t *testing.T) {
	theirs := skillFixture("co-2", "skill-theirs", "Their procedure")
	repo := newFakeSkillRepo(theirs)
	svc := NewSkillService(repo, &fakeSkillAgents{})

	if _, err := svc.Get(context.Background(), "co-1", "skill-theirs"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("cross-tenant Get error = %v, want ErrNotFound", err)
	}

	in := skillFixture("co-1", "", "Renamed by an outsider")
	if _, err := svc.Update(context.Background(), "co-1", "user-1", "skill-theirs", in); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("cross-tenant Update error = %v, want ErrNotFound", err)
	}
	if repo.byID["skill-theirs"].Name != "Their procedure" {
		t.Error("a cross-tenant update changed the row")
	}
}

// Update must not let a tenant relabel their own text as shipped-with-the-
// product, or move it to another company.
func TestUpdateSkillKeepsSourceAndOwnership(t *testing.T) {
	mine := skillFixture("co-1", "skill-1", "Mine")
	repo := newFakeSkillRepo(mine)
	svc := NewSkillService(repo, &fakeSkillAgents{})

	in := skillFixture("co-1", "skill-1", "Mine, edited")
	in.Source = domain.SkillSourceBuiltinPrefix + "forged"
	in.CompanyID = "co-2"
	in.CreatedBy = "user-someone-else"

	got, err := svc.Update(context.Background(), "co-1", "user-2", "skill-1", in)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.Source != domain.SkillSourceTenant {
		t.Errorf("source = %q; a tenant relabelled their own text as shipped", got.Source)
	}
	if got.CompanyID != "co-1" {
		t.Errorf("company = %q; the update moved the row between tenants", got.CompanyID)
	}
	if got.UpdatedBy != "user-2" {
		t.Errorf("updated_by = %q, want the caller", got.UpdatedBy)
	}
	if got.Name != "Mine, edited" {
		t.Errorf("name = %q; the edit did not land", got.Name)
	}
}

func TestSetAgentBindingRefusesAnotherCompanysSkill(t *testing.T) {
	mine := skillFixture("co-1", "skill-mine", "Mine")
	theirs := skillFixture("co-2", "skill-theirs", "Theirs")
	repo := newFakeSkillRepo(mine, theirs)
	agents := &fakeSkillAgents{agents: map[string]*domain.Agent{
		"agent-1": {ID: "agent-1", CompanyID: "co-1"},
	}}
	svc := NewSkillService(repo, agents)

	err := svc.SetAgentBinding(context.Background(), "co-1", "agent-1", []string{"skill-mine", "skill-theirs"})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("error = %v, want ErrInvalidInput", err)
	}
	if !strings.Contains(err.Error(), "skill-theirs") {
		t.Errorf("the refusal does not name the offending id: %v", err)
	}
	if repo.bindingWrites != 0 {
		t.Error("the binding was written despite one id belonging to another company")
	}
}

func TestSetAgentBindingRefusesAnotherCompanysAgent(t *testing.T) {
	repo := newFakeSkillRepo(skillFixture("co-1", "skill-1", "Mine"))
	agents := &fakeSkillAgents{agents: map[string]*domain.Agent{
		"agent-theirs": {ID: "agent-theirs", CompanyID: "co-2"},
	}}
	svc := NewSkillService(repo, agents)

	if err := svc.SetAgentBinding(context.Background(), "co-1", "agent-theirs", []string{"skill-1"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
	if repo.bindingWrites != 0 {
		t.Error("a binding was written onto another company's agent")
	}
}

// The empty binding is a write, not a no-op: it is how an admin says "offer
// this agent everything" after previously restricting it.
func TestSetAgentBindingAcceptsTheEmptyList(t *testing.T) {
	repo := newFakeSkillRepo(skillFixture("co-1", "skill-1", "Mine"))
	repo.binding["agent-1"] = []string{"skill-1"}
	agents := &fakeSkillAgents{agents: map[string]*domain.Agent{
		"agent-1": {ID: "agent-1", CompanyID: "co-1"},
	}}
	svc := NewSkillService(repo, agents)

	if err := svc.SetAgentBinding(context.Background(), "co-1", "agent-1", nil); err != nil {
		t.Fatalf("SetAgentBinding: %v", err)
	}
	if repo.bindingWrites != 1 {
		t.Fatal("clearing a binding did not reach the repository")
	}
	got, err := svc.AgentBinding(context.Background(), "co-1", "agent-1")
	if err != nil {
		t.Fatalf("AgentBinding: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("binding = %v, want empty — which means every enabled company skill", got)
	}
}
