package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
)

// What these tests are about is the wire: which status code a tenant's mistake
// gets, and whether the sentence that comes back names the thing they have to
// change. The decisions behind those errors are tested at the service.

// skillStore and agentStore are in-memory implementations of the two
// repositories this handler reaches. Written here rather than shared with the
// service's fakes because a handler test that imports another package's test
// helpers is a handler test that stops compiling when that package refactors.

type skillStore struct {
	skills  map[string]*domain.Skill
	binding map[string][]string
	count   int
	nextID  int
}

func newSkillStore() *skillStore {
	return &skillStore{skills: map[string]*domain.Skill{}, binding: map[string][]string{}}
}

func (s *skillStore) put(sk *domain.Skill) {
	s.skills[sk.ID] = sk
	s.count++
}

func (s *skillStore) Create(_ context.Context, sk *domain.Skill) error {
	s.nextID++
	sk.ID = fmt.Sprintf("skill-%d", s.nextID)
	s.skills[sk.ID] = sk
	s.count++
	return nil
}

func (s *skillStore) GetByID(_ context.Context, companyID, id string) (*domain.Skill, error) {
	sk, ok := s.skills[id]
	if !ok || sk.CompanyID != companyID {
		return nil, domain.ErrNotFound
	}
	c := *sk
	return &c, nil
}

func (s *skillStore) GetByName(_ context.Context, companyID, name string) (*domain.Skill, error) {
	for _, sk := range s.skills {
		if sk.CompanyID == companyID && strings.EqualFold(sk.Name, name) {
			c := *sk
			return &c, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (s *skillStore) ListByCompany(_ context.Context, companyID string) ([]*domain.Skill, error) {
	out := []*domain.Skill{}
	for _, sk := range s.skills {
		if sk.CompanyID == companyID {
			out = append(out, sk)
		}
	}
	return out, nil
}

func (s *skillStore) ListEnabledRankedForIndex(ctx context.Context, companyID string, _ []float32) ([]*domain.Skill, error) {
	return s.ListEnabledForIndex(ctx, companyID)
}

func (s *skillStore) ListUnembedded(context.Context, string) ([]*domain.Skill, error) {
	return nil, nil
}

func (s *skillStore) SetEmbedding(context.Context, string, *domain.Skill, []float32, string) error {
	return nil
}

func (s *skillStore) ListEnabledForIndex(ctx context.Context, companyID string) ([]*domain.Skill, error) {
	all, _ := s.ListByCompany(ctx, companyID)
	out := []*domain.Skill{}
	for _, sk := range all {
		if sk.Enabled {
			out = append(out, sk)
		}
	}
	return out, nil
}

func (s *skillStore) Update(_ context.Context, sk *domain.Skill) error {
	cur, ok := s.skills[sk.ID]
	if !ok || cur.CompanyID != sk.CompanyID {
		return domain.ErrNotFound
	}
	c := *sk
	s.skills[sk.ID] = &c
	return nil
}

func (s *skillStore) Delete(_ context.Context, companyID, id string) error {
	sk, ok := s.skills[id]
	if !ok || sk.CompanyID != companyID {
		return domain.ErrNotFound
	}
	delete(s.skills, id)
	s.count--
	return nil
}

func (s *skillStore) CountByCompany(context.Context, string) (int, error) { return s.count, nil }

func (s *skillStore) SetAgentBinding(_ context.Context, _, agentID string, ids []string) error {
	s.binding[agentID] = ids
	return nil
}

func (s *skillStore) AgentBinding(_ context.Context, _, agentID string) ([]string, error) {
	return s.binding[agentID], nil
}

type agentStore struct{ agents map[string]*domain.Agent }

func newAgentStore() *agentStore { return &agentStore{agents: map[string]*domain.Agent{}} }

func (a *agentStore) put(ag *domain.Agent) { a.agents[ag.ID] = ag }

func (a *agentStore) Create(context.Context, *domain.Agent) error { return nil }
func (a *agentStore) GetByID(_ context.Context, companyID, id string) (*domain.Agent, error) {
	ag, ok := a.agents[id]
	if !ok || ag.CompanyID != companyID {
		return nil, domain.ErrNotFound
	}
	return ag, nil
}
func (a *agentStore) GetDefault(context.Context, string) (*domain.Agent, error) {
	return nil, domain.ErrNotFound
}
func (a *agentStore) ListByCompany(context.Context, string) ([]*domain.Agent, error) {
	return nil, nil
}
func (a *agentStore) Update(context.Context, *domain.Agent) error         { return nil }
func (a *agentStore) Delete(context.Context, string, string) error        { return nil }
func (a *agentStore) SetDefault(context.Context, string, string) error    { return nil }
func (a *agentStore) CountByCompany(context.Context, string) (int, error) { return 0, nil }

func skillsRouter(t *testing.T, repo domain.SkillRepository, agents domain.AgentRepository) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("/api")
	g.Use(func(c *gin.Context) {
		c.Set("company_id", "co-1")
		c.Set("user_id", "user-9")
		c.Next()
	})
	NewSkillsHandler(app.NewSkillService(repo, agents)).Register(g)
	return r
}

func postSkill(t *testing.T, r *gin.Engine, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/skills", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

// A cap breach is a 400 whose body names the field and the number. A tenant
// looking at a form needs both; "invalid input" is a dead end.
func TestCreateSkillAnswers400NamingTheFieldAndTheLimit(t *testing.T) {
	repo := newSkillStore()
	r := skillsRouter(t, repo, newAgentStore())

	body, _ := json.Marshal(map[string]string{
		"name":        strings.Repeat("n", domain.MaxSkillNameChars+1),
		"when_to_use": "The user asks for the weekly report.",
		"body":        "1. Do the thing.",
	})
	w := postSkill(t, r, string(body))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "name") || !strings.Contains(w.Body.String(), "60") {
		t.Errorf("the refusal names neither the field nor the limit: %s", w.Body.String())
	}
}

// A full workspace is a 409, not a 400: the request is well-formed and the
// answer is "delete one first", which is a different action.
func TestCreateSkillAnswers409WhenTheWorkspaceIsFull(t *testing.T) {
	repo := newSkillStore()
	repo.count = domain.MaxSkillsPerCompany
	r := skillsRouter(t, repo, newAgentStore())

	w := postSkill(t, r, `{"name":"One more","when_to_use":"Whenever.","body":"Steps."}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", w.Code)
	}
}

// The default on create is enabled: a procedure somebody just typed is one they
// want used, and a save that appears to do nothing is the alternative.
func TestCreateSkillDefaultsToEnabled(t *testing.T) {
	repo := newSkillStore()
	r := skillsRouter(t, repo, newAgentStore())

	w := postSkill(t, r, `{"name":"Weekly report","when_to_use":"Weekly summaries.","body":"Steps."}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", w.Code, w.Body.String())
	}
	var got domain.Skill
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Enabled {
		t.Error("a newly created skill is disabled")
	}
	if got.Source != domain.SkillSourceTenant {
		t.Errorf("source = %q, want %q", got.Source, domain.SkillSourceTenant)
	}
}

// The write shape has no `source` field at all, so a client that sends one
// changes nothing — a tenant must not be able to label their own text as
// shipped-with-the-product.
func TestCreateSkillIgnoresAClientSuppliedSource(t *testing.T) {
	repo := newSkillStore()
	r := skillsRouter(t, repo, newAgentStore())

	w := postSkill(t, r, `{"name":"Forged","when_to_use":"Whenever.","body":"Steps.","source":"builtin:weekly-report"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", w.Code, w.Body.String())
	}
	var got domain.Skill
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.IsBuiltin() {
		t.Errorf("a client-supplied source was honoured: %q", got.Source)
	}
}

func TestGetSkillAnswers404ForAnotherCompany(t *testing.T) {
	repo := newSkillStore()
	repo.put(&domain.Skill{ID: "skill-theirs", CompanyID: "co-2", Name: "Theirs"})
	r := skillsRouter(t, repo, newAgentStore())

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/skills/skill-theirs", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

// The list carries the caps, so the form's counters are the server's numbers
// rather than a second copy that drifts from them.
func TestListSkillsServesTheCaps(t *testing.T) {
	r := skillsRouter(t, newSkillStore(), newAgentStore())

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/skills", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var got struct {
		Limits map[string]int `json:"limits"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for field, want := range map[string]int{
		"name_chars":        domain.MaxSkillNameChars,
		"when_to_use_chars": domain.MaxSkillWhenToUseChars,
		"body_chars":        domain.MaxSkillBodyChars,
		"per_company":       domain.MaxSkillsPerCompany,
	} {
		if got.Limits[field] != want {
			t.Errorf("limits[%s] = %d, want %d", field, got.Limits[field], want)
		}
	}
}

// The binding endpoint says which way it reads an empty list, because the
// identically-shaped MCP endpoint next door means the opposite by it.
func TestAgentBindingSaysWhatEmptyMeans(t *testing.T) {
	agents := newAgentStore()
	agents.put(&domain.Agent{ID: "agent-1", CompanyID: "co-1"})
	r := skillsRouter(t, newSkillStore(), agents)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/agents/agent-1/skills", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var got struct {
		SkillIDs []string `json:"skill_ids"`
		Means    string   `json:"means"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if len(got.SkillIDs) != 0 {
		t.Errorf("skill_ids = %v, want empty", got.SkillIDs)
	}
	if !strings.Contains(got.Means, "every enabled skill") {
		t.Errorf("means = %q; an unbound agent is offered everything and the wire should say so", got.Means)
	}
}
