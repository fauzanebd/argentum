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

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/authz"
	"github.com/fauzanebd/argentum/internal/domain"
)

// T-Z6 on Knowledge: the document list narrowed by grant, and a table asking
// about the document it was extracted from. Opening a document by id is
// resourcePolicy's, proven against the real router in cmd/api.

// knowledgeDocs is a company's uploaded documents. It embeds the interface, so a
// route reaching a method these tests did not expect panics rather than passing.
type knowledgeDocs struct {
	domain.SourceDocumentRepository
	rows []*domain.SourceDocument
}

func (d knowledgeDocs) ListByCompany(context.Context, string, int, int) ([]*domain.SourceDocument, error) {
	return d.rows, nil
}

func (d knowledgeDocs) GetForCompany(_ context.Context, companyID, id string) (*domain.SourceDocument, error) {
	for _, r := range d.rows {
		if r.ID == id && r.CompanyID == companyID {
			return r, nil
		}
	}
	return nil, domain.ErrNotFound
}

// knowledgeTables is the tables extracted from those documents, by id.
type knowledgeTables struct {
	domain.DocumentTableRepository
	rows map[string]*domain.DocumentTable
}

func (t knowledgeTables) GetForCompany(_ context.Context, companyID, id string) (*domain.DocumentTable, error) {
	if row, ok := t.rows[id]; ok && row.CompanyID == companyID {
		return row, nil
	}
	return nil, domain.ErrNotFound
}

// noBlobs and noArtifacts stand in for object storage on routes that must not
// touch it; a call panics.
type noBlobs struct{ app.DocumentBlobStore }
type noArtifacts struct{ app.DocumentArtifactStore }

// documentGrants is authz for documents, with a record of what it was asked.
type documentGrants struct {
	hidden   map[string]bool
	err      error
	decided  []string
	listed   [][]string
	subjects []authz.Subject
}

func (g *documentGrants) Decide(_ context.Context, s authz.Subject, kind domain.ResourceKind, id string) (authz.Decision, error) {
	g.decided = append(g.decided, id)
	g.subjects = append(g.subjects, s)
	if kind != domain.ResourceKindDocument {
		return authz.Decision{}, fmt.Errorf("asked about %s, not documents", kind)
	}
	if g.err != nil {
		return authz.Decision{}, g.err
	}
	if g.hidden[id] {
		return authz.Decision{Reason: authz.ReasonNotGranted}, nil
	}
	return authz.Decision{Allowed: true, Reason: authz.ReasonGranted}, nil
}

func (g *documentGrants) Visible(_ context.Context, s authz.Subject, kind domain.ResourceKind, ids []string) ([]string, error) {
	g.listed = append(g.listed, slices.Clone(ids))
	g.subjects = append(g.subjects, s)
	if kind != domain.ResourceKindDocument {
		return nil, fmt.Errorf("asked about %s, not documents", kind)
	}
	if g.err != nil {
		return nil, g.err
	}
	out := []string{}
	for _, id := range ids {
		if !g.hidden[id] {
			out = append(out, id)
		}
	}
	return out, nil
}

func (g *documentGrants) asked() int { return len(g.decided) + len(g.listed) }

// knowledgeRouter is Knowledge's two handlers over Payroll (whose table is
// "Gaji per departemen") and a store SOP, for one person in a given role.
func knowledgeRouter(access *documentGrants, role string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	docs := knowledgeDocs{rows: []*domain.SourceDocument{
		{ID: "doc-pay", CompanyID: "co-1", Filename: "Payroll 2026.pdf"},
		{ID: "doc-sop", CompanyID: "co-1", Filename: "Store SOP v3.pdf"},
	}}
	tables := knowledgeTables{rows: map[string]*domain.DocumentTable{
		"tbl-pay": {ID: "tbl-pay", CompanyID: "co-1", DocumentID: "doc-pay", Title: "Gaji per departemen"},
	}}
	r := gin.New()
	// Recovery, because the one route that must not ask (unpublish) goes on to
	// a warehouse these tests do not build; what is asserted there is that it
	// asked nothing, not how far it got.
	r.Use(gin.Recovery())
	r.Use(func(c *gin.Context) {
		c.Set("company_id", "co-1")
		c.Set("user_id", "u-dewi")
		c.Set("role", role)
	})
	api := r.Group("/api")
	docsH := NewKnowledgeDocumentsHandler(app.NewDocumentIngestService(docs, noBlobs{}, 25), 25)
	tablesH := NewKnowledgeTablesHandler(app.NewDocumentTableService(docs, tables, noArtifacts{}), nil)
	if access != nil {
		docsH = docsH.WithAccess(access)
		tablesH = tablesH.WithAccess(access)
	}
	docsH.Register(api)
	tablesH.Register(api)
	return r
}

func knowledgeCall(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
	return w
}

func TestKnowledgeListsOnlyTheDocumentsThePersonMayRead(t *testing.T) {
	for _, role := range []string{"member", "admin"} {
		t.Run(role, func(t *testing.T) {
			grants := &documentGrants{hidden: map[string]bool{"doc-pay": true}}
			w := knowledgeCall(knowledgeRouter(grants, role), http.MethodGet, "/api/knowledge/documents", "")
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", w.Code, w.Body.String())
			}
			var body struct {
				Documents []struct {
					ID string `json:"id"`
				} `json:"documents"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if len(body.Documents) != 1 || body.Documents[0].ID != "doc-sop" {
				t.Errorf("documents = %+v, want only the SOP — an admin is narrowed like anyone (decision 4)", body.Documents)
			}
			if strings.Contains(w.Body.String(), "Payroll") {
				t.Error("the list named a document the person may not read")
			}
			if len(grants.listed) != 1 || !slices.Equal(grants.listed[0], []string{"doc-pay", "doc-sop"}) || len(grants.decided) != 0 {
				t.Errorf("asked %v / %v, want one question for the page", grants.listed, grants.decided)
			}
			// Guarded, so a list that asked nothing fails here rather than
			// panicking and hiding the table tests after it.
			if len(grants.subjects) == 0 || grants.subjects[0].UserID != "u-dewi" {
				t.Errorf("asked for %+v, want the person reading the list", grants.subjects)
			}
		})
	}
}

func TestAKnowledgeListCheckThatFailsServesNothing(t *testing.T) {
	grants := &documentGrants{err: errors.New("control DB down")}
	w := knowledgeCall(knowledgeRouter(grants, "member"), http.MethodGet, "/api/knowledge/documents", "")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	if body := w.Body.String(); strings.Contains(body, "Payroll") || strings.Contains(body, "control DB") {
		t.Errorf("body = %s, want neither a document nor the storage error", body)
	}
}

// A table is served under its own id, so the classification test cannot see it
// as a document route; without this, restricting Payroll would leave its salary
// table one URL away.
func TestATableAsksAboutTheDocumentItCameFrom(t *testing.T) {
	refused := func(t *testing.T, w *httptest.ResponseRecorder) {
		t.Helper()
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), `"resource_kind":"document"`) {
			t.Errorf("status %d, body %s; want 403 naming the document kind", w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "Gaji") {
			t.Error("a refusal carried the table's title")
		}
	}

	t.Run("reading it", func(t *testing.T) {
		grants := &documentGrants{hidden: map[string]bool{"doc-pay": true}}
		refused(t, knowledgeCall(knowledgeRouter(grants, "member"), http.MethodGet, "/api/knowledge/tables/tbl-pay", ""))
		if !slices.Equal(grants.decided, []string{"doc-pay"}) {
			t.Errorf("decided %v, want one question, about the document", grants.decided)
		}
	})
	t.Run("editing it, before its body is even validated", func(t *testing.T) {
		grants := &documentGrants{hidden: map[string]bool{"doc-pay": true}}
		refused(t, knowledgeCall(knowledgeRouter(grants, "admin"), http.MethodPatch, "/api/knowledge/tables/tbl-pay", "not json"))
	})
	t.Run("publishing it", func(t *testing.T) {
		grants := &documentGrants{hidden: map[string]bool{"doc-pay": true}}
		refused(t, knowledgeCall(knowledgeRouter(grants, "admin"), http.MethodPost, "/api/knowledge/tables/tbl-pay/apply", ""))
	})
}

func TestATableRouteGrantedPassesUnknownIsItsOwn404AndUnpublishNeverAsks(t *testing.T) {
	t.Run("a granted person reaches the handler", func(t *testing.T) {
		grants := &documentGrants{}
		w := knowledgeCall(knowledgeRouter(grants, "admin"), http.MethodPost, "/api/knowledge/tables/tbl-pay/apply", "")
		// No warehouse in this router: apply's own 503 is how far a person the
		// check let through gets.
		if w.Code == http.StatusForbidden || strings.Contains(w.Body.String(), "could not check access") {
			t.Errorf("status %d, body %s; want past the check", w.Code, w.Body.String())
		}
		if len(grants.decided) != 1 {
			t.Errorf("decided %v, want one question", grants.decided)
		}
	})
	t.Run("a table this company does not have", func(t *testing.T) {
		grants := &documentGrants{hidden: map[string]bool{"doc-pay": true}}
		w := knowledgeCall(knowledgeRouter(grants, "member"), http.MethodGet, "/api/knowledge/tables/tbl-theirs", "")
		if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "no such table") || grants.asked() != 0 {
			t.Errorf("status %d, body %s, %d questions; want the handler's own 404, asking nothing", w.Code, w.Body.String(), grants.asked())
		}
	})
	t.Run("a check that fails", func(t *testing.T) {
		grants := &documentGrants{err: errors.New("control DB down")}
		w := knowledgeCall(knowledgeRouter(grants, "member"), http.MethodGet, "/api/knowledge/tables/tbl-pay", "")
		if w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), "control DB") {
			t.Errorf("status %d, body %s; want a 503 without the storage error", w.Code, w.Body.String())
		}
	})
	t.Run("withdrawing it takes access away and never asks", func(t *testing.T) {
		grants := &documentGrants{hidden: map[string]bool{"doc-pay": true}}
		w := knowledgeCall(knowledgeRouter(grants, "admin"), http.MethodPost, "/api/knowledge/tables/tbl-pay/unpublish", "")
		if w.Code == http.StatusForbidden || grants.asked() != 0 {
			t.Errorf("status %d with %d questions; want unpublish never gated", w.Code, grants.asked())
		}
	})
}
