package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/actions"
	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/authz"
	"github.com/fauzanebd/argentum/internal/authz/authztest"
	"github.com/fauzanebd/argentum/internal/domain"
)

// T-Z9's negative suite, for the surfaces a handler decides in its own body
// rather than through resourcePolicy: a generated document, judged by the
// conversation that made it, and a document's extracted table, served under the
// table's own id. The routes resourcePolicy gates are cmd/api's.
func TestTheNegativeSuite(t *testing.T) {
	key := authztest.Key
	authztest.Run(t, authztest.PackageHandlers, map[string]authztest.Probe{
		// The real ConversationAccess, so the refusal is recorded where the
		// product records it — as the conversation's.
		key(authztest.KindGeneratedDocument, authz.DoorDashboard, "opening a carousel made in a conversation an agent in it answered"): func(t *testing.T, c authztest.Cell, rec *authztest.Recorder) bool {
			reader := app.NewConversationAccess(
				suiteThreads{"th-hidden": {"ag-fin", "ag-hr"}},
				rec.Authorizer(authztest.World{Kind: domain.ResourceKindAgent, Target: "ag-hr", Cell: c}),
				suiteRoster{},
			)
			r := personRouter(c)
			NewDocumentsHandler(newFourDocuments(), nil).WithConversationAccess(reader).Register(r.Group("/api"))
			w := serveSuite(r, http.MethodGet, "/api/documents/doc-hidden/carousel")
			return w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "document not found")
		},
		key(string(domain.ResourceKindDocument), authz.DoorDashboard, "GET /api/knowledge/tables/:tableId, served under the table's own id"): func(t *testing.T, c authztest.Cell, rec *authztest.Recorder) bool {
			docs := knowledgeDocs{rows: []*domain.SourceDocument{{ID: "doc-pay", CompanyID: "co-1", Filename: "Payroll 2026.pdf"}}}
			tables := knowledgeTables{rows: map[string]*domain.DocumentTable{
				"tbl-pay": {ID: "tbl-pay", CompanyID: "co-1", DocumentID: "doc-pay", Title: "Gaji per departemen"},
			}}
			r := personRouter(c)
			NewKnowledgeTablesHandler(app.NewDocumentTableService(docs, tables, noArtifacts{}), nil).
				WithAccess(rec.Authorizer(authztest.World{Kind: domain.ResourceKindDocument, Target: "doc-pay", Cell: c})).
				Register(r.Group("/api"))
			w := serveSuite(r, http.MethodGet, "/api/knowledge/tables/tbl-pay")
			if w.Code == http.StatusForbidden && strings.Contains(w.Body.String(), "Gaji") {
				t.Fatal("a refusal carried the table's title")
			}
			return w.Code != http.StatusForbidden
		},
		key(authztest.KindPendingAction, authz.DoorDashboard, "GET /api/actions/:id, raised in a conversation an agent in it answered"):          proposalProbe(http.MethodGet, "/api/actions/act-1"),
		key(authztest.KindPendingAction, authz.DoorDashboard, "POST /api/actions/:id/approve, raised in a conversation an agent in it answered"): proposalProbe(http.MethodPost, "/api/actions/act-1/approve"),
		key(authztest.KindPendingAction, authz.DoorDashboard, "POST /api/actions/:id/reject, raised in a conversation an agent in it answered"):  proposalProbe(http.MethodPost, "/api/actions/act-1/reject"),
	})
}

// proposalProbe requests a proposal raised in a conversation HR answered in, with
// the real ConversationAccess behind the route, as the cell's person. Past the
// check every route answers something other than 404: the proposal exists, and
// its kind is decidable by any member.
func proposalProbe(method, path string) authztest.Probe {
	return func(t *testing.T, c authztest.Cell, rec *authztest.Recorder) bool {
		reader := app.NewConversationAccess(
			suiteThreads{"th-hr": {"ag-fin", "ag-hr"}},
			rec.Authorizer(authztest.World{Kind: domain.ResourceKindAgent, Target: "ag-hr", Cell: c}),
			suiteRoster{},
		)
		repo := &suiteProposals{inv: &domain.ActionInvocation{ID: "act-1", CompanyID: "co-1", ThreadID: "th-hr", Kind: "send_message"}}
		r := personRouter(c)
		NewActionsHandler(app.NewActionService(repo, actions.NewRegistry(), nil)).
			WithConversationAccess(reader).Register(r.Group("/api"))
		w := serveSuite(r, method, path)
		if w.Code == http.StatusNotFound && repo.decided {
			t.Fatal("a hidden proposal was decided")
		}
		return w.Code != http.StatusNotFound
	}
}

// suiteProposals holds one proposal, decidable by any member. It embeds the
// interface, so a route reaching anything else panics.
type suiteProposals struct {
	domain.ActionRepository
	inv     *domain.ActionInvocation
	decided bool
}

func (s *suiteProposals) GetInvocation(_ context.Context, companyID, id string) (*domain.ActionInvocation, error) {
	if s.inv.CompanyID != companyID || s.inv.ID != id {
		return nil, domain.ErrNotFound
	}
	cp := *s.inv
	return &cp, nil
}

func (s *suiteProposals) GetCompanyAction(context.Context, string, string) (*domain.CompanyAction, error) {
	return &domain.CompanyAction{}, nil
}

func (s *suiteProposals) Approve(ctx context.Context, companyID, id, _ string, _, _ time.Time) (*domain.ActionInvocation, bool, error) {
	s.decided = true
	inv, err := s.GetInvocation(ctx, companyID, id)
	return inv, false, err
}

func (s *suiteProposals) Reject(ctx context.Context, companyID, id, _ string, _ time.Time) (*domain.ActionInvocation, error) {
	s.decided = true
	return s.GetInvocation(ctx, companyID, id)
}

// personRouter stands in for Auth as the cell's person.
func personRouter(c authztest.Cell) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery(), func(ctx *gin.Context) {
		ctx.Set("company_id", authztest.Company)
		ctx.Set("user_id", c.Person())
		ctx.Set("role", string(c.Role))
	})
	return r
}

func serveSuite(r *gin.Engine, method, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(method, path, nil))
	return w
}

// suiteThreads is which agents each conversation holds or held.
type suiteThreads map[string][]string

func (s suiteThreads) AgentsByThread(_ context.Context, _ string, ids []string) (map[string][]string, error) {
	out := map[string][]string{}
	for _, id := range ids {
		if agents, ok := s[id]; ok {
			out[id] = agents
		}
	}
	return out, nil
}

// suiteRoster is a company with no default: every conversation here names its
// agents, so none is judged by one.
type suiteRoster struct{}

func (suiteRoster) GetDefault(context.Context, string) (*domain.Agent, error) {
	return nil, domain.ErrNotFound
}

func (suiteRoster) GetByID(context.Context, string, string) (*domain.Agent, error) {
	return nil, domain.ErrNotFound
}
