package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/authz"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/transport/http/middleware"
)

// T-Z8 at the two public doors: `/v1`, whose key carries an agent allowlist, and
// the website widget, which never reaches a restricted agent.

// --- /v1 ---------------------------------------------------------------

func TestAKeyLimitedToNamedAgentsListsOnlyThose(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	v1 := r.Group("/v1")
	v1.Use(func(c *gin.Context) {
		c.Set("company_id", testCompany)
		c.Set(middleware.CtxAPIKeyID, "key-1")
		c.Set(middleware.CtxAPIKeyAgents, []string{"ag-fin"})
	})
	NewV1AgentsHandler(&fakeRoster{agents: []*domain.Agent{
		{ID: "ag-hr", Name: "HR", Enabled: true, IsDefault: true},
		{ID: "ag-fin", Name: "Finance", Enabled: true},
	}}, nil).Register(v1)

	w := listAgents(t, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var page struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(page.Data) != 1 || page.Data[0].ID != "ag-fin" {
		t.Errorf("data = %+v, want only the key's agent", page.Data)
	}
	if strings.Contains(w.Body.String(), "HR") {
		t.Error("the list named an agent the key may not use")
	}
}

// 403, not 404: the agent is the conversation's or the default, which the
// caller did not name, and the fix is a field in their request.
func TestATurnOutsideTheKeysAgentsIsA403NamingTheField(t *testing.T) {
	f := newChatFixture(t, 5*time.Second)
	f.enq.err = fmt.Errorf("resolve thread: %w", app.ErrAgentNotAllowed)

	w := f.send(t, sendRequest(t, "application/json", `{"message":"revenue?","user_ref":"u"}`), nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", w.Code, w.Body.String())
	}
	for _, want := range []string{`"agent_not_allowed"`, `"agent_id"`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("body %s does not carry %s", w.Body.String(), want)
		}
	}
}

// --- the widget --------------------------------------------------------

// widgetAccess answers Visible from a fixed open set and records who asked.
type widgetAccess struct {
	open  []string
	err   error
	asked []authz.Subject
}

func (s *widgetAccess) Visible(_ context.Context, sub authz.Subject, _ domain.ResourceKind, ids []string) ([]string, error) {
	s.asked = append(s.asked, sub)
	if s.err != nil {
		return nil, s.err
	}
	var out []string
	for _, id := range ids {
		if slices.Contains(s.open, id) {
			out = append(out, id)
		}
	}
	return out, nil
}

func widgetConfig(t *testing.T, access *widgetAccess) (*httptest.ResponseRecorder, []string) {
	t.Helper()
	h := NewEmbedChatHandler(nil, &embedThreadsStub{}, &embedMessagesStub{}, &embedRosterStub{
		agents: []*domain.Agent{
			{ID: "ag-support", Name: "Support", Enabled: true, IsDefault: true},
			{ID: "ag-hr", Name: "HR", Enabled: true},
			{ID: "ag-draft", Name: "Draft", Enabled: false},
		},
	}).WithAgentAccess(access)
	w := httptest.NewRecorder()
	embedRouter(h, "co-1", "emp_812").ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/embed/config", nil))
	var body struct {
		Agents []struct {
			ID string `json:"id"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, w.Body.String())
	}
	ids := make([]string, 0, len(body.Agents))
	for _, a := range body.Agents {
		ids = append(ids, a.ID)
	}
	return w, ids
}

func TestTheWidgetNeverOffersARestrictedAgent(t *testing.T) {
	access := &widgetAccess{open: []string{"ag-support", "ag-draft"}}
	w, ids := widgetConfig(t, access)

	if !slices.Equal(ids, []string{"ag-support"}) {
		t.Errorf("agents = %v, want only the enabled, open one", ids)
	}
	if strings.Contains(w.Body.String(), "HR") {
		t.Error("the widget's config named a restricted agent")
	}
	// Nobody asks: a visitor is not a person this workspace can grant, whatever
	// their embed ref is.
	if len(access.asked) != 1 || access.asked[0] != (authz.Subject{CompanyID: "co-1"}) {
		t.Errorf("asked %+v, want one question, for the company, as nobody", access.asked)
	}
}

func TestAWidgetAccessCheckThatFailsOffersNoAgents(t *testing.T) {
	w, ids := widgetConfig(t, &widgetAccess{err: errors.New("control DB down")})
	if w.Code != http.StatusOK || len(ids) != 0 {
		t.Errorf("status %d, agents %v; want the widget still configured, offering none", w.Code, ids)
	}
}

func TestAWidgetTurnOnARestrictedAgentIsARefusalAVisitorCanRead(t *testing.T) {
	cases := []struct {
		err    error
		status int
	}{
		{fmt.Errorf("resolve: %w", app.ErrAgentNotClearedHere), http.StatusForbidden},
		{app.ErrNoAgentAvailable, http.StatusForbidden},
		{fmt.Errorf("%w: control DB down", app.ErrAccessCheckFailed), http.StatusServiceUnavailable},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		embedChatFail(c, tc.err)
		if w.Code != tc.status {
			t.Errorf("%v → %d, want %d", tc.err, w.Code, tc.status)
		}
		// A website visitor is not told to ask an admin for a grant, and is not
		// handed the database's message.
		if body := w.Body.String(); strings.Contains(body, "admin") || strings.Contains(body, "control DB") {
			t.Errorf("%v → body %s", tc.err, body)
		}
	}
}
