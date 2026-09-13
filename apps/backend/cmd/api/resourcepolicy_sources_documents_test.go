package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/authz"
	"github.com/fauzanebd/argentum/internal/domain"
)

// T-Z6's routes, by name. TestResourceGatedRoutesRefuseAnAdminWithoutAGrant
// walks whatever resourcePolicy holds, so an edit that moved one of these to
// resourceExempt would shrink that sweep without failing it. This one fails
// instead: each route a restricted source or document must refuse, refusing an
// admin who is not granted it, with a 403 naming the kind — through the real
// router and the real chain.
func TestSourceAndDocumentReadsRefuseAnAdminWithoutAGrant(t *testing.T) {
	want := map[string]domain.ResourceKind{
		// The three that read what is in a source; the other nine configure it.
		"POST /api/connections/:id/freshness/test":         domain.ResourceKindConnection,
		"POST /api/connections/:id/regenerate-description": domain.ResourceKindConnection,
		"POST /api/connections/:id/test-rag":               domain.ResourceKindConnection,
		// A document, its tables and its pages. Its extracted tables by their own
		// id ask in KnowledgeTablesHandler, tested in the handlers package.
		"GET /api/knowledge/documents/:id":             domain.ResourceKindDocument,
		"GET /api/knowledge/documents/:id/tables":      domain.ResourceKindDocument,
		"GET /api/knowledge/documents/:id/pages/:page": domain.ResourceKindDocument,
	}
	r := routerWithDeps(t, func(d *apiDeps) { d.resourceAuthz = authz.New(&restrictedEverything{}) })
	token := adminToken(t)
	for key, kind := range want {
		method, path, _ := strings.Cut(key, " ")
		req, err := http.NewRequest(method, concreteURL(path), nil)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), `"resource_kind":"`+string(kind)+`"`) {
			t.Errorf("%s: admin with no grant on a restricted %s got %d %s, want 403 naming the kind", key, kind, w.Code, w.Body.String())
		}
	}
}
