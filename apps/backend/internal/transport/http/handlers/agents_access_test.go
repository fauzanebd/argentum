package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/authz"
	"github.com/fauzanebd/argentum/internal/domain"
)

// T-Z4's roster half: "the picker omits what the caller may not reach, and the
// count in the UI matches." The picker is this payload, so the count is
// whatever these routes send.

// fakeAgentRepo is a three-agent roster for co-1, default first.
type fakeAgentRepo struct{ agents []*domain.Agent }

func newFakeAgentRepo() *fakeAgentRepo {
	return &fakeAgentRepo{agents: []*domain.Agent{
		{ID: "ag-fin", CompanyID: "co-1", Name: "Finance", Enabled: true, IsDefault: true},
		{ID: "ag-hr", CompanyID: "co-1", Name: "HR", Enabled: true},
		{ID: "ag-ops", CompanyID: "co-1", Name: "Ops", Enabled: true},
	}}
}

func (f *fakeAgentRepo) Create(context.Context, *domain.Agent) error { return nil }
func (f *fakeAgentRepo) GetByID(_ context.Context, companyID, id string) (*domain.Agent, error) {
	for _, a := range f.agents {
		if a.ID == id && a.CompanyID == companyID {
			return a, nil
		}
	}
	return nil, domain.ErrNotFound
}
func (f *fakeAgentRepo) GetDefault(context.Context, string) (*domain.Agent, error) {
	return f.agents[0], nil
}
func (f *fakeAgentRepo) ListByCompany(context.Context, string) ([]*domain.Agent, error) {
	return f.agents, nil
}
func (f *fakeAgentRepo) Update(context.Context, *domain.Agent) error      { return nil }
func (f *fakeAgentRepo) Delete(context.Context, string, string) error     { return nil }
func (f *fakeAgentRepo) SetDefault(context.Context, string, string) error { return nil }

// hrRestricted answers Visible as a grant store in which HR is restricted and
// nobody holds it.
type hrRestricted struct{ err error }

func (h hrRestricted) Visible(_ context.Context, _ authz.Subject, _ domain.ResourceKind, ids []string) ([]string, error) {
	if h.err != nil {
		return nil, h.err
	}
	var out []string
	for _, id := range ids {
		if id != "ag-hr" {
			out = append(out, id)
		}
	}
	return out, nil
}

func agentsRouter(t *testing.T, access AgentAccess, role string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("company_id", "co-1")
		c.Set("user_id", "u-1")
		c.Set("role", role)
		c.Next()
	})
	h := NewAgentsHandler(app.NewAgentService(newFakeAgentRepo(), nil, nil))
	if access != nil {
		h = h.WithAccess(access)
	}
	h.Register(r.Group(""))
	return r
}

func getRoster(t *testing.T, r *gin.Engine) (int, AgentsResponse, string) {
	t.Helper()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/agents", nil))
	var body AgentsResponse
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode roster: %v", err)
		}
	}
	return w.Code, body, w.Body.String()
}

func agentIDs(agents []*domain.Agent) []string {
	out := make([]string, 0, len(agents))
	for _, a := range agents {
		out = append(out, a.ID)
	}
	return out
}

func TestAMemberIsShownOnlyTheAgentsTheyMayTalkTo(t *testing.T) {
	code, body, raw := getRoster(t, agentsRouter(t, hrRestricted{}, "member"))
	if code != http.StatusOK {
		t.Fatalf("status = %d: %s", code, raw)
	}
	if got := agentIDs(body.Agents); !slices.Equal(got, []string{"ag-fin", "ag-ops"}) {
		t.Errorf("agents = %v, want [ag-fin ag-ops]", got)
	}
	if !slices.Equal(body.ReachableAgentIDs, []string{"ag-fin", "ag-ops"}) {
		t.Errorf("reachable_agent_ids = %v, want [ag-fin ag-ops]", body.ReachableAgentIDs)
	}
	if strings.Contains(raw, "HR") {
		t.Errorf("a member's roster names the agent they may not use: %s", raw)
	}
}

// Decision 4 is not bypassed here: the admin is *shown* HR, because Settings →
// Agents manages the roster from this payload, and is told they may not talk to
// it, which is what the chat picker filters by.
func TestAnAdminIsShownTheWholeRosterAndToldWhichTheyMayUse(t *testing.T) {
	code, body, raw := getRoster(t, agentsRouter(t, hrRestricted{}, "admin"))
	if code != http.StatusOK {
		t.Fatalf("status = %d: %s", code, raw)
	}
	if got := agentIDs(body.Agents); !slices.Equal(got, []string{"ag-fin", "ag-hr", "ag-ops"}) {
		t.Errorf("agents = %v, want the whole roster", got)
	}
	if !slices.Equal(body.ReachableAgentIDs, []string{"ag-fin", "ag-ops"}) {
		t.Errorf("reachable_agent_ids = %v, want [ag-fin ag-ops] — rank is not a grant", body.ReachableAgentIDs)
	}
}

func TestAMemberAskingForAHiddenAgentByIDGetsNotFound(t *testing.T) {
	get := func(role, id string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		agentsRouter(t, hrRestricted{}, role).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/agents/"+id, nil))
		return w
	}
	if w := get("member", "ag-hr"); w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "no such agent") {
		t.Errorf("member, restricted agent: %d %s, want 404 no such agent", w.Code, w.Body)
	}
	if w := get("member", "ag-nope"); w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "no such agent") {
		t.Errorf("member, missing agent: %d %s — must read the same as the restricted one", w.Code, w.Body)
	}
	if w := get("member", "ag-fin"); w.Code != http.StatusOK {
		t.Errorf("member, open agent: %d", w.Code)
	}
	if w := get("admin", "ag-hr"); w.Code != http.StatusOK {
		t.Errorf("admin, restricted agent: %d, want 200 — the admin manages the roster", w.Code)
	}
}

// A failed access read must not serve the unfiltered roster, which is exactly
// the list a member was not supposed to see.
func TestAFailedAccessReadDoesNotServeTheUnfilteredRoster(t *testing.T) {
	code, _, raw := getRoster(t, agentsRouter(t, hrRestricted{err: errors.New("control DB down")}, "member"))
	if code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: %s", code, raw)
	}
	if strings.Contains(raw, "HR") || strings.Contains(raw, "control DB") {
		t.Errorf("the refusal leaked the roster or the error: %s", raw)
	}
}

// With no access wired — every deployment before roadmap 12 — everybody is shown
// everything, and every agent is reachable, never a null list.
func TestWithNoAccessWiredEveryAgentIsReachable(t *testing.T) {
	code, body, raw := getRoster(t, agentsRouter(t, nil, "member"))
	if code != http.StatusOK {
		t.Fatalf("status = %d: %s", code, raw)
	}
	if len(body.Agents) != 3 || len(body.ReachableAgentIDs) != 3 {
		t.Errorf("agents=%d reachable=%d, want 3 and 3", len(body.Agents), len(body.ReachableAgentIDs))
	}
	if !strings.Contains(raw, `"reachable_agent_ids":[`) {
		t.Errorf("reachable_agent_ids is not a list: %s", raw)
	}
}
