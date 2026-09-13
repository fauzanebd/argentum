package handlers

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
)

// memResourceGrants is enough of 084 to drive the HTTP mapping: resources and
// users each belong to one company, and a request under any other company is
// not found. The lifecycle and the decision function are tested in internal/app
// and internal/authz; this file is about status codes and bodies.
type memResourceGrants struct {
	users  map[string]string // user → company
	agents map[string]string // agent id → company
	modes  map[string]domain.AccessMode
	grants map[string]domain.ResourceGrant // "user/agent"
	writes int
}

func newMemResourceGrants() *memResourceGrants {
	return &memResourceGrants{
		users:  map[string]string{"admin-1": "co-1", "u-1": "co-1", "u-9": "co-2"},
		agents: map[string]string{"hr": "co-1", "theirs": "co-2"},
		modes:  map[string]domain.AccessMode{"hr": domain.AccessModeOpen, "theirs": domain.AccessModeOpen},
		grants: map[string]domain.ResourceGrant{},
	}
}

func (m *memResourceGrants) ok(companyID string, kind domain.ResourceKind, id string) bool {
	return kind == domain.ResourceKindAgent && m.agents[id] == companyID
}

func (m *memResourceGrants) LoadAccess(context.Context, string, string, domain.ResourceKind, []string) (map[string]domain.ResourceAccess, error) {
	return map[string]domain.ResourceAccess{}, nil
}

func (m *memResourceGrants) View(_ context.Context, companyID string, kind domain.ResourceKind, id string) (*domain.ResourceAccessView, error) {
	if !m.ok(companyID, kind, id) {
		return nil, domain.ErrNotFound
	}
	v := &domain.ResourceAccessView{Kind: kind, ResourceID: id, AccessMode: m.modes[id], Grants: []domain.ResourceGrant{}}
	for _, g := range m.grants {
		if g.ResourceID == id {
			v.Grants = append(v.Grants, g)
		}
	}
	return v, nil
}

func (m *memResourceGrants) ListViews(ctx context.Context, companyID string, kind domain.ResourceKind) ([]domain.ResourceAccessView, error) {
	// nil when the company has nothing of this kind, on purpose — the handler
	// must answer `[]` whatever it is handed.
	var out []domain.ResourceAccessView
	for _, id := range slices.Sorted(maps.Keys(m.agents)) {
		if !m.ok(companyID, kind, id) {
			continue
		}
		v, err := m.View(ctx, companyID, kind, id)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, nil
}

func (m *memResourceGrants) ListForUser(_ context.Context, companyID, userID string) ([]domain.ResourceGrant, error) {
	if m.users[userID] != companyID {
		return nil, domain.ErrNotFound
	}
	// nil on purpose — the handler must answer `[]` whatever it is handed.
	var out []domain.ResourceGrant
	for _, g := range m.grants {
		if g.UserID == userID {
			out = append(out, g)
		}
	}
	return out, nil
}

func (m *memResourceGrants) SetAccessMode(_ context.Context, companyID string, kind domain.ResourceKind, id string, mode domain.AccessMode) (domain.AccessModeChange, error) {
	m.writes++
	if !m.ok(companyID, kind, id) {
		return domain.AccessModeChange{}, domain.ErrNotFound
	}
	m.modes[id] = mode
	return domain.AccessModeChange{AccessMode: mode}, nil
}

func (m *memResourceGrants) Grant(_ context.Context, companyID, userID string, kind domain.ResourceKind, id, grantedBy string) error {
	m.writes++
	if m.users[userID] != companyID || !m.ok(companyID, kind, id) {
		return domain.ErrNotFound
	}
	key := userID + "/" + id
	if _, held := m.grants[key]; !held {
		m.grants[key] = domain.ResourceGrant{UserID: userID, Kind: kind, ResourceID: id, GrantedBy: grantedBy, GrantedAt: time.Now()}
	}
	return nil
}

func (m *memResourceGrants) Revoke(_ context.Context, companyID, userID string, kind domain.ResourceKind, id string) error {
	m.writes++
	if m.users[userID] != companyID || !m.ok(companyID, kind, id) {
		return domain.ErrNotFound
	}
	delete(m.grants, userID+"/"+id)
	return nil
}

// accessRoutes registers AccessHandler behind a stand-in for Auth that puts
// admin-1 of co-1 on the context, as cmd/api registers it on the authed group.
func accessRoutes(repo domain.ResourceGrantRepository) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("")
	g.Use(func(c *gin.Context) {
		c.Set("company_id", "co-1")
		c.Set("user_id", "admin-1")
		c.Set("role", "admin")
	})
	var svc *app.ResourceAccessService
	if repo != nil {
		svc = app.NewResourceAccessService(repo, nil)
	}
	NewAccessHandler(svc).Register(g)
	return r
}

func serveBody(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestAccessViewAnswersModeAndGrants(t *testing.T) {
	repo := newMemResourceGrants()
	r := accessRoutes(repo)
	if w := serve(r, http.MethodPut, "/access/agent/hr/grants/u-1"); w.Code != http.StatusNoContent {
		t.Fatalf("grant: %d %s", w.Code, w.Body.String())
	}
	if w := serveBody(r, http.MethodPut, "/access/agent/hr/mode", `{"access_mode":"restricted"}`); w.Code != http.StatusOK {
		t.Fatalf("restrict: %d %s", w.Code, w.Body.String())
	}

	w := serve(r, http.MethodGet, "/access/agent/hr")
	if w.Code != http.StatusOK {
		t.Fatalf("view: %d %s", w.Code, w.Body.String())
	}
	var view domain.ResourceAccessView
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if view.AccessMode != domain.AccessModeRestricted || len(view.Grants) != 1 || view.Grants[0].UserID != "u-1" {
		t.Fatalf("view = %+v, want restricted with u-1's grant", view)
	}
	if view.Grants[0].GrantedBy != "admin-1" {
		t.Errorf("granted_by = %q, want the caller from the session", view.Grants[0].GrantedBy)
	}
}

// The list is every agent of the caller's company with its mode and grants —
// including one nobody holds — and none of another company's. It is what the
// matrix draws both directions from, so it must agree with the per-resource view
// field for field.
func TestAccessListAnswersEveryResourceOfTheKind(t *testing.T) {
	repo := newMemResourceGrants()
	repo.agents["finance"] = "co-1"
	repo.modes["finance"] = domain.AccessModeOpen
	r := accessRoutes(repo)
	if w := serve(r, http.MethodPut, "/access/agent/hr/grants/u-1"); w.Code != http.StatusNoContent {
		t.Fatalf("grant: %d %s", w.Code, w.Body.String())
	}
	if w := serveBody(r, http.MethodPut, "/access/agent/hr/mode", `{"access_mode":"restricted"}`); w.Code != http.StatusOK {
		t.Fatalf("restrict: %d %s", w.Code, w.Body.String())
	}

	w := serve(r, http.MethodGet, "/access/agent")
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Resources []domain.ResourceAccessView `json:"resources"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := map[string]domain.ResourceAccessView{}
	for _, v := range body.Resources {
		got[v.ResourceID] = v
	}
	if len(got) != 2 {
		t.Fatalf("listed %d agents (%v), want this company's two and not another's", len(got), slices.Sorted(maps.Keys(got)))
	}
	if fin := got["finance"]; fin.AccessMode != domain.AccessModeOpen || fin.Grants == nil || len(fin.Grants) != 0 {
		t.Errorf("finance = %+v, want open with an empty — not null — grant list", fin)
	}

	single := serve(r, http.MethodGet, "/access/agent/hr")
	var view domain.ResourceAccessView
	if err := json.Unmarshal(single.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode view: %v", err)
	}
	listed := got["hr"]
	if listed.AccessMode != view.AccessMode || len(listed.Grants) != len(view.Grants) || listed.Grants[0].UserID != view.Grants[0].UserID {
		t.Errorf("the list says %+v and the view says %+v about the same agent", listed, view)
	}
}

func TestAccessListOfAKindWithNothingIsAnEmptyList(t *testing.T) {
	w := serve(accessRoutes(newMemResourceGrants()), http.MethodGet, "/access/dashboard")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if got, want := w.Body.String(), `{"resources":[]}`; got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func TestAccessRefusesOutsideTheVocabulary(t *testing.T) {
	repo := newMemResourceGrants()
	r := accessRoutes(repo)
	cases := []struct{ method, path, body string }{
		{http.MethodGet, "/access/folder", ""},
		{http.MethodGet, "/access/folder/hr", ""},
		{http.MethodPut, "/access/folder/hr/grants/u-1", ""},
		{http.MethodPut, "/access/agent/hr/mode", `{"access_mode":"public"}`},
		{http.MethodPut, "/access/agent/hr/mode", `{}`},
	}
	for _, tc := range cases {
		if w := serveBody(r, tc.method, tc.path, tc.body); w.Code != http.StatusBadRequest {
			t.Errorf("%s %s %s: %d, want 400: %s", tc.method, tc.path, tc.body, w.Code, w.Body.String())
		}
	}
	if repo.writes != 0 {
		t.Errorf("an invalid request reached the store %d times", repo.writes)
	}
}

// Another company's agent, another company's user: 404 on every route, with the
// same body, and nothing written.
func TestAccessDoesNotCrossCompanies(t *testing.T) {
	repo := newMemResourceGrants()
	r := accessRoutes(repo)
	cases := []struct{ method, path, body string }{
		{http.MethodGet, "/access/agent/theirs", ""},
		{http.MethodPut, "/access/agent/theirs/mode", `{"access_mode":"restricted"}`},
		{http.MethodPut, "/access/agent/theirs/grants/u-1", ""},
		{http.MethodPut, "/access/agent/hr/grants/u-9", ""},
		{http.MethodDelete, "/access/agent/hr/grants/u-9", ""},
		{http.MethodGet, "/users/u-9/grants", ""},
	}
	for _, tc := range cases {
		w := serveBody(r, tc.method, tc.path, tc.body)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s %s: %d, want 404: %s", tc.method, tc.path, w.Code, w.Body.String())
			continue
		}
		if got := w.Body.String(); got != `{"error":"not found"}` {
			t.Errorf("%s %s: body %s — a cross-company 404 must not say which half was missing", tc.method, tc.path, got)
		}
	}
	if len(repo.grants) != 0 || repo.modes["theirs"] != domain.AccessModeOpen {
		t.Error("a cross-company request changed something")
	}
}

func TestAccessGrantAndRevokeAreIdempotent(t *testing.T) {
	r := accessRoutes(newMemResourceGrants())
	for _, req := range []struct{ method, path string }{
		{http.MethodPut, "/access/agent/hr/grants/u-1"},
		{http.MethodPut, "/access/agent/hr/grants/u-1"},
		{http.MethodDelete, "/access/agent/hr/grants/u-1"},
		{http.MethodDelete, "/access/agent/hr/grants/u-1"},
	} {
		if w := serve(r, req.method, req.path); w.Code != http.StatusNoContent {
			t.Errorf("%s %s: %d, want 204", req.method, req.path, w.Code)
		}
	}
}

func TestUserGrantsHoldingNothingIsAnEmptyList(t *testing.T) {
	w := serve(accessRoutes(newMemResourceGrants()), http.MethodGet, "/users/u-1/grants")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if got, want := w.Body.String(), `{"grants":[]}`; got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func TestAccessRoutesWithoutTheServiceAnswer503(t *testing.T) {
	for _, path := range []string{"/access/agent/hr", "/access/agent"} {
		if w := serve(accessRoutes(nil), http.MethodGet, path); w.Code != http.StatusServiceUnavailable {
			t.Errorf("GET %s: status = %d, want 503", path, w.Code)
		}
	}
}
