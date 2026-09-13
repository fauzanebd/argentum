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
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/actions"
	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
)

// T-Z14, the owner's decision on access-grants §13d: a proposal is as hidden as
// the conversation it was raised in — omitted from the pending list, not found by
// id, and not decided by anyone who may not read it.

// threeProposals is one company's pending proposals: one from a readable
// conversation, one from a hidden one, and one raised outside any conversation.
type threeProposals struct {
	domain.ActionRepository
	rows    map[string]*domain.ActionInvocation
	decided []string
}

func newThreeProposals() *threeProposals {
	return &threeProposals{rows: map[string]*domain.ActionInvocation{
		"act-open":   {ID: "act-open", CompanyID: "co-1", ThreadID: "th-open", Kind: "send_message"},
		"act-hidden": {ID: "act-hidden", CompanyID: "co-1", ThreadID: "th-hidden", Kind: "send_message", ParamsRedacted: json.RawMessage(`{"body":"Gaji direktur naik"}`)},
		"act-none":   {ID: "act-none", CompanyID: "co-1", Kind: "send_message"},
	}}
}

func (p *threeProposals) GetInvocation(_ context.Context, companyID, id string) (*domain.ActionInvocation, error) {
	if inv, ok := p.rows[id]; ok && inv.CompanyID == companyID {
		cp := *inv
		return &cp, nil
	}
	return nil, domain.ErrNotFound
}

func (p *threeProposals) ListPending(context.Context, string) ([]*domain.ActionInvocation, error) {
	var out []*domain.ActionInvocation
	for _, id := range []string{"act-hidden", "act-open", "act-none"} {
		cp := *p.rows[id]
		out = append(out, &cp)
	}
	return out, nil
}

func (p *threeProposals) GetCompanyAction(context.Context, string, string) (*domain.CompanyAction, error) {
	return &domain.CompanyAction{}, nil
}

func (p *threeProposals) Approve(ctx context.Context, companyID, id, _ string, _, _ time.Time) (*domain.ActionInvocation, bool, error) {
	p.decided = append(p.decided, "approve:"+id)
	inv, err := p.GetInvocation(ctx, companyID, id)
	return inv, false, err
}

func (p *threeProposals) Reject(ctx context.Context, companyID, id, _ string, _ time.Time) (*domain.ActionInvocation, error) {
	p.decided = append(p.decided, "reject:"+id)
	return p.GetInvocation(ctx, companyID, id)
}

// proposalsRouter serves the actions routes as an admin: nothing on them reads a
// role for readability, and decision 4 is that an admin without the grant is
// hidden from like anyone else.
func proposalsRouter(repo *threeProposals, reader ConversationReader) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("company_id", "co-1")
		c.Set("user_id", "admin-1")
		c.Set("role", "admin")
	})
	h := NewActionsHandler(app.NewActionService(repo, actions.NewRegistry(), nil))
	if reader != nil {
		h = h.WithConversationAccess(reader)
	}
	h.Register(r.Group("/api"))
	return r
}

func callProposals(r *gin.Engine, method, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(method, path, nil))
	return w
}

func pendingIDs(t *testing.T, w *httptest.ResponseRecorder) []string {
	t.Helper()
	var body struct {
		Actions []struct {
			ID string `json:"id"`
		} `json:"actions"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v: %s", err, w.Body.String())
	}
	var ids []string
	for _, a := range body.Actions {
		ids = append(ids, a.ID)
	}
	return ids
}

func TestThePendingListOmitsProposalsFromHiddenConversations(t *testing.T) {
	reader := &countingReader{hiddenConversations: hiddenConversations{hidden: map[string]bool{"th-hidden": true}}}
	w := callProposals(proposalsRouter(newThreeProposals(), reader), http.MethodGet, "/api/actions/pending")

	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if got := pendingIDs(t, w); !slices.Equal(got, []string{"act-open", "act-none"}) {
		t.Errorf("listed %v, want act-open and act-none in order", got)
	}
	if strings.Contains(w.Body.String(), "Gaji") || strings.Contains(w.Body.String(), "th-hidden") {
		t.Errorf("the list carries the hidden proposal: %s", w.Body.String())
	}
	if len(reader.readable) != 1 || !slices.Equal(reader.readable[0], []string{"th-hidden", "th-open"}) || len(reader.mayRead) != 0 {
		t.Errorf("asked Readable %v and MayRead %v, want one Readable over the page's conversations", reader.readable, reader.mayRead)
	}
}

// By id, a hidden proposal answers exactly what an id that never existed answers
// — and nothing is decided.
func TestAProposalFromAHiddenConversationIsNotFoundByID(t *testing.T) {
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/actions/%s"},
		{http.MethodPost, "/api/actions/%s/approve"},
		{http.MethodPost, "/api/actions/%s/reject"},
	} {
		repo := newThreeProposals()
		r := proposalsRouter(repo, hiddenConversations{hidden: map[string]bool{"th-hidden": true}})
		hidden := callProposals(r, route.method, strings.Replace(route.path, "%s", "act-hidden", 1))
		unknown := callProposals(r, route.method, strings.Replace(route.path, "%s", "act-nope", 1))

		if hidden.Code != http.StatusNotFound || hidden.Body.String() != unknown.Body.String() || unknown.Code != http.StatusNotFound {
			t.Errorf("%s %s: hidden %d %s, unknown %d %s; want both the same 404", route.method, route.path,
				hidden.Code, hidden.Body.String(), unknown.Code, unknown.Body.String())
		}
		if len(repo.decided) != 0 {
			t.Errorf("%s %s decided %v", route.method, route.path, repo.decided)
		}
	}

	// A readable proposal, and one raised outside any conversation, are decided.
	repo := newThreeProposals()
	r := proposalsRouter(repo, hiddenConversations{hidden: map[string]bool{"th-hidden": true}})
	for _, id := range []string{"act-open", "act-none"} {
		if w := callProposals(r, http.MethodPost, "/api/actions/"+id+"/approve"); w.Code != http.StatusOK {
			t.Errorf("approving %s: %d %s", id, w.Code, w.Body.String())
		}
	}
	if !slices.Equal(repo.decided, []string{"approve:act-open", "approve:act-none"}) {
		t.Errorf("decided %v", repo.decided)
	}
}

func TestAProposalCheckThatFailsServesAndDecidesNothing(t *testing.T) {
	repo := newThreeProposals()
	r := proposalsRouter(repo, hiddenConversations{err: errors.New("connection refused")})
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/actions/pending"},
		{http.MethodGet, "/api/actions/act-open"},
		{http.MethodPost, "/api/actions/act-open/approve"},
		{http.MethodPost, "/api/actions/act-open/reject"},
	} {
		w := callProposals(r, route.method, route.path)
		if w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), "connection refused") || strings.Contains(w.Body.String(), "act-") {
			t.Errorf("%s %s: %d %s, want a 503 carrying nothing", route.method, route.path, w.Code, w.Body.String())
		}
	}
	if len(repo.decided) != 0 {
		t.Errorf("a failed check decided %v", repo.decided)
	}
}

func TestWithNoConversationAccessEveryProposalIsListedAsBefore(t *testing.T) {
	w := callProposals(proposalsRouter(newThreeProposals(), nil), http.MethodGet, "/api/actions/pending")
	if got := pendingIDs(t, w); !slices.Equal(got, []string{"act-hidden", "act-open", "act-none"}) {
		t.Errorf("listed %v, want all three", got)
	}
}
